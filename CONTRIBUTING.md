# Contributing to lazydeck

lazydeck is a small personal project, but contributions/issues are welcome.

## Development

```bash
just sync    # uv-installs Python deps (paramiko, appdirs, signalslot, ifaddr)
just build   # go build -o lazydeck ./cmd/lazydeck
just test    # go test ./...
just lint    # golangci-lint + ruff if installed, else go vet + py_compile
```

Before opening a PR, please make sure all of the above pass. CI
(`.github/workflows/ci.yml`) runs the same checks on push/PR.

## Project layout

- `cmd/lazydeck/` — the Go entrypoint.
- `internal/tui/` — the Bubble Tea TUI (all keybindings/state live here).
- `internal/client/` — shells out to `python/cli.py` via `uv run` and
  parses its JSON envelope.
- `internal/config/` — loads/saves `~/.config/lazydeck/devices.toml`.
- `python/cli.py` — headless CLI wrapper around the vendored
  `devkit_client` library.
- `python/vendor/devkit_client/` — Valve/Collabora's MIT-licensed
  steamos-devkit client library, vendored as-is (not linted/reformatted —
  see `pyproject.toml`'s `extend-exclude`). Don't hand-edit this unless
  you're intentionally patching vendored code; prefer updating from
  upstream (`flibitijibibo/steamos-devkit`) instead.

## Testing without hardware

Most of the Go side (`internal/tui`, `internal/config`, and the
`internal/client` envelope-parsing/retry logic) is unit-tested as pure
functions and doesn't require a real Steam Deck/Steam Machine. If you're
changing TUI behavior, add a test to `internal/tui/tui_test.go` following
the existing pattern (construct a `tea.KeyMsg`/result message, call
`Model.Update()` directly, assert on the resulting state) rather than
relying on manual interactive testing.

Changes to the actual devkit protocol (pairing, deploy, mDNS discovery
results, etc.) can only be meaningfully verified against real hardware —
please note in your PR description if you were or weren't able to test
against a real device.

## Reporting issues

Please include your OS, `lazydeck` version (or commit SHA), and — if
relevant — the SteamOS version on the device you were targeting.
