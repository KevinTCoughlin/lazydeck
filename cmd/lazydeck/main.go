// Command lazydeck is a lazydocker-style terminal UI for managing a fleet
// of Steam devkits (Steam Machine, Steam Deck) built on top of Valve's
// steamos-devkit Python client, driven headlessly via `uv run`.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
	"github.com/kevintcoughlin/lazydeck/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazydeck:", err)
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

	userCfgPath, err := config.UserConfigPath()
	if err != nil {
		return err
	}
	userCfg, err := config.LoadUserConfig(userCfgPath)
	if err != nil {
		return err
	}

	m := tui.NewWithUserConfig(cli, cfg, path, userCfg)
	p := tea.NewProgram(m, tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}
