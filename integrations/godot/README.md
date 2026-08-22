# LazyDeck Godot plugin

A Godot 4 editor plugin that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so
you can discover, pair, and inspect Steam Deck / Steam Machine devkits
without leaving the editor.

## Status: connect, devices, build & deploy, logs

The dock (bottom-right editor panel) covers:

- Connecting through `lazydeck serve`'s connection file, starting the
  service automatically when no connection file exists.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.
- Exporting the current project via a chosen export preset and deploying
  the result to the selected device, with job progress polled until it
  finishes.
- Syncing the selected device's logs to a local directory.
- Cancelling an in-flight deploy or log-sync job.

**Deliberate limitations:**

- Launching/stopping a deployed title: this is intentionally unsupported by
  the SteamOS devkit protocol; start or stop the title in the device's Steam
  UI. See [`docs/DEVICE_LAUNCH.md`](../../../docs/DEVICE_LAUNCH.md).
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
- A `lazydeck` executable on `PATH`, unless `LAZYDECK_BIN` or the
  `lazydeck/server/executable` Project Setting names it explicitly.

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

**`api/server_launcher.gd` (auto-starting `lazydeck serve`) postdates
that validation run and has not itself been exercised in a real
editor.** It was checked with `gdformat`/`gdlint` only. The one thing
most worth confirming first: `OS.create_process()`'s return value on
both a successful spawn and a failed one (e.g. a missing executable),
since that return value drives what the dock logs.

## How it finds `lazydeck serve`

`api/connection_locator.gd` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json`. That file names
the loopback port and bearer token for the currently running `lazydeck
serve` process; the dock's "Connect" button re-reads it on demand, so
restarting `lazydeck serve` and clicking Connect again picks up the new
port/token without reloading the editor.

When that connection file does not exist, the dock starts `lazydeck serve`,
then waits up to five seconds for it. The process remains running after Godot
closes so another editor can reuse it. Set `LAZYDECK_AUTOSTART=0` or the
`lazydeck/server/autostart` Project Setting to `false` to manage the service
yourself. Godot cannot capture a detached process's stderr; startup errors
remain available through the normal `lazydeck serve` output or systemd user
service logs.
