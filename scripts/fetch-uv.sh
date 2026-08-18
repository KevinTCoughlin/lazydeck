#!/usr/bin/env bash
set -euo pipefail

readonly UV_VERSION="${UV_VERSION:-0.12.5}"
readonly BASE_URL="https://github.com/astral-sh/uv/releases/download/${UV_VERSION}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT
readonly OUTPUT_DIR="${1:-${ROOT}/packaging/uv}"

tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

verify() {
  local checksum_file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${tmp}" && sha256sum --check "$(basename "${checksum_file}")")
  else
    local expected actual asset
    expected="$(awk '{print $1}' "${checksum_file}")"
    asset="$(awk '{print $2}' "${checksum_file}")"
    asset="${asset#\*}"
    actual="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
    test "${actual}" = "${expected}"
  fi
}

for spec in "amd64:x86_64-unknown-linux-gnu" "arm64:aarch64-unknown-linux-gnu"; do
  arch="${spec%%:*}"
  target="${spec#*:}"
  asset="uv-${target}.tar.gz"
  curl --fail --location --silent --show-error "${BASE_URL}/${asset}" -o "${tmp}/${asset}"
  curl --fail --location --silent --show-error "${BASE_URL}/${asset}.sha256" -o "${tmp}/${asset}.sha256"
  verify "${tmp}/${asset}.sha256"
  tar -xzf "${tmp}/${asset}" -C "${tmp}"
  install -d "${OUTPUT_DIR}/${arch}"
  install -m 0755 "${tmp}/uv-${target}/uv" "${OUTPUT_DIR}/${arch}/uv"
done

install -d "${OUTPUT_DIR}/licenses"
curl --fail --location --silent --show-error \
  "https://raw.githubusercontent.com/astral-sh/uv/${UV_VERSION}/LICENSE-APACHE" \
  -o "${OUTPUT_DIR}/licenses/LICENSE-uv-APACHE"
curl --fail --location --silent --show-error \
  "https://raw.githubusercontent.com/astral-sh/uv/${UV_VERSION}/LICENSE-MIT" \
  -o "${OUTPUT_DIR}/licenses/LICENSE-uv-MIT"
