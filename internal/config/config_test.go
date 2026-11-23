package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), mode))
	return p
}

func TestLoadExpandsEnvAndParsesDurations(t *testing.T) {
	t.Setenv("TEST_N8N_KEY", "n8n_api_secret_value_1234")
	p := writeConfig(t, `
instances:
  - name: production
    url: https://n8n.example.com/
    api_key: ${TEST_N8N_KEY}
  - name: staging
    url: https://staging.example.com/api/v1
    username: admin
    password: ${TEST_N8N_KEY}
    webhook_url: https://hooks.example.com/webhook/
default_instance: staging
refresh_interval: 2s
`, 0o600)

	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, p, cfg.Path)
	assert.Len(t, cfg.Instances, 2)
	assert.Equal(t, 2*time.Second, cfg.RefreshInterval)
	assert.Equal(t, DefaultTimeout, cfg.Timeout)
	assert.Empty(t, cfg.Warnings)

	prod, ok := cfg.Instance("PRODUCTION")
	require.True(t, ok)
	assert.Equal(t, "n8n_api_secret_value_1234", prod.APIKey)
	assert.Equal(t, "https://n8n.example.com", prod.BaseURL())
	assert.Equal(t, "api key", prod.AuthMethod())

	def := cfg.Default()
	assert.Equal(t, "staging", def.Name)
	assert.Equal(t, "https://staging.example.com", def.BaseURL())
	assert.Equal(t, "https://hooks.example.com", def.WebhookBaseURL())
	assert.Equal(t, "basic auth", def.AuthMethod())
}

func TestLoadValidation(t *testing.T) {
	cases := map[string]string{
		"no instances":     "instances: []\n",
		"bad scheme":       "instances:\n  - name: a\n    url: ftp://x\n    api_key: k\n",
		"missing url":      "instances:\n  - name: a\n    api_key: k\n",
		"duplicate names":  "instances:\n  - {name: a, url: 'http://a', api_key: k}\n  - {name: A, url: 'http://b', api_key: k}\n",
		"unknown default":  "instances:\n  - {name: a, url: 'http://a', api_key: k}\ndefault_instance: b\n",
		"password no user": "instances:\n  - {name: a, url: 'http://a', password: p}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, body, 0o600))
			assert.Error(t, err)
		})
	}
}

func TestLoadDefaultsAndWarnings(t *testing.T) {
	p := writeConfig(t, `
instances:
  - url: http://localhost:5678
    api_key: ${TEST_UNSET_VAR_FOR_N8N_TUI}
refresh_interval: 10ms
`, 0o600)
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "instance-1", cfg.Instances[0].Name)
	assert.Equal(t, "instance-1", cfg.DefaultInstance)
	assert.Equal(t, minRefreshInterval, cfg.RefreshInterval)
	require.Len(t, cfg.Warnings, 1)
	assert.Contains(t, cfg.Warnings[0], "no api_key")
}

func TestLoadWarnsOnWorldReadableLiteralSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on windows")
	}
	p := writeConfig(t, "instances:\n  - {name: a, url: 'http://a', api_key: literal-key}\n", 0o644)
	require.NoError(t, os.Chmod(p, 0o644)) // bypass umask
	cfg, err := Load(p)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Warnings)
	assert.Contains(t, cfg.Warnings[0], "chmod 600")

	// Secrets referenced via env vars do not trigger the warning.
	p2 := writeConfig(t, "instances:\n  - {name: a, url: 'http://a', api_key: '${HOME}'}\n", 0o644)
	require.NoError(t, os.Chmod(p2, 0o644))
	cfg, err = Load(p2)
	require.NoError(t, err)
	assert.Empty(t, cfg.Warnings)
}

func TestLoadFromEnvWhenNoFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("N8N_URL", "http://localhost:5678")
	t.Setenv("N8N_API_KEY", "key")
	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Path)
	assert.Equal(t, "http://localhost:5678", cfg.Default().URL)
	assert.Equal(t, "key", cfg.Default().APIKey)

	t.Setenv("N8N_URL", "")
	_, err = Load("")
	assert.ErrorIs(t, err, ErrNoConfig)
}

func TestLoadExplicitMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoConfig)
}

func TestDefaultPathUsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	assert.Equal(t, filepath.Join("/tmp/xdg", "n8n-tui", "config.yaml"), DefaultPath())
}

func TestMaskSecret(t *testing.T) {
	assert.Equal(t, "(not set)", MaskSecret(""))
	assert.Equal(t, "••••••••", MaskSecret("short"))
	assert.Equal(t, "••••••••wxyz", MaskSecret("abcdefghijklmnopqrstuvwxyz"))
}

func TestExampleConfigIsValid(t *testing.T) {
	t.Setenv("N8N_API_KEY", "a")
	t.Setenv("N8N_STAGING_API_KEY", "b")
	cfg, err := Load(writeConfig(t, Example, 0o600))
	require.NoError(t, err)
	assert.Equal(t, "production", cfg.Default().Name)
}
