package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/config"
)

// setupEnv holds the side effects of the setup wizard so tests can replace them.
type setupEnv struct {
	in  *bufio.Reader
	out io.Writer
	// readSecret reads the API key without echoing it.
	readSecret func() (string, error)
	// check verifies that the instance accepts the credentials.
	check func(config.Instance) error
}

func defaultSetupEnv(stdin *os.File, out io.Writer) setupEnv {
	in := bufio.NewReader(stdin)
	return setupEnv{
		in:  in,
		out: out,
		readSecret: func() (string, error) {
			// Input typed or pasted ahead of the prompt is already in the
			// buffer, where ReadPassword (which reads the fd) cannot see it.
			if in.Buffered() == 0 && term.IsTerminal(stdin.Fd()) {
				b, err := readSetupPassword(stdin.Fd())
				fmt.Fprintln(out)
				return string(b), err
			}
			return readLine(in)
		},
		check: checkInstance,
	}
}

// ReadPassword restores echo on return, but a terminating signal skips its
// deferred cleanup. Keep a restoration handler active only during the read.
func readSetupPassword(fd uintptr) ([]byte, error) {
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case sig := <-interrupts:
			_ = term.Restore(fd, state)
			os.Exit(128 + int(sig.(syscall.Signal)))
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(interrupts)
		close(done)
		<-stopped
	}()
	return term.ReadPassword(fd)
}

func checkInstance(in config.Instance) error {
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultTimeout)
	defer cancel()
	_, err := api.New(in, config.DefaultTimeout).ListExecutions(ctx, api.ExecutionFilter{Limit: 1})
	return err
}

func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (e setupEnv) ask(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(e.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(e.out, "%s: ", question)
	}
	s, err := readLine(e.in)
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

func (e setupEnv) confirm(question string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	s, err := e.ask(question+" ("+hint+")", "")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(s) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// setupResult reports how the wizard ended.
type setupResult struct {
	code int
	// wrote is true when a new config file was saved.
	wrote bool
	// envVar names the environment variable that must hold the API key, or
	// "" when the key is stored in the file.
	envVar string
}

// runSetup asks for an instance URL and API key, checks them and writes a
// config file. It never overwrites an existing config.
func runSetup(e setupEnv, path string) setupResult {
	fail := setupResult{code: 1}
	switch ext := filepath.Ext(path); strings.ToLower(ext) {
	case ".json", ".toml":
		fmt.Fprintf(e.out, "✖ n8n-tui init writes YAML, but %s has a %s extension.\n"+
			"  Use a .yaml path instead, e.g. --config %s\n", path, ext, strings.TrimSuffix(path, ext)+".yaml")
		return fail
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil

	fmt.Fprint(e.out, `n8n-tui setup
=============

This connects n8n-tui to your n8n instance. You need two things:

  1. The address you open n8n at in your browser, e.g. https://n8n.example.com
     (for a local Docker install it is usually http://localhost:5678).
  2. An n8n API key. In n8n, open Settings → n8n API → "Create an API key".

`)
	if exists {
		fmt.Fprintf(e.out, "Note: %s already exists and will not be changed.\n"+
			"At the end you get the lines to add to it yourself.\n\n", path)
	}
	rawURL, err := e.ask("n8n URL", "http://localhost:5678")
	if err != nil {
		return fail.abort(e, err)
	}
	in := config.Instance{URL: rawURL}
	in.URL = in.BaseURL() // strip a trailing / or /api/v1
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Contains(in.URL, "$") {
		fmt.Fprintf(e.out, "\n✖ %q is not a valid URL. It should look like https://n8n.example.com.\n", rawURL)
		return fail
	}

	fmt.Fprint(e.out, "API key (input is hidden): ")
	in.APIKey, err = e.readSecret()
	if err != nil {
		return fail.abort(e, err)
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if in.APIKey == "" {
		fmt.Fprint(e.out, "\n✖ An API key is required. Create one in n8n under Settings → n8n API, then run `n8n-tui init` again.\n")
		return fail
	}

	fmt.Fprintf(e.out, "\nChecking %s … ", in.URL)
	var apiErr *api.Error
	if err := e.check(in); errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		// The key works but lacks some scopes: the app still runs, and the
		// affected views say what is not allowed.
		fmt.Fprint(e.out, "connected, with limited access.\n\n"+
			"! This API key can't list executions, so some views will show \"not allowed\".\n"+
			"  For full access use a key from an owner/admin account or with more scopes.\n\n")
	} else if err != nil {
		p := api.Explain(err)
		fmt.Fprintf(e.out, "failed.\n\n✖ %s\n", p.Summary)
		if p.Hint != "" {
			fmt.Fprintf(e.out, "  %s\n", p.Hint)
		}
		fmt.Fprintln(e.out)
		ok, err := e.confirm("Save this configuration anyway?", false)
		if err != nil || !ok {
			fmt.Fprintln(e.out, "Nothing was saved. Fix the problem above and run `n8n-tui init` again.")
			return fail
		}
	} else {
		fmt.Fprint(e.out, "connected ✔\n\n")
	}

	in.Name, err = e.ask("Name for this instance", suggestName(in.URL))
	if err != nil {
		return fail.abort(e, err)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || strings.Contains(in.Name, "$") {
		fmt.Fprintf(e.out, "✖ %q can't be used as a name. Use letters, digits and dashes, e.g. prod.\n", in.Name)
		return fail
	}

	keyValue := in.APIKey
	envVar := ""
	inFile := false
	if strings.Contains(in.APIKey, "$") {
		// The config loader expands $VAR in values, which would corrupt the key.
		fmt.Fprint(e.out, "This key contains a $, which can't be stored in the config file,\n"+
			"so n8n-tui will read it from an environment variable instead.\n")
	} else {
		inFile, err = e.confirm("Store the API key in the config file? It will be readable only by your user", true)
		if err != nil {
			return fail.abort(e, err)
		}
	}
	if !inFile {
		envVar, err = e.ask("Environment variable that will hold the key", "N8N_API_KEY")
		if err != nil {
			return fail.abort(e, err)
		}
		if !envName.MatchString(envVar) {
			fmt.Fprintf(e.out, "✖ %q is not a valid environment variable name.\n", envVar)
			return fail
		}
		keyValue = "${" + envVar + "}"
	}

	if exists {
		return e.existingConfig(path, in, envVar)
	}

	block := instanceYAML(in.Name, in.URL, keyValue)

	content := "# n8n-tui configuration. Created by `n8n-tui init`.\n" +
		"# Reference: https://github.com/kaypee90/n8n-tui#configuration\n\n" +
		"instances:\n" + block +
		"\ndefault_instance: " + yamlString(in.Name) + "\n" +
		"refresh_interval: 5s   # how often views and the log stream poll n8n\n" +
		"timeout: 10s           # how long to wait for each request\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fail.abort(e, err)
	}
	// The file may have appeared while the user was answering the prompts.
	// Exclusive creation also avoids following a newly created symlink.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return e.existingConfig(path, in, envVar)
	}
	if err != nil {
		return fail.abort(e, err)
	}
	_, writeErr := f.WriteString(content)
	closeErr := f.Close()
	if writeErr != nil {
		return fail.abort(e, writeErr)
	}
	if closeErr != nil {
		return fail.abort(e, closeErr)
	}

	fmt.Fprintf(e.out, "\n✔ Saved %s\n\n", path)
	if envVar != "" {
		printExportHint(e.out, envVar)
	}
	command := "n8n-tui"
	if filepath.Clean(path) != filepath.Clean(config.DefaultPath()) {
		// Quote for the same POSIX shells used by the export instructions.
		command += " --config '" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
	}
	fmt.Fprintf(e.out, "Start n8n-tui with:\n\n  %s\n\nPress ? inside the app at any time to see every shortcut.\n", command)
	return setupResult{wrote: true, envVar: envVar}
}

func (e setupEnv) existingConfig(path string, in config.Instance, envVar string) setupResult {
	// Never echo the key itself: terminal scrollback and logs keep it.
	shown := "<your API key>"
	if envVar != "" {
		shown = "${" + envVar + "}"
	}
	fmt.Fprintf(e.out, "\n%s already exists, so it was left unchanged.\n"+
		"To add this instance, paste the following under `instances:` in that file:\n\n%s\n",
		path, instanceYAML(in.Name, in.URL, shown))
	if envVar == "" {
		fmt.Fprint(e.out, "Replace <your API key> with the key you entered.\n")
	} else {
		printExportHint(e.out, envVar)
	}
	return setupResult{envVar: envVar}
}

func printExportHint(w io.Writer, envVar string) {
	fmt.Fprintf(w, "Before starting n8n-tui, set the key in your shell (add it to ~/.zshrc or ~/.bashrc to keep it):\n\n"+
		"  export %s='<your API key>'\n\n", envVar)
}

func (r setupResult) abort(e setupEnv, err error) setupResult {
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(e.out, "\nSetup cancelled. Nothing was saved.")
	} else {
		fmt.Fprintf(e.out, "\n✖ Setup failed: %v\n", err)
	}
	return r
}

func instanceYAML(name, rawURL, key string) string {
	return fmt.Sprintf("  - name: %s\n    url: %s\n    api_key: %s\n", yamlString(name), yamlString(rawURL), yamlString(key))
}

// yamlString quotes s so it is always read back verbatim.
func yamlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// suggestName derives a short instance name from its URL.
func suggestName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "default"
	}
	host := u.Hostname()
	switch {
	case host == "localhost" || host == "127.0.0.1" || host == "::1":
		return "local"
	case host == "" || net.ParseIP(host) != nil:
		return "default"
	}
	parts := strings.Split(host, ".")
	if len(parts) > 2 && (parts[0] == "n8n" || parts[0] == "www") {
		return parts[1]
	}
	return parts[0]
}
