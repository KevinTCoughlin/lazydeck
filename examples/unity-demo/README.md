# LazyDeck Unity demo

A minimal Unity 2023.1+ (Unity 6) project with nothing in it except the
LazyDeck package referenced (see
[`../../integrations/unity`](../../integrations/unity)), so you can try
the editor window without wiring it into a real game first.

`Packages/manifest.json` references `com.lazydeck.editor` via a relative
`file:` path into `../../integrations/unity/com.lazydeck.editor` — there
is only one copy of the package in this repository; this project just
points at it. Unity will regenerate the rest of `ProjectSettings/` and
create a `Library/` cache the first time it opens this folder.

## Try it

1. Run `lazydeck serve` in a terminal (see the root README).
2. Open this folder as a project in Unity 2023.1+ (Unity 6).
3. Open **Window → LazyDeck** and click **Connect**.

This project intentionally contains no device-specific or secret data —
see `integrations/unity/README.md` for what's configured where.

`ProjectSettings/ProjectVersion.txt` names a representative Unity 6
version; if you're on a different 6.x patch, Unity will offer to
open/upgrade the project anyway — that prompt is expected, not an error.
