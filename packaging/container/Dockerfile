# Release image for lazydeck, published to ghcr.io/kevintcoughlin/lazydeck by
# .github/workflows/release.yml. Unlike the repo-root Containerfile (a
# development/test toolchain), this installs the already-built, release
# Debian package, so the image has exactly the layout `apt install` gives
# users: binary, bundled Python runtime, pinned uv, ssh/rsync.
#
# Build context: a directory holding this file and the release's
# lazydeck_*_linux_{amd64,arm64}.deb packages.
#
# Typical use is the stdio MCP server or a one-off CLI command. Devkit
# discovery (mDNS) and SSH need the host's LAN, and pairing state lives in the
# mounted config directory:
#
#   docker run -i --rm --network host \
#     -v "$HOME/.config/lazydeck:/home/lazydeck/.config/lazydeck" \
#     ghcr.io/kevintcoughlin/lazydeck mcp
FROM docker.io/library/debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251

ARG TARGETARCH
COPY lazydeck_*_linux_${TARGETARCH}.deb /tmp/lazydeck.deb

RUN apt-get update \
 && apt-get install --yes --no-install-recommends /tmp/lazydeck.deb \
 && rm -rf /var/lib/apt/lists/* /tmp/lazydeck.deb \
 && useradd --create-home --uid 1000 --shell /usr/sbin/nologin lazydeck

USER lazydeck
WORKDIR /home/lazydeck

# Provision the locked Python environment at build time, at the path lazydeck
# uses (os.UserCacheDir()/lazydeck/python), so the container never needs
# network access to PyPI at run time.
RUN UV_PROJECT_ENVIRONMENT=/home/lazydeck/.cache/lazydeck/python \
    /usr/libexec/lazydeck/uv sync --frozen --project /usr/share/lazydeck/python --quiet \
 && lazydeck version

ENTRYPOINT ["lazydeck"]
CMD ["help"]
