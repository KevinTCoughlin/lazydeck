# LazyDeck Unity package

A Unity Editor package (UPM) that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so
you can discover, pair, and inspect Steam Deck / Steam Machine devkits
without leaving the Unity Editor. The Unity counterpart of the
[Godot plugin](../godot) — same API, same scope.

## Status: connect, devices, build & deploy, logs

The window (**Window → LazyDeck**) covers:

- Connecting through `lazydeck serve`'s connection file, starting the
  service automatically when no connection file exists.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.
- Building the project for a chosen standalone target and deploying the
  result to the selected device, with job progress polled until it
  finishes.
- Syncing the selected device's logs to a local directory.
- Cancelling an in-flight deploy or log-sync job.

**Deliberate limitations:**

- Launching/stopping a deployed title: this is intentionally unsupported by
  the SteamOS devkit protocol; start or stop the title in the device's Steam
  UI. See [`docs/DEVICE_LAUNCH.md`](../../../docs/DEVICE_LAUNCH.md).
- Pairing a device discovered over mDNS that isn't already in
  `devices.toml` — `lazydeck serve` reads `devices.toml` once at startup,
  so add the device to the config and restart `lazydeck serve` first,
  then Connect again (re-clicking Connect alone re-reads the connection
  file, not `devices.toml`).

## Build behavior

Build & deploy calls `BuildPipeline.BuildPlayer` in-process. Unlike the
Godot side — where `EditorExport` turned out not to be exposed to
GDScript, so that plugin shells out to a second `godot --headless
--export-*` process — `BuildPipeline.BuildPlayer` is Unity's documented
scriptable build entry point, so nothing is spawned here.

Practical consequences:

- **The editor is unresponsive while the build runs.** `BuildPlayer` is
  synchronous; Unity's own build progress bar is what you'll see.
- **It builds the scenes enabled in File → Build Settings**, not the
  scene you happen to have open. If that list is empty, the window says
  so rather than building an empty player.
- **Target defaults to `StandaloneLinux64`**, the Deck's native target.
  `StandaloneWindows64` is also offered because Proton runs Windows
  builds on the Deck. Switching targets re-derives the executable name,
  since Unity keys part of its output layout off the extension
  (`.x86_64` vs `.exe`).
- **The whole output directory is deployed**, not just the executable —
  Unity writes the player's `_Data/` folder beside it, and the game
  won't run without it. That's why the window asks for an output
  directory and an executable name separately rather than one combined
  path. Point it somewhere outside `Assets/`, or Unity will import your
  build output as project assets on the next refresh (the default is a
  `Build/` folder beside `Assets/`).

## Batch-mode / CI usage

`Editor/Cli/LazyDeckCli.cs` exposes the same build/deploy/log-sync
operations as the window through Unity's own
[`-batchmode -quit -executeMethod`](https://docs.unity3d.com/Manual/EditorCommandLineArguments.html)
entry points, so a CI job can drive them without an interactive Editor —
the Unity counterpart of the Godot integration shelling out to `godot
--headless --export-*`. Options are passed as `-lazydeck<Name>`
command-line flags rather than read from the window's fields.

```bash
Unity -batchmode -quit -projectPath /path/to/project -logFile - \
  -executeMethod LazyDeck.Editor.Cli.LazyDeckCli.BuildAndDeploy \
  -lazydeckDevice steam-deck \
  -lazydeckGame 480 \
  -lazydeckOutput /path/to/project/Build \
  -lazydeckTarget StandaloneLinux64 \
  -lazydeckDevelopment \
  -lazydeckTimeoutSeconds 900
```

```bash
Unity -batchmode -quit -projectPath /path/to/project -logFile - \
  -executeMethod LazyDeck.Editor.Cli.LazyDeckCli.SyncLogs \
  -lazydeckDevice steam-deck \
  -lazydeckLogsDirectory /path/to/logs
```

- `BuildAndDeploy` requires `-lazydeckDevice`, `-lazydeckGame`, and an
  absolute `-lazydeckOutput`; `-lazydeckTarget` (`StandaloneLinux64` or
  `StandaloneWindows64`, default `StandaloneLinux64`), `-lazydeckExecutable`,
  `-lazydeckDevelopment` (a flag, no value), and `-lazydeckLaunchArgs` (a
  whitespace-separated launch command, same as the window's "Launch command"
  field) are optional.
- `SyncLogs` requires `-lazydeckDevice` and an absolute
  `-lazydeckLogsDirectory`.
- Both accept `-lazydeckTimeoutSeconds` (default 600) bounding how long they
  poll the submitted job before failing.
- Progress and errors go to `Debug.Log`, which Unity's `-logFile` captures.
  On failure they call `EditorApplication.Exit(1)`, since a batch-mode
  process otherwise exits 0 regardless of whether the executed method
  actually succeeded — check the process exit code in CI, not just the log.
- Auto-starting `lazydeck serve` and locating its connection file work the
  same as the window (see "How it finds `lazydeck serve`" below); set
  `LAZYDECK_AUTOSTART=0`/`LAZYDECK_BIN` the same way if the CI runner needs
  different behavior.

## Requirements

- Unity **2023.1** or newer (including Unity 6) — the package uses the
  `Awaitable` API, which isn't available on older LTS releases.
- A `lazydeck` executable on `PATH`, unless `LAZYDECK_BIN` or the
  **LazyDeck service settings** executable field names it explicitly.

## Installing

Add the package to your project's `Packages/manifest.json`:

```json
{
  "dependencies": {
    "com.lazydeck.editor": "file:../path/to/lazydeck/integrations/unity/com.lazydeck.editor"
  }
}
```

`examples/unity-demo` in this repository does exactly that (with a
relative path into this repo), as a minimal example project with nothing
else in it.

## Validation status

The connect/devices/discover/pair sources compile without warnings
against Unity 6.5's real managed editor assemblies.

**The build/deploy/logs/cancel additions, `Editor/Api/ServerLauncher.cs`
(auto-starting `lazydeck serve`), and the `Editor/Cli/` batch-mode entry
points have not been compiled against anything.** They were written
without a Unity Editor or any C# compiler available, so they carry the
same caveat the connect slice originally did — see the note in issue
#23. The pieces most worth checking first in a real editor:

- `BuildRunner.Build`'s `BuildPlayerOptions` shape and its handling of a
  `BuildReport` whose result isn't `Succeeded`.
- `EditorDelay.ForSecondsAsync`, which drives job polling off
  `EditorApplication.update` rather than `Awaitable.WaitForSecondsAsync`
  precisely because frame-loop-driven awaits proved unreliable for an
  `EditorWindow` outside Play mode last time.
- `ServerLauncher.StartIfNeeded`'s `Process.Start`/`ProcessStartInfo`
  shape and its cross-thread `ConcurrentQueue` hand-off of the spawned
  process's stderr back onto the main thread via `DrainMessages`.
- `LazyDeckCli`'s `-executeMethod` entry points end to end: whether Unity
  actually accepts an `internal` class with `public static` methods there,
  whether `EditorApplication.Exit(1)` reliably produces a nonzero shell
  exit code from a batch-mode run, and `LazyDeckCliClient`'s
  `HttpClient`-based synchronous requests (chosen specifically to sidestep
  the `Awaitable`/`EditorApplication.update` pump concerns noted above and
  in `LazyDeckClient`, but unverified against a real batch-mode process).

A full Editor run is also still outstanding: the available local Unity
installation had no active Editor license, so it could recognize the
demo project but could not import and execute it. Pairing, deployment
upload, and discovery still require real Steam hardware testing.

## How it finds `lazydeck serve`

`Editor/Api/ConnectionLocator.cs` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json`. That file names
the loopback port and bearer token for the currently running `lazydeck
serve` process; the window's "Connect" button re-reads it on demand.

When that connection file does not exist, the window starts `lazydeck serve`,
then waits up to five seconds for it. The process remains running after Unity
closes so another editor can reuse it. Expand **LazyDeck service settings** to
disable auto-start or configure a binary path; `LAZYDECK_AUTOSTART=0` and
`LAZYDECK_BIN` override those settings. Startup stderr is forwarded into the
LazyDeck window log.
