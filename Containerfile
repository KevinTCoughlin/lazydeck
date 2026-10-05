# Multi-stage build so Docker, Podman, and Apple's `container` CLI (which
# builds plain OCI images from a Containerfile/Dockerfile but has no
# Compose support) can all reach three different targets from one file:
#
#   toolchain          Go + uv + system deps, matching mise.toml's pins.
#                       Not run directly; base for the stages below.
#   ci                  `just check` / scripts/ci.sh, reproducibly.
#                       `just container-check [ENGINE]`
#   godot-integration   `ci`'s toolchain plus a pinned Godot editor + Linux
#                       export templates, running the Godot integration
#                       test (examples/godot-demo against `lazydeck serve
#                       --fixture`) instead of scripts/ci.sh.
#                       `just container-godot [ENGINE]`
#
# Hardware-dependent behavior (mDNS discovery, real SSH/rsync to a Steam
# Deck/Steam Machine, Unity Editor licensing) is deliberately NOT exercised
# here: see internal/fixture's package doc for why the fake backend exists,
# and CONTRIBUTING.md's "Container-based testing" section for what still
# needs a real LAN/device/license.

FROM ghcr.io/astral-sh/uv:0.12.23@sha256:61d393e44e249f2e4b526b6c7ddcecce245946826e608e11c93ad4f5bba55b21 AS uv
FROM docker.io/library/golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS toolchain

COPY --from=uv /uv /uvx /usr/local/bin/

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        libfontconfig1=2.14.1-4 \
        libpopt0=1.19+dfsg-1 \
        rsync=3.2.7-1+deb12u6 \
        shellcheck=0.9.0-1 \
        unzip=6.0-28 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY python/pyproject.toml python/uv.lock ./python/
RUN uv sync --project python --frozen --no-install-project

COPY . .

# --- ci: the same checks `just check` runs locally, in a clean, ---------
# --- reproducible environment (CI's "container" job / just container-check)
FROM toolchain AS ci
CMD ["bash", "scripts/ci.sh"]

# --- godot-integration: adds a pinned Godot + export templates, runs -----
# --- the Godot plugin's integration test against a fixture server -------
FROM toolchain AS godot-integration

RUN bash scripts/fetch-godot.sh /usr/local/godot \
    && ln -s /usr/local/godot/godot /usr/local/bin/godot

RUN go build -o lazydeck ./cmd/lazydeck

CMD ["bash", "scripts/godot-integration-test.sh"]
