# LazyDeck Unity package

A Unity Editor package (UPM) that talks to `lazydeck serve` (see the root
README's ["How it talks to devices"](../../README.md) and issue #13) so
you can discover, pair, and inspect Steam Deck / Steam Machine devkits
without leaving the Unity Editor. The Unity counterpart of the
[Godot plugin](../godot) — same API, same first-slice scope.

## Status: connect + devices window only

This first slice of issue #17 covers:

- A "LazyDeck" window (**Window → LazyDeck**) that connects to an
  already-running `lazydeck serve` by reading its connection file.
- Listing the devices configured in `devices.toml`.
- Browsing for devkits announcing themselves on the LAN (mDNS discovery).
- Pairing this workstation's SSH key with a device that's already in
  `devices.toml`.

**Not included yet** (tracked as follow-up work on issue #17, matching
what #14/#16 deferred on the Godot side):

- Build, deploy, and log-sync from the editor.
- Launching/stopping a deployed title (the local service API itself
  doesn't support this yet either — see `/v1/capabilities`).
- The package spawning `lazydeck serve` itself. For now, run `lazydeck
  serve` yourself in a terminal before opening the window.
- Pairing a device discovered over mDNS that isn't already in
  `devices.toml` — `lazydeck serve` reads `devices.toml` once at startup,
  so add the device to the config and restart `lazydeck serve` first,
  then Connect again (re-clicking Connect alone re-reads the connection
  file, not `devices.toml`).

## Requirements

- Unity **2023.1** or newer (including Unity 6) — the package uses the
  `Awaitable` API, which isn't available on older LTS releases.
- `lazydeck serve` running (see the root README) before you open the window.

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

## ⚠️ Not yet tested in a real Unity Editor

This package was written without access to a Unity Editor to run it in —
a stricter version of the same gap the Godot plugin (#14/#16) started
with, since there was no C# compiler available at all to even check it
parses, let alone runs. Every `.cs` file was written carefully against
the Unity Editor scripting API docs, checked by hand for balanced
braces/parens, and kept to the most conservative, longest-documented
patterns available (e.g. `Awaitable.NextFrameAsync()` polling rather than
a newer/less-certain bridging API, `JsonUtility` DTOs kept as plain
classes rather than structs). None of that is a substitute for actually
running it.

The piece most worth checking first in a real Editor session: whether
`LazyDeckClient.RequestAsync`'s `while (!operation.isDone) await
Awaitable.NextFrameAsync();` loop pumps correctly for an `EditorWindow`
outside Play mode on your Unity version. If it doesn't, wiring
`UnityWebRequestAsyncOperation.completed` into an
`AwaitableCompletionSource` is the documented alternative — see the
comment on `RequestAsync` in `LazyDeckClient.cs`.

Please test locally and report anything that breaks — this note should
be removed once someone has verified it against a real Unity Editor and
a real (or fake, per `internal/server`'s test pattern) `lazydeck serve`.

## How it finds `lazydeck serve`

`Editor/Api/ConnectionLocator.cs` reads the same connection file
`internal/server/connection.go` writes: `$XDG_RUNTIME_DIR/lazydeck/serve.json`
when set, otherwise `<OS cache dir>/lazydeck/serve.json`. That file names
the loopback port and bearer token for the currently running `lazydeck
serve` process; the window's "Connect" button re-reads it on demand.
