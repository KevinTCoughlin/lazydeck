#!/usr/bin/env bash
# Orchestrates a Godot integration test run against a fixture-backed
# `lazydeck serve` instance: start the fixture server in an isolated
# HOME/config/runtime sandbox, wait for it to become healthy, run the
# Godot integration test script against examples/godot-demo, then always
# stop the server (even on failure) so this is safe to re-run and to use
# as a CI/container step.
#
# Usage: scripts/godot-integration-test.sh [lazydeck_binary] [godot_binary]
#   lazydeck_binary defaults to ./lazydeck (built by `just build`)
#   godot_binary    defaults to $GODOT_BIN, then "godot" on PATH
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT
readonly LAZYDECK_BIN="${1:-${ROOT}/lazydeck}"
readonly GODOT_BIN="${2:-${GODOT_BIN:-godot}}"

if [[ ! -x "${LAZYDECK_BIN}" ]]; then
  echo "lazydeck binary not found or not executable at ${LAZYDECK_BIN} (run 'just build' first)" >&2
  exit 1
fi
if ! command -v "${GODOT_BIN}" >/dev/null 2>&1; then
  echo "Godot binary '${GODOT_BIN}' not found on PATH (run scripts/fetch-godot.sh, or set GODOT_BIN)" >&2
  exit 1
fi

sandbox="$(mktemp -d)"
# Deliberately does NOT override $HOME: lazydeck's own config is isolated
# via XDG_CONFIG_HOME/XDG_RUNTIME_DIR below (both absolute, independent of
# HOME), but Godot's already-installed export templates and editor data
# live under $HOME/.local/share (or $XDG_DATA_HOME, left untouched) --
# overriding HOME would make those invisible and break export.
export XDG_CONFIG_HOME="${sandbox}/config"
export XDG_RUNTIME_DIR="${sandbox}/runtime"
export LAZYDECK_TEST_EXPORT_DIR="${sandbox}/export"
mkdir -p "${XDG_CONFIG_HOME}/lazydeck" "${XDG_RUNTIME_DIR}" "${LAZYDECK_TEST_EXPORT_DIR}"
cp "${ROOT}/scripts/fixtures/devices.toml" "${XDG_CONFIG_HOME}/lazydeck/devices.toml"

# LAZYDECK_FIXTURE_DEPLOY_DELAY gives the test script something real to
# poll/cancel instead of a job that is already terminal by the time it
# asks -- matching how a real deploy (which always takes some nonzero
# time) behaves.
export LAZYDECK_FIXTURE_DEPLOY_DELAY="${LAZYDECK_FIXTURE_DEPLOY_DELAY:-2s}"

connection_file="${XDG_RUNTIME_DIR}/lazydeck/serve.json"
server_log="${sandbox}/serve.log"

"${LAZYDECK_BIN}" serve --fixture >"${server_log}" 2>&1 &
server_pid=$!

cleanup() {
  local status=$?
  if kill -0 "${server_pid}" 2>/dev/null; then
    kill "${server_pid}" 2>/dev/null || true
    wait "${server_pid}" 2>/dev/null || true
  fi
  if [[ ${status} -ne 0 ]]; then
    echo "--- lazydeck serve --fixture log ---" >&2
    cat "${server_log}" >&2 || true
  fi
  rm -rf -- "${sandbox}"
  exit "${status}"
}
trap cleanup EXIT

echo "waiting for fixture server (connection file: ${connection_file})..."
for _ in $(seq 1 50); do
  if [[ -f "${connection_file}" ]]; then
    break
  fi
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    echo "lazydeck serve --fixture exited before writing a connection file" >&2
    exit 1
  fi
  sleep 0.1
done
if [[ ! -f "${connection_file}" ]]; then
  echo "timed out waiting for ${connection_file}" >&2
  exit 1
fi

# A fresh checkout has no examples/godot-demo/.godot cache, so the addon's
# class_name-declared globals (LazyDeckClient, LazyDeckExportRunner, ...)
# aren't registered yet. `--script` alone only parses/runs the one script
# given to it -- it never scans the project -- so referencing any of those
# globals from integration_test.gd would fail to parse with "Identifier
# ... not declared in the current scope" the first time this runs (e.g. in
# CI, or any other clone that hasn't opened the project in the editor
# before). Booting once in `--editor --quit` mode forces Godot's
# first_scan_filesystem + update_scripts_classes step, which writes
# .godot/global_script_class_cache.cfg and makes the globals resolvable by
# name for the `--script` invocation that follows.
echo "warming Godot project class cache..."
"${GODOT_BIN}" --headless --editor \
  --path "${ROOT}/examples/godot-demo" \
  --quit

echo "running Godot integration test..."
"${GODOT_BIN}" --headless \
  --path "${ROOT}/examples/godot-demo" \
  --script "${ROOT}/integrations/godot/tests/integration_test.gd"
