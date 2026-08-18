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

# CI-equivalent correctness checks.
check:
    go vet ./...
    go test -race ./...
    cd {{python_dir}} && uv lock --check && uv sync --frozen
    cd {{python_dir}} && uv run --frozen ruff check .
    cd {{python_dir}} && uv run --frozen python -m unittest discover -s . -p 'test_*.py' -v
    shellcheck install.sh packaging/deb/postinstall.sh scripts/*.sh

# Validate release configuration and build local snapshot artifacts.
snapshot:
    goreleaser check
    goreleaser release --snapshot --clean

# Build and run the reproducible test container.
container-test:
    podman build --tag lazydeck-dev:test --file Containerfile .
    podman run --rm lazydeck-dev:test

# Run the headless python CLI directly, e.g.:
#   just cli status --machine 192.168.1.50
cli *args:
    cd {{python_dir}} && uv run python cli.py {{args}}

# Remove build artifacts and Python caches.
clean:
    rm -f {{bin}}
    find . -type d -name __pycache__ -exec rm -rf {} +
