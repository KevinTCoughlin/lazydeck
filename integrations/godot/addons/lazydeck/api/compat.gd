@tool
class_name LazyDeckCompat
extends RefCounted

## Feature-detection helper so this addon can opportunistically use a
## newer-Godot-only API (e.g. an export option or platform hook only
## present on 4.4/4.5) while still fully loading and working, minus that
## one feature, on the plugin's stable compatibility floor (Godot 4.3).
##
## Prefer this over scattering `Engine.get_version_info()` checks (or,
## worse, hard `#if`-style version assumptions GDScript doesn't even have)
## through the codebase: raising or narrowing the floor, or adding a new
## feature-gated call site, becomes a one-line change here instead of a
## project-wide audit.
##
## Usage:
##   if LazyDeckCompat.engine_at_least(4, 4):
##       # call something only present on Godot >= 4.4
##
## MIN_MAJOR/MIN_MINOR document the floor this whole addon is written
## against; nothing outside a LazyDeckCompat.engine_at_least(...) guard
## should assume anything newer than this.
const MIN_MAJOR := 4
const MIN_MINOR := 3


static func engine_at_least(major: int, minor: int, patch: int = 0) -> bool:
	var info := Engine.get_version_info()
	var have := [int(info.get("major", 0)), int(info.get("minor", 0)), int(info.get("patch", 0))]
	var want := [major, minor, patch]
	return have >= want


## True if the running editor meets this addon's own stable floor. The
## plugin script checks this once on enable and refuses to activate below
## it, rather than loading partially and failing confusingly later.
static func meets_minimum_version() -> bool:
	return engine_at_least(MIN_MAJOR, MIN_MINOR)
