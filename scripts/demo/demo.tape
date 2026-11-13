# SPDX-License-Identifier: MIT
# Records the landing-page demo against a fake backend (no hardware, no network):
#   just demo   (vhs scripts/demo/demo.tape, then scripts/demo/gif.sh)
# Run from the repository root.
#
# Recorded at 2x (2400x1280, 30px font) so it stays sharp on HiDPI screens.
# The site plays the WebM/MP4; scripts/demo/gif.sh downsamples the MP4 into
# docs/assets/demo.gif for the README, where video tags aren't rendered.

Output docs/assets/demo.webm
Output docs/assets/demo.mp4
Require go

Set Shell "bash"
Set FontSize 30
Set Width 2400
Set Height 1280
Set Padding 32
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type `export DEMO=$(mktemp -d) && mkdir -p "$DEMO/lazydeck" && cp scripts/demo/devices.toml "$DEMO/lazydeck/devices.toml"` Enter
Type `go build -o "$DEMO/lazydeck-bin" ./cmd/lazydeck` Enter
Type `export XDG_CONFIG_HOME="$DEMO" LAZYDECK_UV="$PWD/scripts/demo/fake-uv" LAZYDECK_PYTHON_DIR="$PWD/python"` Enter
Type `alias lazydeck="$DEMO/lazydeck-bin" && clear` Enter
# Run `go mod download` first on a cold module cache so the build fits here.
Sleep 8s
Show

Type "lazydeck" Sleep 400ms Enter
Sleep 2.5s

# Refresh the whole fleet; one devkit is offline.
Type "s" Sleep 3s

# List games on the Steam Deck.
Type "j" Sleep 500ms
Type "g" Sleep 2s

# Deploy a build to it.
Type "d" Sleep 700ms
Type "HollowGrove" Sleep 300ms Enter
Sleep 600ms
Type "./build/linux" Sleep 300ms Enter
# Run `go mod download` first on a cold module cache so the build fits here.
Sleep 8s

# Help overlay, then quit.
Type "?" Sleep 2.5s
Escape Sleep 600ms
Type "q"
Sleep 500ms

Hide
Type `rm -rf "$DEMO"` Enter
