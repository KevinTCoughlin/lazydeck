#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Derives docs/assets/demo.gif (README) and docs/assets/demo-poster.png (the
# site's <video> poster) from the 2x docs/assets/demo.mp4 recorded by
# scripts/demo/demo.tape. Downsampling with lanczos from 2x keeps text crisp.
set -euo pipefail
cd "$(dirname "$0")/../.."
src=docs/assets/demo.mp4
ffmpeg -y -loglevel error -i "$src" \
  -vf "fps=15,scale=1200:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" \
  docs/assets/demo.gif
# Poster: a frame from the fleet view after the refresh, at full 2x size.
ffmpeg -y -loglevel error -ss 5 -i "$src" -frames:v 1 docs/assets/demo-poster.png
