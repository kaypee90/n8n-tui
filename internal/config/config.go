// Package config loads n8n-tui configuration from
// ~/.config/n8n-tui/config.yaml (or $XDG_CONFIG_HOME/n8n-tui/config.yaml).
//
// Secrets should be referenced as environment variables (${N8N_API_KEY}) so
// they never have to be written to disk; they are expanded at load time.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	DefaultRefreshInterval = 5 * time.Second
	DefaultTimeout         = 10 * time.Second
	minRefreshInterval     = 500 * time.Millisecond
)

// Instance is a single n8n instance the TUI can connect to.
type Instance struct {
	Name string `mapstructure:"name"`
	// URL is the instance root, e.g. https://n8n.example.com. A trailing
	// /api/v1 is tolerated and stripped.
	URL string `mapstructure:"url"`
	// APIKey is sent as X-N8N-API-KEY.
	APIKey string `mapstructure:"api_key"`
	// Username/Password enable HTTP basic auth, typically required by a
	// reverse proxy in front of n8n. Can be combined with APIKey.
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	// WebhookURL overrides the base used for webhook calls when webhooks are
	// served from a different host (N8N_WEBHOOK_URL / queue-mode webhook
	// processors).
	WebhookURL string `mapstructure:"webhook_url"`
	// InsecureSkipVerify disables TLS verification (self-signed dev setups).
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`
}

// AuthMethod describes how the instance authenticates, for display.
func (i Instance) AuthMethod() string {
	switch {
	case i.APIKey != "" && i.Username != "":
		return "api key + basic auth"
	case i.APIKey != "":
		return "api key"
	case i.Username != "":
		return "basic auth"
	default:
		return "none"
	}
}

// BaseURL returns the instance root URL without a trailing slash or /api/v1.
func (i Instance) BaseURL() string {
	u := strings.TrimRight(i.URL, "/")
	u = strings.TrimSuffix(u, "/api/v1")
	return strings.TrimRight(u, "/")
}

// WebhookBaseURL returns the root used for /webhook and /webhook-test calls.
func (i Instance) WebhookBaseURL() string {
	if i.WebhookURL != "" {
		u := strings.TrimRight(i.WebhookURL, "/")
		u = strings.TrimSuffix(u, "/webhook")
		return strings.TrimRight(u, "/")
	}
	return i.BaseURL()
}

// Config is the full configuration file.
type Config struct {
	Instances       []Instance    `mapstructure:"instances"`
	DefaultInstance string        `mapstructure:"default_instance"`
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
	Timeout         time.Duration `mapstructure:"timeout"`

	// Path is the file the config was loaded from ("" when built from env).
	Path string `mapstructure:"-"`
	// Warnings are non-fatal problems worth surfacing to the user.
	Warnings []string `mapstructure:"-"`
}

// Instance returns the named instance.
func (c *Config) Instance(name string) (Instance, bool) {
	for _, in := range c.Instances {
		if strings.EqualFold(in.Name, name) {
			return in, true
		}
	}
	return Instance{}, false
}

// Default returns the default instance (or the first one).
func (c *Config) Default() Instance {
	if in, ok := c.Instance(c.DefaultInstance); ok {
		return in
	}
	return c.Instances[0]
}

// ErrNoConfig is returned when neither a config file nor N8N_URL is present.
var ErrNoConfig = errors.New("no configuration found")

// DefaultPath returns the config file location.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "n8n-tui", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "n8n-tui", "config.yaml")
	}
	return filepath.Join(home, ".config", "n8n-tui", "config.yaml")
}

// Load reads the config at path. If path is empty the default location is
// used; if that does not exist, a single instance is built from N8N_URL,
// N8N_API_KEY, N8N_USERNAME and N8N_PASSWORD.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultPath()
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		return loadFile(path, info)
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return fromEnv()
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("config file %s does not exist", path)
	default:
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
}

func loadFile(path string, info fs.FileInfo) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType(configType(path))
	v.SetDefault("refresh_interval", DefaultRefreshInterval)
	v.SetDefault("timeout", DefaultTimeout)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("decoding config %s: %w", path, err)
	}
	cfg.Path = path

	literalSecret := false
	for i := range cfg.Instances {
		in := &cfg.Instances[i]
		if hasLiteralSecret(in.APIKey) || hasLiteralSecret(in.Password) {
			literalSecret = true
		}
		in.Name = os.ExpandEnv(in.Name)
		in.URL = os.ExpandEnv(in.URL)
		in.APIKey = os.ExpandEnv(in.APIKey)
		in.Username = os.ExpandEnv(in.Username)
		in.Password = os.ExpandEnv(in.Password)
		in.WebhookURL = os.ExpandEnv(in.WebhookURL)
	}

	if literalSecret && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
			"%s contains secrets and is readable by other users (mode %04o); run: chmod 600 %s",
			path, info.Mode().Perm(), path))
	}

	if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func configType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	default:
		return "yaml"
	}
}

// hasLiteralSecret reports whether a value is a secret written directly in
// the file rather than referenced through an environment variable.
func hasLiteralSecret(s string) bool {
	return s != "" && !strings.Contains(s, "$")
}

func fromEnv() (*Config, error) {
	u := os.Getenv("N8N_URL")
	if u == "" {
		return nil, ErrNoConfig
	}
	cfg := &Config{
		Instances: []Instance{{
			Name:     "default",
			URL:      u,
			APIKey:   os.Getenv("N8N_API_KEY"),
			Username: os.Getenv("N8N_USERNAME"),
			Password: os.Getenv("N8N_PASSWORD"),
		}},
		DefaultInstance: "default",
		RefreshInterval: DefaultRefreshInterval,
		Timeout:         DefaultTimeout,
	}
	if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("invalid environment configuration: %w", err)
	}
	return cfg, nil
}

func (c *Config) normalize() error {
	if len(c.Instances) == 0 {
		return errors.New("at least one instance is required")
	}
	seen := map[string]bool{}
	for i := range c.Instances {
		in := &c.Instances[i]
		if in.Name == "" {
			in.Name = fmt.Sprintf("instance-%d", i+1)
		}
		key := strings.ToLower(in.Name)
		if seen[key] {
			return fmt.Errorf("duplicate instance name %q", in.Name)
		}
		seen[key] = true

		if err := validateURL(in.URL); err != nil {
			return fmt.Errorf("instance %q: url: %w", in.Name, err)
		}
		if in.WebhookURL != "" {
			if err := validateURL(in.WebhookURL); err != nil {
				return fmt.Errorf("instance %q: webhook_url: %w", in.Name, err)
			}
		}
		if in.APIKey == "" && in.Username == "" {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"instance %q has no api_key or username; requests will be unauthenticated (is the env var set?)", in.Name))
		}
		if in.Password != "" && in.Username == "" {
			return fmt.Errorf("instance %q: password set without username", in.Name)
		}
	}

	if c.DefaultInstance == "" {
		c.DefaultInstance = c.Instances[0].Name
	} else if _, ok := c.Instance(c.DefaultInstance); !ok {
		return fmt.Errorf("default_instance %q is not defined", c.DefaultInstance)
	}

	if c.RefreshInterval <= 0 {
		c.RefreshInterval = DefaultRefreshInterval
	} else if c.RefreshInterval < minRefreshInterval {
		c.RefreshInterval = minRefreshInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return nil
}

func validateURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	return nil
}

// MaskSecret renders a secret safely for display: only the last 4 characters
// are shown, and only when the secret is long enough for that to be harmless.
func MaskSecret(s string) string {
	switch {
	case s == "":
		return "(not set)"
	case len(s) < 12:
		return "••••••••"
	default:
		return "••••••••" + s[len(s)-4:]
	}
}

// Example is the commented starter config printed by --example-config.
const Example = `# n8n-tui configuration
# Save as ~/.config/n8n-tui/config.yaml (or run "n8n-tui init" to create it for you).
#
# Each instance needs a url and an API key. Create the key in n8n under
# Settings → n8n API. ${NAME} is replaced with the environment variable NAME,
# so the key itself never has to be written to this file.

instances:
  - name: production                    # any name; shown in the header and Settings view
    url: https://n8n.example.com        # the address you open n8n at (not /api/v1)
    api_key: ${N8N_API_KEY}

  - name: staging
    url: https://n8n-staging.example.com
    api_key: ${N8N_STAGING_API_KEY}
    # Optional: basic auth, if a reverse proxy in front of n8n asks for a password
    # username: ${PROXY_USER}
    # password: ${PROXY_PASSWORD}
    # Optional: webhooks served from a different host (n8n's WEBHOOK_URL setting)
    # webhook_url: https://hooks.example.com
    # Optional: accept a self-signed TLS certificate (development only)
    # insecure_skip_verify: true

default_instance: production   # used when you don't pass --instance
refresh_interval: 5s           # how often views and the log stream poll n8n (min 500ms)
timeout: 10s                   # how long to wait for each request
`
