#!/usr/bin/env bash
# Downloads a pinned Godot editor build + Linux export templates, so the
# Containerfile's `godot-integration` stage (and any dev machine that wants
# an identical version) gets byte-for-byte the same Godot every time,
# instead of "whatever Godot happens to be installed" -- important because
# integrations/godot/addons/lazydeck/api/export_runner.gd shells out to
# this exact binary with --headless --export-release/--export-debug, and
# export behavior/flags have changed across Godot releases.
#
# Usage: scripts/fetch-godot.sh [output_dir]
#   output_dir defaults to /usr/local/godot (matching the Containerfile).
#
# Installs:
#   <output_dir>/godot                          the editor/CLI binary
#   <output_dir>/export_templates/<ver>.stable/  Linux export templates
set -euo pipefail

readonly GODOT_VERSION="${GODOT_VERSION:-4.7.1}"
readonly BASE_URL="https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable"
readonly OUTPUT_DIR="${1:-/usr/local/godot}"

tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

editor_zip="Godot_v${GODOT_VERSION}-stable_linux.x86_64.zip"
templates_tpz="Godot_v${GODOT_VERSION}-stable_export_templates.tpz"
checksums="SHA512-SUMS.txt"

for asset in "${editor_zip}" "${templates_tpz}" "${checksums}"; do
  curl --fail --location --silent --show-error \
    --output "${tmp}/${asset}" "${BASE_URL}/${asset}"
done

verify() {
  local asset="$1"
  local expected
  expected="$(grep " ${asset}\$" "${tmp}/${checksums}" | awk '{print $1}')"
  if [[ -z "${expected}" ]]; then
    echo "no checksum entry for ${asset} in ${checksums}" >&2
    exit 1
  fi
  local actual
  actual="$(sha512sum "${tmp}/${asset}" | awk '{print $1}')"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "checksum mismatch for ${asset}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
}
verify "${editor_zip}"
verify "${templates_tpz}"

mkdir -p "${OUTPUT_DIR}"
(cd "${tmp}" && unzip -q "${editor_zip}")
install -m 0755 "${tmp}/Godot_v${GODOT_VERSION}-stable_linux.x86_64" "${OUTPUT_DIR}/godot"

# Godot looks for export templates under $XDG_DATA_HOME/godot/export_templates
# (falling back to ~/.local/share/godot/export_templates), in a directory
# named exactly "<version>.stable" -- the same layout its own "Install
# Export Templates" UI action produces. Installing there directly (rather
# than under OUTPUT_DIR) means export_runner.gd's `--export-release`/
# `--export-debug` calls find them with zero extra configuration.
data_home="${XDG_DATA_HOME:-${HOME}/.local/share}"
templates_dir="${data_home}/godot/export_templates/${GODOT_VERSION}.stable"
mkdir -p "${templates_dir}"
(cd "${tmp}" && unzip -q "${templates_tpz}" -d extracted)
mv "${tmp}"/extracted/templates/* "${templates_dir}/"

echo "installed Godot ${GODOT_VERSION} editor to ${OUTPUT_DIR}/godot"
echo "installed export templates to ${templates_dir}"
