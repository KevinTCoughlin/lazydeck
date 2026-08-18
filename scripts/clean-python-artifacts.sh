#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT

find "${ROOT}/python" -type f \( -name '*.pyc' -o -name '*.pyo' \) -delete
find "${ROOT}/python" -depth -type d \( -name '__pycache__' -o -name '.ruff_cache' \) -exec rmdir {} + 2>/dev/null || true
