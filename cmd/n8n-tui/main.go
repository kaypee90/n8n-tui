// Command n8n-tui is a terminal user interface for n8n instances.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("n8n-tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config file (default "+config.DefaultPath()+")")
	instance := fs.String("instance", "", "instance name to connect to (default: default_instance)")
	showVersion := fs.Bool("version", false, "print version and exit")
	printExample := fs.Bool("example-config", false, "print an example config file and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: n8n-tui [flags]\n\nA terminal UI for n8n.\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nWithout a config file, N8N_URL and N8N_API_KEY are used.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	switch {
	case *showVersion:
		fmt.Fprintln(stdout, "n8n-tui", version)
		return 0
	case *printExample:
		fmt.Fprint(stdout, config.Example)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if errors.Is(err, config.ErrNoConfig) {
		fmt.Fprintf(stderr, "n8n-tui: no configuration found.\n\n"+
			"Create %s, for example:\n\n%s\n"+
			"or set N8N_URL and N8N_API_KEY.\n", config.DefaultPath(), config.Example)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "n8n-tui: %v\n", err)
		return 1
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
