package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
	var out, errOut bytes.Buffer
	assert.Equal(t, 1, run(nil, &out, &errOut))
	assert.Contains(t, errOut.String(), "no configuration found")
	assert.Contains(t, errOut.String(), "N8N_API_KEY")
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
