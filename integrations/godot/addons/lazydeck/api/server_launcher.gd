@tool
class_name LazyDeckServerLauncher
extends RefCounted

## Starts `lazydeck serve` when no connection file exists. The child process
## intentionally outlives the editor: it may also be serving another engine
## integration, and preserving it matches the established terminal workflow.

const AUTOSTART_SETTING := "lazydeck/server/autostart"
const EXECUTABLE_SETTING := "lazydeck/server/executable"


static func auto_start_enabled() -> bool:
	var environment := OS.get_environment("LAZYDECK_AUTOSTART").to_lower()
	if environment == "0" or environment == "false":
		return false
	return bool(ProjectSettings.get_setting(AUTOSTART_SETTING, true))


static func executable() -> String:
	var environment := OS.get_environment("LAZYDECK_BIN")
	if environment != "":
		return environment
	return str(ProjectSettings.get_setting(EXECUTABLE_SETTING, "lazydeck"))


## Returns {"started": bool, "error": String}. Existing but invalid connection
## files deliberately do not trigger a spawn, because overwriting a live
## service's diagnostic state would make the real problem harder to diagnose.
static func start_if_needed() -> Dictionary:
	if FileAccess.file_exists(LazyDeckConnectionLocator.default_path()):
		return {"started": false}
	if not auto_start_enabled():
		return {"started": false, "error": "lazydeck serve is not running; auto-start is disabled"}

	var binary := executable()
	if binary.strip_edges() == "":
		return {
			"started": false, "error": "lazydeck serve auto-start failed: no executable configured"
		}
	# create_process() returns the new PID, or -1 if it couldn't be created —
	# not a Godot Error code, so error_string() isn't meaningful here. -1 is
	# the only documented failure value; anything else non-positive is
	# reported as-is rather than guessed at.
	var pid := OS.create_process(binary, PackedStringArray(["serve"]))
	if pid <= 0:
		return {
			"started": false,
			"error":
			(
				"lazydeck serve auto-start failed: could not start %s (create_process returned %d)"
				% [binary, pid]
			),
		}
	return {"started": true, "pid": pid}
