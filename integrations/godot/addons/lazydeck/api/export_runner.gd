@tool
class_name LazyDeckExportRunner
extends RefCounted

## Wraps Godot's own export machinery (the EditorExport singleton and
## EditorExportPlatform.export_project()) — the same API Godot's Export
## dialog and the `--export-release`/`--export-debug` CLI flags use
## internally — per issue #14's "Run the Godot export through supported
## editor APIs where possible."
##
## ⚠️ This class was written without a Godot editor available to verify
## it against (see integrations/godot/README.md). export_project()'s
## exact path semantics are the specific thing most worth checking first:
## Godot's Export dialog asks for a *file* path (e.g.
## "/tmp/export/mygame.x86_64"), and the platform writes that executable
## plus its accompanying .pck (and any other per-platform files) into the
## same directory — which is why export_preset() below takes a directory
## and an executable name rather than a single combined path, and why the
## resulting directory (not just the executable) is what gets deployed.
## If the platform doesn't accept the file name as given (e.g. appends or
## corrects an extension itself), the executable name shown in the dock
## after a successful export may not exactly match what you typed; adjust
## to match what's actually on disk.
##
## No subprocess is spawned, so this exports the project's current
## on-disk state — save unsaved scenes/resources first.


## Returns the display names of every export preset configured for this
## project (Project > Export... in the editor), in configuration order.
static func list_preset_names() -> PackedStringArray:
	var names := PackedStringArray()
	var export_singleton := EditorExport.get_singleton()
	if export_singleton == null:
		return names
	for i in export_singleton.get_export_preset_count():
		var preset := export_singleton.get_export_preset(i)
		if preset:
			names.append(preset.get_name())
	return names


## Exports the named preset's binary as output_directory/executable_name
## (debug or release per the debug flag) and everything else the platform
## writes alongside it into output_directory. Returns {"ok": bool,
## "error": String}, consistent with the rest of this addon's API surface,
## rather than throwing.
static func export_preset(
	preset_name: String, output_directory: String, executable_name: String, debug: bool
) -> Dictionary:
	var export_singleton := EditorExport.get_singleton()
	if export_singleton == null:
		return {
			"ok": false, "error": "EditorExport singleton is unavailable in this editor session"
		}

	var preset: EditorExportPreset = null
	for i in export_singleton.get_export_preset_count():
		var candidate := export_singleton.get_export_preset(i)
		if candidate and candidate.get_name() == preset_name:
			preset = candidate
			break

	if preset == null:
		return {"ok": false, "error": "no export preset named %s" % preset_name}

	var platform := preset.get_platform()
	if platform == null:
		return {"ok": false, "error": "preset %s has no export platform" % preset_name}

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
	var err: int = platform.export_project(preset, debug, output_path)
	if err != OK:
		return {"ok": false, "error": "export failed: %s" % error_string(err)}

	return {"ok": true, "error": ""}
