#!/usr/bin/env bash
# Single source of truth for the correctness checks that must pass before
# merge: `just check`, the Containerfile's `ci` stage, and (optionally) CI
# workflow steps all call this script instead of each re-implementing the
# same gofmt/vet/test/ruff/shellcheck sequence in a slightly different way.
#
# Usage: scripts/ci.sh
#
# Run from anywhere; paths are resolved relative to the repo root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT
readonly PYTHON_DIR="${ROOT}/python"

log() { printf '\n==> %s\n' "$1"; }

log "gofmt"
unformatted="$(cd "${ROOT}" && gofmt -l .)"
if [[ -n "${unformatted}" ]]; then
  echo "gofmt found unformatted files:" >&2
  echo "${unformatted}" >&2
  exit 1
fi

log "go vet"
(cd "${ROOT}" && go vet ./...)

log "go test -race"
(cd "${ROOT}" && go test -race ./...)

log "uv lock --check && uv sync --frozen"
(cd "${PYTHON_DIR}" && uv lock --check && uv sync --frozen)

log "ruff check"
(cd "${PYTHON_DIR}" && uv run --frozen ruff check .)

log "python unittest"
(cd "${PYTHON_DIR}" && uv run --frozen python -m unittest discover -s . -p 'test_*.py' -v)

log "shellcheck"
(cd "${ROOT}" && shellcheck install.sh packaging/deb/postinstall.sh scripts/*.sh)

log "all checks passed"
