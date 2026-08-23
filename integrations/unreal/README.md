# LazyDeck Unreal Engine plugin

An Unreal Engine editor plugin that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so you
can discover, pair, and inspect Steam Deck / Steam Machine devkits without
leaving the Unreal Editor. The Unreal counterpart of the
[Godot plugin](../godot) and the [Unity package](../unity) — same API,
similar scope.

## Status: connect, devices, pair, discover, deploy, logs

The dock (**Window → LazyDeck**) covers:

- Connecting through `lazydeck serve`'s connection file, starting the
  service automatically when no connection file exists.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.
- Deploying a build directory you already have (game ID + absolute path)
  to the selected device, with job progress polled until it finishes.
- Syncing the selected device's logs to a local directory.
- Cancelling an in-flight deploy or log-sync job.

**Deliberate limitations:**

- Launching/stopping a deployed title: this is intentionally unsupported by
  the SteamOS devkit protocol; start or stop the title in the device's Steam
  UI. See [`docs/DEVICE_LAUNCH.md`](../../docs/DEVICE_LAUNCH.md).
- Pairing a device discovered over mDNS that isn't already in
  `devices.toml` — `lazydeck serve` reads `devices.toml` once at startup,
  so add the device to the config and restart `lazydeck serve` first, then
  Connect again (re-clicking Connect alone re-reads the connection file,
  not `devices.toml`).
- **No automated cook/package step.** Unlike the Unity package (which
  drives `BuildPipeline.BuildPlayer` in-process) or the Godot addon (which
  shells out to `godot --headless --export-*`), this plugin does not yet
  invoke Unreal's own packaging pipeline. Package your project through the
  normal Editor/UAT flow first, then point **Deploy** at the resulting
  staged build directory. Wiring up `IUATHelperModule` (or an
  `UnrealAutomationTool` RunUAT invocation) to build-then-deploy in one
  click is a natural follow-up, mirroring `export_runner.gd` /
  `BuildRunner.cs`.

## Requirements

- Unreal Engine 5.x with a C++ project (an editor plugin's module must be
  compiled; a Blueprint-only project can't load it without first adding any
  C++ file to generate build targets).
- A `lazydeck` executable on `PATH`, unless `LAZYDECK_BIN` names it
  explicitly.

## Installing

Copy (or symlink) `LazyDeck/` into your Unreal project's `Plugins/`
directory, then enable **LazyDeck** in Edit → Plugins (under the Editor
category) and restart the editor.

## Tooling

- **Style**: `integrations/unreal/.clang-format` pins the code to Epic's
  Unreal Engine coding standard (tabs, Allman braces, `PascalCase` types).
  `just lint-unreal` checks it; `just format-unreal` reformats in place.
  Both need a `clang-format` on `PATH` (any recent LLVM release works; no
  Unreal Engine install required, since this only touches formatting).
  CI runs the same check as the **Unreal C++ format** job.
- **Static analysis**: `integrations/unreal/.clang-tidy` scopes a
  modernize/bugprone/performance check set for local use once you have an
  Unreal-aware `compile_commands.json` (that needs a real engine build, so
  it isn't wired into CI — see the file's header comment for the exact
  invocation).
- **Build settings**: `LazyDeckEditor.Build.cs` opts into the modern UE5
  module defaults new plugin templates use —
  `DefaultBuildSettings = BuildSettingsVersion.Latest`,
  `IWYUSupport = IWYUSupport.Full` (strict include-what-you-use), and the
  engine's current default C++ standard — rather than silently inheriting
  whatever an older module default would otherwise imply.

## How it finds `lazydeck serve`

`LazyDeckConnectionLocator` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json` ($XDG_CACHE_HOME or
`~/.cache` on Linux, `~/Library/Caches` on macOS, `%LocalAppData%` on
Windows — matching Go's `os.UserCacheDir()`). That file
names the loopback port and bearer token for the currently running
`lazydeck serve` process; the dock's "Connect" button re-reads it on demand.

When that connection file does not exist, the dock starts `lazydeck serve`
via `FPlatformProcess::CreateProc` (detached, so it outlives the editor) and
polls for up to five seconds. Set `LAZYDECK_AUTOSTART=0` to disable
auto-start and manage the service yourself.

## Validation status

**This plugin was written without access to an Unreal Editor or a C++
toolchain able to compile against the Unreal API, and has not been
built or run.** It was authored by close reading of the Unreal APIs
involved (`FHttpModule`/`IHttpRequest`, `FJsonSerializer`,
`FPlatformProcess::CreateProc`, `SCompoundWidget`/Slate, `FGlobalTabmanager`)
and by mirroring the request/response shapes the already-validated Godot and
Unity clients use against the same `api/openapi.yaml` contract. Treat it as
a starting point, not a drop-in verified integration. Before relying on it:

- Compile the `LazyDeckEditor` module against your engine version and fix
  any API drift (Slate/HTTP/Json module APIs do shift between engine
  releases).
- Exercise Connect/Discover/Pair against a `lazydeck serve --fixture`
  instance first (no real hardware required).
- Confirm `FPlatformProcess::CreateProc`'s detached-launch behavior on your
  target platform, and that `FLazyDeckConnectionLocator::DefaultPath()`
  resolves to a location `lazydeck serve` actually writes to on that
  platform.
- Real deploy/log-sync/pairing still requires Steam Deck / Steam Machine
  hardware to validate end to end, same as the Godot and Unity integrations.
