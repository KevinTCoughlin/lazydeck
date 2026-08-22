@tool
extends EditorPlugin

## LazyDeck editor plugin (see issue #14): adds a dock for discovering,
## pairing, and inspecting Steam Deck / Steam Machine devkits via
## lazydeck's local service API (`lazydeck serve`, issue #13).
##
## Scope of this first slice: connect to an already-running `lazydeck
## serve` and the read/pair-only Devices dock. It does not export, deploy,
## launch/stop, or sync logs yet, and it does not spawn `lazydeck serve`
## itself — see integrations/godot/README.md for what's deferred and why.

const DevicesDockScript := preload("res://addons/lazydeck/ui/devices_dock.gd")

var _dock: Control


func _enter_tree() -> void:
	if not LazyDeckCompat.meets_minimum_version():
		var info := Engine.get_version_info()
		push_error(
			(
				"LazyDeck requires Godot %d.%d or newer; this editor is %s. The plugin will not activate."
				% [
					LazyDeckCompat.MIN_MAJOR,
					LazyDeckCompat.MIN_MINOR,
					info.get("string", "unknown")
				]
			)
		)
		return

	_dock = DevicesDockScript.new()
	add_control_to_dock(DOCK_SLOT_RIGHT_UL, _dock)


func _exit_tree() -> void:
	if _dock == null:
		return
	remove_control_from_docks(_dock)
	_dock.queue_free()
	_dock = null
