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
# two host directories. See README.md for first-run ownership setup (UID 1000)
# and the complete command that persists both LazyDeck config and SteamOS SSH
# pairing state; both mounts are required across container runs.
FROM docker.io/library/debian:bookworm-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3

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
