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
validates GoReleaser, rebuilds/runs both container stages (`ci` and
`godot-integration`) for parity, lints shell scripts, and installs the generated
amd64 Debian package before smoke-testing its bundled runtime.

Nix users can enter the pinned development environment with `nix develop`.
The flake builds and wraps the Go binary with its bundled Python bridge and
the Nix-provided `uv`, OpenSSH, and rsync executables.

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
- `Containerfile` — multi-stage pinned Linux environment: `toolchain` (base
  build deps), `ci` (`just check`, reproducibly), `godot-integration`
  (adds a pinned Godot + export templates, runs the Godot engine
  integration test). Not a runtime service image.
- `internal/fixture/` — a fake `devkitClient` backend (no uv/Python/SSH)
  used by `lazydeck serve --fixture`, so engine-integration tests and
  container CI can exercise the full API without real hardware.
- `.goreleaser.yml` and `packaging/` — release archives, Debian packages,
  checksums, SBOM inputs, bundled uv provisioning, and the release container
  image (`packaging/container/Dockerfile`, built from the release `.deb`).
- `release-please-config.json` / `.release-please-manifest.json` — release
  automation (see [Releases](#releases)).

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

## Container-based testing (Docker / Podman / Apple Container)

`Containerfile` has three stages, all buildable with `docker`, `podman`,
or macOS 26+'s Apple Silicon-only `container` CLI (all three build plain
OCI images; none of this relies on Compose, since `container` doesn't
support it yet):

```bash
just container-check           # `ci` stage: same checks as `just check`, in a clean image
just container-check docker    # same, with a different engine
just container-godot           # `godot-integration` stage: Godot plugin integration test
just integration-godot          # same test, natively (no container), for fast local iteration
```

The Godot integration test (`integrations/godot/tests/integration_test.gd`,
run via `scripts/godot-integration-test.sh`) drives the actual Godot addon
API (`LazyDeckClient`, `LazyDeckExportRunner`, `LazyDeckConnectionLocator`)
against a real `lazydeck serve --fixture` instance: it exports
`examples/godot-demo`, then connects, lists devices, submits a deployment,
and cancels it — all through the same HTTP/SSE surface a real plugin user
hits, but with `internal/fixture`'s fake backend standing in for SSH/rsync
to a real device (see that package's doc comment for why, and its
`LAZYDECK_FIXTURE_*` environment variables for shaping other scenarios,
like a failing deploy).

This intentionally does **not** cover: mDNS/LAN discovery, pairing against
a real devkit, or the Unity plugin (which needs a licensed Editor to run
in batch mode — see `integrations/unity/README.md`'s validation notes).
Those remain manual/hardware-gated testing until a licensed CI runner or
self-hosted LAN runner is available.

## Pull request titles

PRs are squash-merged, and the PR title becomes the commit message on `main`.
Titles must be [Conventional Commits](https://www.conventionalcommits.org/)
(enforced by the `PR title` check), because release-please reads them to
choose the next version and write `CHANGELOG.md`:

| Title | Release effect | Changelog section |
| --- | --- | --- |
| `feat(scope): ...` | minor bump | Added |
| `fix(scope): ...` | patch bump | Fixed |
| `perf: ...` | patch bump | Changed |
| `deps: ...` | patch bump | Dependencies |
| `feat!: ...` (any type with `!`) | breaking; minor bump while 0.x | as its type |
| `docs`, `test`, `build`, `ci`, `chore`, `refactor`, `style`, `revert` | none on its own | hidden |

Write the title for users: it is the changelog line.

## Releases

Releases are cut by merging a PR, not by pushing tags:

1. Every push to `main` runs `.github/workflows/release.yml`, where
   release-please keeps a `chore(main): release X.Y.Z` PR open with the next version
   and its `CHANGELOG.md` entry. Edit that PR's changelog text if you want to
   polish the wording before release.
2. Merging the release PR tags `vX.Y.Z`, creates the GitHub release, and the
   same workflow then runs the test gate, GoReleaser (archives, `.deb`s,
   SBOMs, a cosign-signed `checksums.txt`, provenance attestations), pushes
   the `ghcr.io/kevintcoughlin/lazydeck` image, bumps the Homebrew tap, and
   finally installs the published release with `install.sh` on Linux and
   macOS and verifies its signature and provenance.
3. For a release candidate (or any explicit tag), run the **Release** workflow
   manually from `main` with a tag such as `v0.3.0-rc.1`. Semver prereleases
   publish as GitHub prereleases: `install.sh`'s default, `lazydeck version
   --check`, Homebrew, and the container `latest` tag all skip them.

Between releases, `.github/workflows/nightly.yml` publishes a rolling
`nightly` prerelease from `main` whenever it has changed
(`VERSION=nightly ./install.sh`). Dependabot patch/minor updates merge
automatically once CI passes on them; majors wait for review.

Optional repository secrets:

- `RELEASE_BOT_TOKEN`: a GitHub App or fine-grained token (contents and pull
  requests: write). With it, the release PR runs CI and Dependabot
  auto-merges trigger the push workflows on `main`; without it both still
  work, using `GITHUB_TOKEN`.
- `HOMEBREW_TAP_TOKEN`: contents: write on `KevinTCoughlin/homebrew-lazydeck`.
  Without it, the tap update is skipped with a notice.

Don't push `v*` tags by hand: the workflow creates them, so there is only one
path from a tag to published artifacts. If a publishing job fails after the
tag exists, fix the cause and use **Re-run failed jobs** on that workflow run.

## Release and license maintenance

Run `goreleaser check` after changing `.goreleaser.yml`. A snapshot release
(`just snapshot`) downloads the pinned uv release for both Linux package
architectures and verifies upstream checksums. Re-run
`scripts/generate-notices.sh` after changing Go dependencies and include the
updated `THIRD_PARTY_GO.md`. Do not edit generated notices manually.

Report vulnerabilities privately as described in `SECURITY.md`.
All community participation is governed by `CODE_OF_CONDUCT.md`; the current
maintainer model is described in `GOVERNANCE.md`.

## Reporting issues

Please include your OS, `lazydeck` version (or commit SHA), and — if
relevant — the SteamOS version on the device you were targeting.
