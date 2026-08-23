# Changelog

All notable changes to lazydeck are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Investigated sending `steam://` commands (e.g. `rungameid`) over the local
  IPC pipe used by `steam-client-create-shortcut` as a launch/stop primitive;
  found not viable (undocumented, closed-source protocol with real risk of
  destabilizing the Steam client). `launch_game`/`stop_game` remain permanent
  `501 unsupported` stubs; see docs/DEVICE_LAUNCH.md for details.

## [0.2.0] - 2026-08-23

### Added
- `lazydeck version` / `--version` command reporting build metadata (version,
  commit, build date, builder, platform), injected via `-ldflags` at release
  time and falling back to Go build info for `go install`.
- Persistent, dedicated trust-on-first-use SSH host-key handling for devkits
  (a separate `known_hosts` file), replacing the silent accept-any policy.
  Opt into strict rejection of changed keys with `LAZYDECK_SSH_STRICT=1`.
- Debian packages (`.deb`) for linux/amd64 and linux/arm64 that bundle the
  binary, Python runtime, and checksum-verified pinned uv, built via
  GoReleaser/nFPM.
- Software Bill of Materials (SBOM) and reproducible third-party license
  notices (`NOTICE`, `THIRD_PARTY_GO.md`) shipped in release archives.
- Reproducible development/test container (`Containerfile` + `.dockerignore`).
- `lazydeck mcp` Model Context Protocol server exposing lazydeck operations
  to MCP-compatible clients.
- Deploy `argv` wiring through API, CLI, and MCP layers for passing custom
  arguments to deploy operations.
- Supply-chain baseline: pinned tool versions (`mise.toml`), Dependabot,
  CodeQL, `SECURITY.md`, issue/PR templates, and this changelog.

### Changed
- Config writes (`internal/config`) are now atomic (same-directory temp file +
  file/directory fsync + rename) and only mutate in-memory state after a
  successful write.
- The Go client no longer clamps caller-supplied operation deadlines to a
  fixed 60s timeout; the per-operation context controls the deadline.
- Cancelling an operation now tears down the whole `uv`→python→ssh/rsync
  process group (Unix), not just the direct child.
- The TUI serializes device operations: a device that is busy rejects new
  manual operations, and stale completions can no longer clear a newer one.
- Vendored `devkit_client`: game names are shell-quoted before entering remote
  commands; discovery snapshots are taken under a lock; `sync-logs` honors the
  requested folder and login.
- CI runs Go tests (race/vet) on Linux and macOS, real Python unit tests and
  Ruff, and a GoReleaser config check + snapshot; all GitHub Actions are
  pinned to immutable commit SHAs with least-privilege permissions.
- Attribution for the vendored LGPL-2.1 `python-zeroconf` is now complete,
  including its full license text.
- Installed runtime discovery now covers archive, user-data, Homebrew-style,
  and Debian layouts and provisions the locked Python environment in a
  writable per-user cache.

### Fixed
- `sync-logs` passed an attribute the vendored API ignored and dropped the
  login override, so logs never synced; both are corrected.

## [0.1.0]

Initial tagged release: Bubble Tea TUI driving the vendored steamos-devkit
Python bridge via `uv` for pairing, deploy, status, logs, and discovery.

[Unreleased]: https://github.com/kevintcoughlin/lazydeck/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/kevintcoughlin/lazydeck/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/kevintcoughlin/lazydeck/releases/tag/v0.1.0
