@tool
class_name LazyDeckExportRunner
extends RefCounted

## Runs Godot's supported command-line export flow with the current editor
## executable. Godot does not expose its EditorExport singleton to GDScript,
## so an editor plugin cannot invoke EditorExportPlatform.export_project()
## in-process.


## Returns the display names of every export preset configured for this
## project (Project > Export... in the editor), in configuration order.
static func list_preset_names() -> PackedStringArray:
	var names := PackedStringArray()
	var config := ConfigFile.new()
	if config.load("res://export_presets.cfg") != OK:
		return names
	for section in config.get_sections():
		if section.begins_with("preset.") and not section.contains(".options"):
			var preset_name: String = config.get_value(section, "name", "")
			if preset_name != "":
				names.append(preset_name)
	return names


## Exports the named preset's binary as output_directory/executable_name
## (debug or release per the debug flag) and everything else the platform
## writes alongside it into output_directory. Returns {"ok": bool,
## "error": String}, consistent with the rest of this addon's API surface,
## rather than throwing.
static func export_preset(
	preset_name: String, output_directory: String, executable_name: String, debug: bool
) -> Dictionary:
	if not list_preset_names().has(preset_name):
		return {"ok": false, "error": "no export preset named %s" % preset_name}

	if not output_directory.is_absolute_path():
		return {"ok": false, "error": "output_directory must be an absolute path"}

	if not DirAccess.dir_exists_absolute(output_directory):
		var mkdir_err := DirAccess.make_dir_recursive_absolute(output_directory)
		if mkdir_err != OK:
			return {
				"ok": false,
				"error": "could not create %s: %s" % [output_directory, error_string(mkdir_err)]
			}

	var output_path := output_directory.path_join(executable_name)
	var output: Array[String] = []
	var args := PackedStringArray(
		[
			"--headless",
			"--path",
			ProjectSettings.globalize_path("res://"),
			"--export-debug" if debug else "--export-release",
			preset_name,
			output_path,
		]
	)
	var exit_code := OS.execute(OS.get_executable_path(), args, output, true)
	if exit_code != 0:
		var details := "\n".join(output).strip_edges()
		return {
			"ok": false,
			"error":
			"Godot export exited with code %d%s"
			% [exit_code, "" if details == "" else ": %s" % details]
		}

	return {"ok": true, "error": ""}
