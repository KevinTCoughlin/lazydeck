# LazyDeck Unreal Engine plugin

An Unreal Engine editor plugin that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so you
can discover, pair, and inspect Steam Deck / Steam Machine devkits without
leaving the Unreal Editor. The Unreal counterpart of the
[Godot plugin](../godot) and the [Unity package](../unity) — same API,
similar scope.

## Status: connect, devices, pair, discover, cook/package, deploy, logs

The dock (**Window → LazyDeck**) covers:

- Connecting through `lazydeck serve`'s connection file, starting the
  service automatically when no connection file exists.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.
- Cooking and packaging the currently open project for Linux or Win64
  (Proton) via UAT's `BuildCookRun`, archiving into the same directory
  Deploy reads from — the Unreal counterpart of Unity's in-process
  `BuildPipeline.BuildPlayer` and the Godot addon's headless
  `godot --export-*` subprocess. See "Build/cook behavior" below.
- Deploying a build directory (game ID + absolute path) to the selected
  device, with job progress polled until it finishes.
- Syncing the selected device's logs to a local directory.
- Cancelling an in-flight deploy or log-sync job.

### Build/cook behavior

**Cook and Package** runs `RunUAT BuildCookRun -cook -allmaps -build -stage
-pak -archive` for the currently open `.uproject` against the platform
picked in the dropdown (`Linux`, native Steam Deck/Steam Machine target, or
`Win64`, for titles that rely on Proton) and either the Development or
Shipping client config. Unlike Unity's synchronous `BuildPipeline.BuildPlayer`,
UAT runs out-of-process via `IUATHelperModule` and reports back
asynchronously — the dock stays responsive, and progress/errors also land in
the Output Log the way any other Editor-triggered UAT task's would.

UAT stages the archived build under a platform subfolder of the directory
you give it (e.g. `<output>/Linux`), not the bare directory itself, so after
a successful cook point **Deploy**'s build-directory field at that subfolder
before deploying. This differs from Unity/Godot, where the build output
lands directly in the directory you named — a consequence of how
`BuildCookRun -archivedirectory` lays out its output, not a LazyDeck choice.

**Deliberate limitations:**

- Launching/stopping a deployed title: this is intentionally unsupported by
  the SteamOS devkit protocol; start or stop the title in the device's Steam
  UI. See [`docs/DEVICE_LAUNCH.md`](../../docs/DEVICE_LAUNCH.md).
- Pairing a device discovered over mDNS that isn't already in
  `devices.toml` — `lazydeck serve` reads `devices.toml` once at startup,
  so add the device to the config and restart `lazydeck serve` first, then
  Connect again (re-clicking Connect alone re-reads the connection file,
  not `devices.toml`).
- **Cooking still requires a one-time manual step to get to a
  `Development`/`Shipping` config that actually runs on the target**: UAT's
  `BuildCookRun` cooks and stages, but does not run a first-time
  content/plugin-compatibility pass for you — projects with editor-only
  plugins or platform-gated content may still need an initial manual
  `Package Project` run to shake those out before this dock's automated
  path is reliable, the same caveat that applies to `BuildCookRun` when run
  by hand.

## Requirements

- Unreal Engine 5.x with a C++ project (an editor plugin's module must be
  compiled; a Blueprint-only project can't load it without first adding any
  C++ file to generate build targets). Cooking targets whatever engine
  version the project itself is built against; there is no LazyDeck-side
  version pin beyond the `IUATHelperModule`/UAT command-line surface this
  plugin targets (UE 5.3–5.4; see "Validation status").
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
  releases). `IUATHelperModule::CreateUatTask`'s signature in particular has
  changed across 5.x releases (the result-callback parameter shape most
  notably); `LazyDeckCookRunner.cpp` targets the UE 5.3–5.4 signature and
  will need adjusting for other versions.
- Exercise Connect/Discover/Pair against a `lazydeck serve --fixture`
  instance first (no real hardware required).
- Confirm `FPlatformProcess::CreateProc`'s detached-launch behavior on your
  target platform, and that `FLazyDeckConnectionLocator::DefaultPath()`
  resolves to a location `lazydeck serve` actually writes to on that
  platform.
- **Cook and Package (`LazyDeckCookRunner`) has not been run against a real
  UE 5.x project or toolchain.** The `BuildCookRun` command line it issues
  mirrors the Editor's own File > Package Project menu, but has not been
  verified to produce a deployable Linux or Win64 archive end to end;
  budget for at least one real cook/package/deploy/launch cycle against a
  Steam Deck or Steam Machine before trusting it in a release pipeline.
- Real deploy/log-sync/pairing still requires Steam Deck / Steam Machine
  hardware to validate end to end, same as the Godot and Unity integrations.
