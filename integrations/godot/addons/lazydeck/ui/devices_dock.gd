@tool
extends Control

## The LazyDeck editor dock: connects to a running `lazydeck serve`,
## lists configured devices, discovers devkits on the LAN, and pairs a
## configured device. Export/deploy/log-sync are deliberately out of
## scope for this dock — see integrations/godot/README.md.
##
## Built entirely in code rather than a .tscn scene: this addon was
## authored without access to a Godot editor to visually assemble and
## verify a scene file in (see integrations/godot/README.md), and a typo
## in hand-written .tscn resource syntax fails far less gracefully than a
## mistake in a method call here.

var _client: LazyDeckClient
var _devices: Array = []  # cached [{"id":..., "machine":..., "login":...}, ...]

var _status_label: Label
var _connect_button: Button
var _devices_list: ItemList
var _pair_button: Button
var _discover_button: Button
var _discovered_list: ItemList
var _log: RichTextLabel

var _preset_option: OptionButton
var _refresh_presets_button: Button
var _debug_check: CheckBox
var _game_id_field: LineEdit
var _output_dir_field: LineEdit
var _executable_name_field: LineEdit
var _launch_args_field: LineEdit
var _build_deploy_button: Button
var _logs_dir_field: LineEdit
var _sync_logs_button: Button
var _cancel_job_button: Button
var _current_job_id: String = ""
var _busy := false


func _ready() -> void:
	name = "LazyDeck"
	custom_minimum_size = Vector2(260, 0)
	_build_ui()
	_refresh_presets()
	_connect()


func _exit_tree() -> void:
	if _client:
		_client.queue_free()
		_client = null


func _build_ui() -> void:
	var root := VBoxContainer.new()
	root.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	add_child(root)

	_status_label = Label.new()
	_status_label.text = "Not connected"
	root.add_child(_status_label)

	_connect_button = Button.new()
	_connect_button.text = "Connect"
	_connect_button.pressed.connect(_on_connect_pressed)
	root.add_child(_connect_button)

	root.add_child(HSeparator.new())

	var devices_header := Label.new()
	devices_header.text = "Configured devices (devices.toml)"
	root.add_child(devices_header)

	_devices_list = ItemList.new()
	_devices_list.custom_minimum_size = Vector2(0, 100)
	_devices_list.item_selected.connect(func(_index: int) -> void: _update_buttons())
	root.add_child(_devices_list)

	_pair_button = Button.new()
	_pair_button.text = "Pair selected device"
	_pair_button.disabled = true
	_pair_button.pressed.connect(_on_pair_pressed)
	root.add_child(_pair_button)

	root.add_child(HSeparator.new())

	var discover_header := Label.new()
	discover_header.text = "Discover on LAN"
	root.add_child(discover_header)

	_discover_button = Button.new()
	_discover_button.text = "Discover"
	_discover_button.pressed.connect(_on_discover_pressed)
	root.add_child(_discover_button)

	var discover_note := Label.new()
	discover_note.autowrap_mode = TextServer.AUTOWRAP_WORD
	discover_note.text = (
		"Discovered devices aren't automatically pairable: add a matching "
		+ "[[device]] entry to devices.toml and restart lazydeck serve first "
		+ "(it only reads devices.toml at startup), then Connect again."
	)
	root.add_child(discover_note)

	_discovered_list = ItemList.new()
	_discovered_list.custom_minimum_size = Vector2(0, 80)
	root.add_child(_discovered_list)

	root.add_child(HSeparator.new())
	_build_deploy_section(root)
	root.add_child(HSeparator.new())
	_build_logs_section(root)

	_cancel_job_button = Button.new()
	_cancel_job_button.text = "Cancel current job"
	_cancel_job_button.disabled = true
	_cancel_job_button.pressed.connect(_on_cancel_job_pressed)
	root.add_child(_cancel_job_button)

	root.add_child(HSeparator.new())

	_log = RichTextLabel.new()
	_log.custom_minimum_size = Vector2(0, 100)
	_log.bbcode_enabled = false
	_log.scroll_following = true
	root.add_child(_log)


## Exports the currently selected preset and deploys it to whichever
## device is selected in _devices_list above. Building this UI as an
## extension of the same dock (rather than a separate window) keeps a
## single "pick a device" selection driving both pairing and deploy,
## matching the workflow issue #14 describes.
func _build_deploy_section(root: VBoxContainer) -> void:
	var header := Label.new()
	header.text = "Build & deploy"
	root.add_child(header)

	var preset_row := HBoxContainer.new()
	root.add_child(preset_row)
	_preset_option = OptionButton.new()
	_preset_option.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	preset_row.add_child(_preset_option)
	_refresh_presets_button = Button.new()
	_refresh_presets_button.text = "Refresh presets"
	_refresh_presets_button.pressed.connect(_refresh_presets)
	preset_row.add_child(_refresh_presets_button)

	_debug_check = CheckBox.new()
	_debug_check.text = "Debug export"
	_debug_check.button_pressed = true
	root.add_child(_debug_check)

	_game_id_field = LineEdit.new()
	_game_id_field.placeholder_text = "Game ID (Steam shortcut name)"
	root.add_child(_game_id_field)

	_output_dir_field = LineEdit.new()
	_output_dir_field.placeholder_text = "Local export output directory (absolute path)"
	_output_dir_field.text = ProjectSettings.globalize_path("res://export")
	root.add_child(_output_dir_field)

	_executable_name_field = LineEdit.new()
	_executable_name_field.placeholder_text = "Executable name"
	_executable_name_field.text = _default_executable_name()
	root.add_child(_executable_name_field)

	_launch_args_field = LineEdit.new()
	_launch_args_field.placeholder_text = "Launch command (optional), e.g. ./MyGame.sh --fullscreen"
	root.add_child(_launch_args_field)

	var export_note := Label.new()
	export_note.autowrap_mode = TextServer.AUTOWRAP_WORD
	export_note.text = (
		"Exports the project's current saved state (save first if you have "
		+ "unsaved changes), then deploys the output directory. If the "
		+ "resulting file on disk doesn't match the executable name above, "
		+ "update it to match — see export_runner.gd."
	)
	root.add_child(export_note)

	_build_deploy_button = Button.new()
	_build_deploy_button.text = "Build & deploy"
	_build_deploy_button.disabled = true
	_build_deploy_button.pressed.connect(_on_build_deploy_pressed)
	root.add_child(_build_deploy_button)


func _build_logs_section(root: VBoxContainer) -> void:
	var header := Label.new()
	header.text = "Sync logs"
	root.add_child(header)

	_logs_dir_field = LineEdit.new()
	_logs_dir_field.placeholder_text = "Local logs directory (absolute path)"
	_logs_dir_field.text = OS.get_user_data_dir().path_join("lazydeck_logs")
	root.add_child(_logs_dir_field)

	_sync_logs_button = Button.new()
	_sync_logs_button.text = "Sync logs from selected device"
	_sync_logs_button.disabled = true
	_sync_logs_button.pressed.connect(_on_sync_logs_pressed)
	root.add_child(_sync_logs_button)


## A reasonable default executable name: the project's configured name,
## sanitized to characters safe in a file name, falling back to a fixed
## name if the project name is empty or sanitizes away to nothing.
##
## Uses a regex rather than String.is_valid_identifier() per character:
## that method judges whether an entire string would be a valid
## identifier (so, among other things, "no leading digit"), not whether
## one character is filename-safe — checking it per character would
## reject every digit, not just a leading one.
func _default_executable_name() -> String:
	var project_name: String = ProjectSettings.get_setting("application/config/name", "")
	var non_filename_chars := RegEx.new()
	non_filename_chars.compile("[^a-z0-9_-]+")
	var sanitized := non_filename_chars.sub(project_name.to_lower(), "_", true)
	return sanitized if sanitized != "" else "game"


## Splits a whitespace-separated launch command into argv tokens for the
## deployments endpoint's optional argv field (see api/openapi.yaml). No
## quoting support is offered; this mirrors the simple space-splitting
## expectation set by the field's own example (["./MyGame.sh", "--fullscreen"]).
##
## Matches runs of non-whitespace characters (rather than splitting on " ")
## so tabs and repeated spaces between tokens are handled the same way as
## the Unreal (FString::ParseIntoArrayWS) and Unity (string.Split with a
## null separator) clients.
func _parse_argv(launch_command: String) -> PackedStringArray:
	var tokens := RegEx.new()
	tokens.compile("\\S+")
	var argv := PackedStringArray()
	for m in tokens.search_all(launch_command):
		argv.append(m.get_string())
	return argv


func _refresh_presets() -> void:
	_preset_option.clear()
	var names := LazyDeckExportRunner.list_preset_names()
	for preset_name in names:
		_preset_option.add_item(preset_name)
	if names.is_empty():
		_log_line("No export presets configured (Project > Export...).")


func _connect() -> void:
	if _client:
		_client.queue_free()
		_client = null
	# Reconnecting invalidates any job this dock was tracking against the
	# old client — there's nothing left to poll or cancel.
	_current_job_id = ""
	_busy = false

	var located := LazyDeckConnectionLocator.load_connection()
	if not located.get("ok", false):
		var launch := LazyDeckServerLauncher.start_if_needed()
		if launch.get("started", false):
			_log_line(
				(
					"Starting lazydeck serve (%s, pid %d)..."
					% [LazyDeckServerLauncher.executable(), int(launch.get("pid", 0))]
				)
			)
			for attempt in 20:
				await get_tree().create_timer(0.25).timeout
				located = LazyDeckConnectionLocator.load_connection()
				if located.get("ok", false):
					break
		elif launch.has("error"):
			_log_line(str(launch["error"]))
	if not located.get("ok", false):
		_status_label.text = "Not connected"
		_log_line(
			"Could not find a running lazydeck serve: %s" % located.get("error", "unknown error")
		)
		return

	var info: LazyDeckConnectionInfo = located["info"]
	# Captured locally rather than read back from _client after the await
	# below: if Connect is clicked again (or _ready()'s initial connect is
	# still in flight) before this resumes, _client will already point at
	# a newer, different client by then, and this call must recognize it's
	# been superseded instead of mutating UI state on the newer client's
	# behalf.
	var client := LazyDeckClient.new(info)
	_client = client
	add_child(client)

	var caps := await client.get_capabilities()
	if _client != client:
		return

	if not caps.get("ok", false):
		_status_label.text = "Not connected"
		_log_line("Found a connection file but the request failed: %s" % _error_message(caps))
		client.queue_free()
		_client = null
		_update_buttons()
		return

	_status_label.text = "Connected: %s (pid %d, port %d)" % [info.api_version, info.pid, info.port]
	_log_line("Connected to lazydeck serve at %s" % info.base_url)
	await _refresh_devices(client)


func _refresh_devices(client: LazyDeckClient) -> void:
	var result := await client.list_devices()
	if _client != client:
		return

	_devices_list.clear()
	_devices.clear()
	if not result.get("ok", false):
		_log_line("Failed to list devices: %s" % _error_message(result))
		_update_buttons()
		return

	var data: Dictionary = result.get("data", {})
	var devices: Array = data.get("devices", [])
	for device in devices:
		_devices.append(device)
		_devices_list.add_item("%s (%s)" % [device.get("id", "?"), device.get("machine", "?")])
	_update_buttons()


func _update_buttons() -> void:
	var has_selection := not _devices_list.get_selected_items().is_empty()
	var connected := _client != null
	_pair_button.disabled = _busy or not connected or not has_selection
	_discover_button.disabled = _busy or not connected
	_build_deploy_button.disabled = _busy or not connected or not has_selection
	_sync_logs_button.disabled = _busy or not connected or not has_selection
	_cancel_job_button.disabled = not connected or _current_job_id == ""


func _on_connect_pressed() -> void:
	await _connect()


func _on_pair_pressed() -> void:
	var selected := _devices_list.get_selected_items()
	if selected.is_empty() or _client == null:
		return
	var client := _client
	var device: Dictionary = _devices[selected[0]]
	var device_id: String = device.get("id", "")
	_log_line("Pairing %s..." % device_id)
	var result := await client.pair_device(device_id)
	if _client != client:
		return
	if result.get("ok", false):
		_log_line("Paired %s." % device_id)
	else:
		_log_line("Failed to pair %s: %s" % [device_id, _error_message(result)])


func _on_discover_pressed() -> void:
	if _client == null:
		_log_line("Not connected.")
		return
	var client := _client
	_discovered_list.clear()
	_log_line("Discovering devkits on the LAN...")
	var result := await client.discover_devices()
	if _client != client:
		return
	if not result.get("ok", false):
		_log_line("Discover failed: %s" % _error_message(result))
		return

	var data: Dictionary = result.get("data", {})
	var found: Array = data.get("devices", [])
	if found.is_empty():
		_log_line("No devkits found.")
		return
	for device in found:
		_discovered_list.add_item(
			(
				"%s @ %s:%d"
				% [device.get("name", "?"), device.get("address", "?"), int(device.get("port", 0))]
			)
		)


## Returns the currently selected configured device, or an empty
## Dictionary if nothing (or nothing valid) is selected. Shared by every
## action below that operates on "the selected device" — pairing,
## build/deploy, and log sync all act on the same selection.
func _selected_device() -> Dictionary:
	var selected := _devices_list.get_selected_items()
	if selected.is_empty():
		return {}
	var index: int = selected[0]
	if index < 0 or index >= _devices.size():
		return {}
	return _devices[index]


## Validates and gathers everything _on_build_deploy_pressed needs, or
## logs why it can't and returns {} — pulled out of that handler so its
## own return count stays under gdlint's max-returns rather than piling
## every early-exit validation into the same function as the async flow.
func _validate_deploy_inputs() -> Dictionary:
	if _client == null:
		_log_line("Not connected.")
		return {}
	var device := _selected_device()
	if device.is_empty():
		_log_line("Select a configured device first.")
		return {}
	if _preset_option.item_count == 0 or _preset_option.selected < 0:
		_log_line("Select an export preset first.")
		return {}
	# Globalized so a res://... or user://... path (easy to type by habit in
	# a Godot editor field) resolves to the real workstation path the
	# backend and LazyDeckExportRunner both require, rather than being
	# rejected as "not absolute".
	var output_dir := ProjectSettings.globalize_path(_output_dir_field.text.strip_edges())
	var executable_name := _executable_name_field.text.strip_edges()
	var game_id := _game_id_field.text.strip_edges()
	if output_dir == "" or executable_name == "" or game_id == "":
		_log_line("Output directory, executable name, and game ID are all required.")
		return {}
	if not output_dir.is_absolute_path():
		_log_line("Output directory must be an absolute path.")
		return {}
	return {
		"device_id": device.get("id", ""),
		"preset_name": _preset_option.get_item_text(_preset_option.selected),
		"output_dir": output_dir,
		"executable_name": executable_name,
		"game_id": game_id,
		"argv": _parse_argv(_launch_args_field.text),
	}


func _on_build_deploy_pressed() -> void:
	var params := _validate_deploy_inputs()
	if params.is_empty():
		return
	var client := _client

	_busy = true
	_update_buttons()
	_log_line(
		(
			"Exporting preset '%s' (%s) to %s..."
			% [
				params.preset_name,
				"debug" if _debug_check.button_pressed else "release",
				params.output_dir
			]
		)
	)
	var export_result := LazyDeckExportRunner.export_preset(
		params.preset_name, params.output_dir, params.executable_name, _debug_check.button_pressed
	)
	if not export_result.get("ok", false):
		_log_line("Export failed: %s" % export_result.get("error", "unknown error"))
		_busy = false
		_update_buttons()
		return

	_log_line("Export finished. Deploying %s to %s..." % [params.output_dir, params.device_id])
	var submit_result := await client.submit_deployment(
		params.device_id, params.game_id, params.output_dir, false, params.argv
	)
	if _client != client:
		return
	if not submit_result.get("ok", false):
		_log_line("Deploy submission failed: %s" % _error_message(submit_result))
		_busy = false
		_update_buttons()
		return

	var job: Dictionary = submit_result.get("data", {}).get("job", {})
	_current_job_id = job.get("id", "")
	_log_line("Deploy job %s queued." % _current_job_id)
	_update_buttons()

	var final_result := await _poll_job(client, _current_job_id)
	if _client != client:
		return
	_current_job_id = ""
	_busy = false
	_update_buttons()
	_report_job_outcome("Deploy", final_result)


## See _validate_deploy_inputs's doc comment — same reasoning.
func _validate_logs_sync_inputs() -> Dictionary:
	if _client == null:
		_log_line("Not connected.")
		return {}
	var device := _selected_device()
	if device.is_empty():
		_log_line("Select a configured device first.")
		return {}
	var logs_dir := ProjectSettings.globalize_path(_logs_dir_field.text.strip_edges())
	if logs_dir == "":
		_log_line("Local logs directory is required.")
		return {}
	if not logs_dir.is_absolute_path():
		_log_line("Local logs directory must be an absolute path.")
		return {}
	return {"device_id": device.get("id", ""), "logs_dir": logs_dir}


func _on_sync_logs_pressed() -> void:
	var params := _validate_logs_sync_inputs()
	if params.is_empty():
		return
	var client := _client

	_busy = true
	_update_buttons()
	_log_line("Syncing logs from %s to %s..." % [params.device_id, params.logs_dir])

	var submit_result := await client.submit_logs_sync(params.device_id, params.logs_dir)
	if _client != client:
		return
	if not submit_result.get("ok", false):
		_log_line("Log sync submission failed: %s" % _error_message(submit_result))
		_busy = false
		_update_buttons()
		return

	var job: Dictionary = submit_result.get("data", {}).get("job", {})
	_current_job_id = job.get("id", "")
	_update_buttons()

	var final_result := await _poll_job(client, _current_job_id)
	if _client != client:
		return
	_current_job_id = ""
	_busy = false
	_update_buttons()
	_report_job_outcome("Log sync", final_result)


func _on_cancel_job_pressed() -> void:
	if _client == null or _current_job_id == "":
		return
	var client := _client
	var job_id := _current_job_id
	_log_line("Cancelling job %s..." % job_id)
	var result := await client.cancel_job(job_id)
	if _client != client:
		return
	if not result.get("ok", false):
		_log_line("Cancel failed: %s" % _error_message(result))
	# Deliberately does not touch _busy/_current_job_id/_update_buttons here:
	# the in-flight _poll_job loop for this job observes the cancelled
	# status on its next tick and finishes normally through the same path
	# every other outcome does, so there's exactly one place that clears
	# job state instead of two racing to do it.


## Polls GET /v1/jobs/{id} once a second until it reaches a terminal
## status, logging only on status changes rather than every tick (a 10
## minute deploy would otherwise write ~600 near-identical log lines).
## Returns the final get_job() result Dictionary, or {} if superseded by
## a Connect click that replaced _client mid-poll.
func _poll_job(client: LazyDeckClient, job_id: String) -> Dictionary:
	var last_status := ""
	while true:
		var result := await client.get_job(job_id)
		if _client != client:
			return {}
		if not result.get("ok", false):
			return result

		var job: Dictionary = result.get("data", {}).get("job", {})
		var status: String = job.get("status", "")
		if status != last_status:
			_log_line("Job %s: %s" % [job_id, status])
			last_status = status
		if status == "succeeded" or status == "failed" or status == "cancelled":
			return result

		await get_tree().create_timer(1.0).timeout
	return {}


func _report_job_outcome(label: String, final_result: Dictionary) -> void:
	if not final_result.get("ok", false):
		_log_line("Failed to poll %s job: %s" % [label, _error_message(final_result)])
		return
	var job: Dictionary = final_result.get("data", {}).get("job", {})
	var status: String = job.get("status", "unknown")
	if status == "succeeded":
		_log_line("%s complete." % label)
	else:
		var job_error: Dictionary = job.get("error", {})
		_log_line(
			"%s did not succeed (%s): %s" % [label, status, String(job_error.get("message", ""))]
		)


func _error_message(result: Dictionary) -> String:
	var err: Dictionary = result.get("error", {})
	return String(err.get("message", "unknown error"))


func _log_line(text: String) -> void:
	if _log:
		# append_text(), not `text +=`: the latter reparses/rebuilds the
		# whole label from scratch on every line, getting steadily more
		# expensive as a long-running editor session's log grows.
		_log.append_text(text + "\n")
