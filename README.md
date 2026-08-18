# lazydeck

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
to clone the repo, but you still need `uv` installed locally and must run
`uv sync --project python` once after extracting, since the Python
dependencies (`paramiko` et al.) aren't vendored into the binary.

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

## Setup

```bash
git clone <this repo> && cd lazydeck

just sync   # one-time (and after pulling python/pyproject.toml changes):
            # uv-installs paramiko/appdirs/etc.
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
just test       # go test ./...
just lint       # gofmt -l . && go vet ./... && python3 -m py_compile ...
just cli status --machine 192.168.1.50   # call the headless python CLI directly
just clean      # remove the built binary and __pycache__ dirs
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

## Troubleshooting

- **"could not locate the python/ directory"** — set `LAZYDECK_PYTHON_DIR`
  to point at the `python/` directory (a pre-built release archive bundles
  it as a sibling of the binary; a dev checkout resolves it automatically).
- **`uv run` fails with a missing-package error** — run `just sync` (or
  `uv sync --project python` if you installed a release archive) to
  install `paramiko`/`appdirs`/`signalslot`/`ifaddr` into the managed venv.
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
`python/vendor/LICENSE-steamos-devkit`. Everything else in this repo is
original.
