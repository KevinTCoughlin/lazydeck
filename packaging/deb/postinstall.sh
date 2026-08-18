#!/bin/sh
# Debian postinstall for lazydeck. lazydeck drives a bundled Python bridge via
# `uv`. The package includes an architecture-matched uv binary under
# /usr/libexec/lazydeck, so the maintainer script performs no network access.
# Dependencies are provisioned into the invoking user's cache on first run.
set -e

if [ ! -x /usr/libexec/lazydeck/uv ]; then
  echo "lazydeck: packaged uv runtime is missing or not executable" >&2
  exit 1
fi

exit 0
