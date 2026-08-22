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

## Export behavior

Build/deploy runs the current Godot executable with
`--headless --export-release` or `--export-debug`, because Godot does not
expose its internal `EditorExport` singleton to GDScript plugins. Save
open scenes and resources before clicking **Build & deploy**: the export
subprocess reads the project from disk. The dock supplies the configured
output directory and executable name as the export path, then deploys the
whole directory so accompanying `.pck` and platform files are included.

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

## Validation

The demo plugin has been loaded in Godot 4.7.1 on Fedora Linux. Its
release export completed successfully, and its API client connected to
an isolated live `lazydeck serve`, submitted a deployment job, polled
the job, and cancelled it. This validation used a fake unreachable
device rather than real Steam hardware, so pairing and a successful
upload still require hardware testing.

## How it finds `lazydeck serve`

`api/connection_locator.gd` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json`. That file names
the loopback port and bearer token for the currently running `lazydeck
serve` process; the dock's "Connect" button re-reads it on demand, so
restarting `lazydeck serve` and clicking Connect again picks up the new
port/token without reloading the editor.
