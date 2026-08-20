FROM ghcr.io/astral-sh/uv:0.12.5@sha256:e85be844203885286c60ffad8a858d48afb6c5a5c237ca0e67f12e74b8f174b1 AS uv
FROM docker.io/library/golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36

COPY --from=uv /uv /uvx /usr/local/bin/

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        libpopt0=1.19+dfsg-1 \
        rsync=3.2.7-1+deb12u6 \
        shellcheck=0.9.0-1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY python/pyproject.toml python/uv.lock ./python/
RUN uv sync --project python --frozen --no-install-project

COPY . .

CMD ["bash", "-euo", "pipefail", "-c", "gofmt_check=$(gofmt -l .); test -z \"$gofmt_check\"; go vet ./...; go test -race ./...; uv run --frozen --project python ruff check python; uv run --frozen --project python python -m unittest discover -s python -p 'test_*.py' -v; shellcheck install.sh packaging/deb/postinstall.sh scripts/*.sh"]
