// Command n8n-tui is a terminal user interface for n8n instances.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// Replaced in tests.
var (
	stdin       = os.Stdin
	newSetupEnv = func(out io.Writer) setupEnv { return defaultSetupEnv(stdin, out) }
	interactive = func() bool { return term.IsTerminal(stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `n8n-tui: a keyboard-driven terminal UI for n8n.

Usage:
  n8n-tui [flags]          open the TUI
  n8n-tui init [flags]     set up a connection to an n8n instance

Examples:
  n8n-tui init                              first-time setup (asks for URL and API key)
  n8n-tui                                   connect to the default instance
  n8n-tui --instance staging                connect to another configured instance
  N8N_URL=http://localhost:5678 N8N_API_KEY=... n8n-tui
                                            connect without a config file

Flags:
`

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("n8n-tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file to use (default "+config.DefaultPath()+")")
	instance := fs.String("instance", "", "name of the instance to connect to (default: default_instance from the config)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	printExample := fs.Bool("example-config", false, "print an example config file and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
		fmt.Fprint(stderr, "\nInside the app, press ? for help or ctrl+p to search actions.\n"+
			"Docs: https://github.com/kaypee90/n8n-tui#readme\n")
	}

	// Accept flags before or after the "init" command.
	setup := false
	if len(args) > 0 && args[0] == "init" {
		setup, args = true, args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if rest := fs.Args(); len(rest) > 0 {
		if rest[0] == "init" && !setup {
			setup, rest = true, rest[1:]
		}
		if len(rest) > 0 {
			fmt.Fprintf(stderr, "n8n-tui: unknown command %q (run n8n-tui -h for help)\n", rest[0])
			return 2
		}
	}

	path := *configPath
	if path == "" {
		path = config.DefaultPath()
	}
	switch {
	case *showVersion:
		fmt.Fprintln(stdout, "n8n-tui", version)
		return 0
	case *printExample:
		fmt.Fprint(stdout, config.Example)
		return 0
	case setup:
		return runSetup(newSetupEnv(stdout), path).code
	}

	cfg, err := config.Load(*configPath)
	if errors.Is(err, config.ErrNoConfig) {
		if interactive() {
			fmt.Fprintf(stdout, "No n8n-tui configuration found at %s.\n\n", path)
			env := newSetupEnv(stdout)
			if ok, _ := env.confirm("Set one up now?", true); ok {
				fmt.Fprintln(stdout)
				res := runSetup(env, path)
				if res.code != 0 {
					return res.code
				}
				if res.envVar != "" && os.Getenv(res.envVar) == "" {
					// This process can't see an export the user runs now.
					fmt.Fprintf(stdout, "%s is not set in this shell yet. After exporting it, start the app with:\n\n  n8n-tui\n", res.envVar)
					return 0
				}
				fmt.Fprintln(stdout)
				if ok, _ := env.confirm("Open n8n-tui now?", true); !ok {
					return 0
				}
				cfg, err = config.Load(*configPath)
			} else {
				printNoConfig(stdout, path)
				return 1
			}
		} else {
			printNoConfig(stderr, path)
			return 1
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "n8n-tui: %v\n", err)
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Fprintf(stderr, "\nCheck %s (n8n-tui --example-config prints a valid example).\n", path)
		}
		return 1
	}

	if *instance != "" {
		if _, ok := cfg.Instance(*instance); !ok {
			names := make([]string, len(cfg.Instances))
			for i, in := range cfg.Instances {
				names[i] = in.Name
			}
			fmt.Fprintf(stderr, "n8n-tui: instance %q is not configured. Configured instances: %s\n",
				*instance, strings.Join(names, ", "))
			return 1
		}
	}

	app, err := tui.New(cfg, *instance, nil)
	if err != nil {
		fmt.Fprintf(stderr, "n8n-tui: %v\n", err)
		return 1
	}

	if _, err := tea.NewProgram(app, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(stderr, "n8n-tui: %v\n", err)
		return 1
	}
	return 0
}

func printNoConfig(w io.Writer, path string) {
	fmt.Fprintf(w, `n8n-tui: no configuration found.

The quickest way to get started is the setup wizard:

  n8n-tui init

Or connect without a config file:

  N8N_URL=https://n8n.example.com N8N_API_KEY=<your key> n8n-tui

Or write %s yourself. This prints a commented example:

  n8n-tui --example-config

Create an API key in n8n under Settings → n8n API.
`, path)
}
