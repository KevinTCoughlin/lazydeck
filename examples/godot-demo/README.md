# LazyDeck Godot demo

A minimal Godot 4.3+ project with nothing in it except the LazyDeck
plugin enabled (see [`../../integrations/godot`](../../integrations/godot)),
so you can try the editor dock without wiring it into a real game first.

`addons/lazydeck` is a symlink to `../../integrations/godot/addons/lazydeck`
— there is only one copy of the plugin in this repository; this project
just points at it.

## Try it

1. Run `lazydeck serve` in a terminal (see the root README).
2. Open this folder as a project in Godot 4.3+.
3. Open the **LazyDeck** dock (bottom-right by default) and click
   **Connect**.

This project intentionally contains no device-specific or secret data —
see `integrations/godot/README.md` for what's configured where.
