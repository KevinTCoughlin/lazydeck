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

FROM ghcr.io/astral-sh/uv:0.13.5@sha256:32a7bb39b496babbd997b0a39a4d66c126bdaf5661b302bf6f06abda8deca6ff AS uv
FROM docker.io/library/golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS toolchain

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
