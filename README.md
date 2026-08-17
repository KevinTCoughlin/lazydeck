# devkit-tui

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
3. Wraps that CLI in a Go + Bubble Tea TUI (`cmd/devkit-tui`) that shows one
   panel per configured device and drives `uv run` under the hood.

## Requirements

- Go 1.23+
- [`uv`](https://docs.astral.sh/uv/) (manages the Python 3.10+ venv/deps for you)
- [`just`](https://github.com/casey/just) task runner
- `ssh`/`rsync` available on your machine (standard on macOS)

## Setup

```bash
git clone <this repo> && cd devkit-tui

just sync   # one-time (and after pulling python/pyproject.toml changes):
            # uv-installs paramiko/appdirs/etc.
just build  # go build -o devkit-tui ./cmd/devkit-tui
```

Edit `~/.config/devkit-tui/devices.toml` (created for you on first run) to
list your devkits:

```toml
[[device]]
name = "steam-machine"
machine = "192.168.1.50"   # hostname, IP, or mDNS service name
login = "deck"             # optional; auto-detected if omitted

[[device]]
name = "steam-deck"
machine = "steamdeck.local"
```

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
| `s`       | refresh status for all devices                               |
| `r`       | register/pair the selected device                            |
| `d`       | deploy — prompts for gameid, then local build directory      |
| `l`       | sync-logs — prompts for gameid, then local directory to save |
| `x`       | delete a previously deployed title — prompts for gameid      |
| `g`       | list games currently deployed on the selected device          |
| `f`       | find devkits on the LAN via mDNS/Bonjour (~4s scan)          |
| `enter`   | open a real interactive `ssh` shell on the selected device    |
| `esc`     | cancel an in-progress prompt                                 |
| `q`       | quit                                                         |

## How it talks to devices

`internal/client.Client.run` shells out to:

```bash
uv run --project python python cli.py <subcommand> --machine <host> [...]
```

`cli.py` imports the vendored `devkit_client` package and calls the same
functions Valve's GUI calls (`register`, `steamos_get_status`, `list_games`,
`new_or_ensure_game`, `sync_logs`, `delete_title`), each wrapped to emit a
single JSON envelope (`{"ok": true, "data": ...}` or `{"ok": false, "error": ...}`)
that the Go side parses.

## License note

`python/vendor/devkit_client` is Valve/Collabora's code, MIT-licensed — see
`python/vendor/LICENSE-steamos-devkit`. Everything else in this repo is
original.
