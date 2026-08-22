@tool
class_name LazyDeckConnectionLocator
extends RefCounted

## Finds and parses the connection file `lazydeck serve` writes on
## startup. Mirrors internal/server/connection.go's connectionFilePath()
## exactly (same env var, same fallback, same relative path) so this
## plugin locates the same file the Go service itself considers
## authoritative, without needing to invoke `lazydeck` at all.


## Prefers $XDG_RUNTIME_DIR (matches the Go side's preference for a
## tmpfs-backed, per-session, already-private location for a file holding
## a bearer token) and falls back to the OS cache directory otherwise
## (matches Go's os.UserCacheDir(), e.g. ~/Library/Caches on macOS).
static func default_path() -> String:
	var runtime_dir := OS.get_environment("XDG_RUNTIME_DIR")
	if runtime_dir != "":
		return runtime_dir.path_join("lazydeck").path_join("serve.json")
	return OS.get_cache_dir().path_join("lazydeck").path_join("serve.json")


## Reads and parses the connection file at path (the default location
## when path is empty). Returns {"ok": true, "info": LazyDeckConnectionInfo,
## "path": String} on success, or {"ok": false, "error": String} — a
## Dictionary rather than two return values, since GDScript static
## functions can't return an (info, error) pair directly.
static func load_connection(path: String = "") -> Dictionary:
	var resolved_path := path if path != "" else default_path()
	if not FileAccess.file_exists(resolved_path):
		return {
			"ok": false,
			"error": "no connection file at %s (is `lazydeck serve` running?)" % resolved_path,
		}

	var file := FileAccess.open(resolved_path, FileAccess.READ)
	if file == null:
		return {
			"ok": false,
			"error":
			"could not open %s: %s" % [resolved_path, error_string(FileAccess.get_open_error())],
		}

	var text := file.get_as_text()
	file.close()

	var parsed = JSON.parse_string(text)
	if typeof(parsed) != TYPE_DICTIONARY:
		return {"ok": false, "error": "malformed connection file at %s" % resolved_path}

	return {"ok": true, "info": LazyDeckConnectionInfo.from_dict(parsed), "path": resolved_path}
