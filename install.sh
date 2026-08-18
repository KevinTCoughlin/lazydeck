#!/usr/bin/env bash
set -euo pipefail

readonly REPO="kevintcoughlin/lazydeck"
readonly PREFIX="${PREFIX:-${HOME}/.local}"
readonly INSTALL_DIR="${INSTALL_DIR:-${LAZYDECK_INSTALL_DIR:-${PREFIX}/bin}}"
readonly DATA_DIR="${LAZYDECK_DATA_DIR:-${PREFIX}/share/lazydeck}"

version="${VERSION:-${1:-latest}}"
version="${version#v}"

case "${INSTALL_DIR}:${DATA_DIR}" in
  /*:/*) ;;
  *)
    echo "lazydeck: INSTALL_DIR and data directory must be absolute paths" >&2
    exit 1
    ;;
esac
if [[ "${INSTALL_DIR}" == "/" || "${DATA_DIR}" == "/" ]]; then
  echo "lazydeck: refusing to install directly into the filesystem root" >&2
  exit 1
fi
if [[ ! "${version}" =~ ^[0-9A-Za-z._-]+$ ]]; then
  echo "lazydeck: invalid VERSION ${version@Q}" >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin) goos="darwin" ;;
  Linux) goos="linux" ;;
  *)
    echo "lazydeck: unsupported OS (only macOS and Linux are published)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  arm64|aarch64) goarch="arm64" ;;
  x86_64|amd64) goarch="amd64" ;;
  *)
    echo "lazydeck: unsupported architecture" >&2
    exit 1
    ;;
esac

for command in curl tar uv; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "lazydeck: required command not found: ${command}" >&2
    exit 1
  fi
done

if [[ "${version}" == "latest" ]]; then
  version="$(
    curl --fail --location --silent --show-error \
      "https://api.github.com/repos/${REPO}/releases/latest" |
      sed -nE 's/.*"tag_name":[[:space:]]*"v?([^"]+)".*/\1/p' |
      head -n 1
  )"
  if [[ -z "${version}" ]]; then
    echo "lazydeck: could not determine the latest release" >&2
    exit 1
  fi
fi

readonly asset="lazydeck_${version}_${goos}_${goarch}.tar.gz"
readonly base_url="${LAZYDECK_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download/v${version}}"
tmp="$(mktemp -d)"
new_binary=""
new_runtime=""
old_binary=""
old_runtime=""
committed=false
binary_replaced=false
runtime_replaced=false

cleanup() {
  if [[ "${committed}" != true ]]; then
    if [[ "${binary_replaced}" == true ]]; then
      rm -f -- "${INSTALL_DIR}/lazydeck"
    fi
    if [[ -n "${old_binary}" && -e "${old_binary}" ]]; then
      mv -- "${old_binary}" "${INSTALL_DIR}/lazydeck"
    fi
    if [[ "${runtime_replaced}" == true ]]; then
      rm -rf -- "${DATA_DIR}/python"
    fi
    if [[ -n "${old_runtime}" && -e "${old_runtime}" ]]; then
      mv -- "${old_runtime}" "${DATA_DIR}/python"
    fi
  fi
  [[ -z "${new_binary}" || ! -e "${new_binary}" ]] || rm -f -- "${new_binary}"
  [[ -z "${new_runtime}" || ! -e "${new_runtime}" ]] || rm -rf -- "${new_runtime}"
  rm -rf -- "${tmp}"
}
trap cleanup EXIT

echo "lazydeck: downloading v${version} for ${goos}/${goarch}"
curl --fail --location --silent --show-error "${base_url}/${asset}" -o "${tmp}/${asset}"
curl --fail --location --silent --show-error "${base_url}/checksums.txt" -o "${tmp}/checksums.txt"

expected="$(awk -v asset="${asset}" '$2 == asset || $2 == "*" asset {print $1; exit}' "${tmp}/checksums.txt")"
if [[ -z "${expected}" ]]; then
  echo "lazydeck: ${asset} is absent from checksums.txt" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
fi
if [[ "${actual}" != "${expected}" ]]; then
  echo "lazydeck: checksum verification failed for ${asset}" >&2
  exit 1
fi

if tar -tzf "${tmp}/${asset}" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
  echo "lazydeck: release archive contains an unsafe path" >&2
  exit 1
fi
mkdir -p "${tmp}/release"
tar -xzf "${tmp}/${asset}" -C "${tmp}/release"

for required in lazydeck python/cli.py python/pyproject.toml python/uv.lock; do
  if [[ ! -f "${tmp}/release/${required}" ]]; then
    echo "lazydeck: release archive is missing ${required}" >&2
    exit 1
  fi
done

"${tmp}/release/lazydeck" version >/dev/null
UV_PROJECT_ENVIRONMENT="${tmp}/venv" \
  uv sync --frozen --no-dev --project "${tmp}/release/python" --quiet
"${tmp}/venv/bin/python" "${tmp}/release/python/cli.py" --help >/dev/null

install -d "${INSTALL_DIR}" "${DATA_DIR}"
new_binary="${INSTALL_DIR}/.lazydeck.new.$$"
new_runtime="${DATA_DIR}/.python.new.$$"
install -m 0755 "${tmp}/release/lazydeck" "${new_binary}"
cp -R "${tmp}/release/python" "${new_runtime}"

if [[ -e "${INSTALL_DIR}/lazydeck" ]]; then
  old_binary="${INSTALL_DIR}/.lazydeck.old.$$"
  mv -- "${INSTALL_DIR}/lazydeck" "${old_binary}"
fi
mv -- "${new_binary}" "${INSTALL_DIR}/lazydeck"
new_binary=""
binary_replaced=true

if [[ -e "${DATA_DIR}/python" ]]; then
  old_runtime="${DATA_DIR}/.python.old.$$"
  mv -- "${DATA_DIR}/python" "${old_runtime}"
fi
mv -- "${new_runtime}" "${DATA_DIR}/python"
new_runtime=""
runtime_replaced=true

committed=true
[[ -z "${old_binary}" || ! -e "${old_binary}" ]] || rm -f -- "${old_binary}"
[[ -z "${old_runtime}" || ! -e "${old_runtime}" ]] || rm -rf -- "${old_runtime}"

echo "lazydeck: installed v${version} to ${INSTALL_DIR}/lazydeck"
echo "lazydeck: installed Python runtime to ${DATA_DIR}/python"
