# lazydeck task runner. Run `just` (or `j` per your shell aliases) with no
# arguments to see this list.

set shell := ["bash", "-euo", "pipefail", "-c"]

python_dir := justfile_directory() / "python"
bin := justfile_directory() / "lazydeck"

# List available recipes.
default:
    @just --list

# One-time (or after pulling changes to python/pyproject.toml): resolve and
# install the Python side's dependencies (paramiko, appdirs, ...) via uv.
sync:
    cd {{python_dir}} && uv sync --frozen

# Build the lazydeck binary into the repo root.
build:
    go build -o {{bin}} ./cmd/lazydeck

# Build (if needed) and launch the TUI.
run: build
    {{bin}}

# Run the Go unit tests.
test:
    go test ./...
    cd {{python_dir}} && uv run --frozen python -m unittest discover -s . -p 'test_*.py' -v

# Full lint pass: golangci-lint (falls back to go vet if not installed),
# ruff (falls back to py_compile if not installed), plus gofmt.
lint:
    test -z "$(gofmt -l .)"
    if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else go vet ./...; fi
    cd {{python_dir}} && uv run --frozen ruff check .
    shellcheck install.sh packaging/deb/postinstall.sh scripts/*.sh
    if command -v clang-format >/dev/null; then just lint-unreal; fi

# Check integrations/unreal/'s C++ against its .clang-format (Epic's coding
# standard: tabs, Allman braces). Formatting only -- verifying it actually
# compiles needs a real Unreal Engine install, see integrations/unreal/README.md.
# NUL-delimited (-print0/-d '') so this stays correct for any path name, and
# guarded on a non-empty match so it never runs clang-format with zero files
# (which would otherwise block reading a dry-run stdin).
lint-unreal:
    mapfile -d '' files < <(find integrations/unreal \( -name '*.h' -o -name '*.cpp' \) -print0)
    if [ "${#files[@]}" -gt 0 ]; then clang-format --dry-run --Werror "${files[@]}"; fi

# Reformat integrations/unreal/'s C++ in place to match .clang-format.
format-unreal:
    mapfile -d '' files < <(find integrations/unreal \( -name '*.h' -o -name '*.cpp' \) -print0)
    if [ "${#files[@]}" -gt 0 ]; then clang-format -i "${files[@]}"; fi

# CI-equivalent correctness checks (see scripts/ci.sh, the single source
# of truth also used by the Containerfile's `ci` stage).
check:
    bash scripts/ci.sh

# Validate release configuration and build local snapshot artifacts.
snapshot:
    goreleaser check
    goreleaser release --snapshot --clean

# Build and run the reproducible checks inside a container. ENGINE selects
# the container runtime (podman, docker, or Apple's `container` CLI on
# macOS 26+); all three build/run plain OCI images from the same
# Containerfile, so this recipe works unmodified across engines.
container-check ENGINE="podman":
    {{ENGINE}} build --target ci --tag lazydeck-ci:test --file Containerfile .
    {{ENGINE}} run --rm lazydeck-ci:test

# Build and run the Godot integration test (examples/godot-demo, exercised
# against `lazydeck serve --fixture`) inside a container with a pinned
# Godot + export templates. Slower than container-check (a fresh Godot
# download + editor import on first build), so it's kept as a separate
# recipe/CI job rather than folded into container-check.
container-godot ENGINE="podman":
    {{ENGINE}} build --target godot-integration --tag lazydeck-godot:test --file Containerfile .
    {{ENGINE}} run --rm lazydeck-godot:test

# Run the Godot integration test directly on this machine (no container),
# e.g. for fast local iteration while editing integrations/godot/. Needs a
# `godot` binary on PATH or GODOT_BIN set; run scripts/fetch-godot.sh once
# to install a pinned version if you don't already have one.
integration-godot: build
    bash scripts/godot-integration-test.sh {{bin}}

# Build and run the reproducible test container (kept for backward
# compatibility; equivalent to `just container-check`).
container-test: (container-check "podman")

# Run the headless python CLI directly, e.g.:
#   just cli status --machine 192.168.1.50
cli *args:
    cd {{python_dir}} && uv run python cli.py {{args}}

# Remove build artifacts and Python caches.
clean:
    rm -f {{bin}}
    find . -type d -name __pycache__ -exec rm -rf {} +
