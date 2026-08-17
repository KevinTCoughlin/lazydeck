# devkit-tui task runner. Run `just` (or `j` per your shell aliases) with no
# arguments to see this list.

set shell := ["bash", "-euo", "pipefail", "-c"]

python_dir := justfile_directory() / "python"
bin := justfile_directory() / "devkit-tui"

# List available recipes.
default:
    @just --list

# One-time (or after pulling changes to python/pyproject.toml): resolve and
# install the Python side's dependencies (paramiko, appdirs, ...) via uv.
sync:
    cd {{python_dir}} && uv sync

# Build the devkit-tui binary into the repo root.
build:
    go build -o {{bin}} ./cmd/devkit-tui

# Build (if needed) and launch the TUI.
run: build
    {{bin}}

# Run the Go unit tests.
test:
    go test ./...

# Full lint pass: golangci-lint (falls back to go vet if not installed),
# ruff (falls back to py_compile if not installed), plus gofmt.
lint:
    gofmt -l .
    if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else go vet ./...; fi
    if command -v ruff >/dev/null; then ruff check {{python_dir}}/cli.py; else python3 -m py_compile {{python_dir}}/cli.py; fi
    python3 -m py_compile {{python_dir}}/vendor/devkit_client/__init__.py

# Run the headless python CLI directly, e.g.:
#   just cli status --machine 192.168.1.50
cli *args:
    cd {{python_dir}} && uv run python cli.py {{args}}

# Remove build artifacts and Python caches.
clean:
    rm -f {{bin}}
    find . -type d -name __pycache__ -exec rm -rf {} +
