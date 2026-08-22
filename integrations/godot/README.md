# LazyDeck Godot plugin

A Godot 4 editor plugin that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so
you can discover, pair, and inspect Steam Deck / Steam Machine devkits
without leaving the editor.

## Status: connect, devices, build & deploy, logs

The dock (bottom-right editor panel) covers:

- Connecting to an already-running `lazydeck serve` by reading its
  connection file.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.
- Exporting the current project via a chosen export preset and deploying
  the result to the selected device, with job progress polled until it
  finishes.
- Syncing the selected device's logs to a local directory.
- Cancelling an in-flight deploy or log-sync job.

**Not included yet** (tracked as follow-up work on issue #14):

- Launching/stopping a deployed title (the local service API itself
  doesn't support this yet either — see `/v1/capabilities`).
- The plugin spawning `lazydeck serve` itself. For now, run `lazydeck
  serve` yourself in a terminal before opening the dock.
- Pairing a device discovered over mDNS that isn't already in
  `devices.toml` — `lazydeck serve` reads `devices.toml` once at
  startup, so add the device to the config and restart `lazydeck
  serve` first (same as the TUI's separate add-device flow), then
  Connect again (re-clicking Connect alone re-reads the connection
  file, not `devices.toml`).

## ⚠️ The export step is the least certain part of this plugin

Build/deploy calls Godot's own export machinery in-process
(`EditorExportPlatform.export_project()`, via `api/export_runner.gd`) —
the same API Godot's Export dialog and `--export-release`/`--export-debug`
use internally, per issue #14's "run the Godot export through supported
editor APIs where possible." That was a deliberate choice over shelling
out to a second `godot --headless --export-...` process, but it also
means the dock exports the project's *currently saved* state — save any
open scenes/resources before clicking **Build & deploy**, or the export
won't include unsaved changes.

The specific detail most likely to need adjusting on a real run:
`export_project()` takes a full *file* path (e.g.
`/tmp/export/mygame.x86_64`), and Godot writes that executable plus its
`.pck` and any other per-platform files into the same directory — which
is why the dock asks for an output directory and an executable name
separately rather than one combined path. If the platform corrects or
appends an extension itself, what's actually on disk may not exactly
match the executable name you typed; check the output directory and
adjust the field to match before deploying again. See the comment at the
top of `api/export_runner.gd` for the full reasoning.

## Requirements

- Godot **4.3** or newer. The plugin checks this on load and refuses to
  activate (with a clear editor error) below that floor, rather than
  loading partially and failing confusingly later. It's written to
  feature-detect anything from a newer Godot release through
  `api/compat.gd`'s `LazyDeckCompat.engine_at_least(...)`, so it can pick
  up newer/beta platform features over time without raising this floor.
- `lazydeck serve` running (see the root README) before you open the dock.

## Installing

Copy (or symlink) `addons/lazydeck` into your Godot project's own
`addons/` directory, then enable **LazyDeck** under Project Settings →
Plugins. `examples/godot-demo` in this repository does exactly that via a
symlink, as a minimal example project with nothing else in it.

## ⚠️ Not yet tested in a real Godot editor

This plugin was written without access to a Godot editor to run it in.
Every `.gd` file is formatted and linted with
[gdtoolkit](https://github.com/Scony/godot-gdscript-toolkit)
(`gdformat`/`gdlint`, both clean) and reviewed carefully against the
Godot 4.3 API docs, but that's static review, not a real run. If
something doesn't work as described here, please open an issue — this
note should be removed once someone has verified it against a real
Godot editor and a real (or `internal/client`-fake) `lazydeck serve`.

## How it finds `lazydeck serve`

`api/connection_locator.gd` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json`. That file names
the loopback port and bearer token for the currently running `lazydeck
serve` process; the dock's "Connect" button re-reads it on demand, so
restarting `lazydeck serve` and clicking Connect again picks up the new
port/token without reloading the editor.
