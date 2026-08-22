# lazydeck

[![CI](https://github.com/KevinTCoughlin/lazydeck/actions/workflows/ci.yml/badge.svg)](https://github.com/KevinTCoughlin/lazydeck/actions/workflows/ci.yml)
[![CodeQL](https://github.com/KevinTCoughlin/lazydeck/actions/workflows/codeql.yml/badge.svg)](https://github.com/KevinTCoughlin/lazydeck/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/kevintcoughlin/lazydeck.svg)](https://pkg.go.dev/github.com/kevintcoughlin/lazydeck)
[![Go Report Card](https://goreportcard.com/badge/github.com/kevintcoughlin/lazydeck)](https://goreportcard.com/report/github.com/kevintcoughlin/lazydeck)
[![Latest release](https://img.shields.io/github/v/release/KevinTCoughlin/lazydeck)](https://github.com/KevinTCoughlin/lazydeck/releases)
[![License: MIT](https://img.shields.io/github/license/KevinTCoughlin/lazydeck)](LICENSE)

A lazydocker-style terminal UI for managing a fleet of Steam devkits (Steam
Machine, Steam Deck, ...) — pairing, deploying builds, checking status, and
tailing logs from one keyboard-driven panel, instead of Valve's single-target
PySDL2/imgui GUI.

## Why

Valve ships `steamos-devkit` (the "SteamOS Devkit Client") as a Python +
PySDL2/imgui GUI with no scriptable/headless entrypoint, and no notion of
managing several paired devices from one view. This project:

1. Vendors Valve's MIT-licensed `devkit_client` Python library (see
   `python/vendor/`, from the actively-maintained
   [flibitijibibo/steamos-devkit](https://github.com/flibitijibibo/steamos-devkit)
   fork), which contains all the real pairing / SSH / rsync / mDNS protocol
   logic — reimplementing that from scratch would be wasteful and risky.
2. Adds a small headless CLI (`python/cli.py`) that drives that library and
   prints JSON, since upstream only exposes it through the GUI.
3. Wraps that CLI in a Go + Bubble Tea TUI (`cmd/lazydeck`) that shows one
   panel per configured device and drives `uv run` under the hood.

## Requirements

- Go 1.25+
- [`uv`](https://docs.astral.sh/uv/) (manages the Python 3.10+ venv/deps for you)
- [`just`](https://github.com/casey/just) task runner
- `ssh`/`rsync` available on your machine (standard on macOS)
- optional but recommended: [`golangci-lint`](https://golangci-lint.run/) and
  [`ruff`](https://docs.astral.sh/ruff/) (`brew install golangci-lint ruff`)
  — `just lint` uses them automatically if present, otherwise falls back to
  `go vet`/`py_compile`.

## Installing a pre-built release

Tagged releases (`v*`) are built for macOS and Linux (amd64/arm64) via
[goreleaser](https://goreleaser.com/) — see
[Releases](https://github.com/kevintcoughlin/lazydeck/releases). Each
archive bundles the `lazydeck` binary alongside `python/` so you don't need
to clone the repo. You still need `uv` installed locally; LazyDeck finds the
sibling Python runtime and provisions its locked dependencies into your user
cache on first run.

Install the macOS release with the Homebrew tap:

```bash
brew install kevintcoughlin/lazydeck/lazydeck
```

The formula depends on `uv`, installs the bundled `python/` runtime, and
creates its writable managed environment under Homebrew's `var` directory on
first run. No separate `uv sync` step is needed. The formula currently
supports macOS and Linux on amd64 and arm64.

Alternatively, `install.sh` automates the above (downloads the right
archive for your OS/arch, installs the binary to `~/.local/bin`, and copies
`python/` to `~/.local/share/lazydeck/python`):

```bash
curl -fsSL https://raw.githubusercontent.com/kevintcoughlin/lazydeck/main/install.sh | bash
```

The installer verifies the release checksum, smoke-tests the staged binary and
Python bridge, then atomically replaces the existing installation. Pin a
release or customize its destination without editing the script:

```bash
VERSION=0.2.0 PREFIX="$HOME/.local" ./install.sh
VERSION=0.2.0 INSTALL_DIR="$HOME/bin" LAZYDECK_DATA_DIR="$HOME/lib/lazydeck" ./install.sh
```

Linux releases also include amd64/arm64 Debian packages. They install the
binary, Python runtime, and an architecture-matched pinned `uv`; `openssh-client`
and `rsync` remain normal package dependencies:

```bash
sudo apt install ./lazydeck_0.2.0_linux_amd64.deb
```

Nix users can run LazyDeck without a global install, or enter a development
shell with the project toolchain:

```bash
nix run github:KevinTCoughlin/lazydeck -- version
nix develop github:KevinTCoughlin/lazydeck
```

Release archives, Debian packages, checksums, SBOMs, and GitHub build
provenance are generated from the tagged commit.

## Setup

```bash
git clone <this repo> && cd lazydeck

mise install  # optional: installs the versions pinned in mise.toml
just sync     # installs exactly the dependencies in python/uv.lock
just build  # go build -o lazydeck ./cmd/lazydeck
```

Edit `~/.config/lazydeck/devices.toml` (created for you on first run) to
list your devkits:

```toml
# Optional root-level setting; it must precede the first [[device]] table.
refresh_interval_seconds = 30

[[device]]
name = "steam-machine"
machine = "192.168.1.50"   # hostname, IP, or mDNS service name
login = "deck"             # optional; auto-detected if omitted

[[device]]
name = "steam-deck"
machine = "steamdeck.local"
```

Periodic background status refresh is off by default; omit
`refresh_interval_seconds` to refresh only on startup and with the `s` key.

Don't know the Deck's IP yet? Find it via mDNS/Bonjour (works once the Deck
is on the same Wi-Fi and Developer Mode pairing is enabled):

```bash
just cli discover --timeout 5
```

or press `f` inside the running TUI.

Then run:

```bash
just run    # builds (if needed) and launches the TUI
```

The main screen uses separate **Devices**, **Detail**, and **Activity**
panels. They render side-by-side in wide terminals and stack vertically in
narrow terminals; long fleets are paginated around the current cursor.

### Linux editor-integration service

The Godot and Unity integrations locate `lazydeck serve` through its
session-scoped, permission-restricted connection file at
`$XDG_RUNTIME_DIR/lazydeck/serve.json`. On Debian installations, enable the
optional systemd user service after configuring at least one device:

```bash
systemctl --user enable --now lazydeck-serve.service
systemctl --user status lazydeck-serve.service
```

It is intentionally **not** enabled at package-install time: the service uses
your devkit configuration and local SSH trust state. Stop it with
`systemctl --user disable --now lazydeck-serve.service`; the interactive TUI
does not require it. Archive, Homebrew, and Nix users can copy
`packaging/systemd/user/lazydeck-serve.service` and replace its `ExecStart`
path with their installed `lazydeck` binary.

### Launching deployed titles

LazyDeck deploys and registers games with Steam, but does not remotely launch
or stop them. The supported SteamOS devkit protocol has no launch/stop
primitive, so start and stop a deployed title from the device's Steam UI.
`/v1/capabilities` intentionally reports both operations as unavailable; see
[the launch policy](docs/DEVICE_LAUNCH.md) for the rationale.

### Custom keybindings/commands (config.yml)

Optionally edit `~/.config/lazydeck/config.yml` (created for you on first
run, commented out) to bind extra keys to arbitrary shell commands run
against the selected device(s) — lazygit-style, without forking lazydeck.
This file composes with `devices.toml`; it does not replace it.

```yaml
customCommands:
  - key: "p"
    name: "ping device"
    command: "ping -c 3 {{.Machine}}"
  - key: "u"
    name: "uptime"
    command: "ssh {{.Login}}@{{.Machine}} uptime"
```

Commands run via `sh -c` and may reference `{{.Name}}`, `{{.Machine}}`, and
`{{.Login}}` from the targeted device. LazyDeck passes those values as
positional shell arguments rather than source text, so device fields cannot
inject additional shell commands. Like `d`/`l`/`x`, a custom command runs against
every multi-selected device (`space`) if any are selected, otherwise just
the one under the cursor. Keys that collide with a built-in binding (see
the table below) are ignored so custom commands can never shadow lazydeck's
own behavior; the custom binding also shows up in the `?` help screen.

### Recording a real-device demo

From the repository root, with a paired Deck configured in
`~/.config/lazydeck/devices.toml`, record a 100×30 terminal session with:

```bash
mkdir -p recordings && asciinema rec --overwrite --cols 100 --rows 30 \
  --title "lazydeck Steam Deck demo" --command "just run" \
  recordings/lazydeck-steam-deck.cast
```

Use this non-destructive sequence (wait for each refresh to finish):

1. `s` — refresh the configured device status.
2. `?`, then any key — show and close the keybinding help.
3. `g` — list deployed games.
4. `f` — run LAN discovery and show the result in the log.
5. `q` — quit and let asciinema save the recording.

Before sharing, inspect the cast for hostnames, IP addresses, usernames, game
IDs, paths, or error text. Use a sanitized `name` in `devices.toml`, do not
press `d`, `x`, or `enter` during the demo, and remove the local cast if it
contains private output. Recordings under `recordings/` are ignored by git.

### Other recipes

```bash
just            # list all recipes
just test       # Go + complete Python unit test suites
just lint       # Go, Ruff, and shell lint
just check      # CI-equivalent race/vet/test/lock checks
just snapshot   # GoReleaser check plus local release snapshot
just cli status --machine 192.168.1.50   # call the headless python CLI directly
just clean      # remove the built binary and __pycache__ dirs
```

### Development/test container

`Containerfile` is a pinned, reproducible Linux development and test
environment; it is not a runtime service image. LazyDeck is an interactive,
host-network terminal application, so publishing it as an OCI service would
misrepresent its operating model.

```bash
just container-test
```

## Keybindings

| Key       | Action                                                    |
|-----------|-------------------------------------------------------------|
| `↑`/`k`   | previous device                                              |
| `↓`/`j`   | next device                                                  |
| mouse     | click a device to select it, scroll wheel to move the cursor |
| `/`       | fuzzy-filter the device list by name/machine (`esc` clears)  |
| `space`   | toggle multi-select (batches `d`/`l`/`x` across the selection)|
| `s`       | refresh status for all devices                               |
| `r`       | register/pair the selected device                            |
| `d`       | deploy — prompts for gameid, then local build directory      |
| `l`       | sync-logs — prompts for gameid, then local directory to save |
| `x`       | delete a previously deployed title — prompts for gameid, then a `y`/`n` confirmation (no undo) |
| `g`       | list games currently deployed on the selected device          |
| `f`       | find devkits on the LAN via mDNS/Bonjour (~4s scan, logs only)|
| `a`       | add-device wizard — discover, pick, persist to devices.toml, register |
| `enter`   | open a real interactive `ssh` shell on the selected device    |
| `?`       | toggle the full keybinding help screen                       |
| `esc`     | cancel an in-progress prompt / wizard                        |
| `q`       | quit                                                         |

Selecting one or more devices with `space` before pressing `d`/`l`/`x` runs
that operation across every selected device at once — the lazydocker-style
"batch operation on the fleet" workflow.

## How it talks to devices

`internal/client.Client.run` shells out to:

```bash
uv run --project python python cli.py <subcommand> --machine <host> [...]
```

`cli.py` imports the vendored `devkit_client` package and calls the same
functions Valve's GUI calls (`register`, `steamos_get_status`, `list_games`,
`new_or_ensure_game`, `sync_logs`, `delete_title`), each wrapped to emit a
single JSON envelope (`{"ok": true, "data": ...}` or
`{"ok": false, "error": ..., "error_kind": ...}`) that the Go side parses.

### Architecture

```
┌─────────────────────────┐        ┌──────────────────────────────┐
│  Go TUI (cmd/lazydeck)   │        │  Steam Deck / Steam Machine   │
│  Bubble Tea + lipgloss   │        │  (SteamOS, Developer Mode)    │
│                          │        │                                │
│  internal/tui  ────────► │        │  steamos-devkit-client         │
│  internal/client ─┐      │        │  (paired via HTTP, port 32000) │
└────────────────────┼─────┘        └───────────────┬────────────────┘
                     │  `uv run python cli.py <cmd>` │
                     ▼                               │
        ┌─────────────────────────┐                  │
        │ python/cli.py            │                  │
        │ (headless JSON wrapper)  │                  │
        │                          │                  │
        │ python/vendor/           │   HTTP (pair)    │
        │  devkit_client ──────────┼──────────────────┤
        │  (Valve/Collabora, MIT)  │   SSH (paramiko)  │
        │                          │──────────────────┤
        │                          │   rsync (subproc) │
        │                          │──────────────────┤
        │                          │   mDNS/Bonjour    │
        │                          │◄─────────────────┘
        └─────────────────────────┘   (_steamos-devkit._tcp.local.)
```

The Go side never speaks HTTP/SSH/rsync/mDNS itself — it only shells out to
`cli.py`, which is a thin argparse wrapper around the same vendored library
Valve's own GUI uses. This keeps the actual pairing/deploy protocol logic
in one well-tested place instead of being reimplemented in Go.

### SSH host-key trust

The SteamOS pairing protocol does not authenticate an SSH host key out of band.
LazyDeck records first-seen keys in a dedicated steamos-devkit `known_hosts`
file, separate from your normal OpenSSH database. Unknown keys are enrolled on
first use. Changed keys warn by default so re-imaged devkits remain usable; set
`LAZYDECK_SSH_STRICT=1` to reject changed keys. Verify changes out of band and
pair only on a trusted LAN. See [SECURITY.md](SECURITY.md).

## Engine integrations

`lazydeck serve` (see [issue #13](https://github.com/kevintcoughlin/lazydeck/issues/13))
exposes the same devkit operations above through a versioned, loopback-only
HTTP+SSE API, so engine editors can drive them without shelling out to
`lazydeck` or reimplementing the SteamOS Devkit protocol. Browse the
[rendered API docs](https://kevintcoughlin.com/lazydeck/api/) or read
[`api/openapi.yaml`](api/openapi.yaml) directly for the contract.

Two editor integrations are built on that API, both covering the same
operations — discover, pair, and inspect devkits, build and deploy the
current project, and sync logs, without leaving the editor:

- A Godot 4 editor plugin (Godot 4.3+):
  [`integrations/godot`](integrations/godot).
- A Unity Editor package (Unity 2023.1 or newer, including Unity 6):
  [`integrations/unity`](integrations/unity).

Run `lazydeck serve`, then enable the plugin or add the package. Each
directory's README covers current scope, engine-specific build behavior,
and a runnable example project.

### MCP server for LLM agents

`lazydeck mcp` exposes the same `/v1` API as a third client, this time for
LLM agents (Claude Desktop, VS Code Copilot, etc.) over the [Model Context
Protocol](https://modelcontextprotocol.io), using the official
[`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk).
It discovers or auto-starts `lazydeck serve` exactly like the Godot/Unity
integrations. Read-only tools (list devices, discover, status, games, job
status) are always available; tools that change device or job state
(deploy, pair, sync logs, cancel, launch/stop) are opt-in via
`--allow-mutations`, since an agent calling those is a different trust
model than a human clicking a button in an editor. See
[`docs/mcp.md`](docs/mcp.md) for setup and configuration.

## Troubleshooting

- **"could not locate a complete Python runtime"** — set `LAZYDECK_PYTHON_DIR`
  to point at the `python/` directory (a pre-built release archive bundles
  it as a sibling of the binary; a dev checkout resolves it automatically).
- **"uv is required"** — install `uv`, or point `LAZYDECK_UV` at an executable.
  Debian packages include their own copy automatically.
- **`uv run` fails with a lock/package error** — from a checkout run
  `uv sync --frozen --project python`; release layouts are provisioned from
  their bundled lockfile automatically.
- **Device shows "offline / unpaired" after `s`** — press `r` to
  (re-)register your workstation's SSH key with it first; devices must be
  paired via the same Developer Mode pairing flow the official GUI uses.
- **`f`/`a` (mDNS discover) finds nothing** — confirm the Deck/Steam
  Machine is on the *same* Wi-Fi network/subnet as your Mac (mDNS doesn't
  cross routed subnets or most VPNs), and that Developer Mode + pairing
  are enabled on the device. A bare USB-C cable to the Deck does **not**
  expose a network interface on retail SteamOS — you need Wi-Fi or a
  USB-C-to-Ethernet adapter (see Valve's own devkit docs).
- **A device row turns yellow/orange** — that's an `auth-failed` or
  `invalid-input` error (see `error_kind` in the CLI's JSON, surfaced in
  the TUI's status color); red means `unreachable` or an unexpected
  script error. Check the log pane at the bottom of the TUI for the full
  message.
- **`ssh` (via `enter`) fails immediately** — the resolved key lives at
  the path reported by `connection-info`; make sure it wasn't deleted or
  regenerated outside of lazydeck/the official GUI.

## License note

`python/vendor/devkit_client` is Valve/Collabora's code, MIT-licensed — see
`python/vendor/LICENSE-steamos-devkit`. Its bundled python-zeroconf copy is
LGPL-2.1 and includes the complete license at
`python/vendor/devkit_client/zeroconf/COPYING`. Go and packaged uv attribution
is recorded in `THIRD_PARTY_GO.md` and `NOTICE`.
