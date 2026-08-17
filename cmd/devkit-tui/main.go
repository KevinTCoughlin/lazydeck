// Command devkit-tui is a lazydocker-style terminal UI for managing a fleet
// of Steam devkits (Steam Machine, Steam Deck) built on top of Valve's
// steamos-devkit Python client, driven headlessly via `uv run`.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/devkit-tui/internal/client"
	"github.com/kevintcoughlin/devkit-tui/internal/config"
	"github.com/kevintcoughlin/devkit-tui/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "devkit-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	cli, err := client.New()
	if err != nil {
		return err
	}

	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	m := tui.New(cli, cfg)
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}
