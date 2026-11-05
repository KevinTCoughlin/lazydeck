# SPDX-License-Identifier: MIT
# Records docs/assets/demo.gif against a fake backend (no hardware, no network):
#   vhs scripts/demo/demo.tape
# Run from the repository root.

Output docs/assets/demo.gif
Require go

Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 640
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type `export DEMO=$(mktemp -d) && mkdir -p "$DEMO/lazydeck" && cp scripts/demo/devices.toml "$DEMO/lazydeck/devices.toml"` Enter
Type `go build -o "$DEMO/lazydeck-bin" ./cmd/lazydeck` Enter
Type `export XDG_CONFIG_HOME="$DEMO" LAZYDECK_UV="$PWD/scripts/demo/fake-uv" LAZYDECK_PYTHON_DIR="$PWD/python"` Enter
Type `alias lazydeck="$DEMO/lazydeck-bin" && clear` Enter
Sleep 4s
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
Sleep 4s

# Help overlay, then quit.
Type "?" Sleep 2.5s
Escape Sleep 600ms
Type "q"
Sleep 500ms

Hide
Type `rm -rf "$DEMO"` Enter
