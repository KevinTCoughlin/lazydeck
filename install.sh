#!/usr/bin/env bash
# Installs the latest lazydeck release into ~/.local/bin (or $LAZYDECK_INSTALL_DIR),
# and unpacks the bundled python/ directory alongside it.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/kevintcoughlin/lazydeck/main/install.sh | bash
#
# Requires: curl, tar, and (to actually run lazydeck afterwards) uv.
set -euo pipefail

REPO="kevintcoughlin/lazydeck"
INSTALL_DIR="${LAZYDECK_INSTALL_DIR:-${HOME}/.local/bin}"
DATA_DIR="${LAZYDECK_DATA_DIR:-${HOME}/.local/share/lazydeck}"

os="$(uname -s)"
arch="$(uname -m)"

case "${os}" in
  Darwin) goos="darwin" ;;
  Linux) goos="linux" ;;
  *)
    echo "lazydeck: unsupported OS: ${os} (only macOS and Linux release binaries are published)" >&2
    exit 1
    ;;
esac

case "${arch}" in
  arm64|aarch64) goarch="arm64" ;;
  x86_64|amd64) goarch="amd64" ;;
  *)
    echo "lazydeck: unsupported architecture: ${arch}" >&2
    exit 1
    ;;
esac

version="${1:-latest}"
if [[ "${version}" = "latest" ]]; then
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep -m1 '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')"
  url="https://github.com/${REPO}/releases/download/v${tag}/lazydeck_${tag}_${goos}_${goarch}.tar.gz"
else
  url="https://github.com/${REPO}/releases/download/v${version}/lazydeck_${version}_${goos}_${goarch}.tar.gz"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

echo "lazydeck: downloading ${url}"
curl -fsSL "${url}" -o "${tmp}/lazydeck.tar.gz"
tar -xzf "${tmp}/lazydeck.tar.gz" -C "${tmp}"

mkdir -p "${INSTALL_DIR}" "${DATA_DIR}"
install -m 0755 "${tmp}/lazydeck" "${INSTALL_DIR}/lazydeck"
rm -rf "${DATA_DIR}/python"
cp -R "${tmp}/python" "${DATA_DIR}/python"

echo "lazydeck: installed binary to ${INSTALL_DIR}/lazydeck"
echo "lazydeck: python/ runtime copied to ${DATA_DIR}/python"
echo
echo "Next steps:"
echo "  1. Make sure ${INSTALL_DIR} is on your PATH."
echo "  2. Install uv if you haven't: https://docs.astral.sh/uv/getting-started/installation/"
echo "  3. export LAZYDECK_PYTHON_DIR=\"${DATA_DIR}/python\""
echo "  4. cd \"${DATA_DIR}/python\" && uv sync"
echo "  5. Run: lazydeck"
