# Contributing to lazydeck

lazydeck is a small personal project, but contributions/issues are welcome.

## Development

```bash
mise install # optional, installs the versions pinned in mise.toml
just sync    # uv sync --frozen from the committed lockfile
just build   # go build -o lazydeck ./cmd/lazydeck
just test    # complete Go and Python unit suites
just lint    # golangci-lint/go vet, Ruff, gofmt, and ShellCheck
just check   # CI-equivalent race, vet, lock, lint, and test checks
```

Do not regenerate dependencies without committing the resulting `python/uv.lock`.
Before opening a PR, run `just check`. CI repeats the checks on Linux and macOS,
validates GoReleaser, and runs the test container.

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
- `Containerfile` — pinned Linux development/test environment, not a runtime
  service image.
- `.goreleaser.yml` and `packaging/` — release archives, Debian packages,
  checksums, SBOM inputs, and bundled uv provisioning.

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

## Release and license maintenance

Run `goreleaser check` after changing `.goreleaser.yml`. A snapshot release
(`just snapshot`) downloads the pinned uv release for both Linux package
architectures and verifies upstream checksums. Re-run
`scripts/generate-notices.sh` after changing Go dependencies and include the
updated `THIRD_PARTY_GO.md`. Do not edit generated notices manually.

Report vulnerabilities privately as described in `SECURITY.md`.

## Reporting issues

Please include your OS, `lazydeck` version (or commit SHA), and — if
relevant — the SteamOS version on the device you were targeting.
