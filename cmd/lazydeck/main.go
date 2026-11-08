// Command lazydeck is a lazydocker-style terminal UI for managing a fleet
// of Steam devkits (Steam Machine, Steam Deck) built on top of Valve's
// steamos-devkit Python client, driven headlessly via `uv run`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/lazydeck/internal/audit"
	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
	"github.com/kevintcoughlin/lazydeck/internal/fixture"
	mcpserver "github.com/kevintcoughlin/lazydeck/internal/mcp"
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
			if hasFlag(os.Args[2:], "--check") {
				if err := runVersionCheck(os.Stdout); err != nil {
					fmt.Fprintln(os.Stderr, "lazydeck:", err)
					os.Exit(1)
				}
				return
			}
			fmt.Println(versionString())
			return
		case "help", "--help", "-h":
			printUsage()
			return
		case "serve":
			if err := runServe(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "lazydeck:", err)
				os.Exit(1)
			}
			return
		case "api-key":
			if err := runAPIKeyCommand(os.Args[2:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return
				}
				fmt.Fprintln(os.Stderr, "lazydeck:", err)
				os.Exit(1)
			}
			return
		case "mcp":
			if err := runMCP(hasFlag(os.Args[2:], "--fixture"), hasFlag(os.Args[2:], "--allow-mutations")); err != nil {
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
	v, c := effectiveBuild()
	return fmt.Sprintf("lazydeck %s (commit %s, built %s by %s, %s/%s, %s)",
		v, c, date, builtBy, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// effectiveVersion is the version `lazydeck version` reports, without the
// rest of the build line.
func effectiveVersion() string {
	v, _ := effectiveBuild()
	return v
}

func effectiveBuild() (v, c string) {
	v, c = version, commit
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
	return v, c
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
  lazydeck serve --fixture
                      Run the API against a fake in-memory devkit backend
                      instead of the real uv/Python/SSH bridge -- for engine
                      integration testing and container-based CI, where no
                      real Steam Deck/Steam Machine is reachable. Shape its
                      behavior with LAZYDECK_FIXTURE_* env vars; see
                      internal/fixture's package doc.
  lazydeck serve --port N
                      Bind loopback port N instead of an ephemeral one, so
                      CI jobs (possibly running as another OS user, which
                      can't read the connection file) can find the server.
  lazydeck serve --listen HOST:PORT
                      Also listen on HOST:PORT (e.g. 0.0.0.0:47600) so other
                      machines on a trusted network can connect. Plain HTTP;
                      accepts API keys only, and refuses to start until an
                      active key exists. See README "Sharing serve".
  lazydeck serve --registry PATH
                      Load the shared device registry at PATH (see
                      LAZYDECK_REGISTRY below).
  lazydeck api-key create --name NAME [--scopes read,deploy] [--expires-in 2160h]
                      [--devices NAME,NAME]
                      Create a long-lived API key for lazydeck serve, e.g.
                      for CI. Prints the key once, on stdout. Scopes: read,
                      discover, pair, deploy, logs:sync, jobs:cancel.
                      --devices limits the key to those devices.
  lazydeck api-key list
                      List API keys (ids, names, scopes; never the keys).
  lazydeck api-key revoke ID
                      Revoke a key; a running serve rejects it immediately.
  lazydeck mcp        Run a Model Context Protocol server over stdio for
                      LLM agents (Claude Desktop, VS Code Copilot, etc.),
                      wrapping the same API as the Godot/Unity integrations.
                      Discovers or auto-starts lazydeck serve as needed.
                      Read-only by default; see docs/mcp.md.
  lazydeck mcp --allow-mutations
                      Also register tools that change device/job state
                      (deploy, pair, sync-logs, cancel-job, launch, stop).
  lazydeck mcp --fixture
                      If lazydeck mcp needs to auto-start lazydeck serve,
                      run it with --fixture (see above).
  lazydeck version    Print version and build metadata.
  lazydeck version --check
                      Also compare against the latest stable release on
                      GitHub (the only command that checks for updates).
  lazydeck help       Show this help.

Environment:
  LAZYDECK_PYTHON_DIR  Path to the bundled python/ runtime (auto-detected
                       from the installed layout or the dev checkout).
  LAZYDECK_UV          Path to the uv executable (auto-detected by default).
  LAZYDECK_SSH_STRICT  Set to 1 to refuse connecting when a devkit's SSH
                       host key changes (default: trust-on-first-use).
  LAZYDECK_AUTOSTART   Set to 0 to disable lazydeck mcp's (and the
                       Godot/Unity integrations') auto-start of
                       lazydeck serve when no instance is running.
  LAZYDECK_BIN         Executable lazydeck mcp auto-starts as
                       lazydeck serve (default: this same binary).
  LAZYDECK_REGISTRY    Shared, read-only device registry (a devices.toml-style
                       file of [[device]] tables, e.g. on a network share or
                       in a git checkout). Overrides devices.toml's registry.
  LAZYDECK_REGISTRY_MODE
                       merge (default: local devices override shared ones by
                       name) or replace (shared devices only).
  LAZYDECK_AUDIT_LOG   lazydeck serve's append-only audit log (default:
                       $XDG_STATE_HOME/lazydeck/audit.log, else
                       ~/.local/state/lazydeck/audit.log).

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
//
// When useFixture is true, the real uv/Python/SSH-backed client is
// replaced with internal/fixture's fake devkit backend, so engine
// integration tests and container-based CI can exercise the full API
// surface (deploy/poll/cancel/log-sync/etc) without uv, Python, SSH,
// rsync, or real Steam Deck/Steam Machine hardware.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	useFixture := fs.Bool("fixture", false, "use the in-memory fake devkit backend")
	port := fs.Int("port", 0, "loopback port to bind (0 = ephemeral)")
	listen := fs.String("listen", "", "also listen on HOST:PORT for API keys only, e.g. 0.0.0.0:47600 (trusted networks only; see README)")
	registry := fs.String("registry", "", "shared device registry to load (overrides $LAZYDECK_REGISTRY and devices.toml's registry)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithOptions(path, config.LoadOptions{Registry: *registry})
	if err != nil {
		return err
	}
	if info, ok := cfg.SharedRegistry(); ok {
		fmt.Printf("lazydeck serve: %s (restart serve to pick up registry changes)\n", info.Summary())
	}
	keysPath, err := config.APIKeysPath()
	if err != nil {
		return err
	}
	auditPath, err := audit.DefaultPath()
	if err != nil {
		return err
	}
	opts := server.Options{Port: *port, Listen: *listen, APIKeysPath: keysPath, AuditLogPath: auditPath}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *useFixture {
		return server.Run(ctx, fixture.NewFromEnv(), cfg, opts)
	}

	cli, err := client.New()
	if err != nil {
		return err
	}
	return server.Run(ctx, cli, cfg, opts)
}

func runAPIKeyCommand(args []string) error {
	path, err := config.APIKeysPath()
	if err != nil {
		return err
	}
	return runAPIKey(args, path, os.Stdout, os.Stderr, time.Now())
}

// hasFlag reports whether name appears verbatim among args, so subcommands
// like `serve` can accept simple boolean flags without pulling in the flag
// package for a single on/off switch.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// runMCP starts `lazydeck mcp`: a Model Context Protocol server (see
// internal/mcp's package doc) exposing lazydeck's devkit operations as
// tools for LLM agents over stdio. It discovers or auto-starts `lazydeck
// serve` the same way the Godot/Unity integrations do, then wraps its /v1
// API as MCP tools; mutating tools (deploy, pair, sync-logs, ...) are only
// registered when allowMutations is set, since an agent calling them is a
// different trust model than a human clicking a button in an editor.
func runMCP(useFixture, allowMutations bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return mcpserver.Run(ctx, mcpserver.Options{
		AllowMutations: allowMutations,
		Version:        versionString(),
		FixtureBackend: useFixture,
	})
}
