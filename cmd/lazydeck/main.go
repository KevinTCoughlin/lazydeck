// Command lazydeck is a lazydocker-style terminal UI for managing a fleet
// of Steam devkits (Steam Machine, Steam Deck) built on top of Valve's
// steamos-devkit Python client, driven headlessly via `uv run`.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
	"github.com/kevintcoughlin/lazydeck/internal/server"
	"github.com/kevintcoughlin/lazydeck/internal/tui"
)

// Build metadata, overridden at release time via -ldflags "-X main.version=..."
// (see .goreleaser.yml). Defaults keep `go build`/`go run` working locally;
// buildInfoVersion() fills in a sensible value for `go install module@version`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println(versionString())
			return
		case "help", "--help", "-h":
			printUsage()
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintln(os.Stderr, "lazydeck:", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazydeck:", err)
		os.Exit(1)
	}
}

// versionString renders the human-readable version/build line printed by
// `lazydeck version`. It prefers ldflags-injected values and falls back to
// Go module build info so `go install .../lazydeck@vX.Y.Z` still reports a
// real version instead of "dev".
func versionString() string {
	v := version
	c := commit
	if v == "dev" {
		if bv, bc, ok := buildInfoVersion(); ok {
			if bv != "" {
				v = bv
			}
			if bc != "" {
				c = bc
			}
		}
	}
	return fmt.Sprintf("lazydeck %s (commit %s, built %s by %s, %s/%s, %s)",
		v, c, date, builtBy, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func buildInfoVersion() (ver, rev string, ok bool) {
	info, available := debug.ReadBuildInfo()
	if !available {
		return "", "", false
	}
	ver = info.Main.Version
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			rev = s.Value
		}
	}
	return ver, rev, true
}

func printUsage() {
	fmt.Print(`lazydeck - a terminal UI for managing a fleet of Steam devkits.

Usage:
  lazydeck            Launch the interactive TUI.
  lazydeck serve      Run the local engine-integration API (see issue #13).
  lazydeck version    Print version and build metadata.
  lazydeck help       Show this help.

Environment:
  LAZYDECK_PYTHON_DIR  Path to the bundled python/ runtime (auto-detected
                       from the installed layout or the dev checkout).
  LAZYDECK_UV          Path to the uv executable (auto-detected by default).
  LAZYDECK_SSH_STRICT  Set to 1 to refuse connecting when a devkit's SSH
                       host key changes (default: trust-on-first-use).

See README.md for configuration and the LAN SSH trust model.
`)
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

// runServe starts the local engine-integration API (issue #13) and blocks
// until it shuts down or is interrupted. Config is loaded once at startup,
// matching run()'s behavior for the TUI: the device list is not expected to
// change out from under a running server in this first slice.
func runServe() error {
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return server.Run(ctx, cli, cfg)
}
