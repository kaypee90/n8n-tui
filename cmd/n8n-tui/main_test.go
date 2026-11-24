package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/config"
)

func TestVersionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	assert.Equal(t, 0, run([]string{"--version"}, &out, &errOut))
	assert.Contains(t, out.String(), "n8n-tui dev")
}

func TestExampleConfigFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	assert.Equal(t, 0, run([]string{"--example-config"}, &out, &errOut))
	assert.Contains(t, out.String(), "default_instance: production")
}

func TestNoConfigPrintsGuidance(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("N8N_URL", "")
	old := interactive
	interactive = func() bool { return false }
	t.Cleanup(func() { interactive = old })
	var out, errOut bytes.Buffer
	assert.Equal(t, 1, run(nil, &out, &errOut))
	assert.Contains(t, errOut.String(), "no configuration found")
	assert.Contains(t, errOut.String(), "N8N_API_KEY")
	assert.Contains(t, errOut.String(), "n8n-tui init")
}

func TestInvalidConfigAndUnknownInstance(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	assert.NoError(t, os.WriteFile(bad, []byte("instances: []\n"), 0o600))
	var out, errOut bytes.Buffer
	assert.Equal(t, 1, run([]string{"--config", bad}, &out, &errOut))
	assert.Contains(t, errOut.String(), "at least one instance")

	good := filepath.Join(dir, "good.yaml")
	assert.NoError(t, os.WriteFile(good, []byte("instances:\n  - {name: a, url: 'http://a', api_key: k}\n"), 0o600))
	errOut.Reset()
	assert.Equal(t, 1, run([]string{"--config", good, "--instance", "zzz"}, &out, &errOut))
	assert.Contains(t, errOut.String(), `instance "zzz" is not configured`)
}

func TestBadFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	assert.Equal(t, 2, run([]string{"--nope"}, &out, &errOut))
	assert.Equal(t, 0, run([]string{"-h"}, &out, &errOut))
}

func fakeSetup(t *testing.T, input string, checkErr error) *bytes.Buffer {
	t.Helper()
	out := &bytes.Buffer{}
	r := bufio.NewReader(strings.NewReader(input))
	old := newSetupEnv
	newSetupEnv = func(io.Writer) setupEnv {
		return setupEnv{in: r, out: out,
			readSecret: func() (string, error) { return readLine(r) },
			check: func(in config.Instance) error {
				assert.Equal(t, "http://n8n.local:5678", in.URL, "trailing /api/v1 is stripped")
				return checkErr
			},
		}
	}
	t.Cleanup(func() { newSetupEnv = old })
	return out
}

func TestInitWritesWorkingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	out := fakeSetup(t, "http://n8n.local:5678/api/v1/\nsecret-key-123\n\n\n", nil)
	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"init", "--config", path}, out, &errOut), out.String())
	assert.Contains(t, out.String(), "connected ✔")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a key stored in the file must be private")

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Warnings)
	assert.Equal(t, "n8n", cfg.Default().Name)
	assert.Equal(t, "secret-key-123", cfg.Default().APIKey)

	// Running it again never overwrites the file; it prints a snippet instead.
	out2 := fakeSetup(t, "http://n8n.local:5678\nk2\nsecond\n\n", nil)
	require.Equal(t, 0, run([]string{"init", "--config", path}, out2, &errOut))
	assert.Contains(t, out2.String(), "already exists")
	assert.Contains(t, out2.String(), "name: 'second'")
	cfg, err = config.Load(path)
	require.NoError(t, err)
	assert.Len(t, cfg.Instances, 1)
}

func TestInitWithEnvVarKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out := fakeSetup(t, "http://n8n.local:5678\nsecret\nprod\nn\nMY_N8N_KEY\n", nil)
	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"init", "--config", path}, out, &errOut), out.String())
	assert.Contains(t, out.String(), "export MY_N8N_KEY=")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "${MY_N8N_KEY}")
	assert.NotContains(t, string(data), "secret")
}

func TestInitConnectionFailureExplainsAndAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out := fakeSetup(t, "http://n8n.local:5678\nbad\n\n", &api.Error{StatusCode: 401, Method: "GET", Path: "/executions"})
	var errOut bytes.Buffer
	assert.Equal(t, 1, run([]string{"init", "--config", path}, out, &errOut))
	assert.Contains(t, out.String(), "rejected the API key")
	assert.Contains(t, out.String(), "Settings → n8n API")
	assert.NoFileExists(t, path)
}

func TestInitCancelled(t *testing.T) {
	out := fakeSetup(t, "", nil)
	var errOut bytes.Buffer
	assert.Equal(t, 1, run([]string{"init", "--config", filepath.Join(t.TempDir(), "c.yaml")}, out, &errOut))
	assert.Contains(t, out.String(), "Setup cancelled")
}

func TestSuggestName(t *testing.T) {
	assert.Equal(t, "local", suggestName("http://localhost:5678"))
	assert.Equal(t, "acme", suggestName("https://n8n.acme.com"))
	assert.Equal(t, "automation", suggestName("https://automation.acme.com"))
	assert.Equal(t, "default", suggestName("http://10.0.0.5:5678"))
	assert.Equal(t, "default", suggestName("http://[fd00::1]:5678"))
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	assert.Equal(t, 2, run([]string{"frobnicate"}, &out, &errOut))
	assert.Contains(t, errOut.String(), "unknown command")
}

func TestInitRefusesNonYAMLPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	out := fakeSetup(t, "", nil)
	var errOut bytes.Buffer
	assert.Equal(t, 1, run([]string{"init", "--config", path}, out, &errOut))
	assert.Contains(t, out.String(), "writes YAML")
	assert.Contains(t, out.String(), "config.yaml")
	assert.NoFileExists(t, path)
}

func TestInitWithExistingConfigWarnsFirstAndHidesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("instances:\n  - {name: a, url: 'http://a', api_key: k}\n"), 0o600))
	out := fakeSetup(t, "http://n8n.local:5678\nsuper-secret-key\nsecond\n\n", nil)
	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"init", "--config", path}, out, &errOut), out.String())
	s := out.String()
	note := strings.Index(s, "will not be changed")
	require.GreaterOrEqual(t, note, 0)
	assert.Less(t, note, strings.Index(s, "n8n URL"), "the user is told before answering anything")
	assert.NotContains(t, s, "super-secret-key", "the key is never echoed")
	assert.Contains(t, s, "api_key: '<your API key>'")
}

func TestInitPreservesFileCreatedDuringSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "instances:\n  - {name: existing, url: 'http://existing', api_key: existing-key}\n"
	var out bytes.Buffer
	e := setupEnv{
		in:         bufio.NewReader(strings.NewReader("http://n8n.local:5678\nsecond\n\n")),
		out:        &out,
		readSecret: func() (string, error) { return "new-secret-key", nil },
		check: func(config.Instance) error {
			// Another setup process or editor saves the config after our first stat.
			return os.WriteFile(path, []byte(original), 0o644)
		},
	}
	result := runSetup(e, path)
	require.Equal(t, 0, result.code, out.String())
	assert.False(t, result.wrote)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.Contains(t, out.String(), "already exists")
	assert.Contains(t, out.String(), "name: 'second'")
	assert.NotContains(t, out.String(), "new-secret-key")
}

func TestInitKeyWithDollarUsesEnvVar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out := fakeSetup(t, "http://n8n.local:5678\nab$cd\nprod\nMY_KEY\n", nil)
	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"init", "--config", path}, out, &errOut), out.String())
	assert.Contains(t, out.String(), "contains a $")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "${MY_KEY}")
	assert.NotContains(t, string(data), "ab$cd")
}

func TestInitForbiddenKeyStillSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	out := fakeSetup(t, "http://n8n.local:5678\nscoped\n\n\n", &api.Error{StatusCode: 403, Method: "GET", Path: "/executions"})
	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"init", "--config", path}, out, &errOut), out.String())
	assert.Contains(t, out.String(), "limited access")
	assert.FileExists(t, path)
}

func TestFirstRunWithUnsetEnvVarDoesNotOpenApp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("N8N_URL", "")
	t.Setenv("FRESH_N8N_KEY", "")
	old := interactive
	interactive = func() bool { return true }
	t.Cleanup(func() { interactive = old })
	out := fakeSetup(t, "y\nhttp://n8n.local:5678\nkey\nprod\nn\nFRESH_N8N_KEY\n", nil)
	var errOut bytes.Buffer
	require.Equal(t, 0, run(nil, out, &errOut), out.String())
	assert.Contains(t, out.String(), "FRESH_N8N_KEY is not set in this shell yet")
	assert.NotContains(t, out.String(), "Open n8n-tui now?")
	assert.FileExists(t, filepath.Join(dir, "n8n-tui", "config.yaml"))
}

func TestEnvConfigErrorDoesNotPointAtMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("N8N_URL", "::bad")
	var out, errOut bytes.Buffer
	assert.Equal(t, 1, run(nil, &out, &errOut))
	assert.NotContains(t, errOut.String(), "Check ")
}

func TestSecretReadsBufferedInputFirst(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, _ = w.WriteString("https://n8n.example.com\nKEY\nname\n")
	w.Close()
	e := defaultSetupEnv(r, io.Discard)
	u, err := e.ask("url", "")
	require.NoError(t, err)
	assert.Equal(t, "https://n8n.example.com", u)
	k, err := e.readSecret()
	require.NoError(t, err)
	assert.Equal(t, "KEY", k)
	n, _ := e.ask("name", "")
	assert.Equal(t, "name", n)
}
