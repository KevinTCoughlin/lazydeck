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


func _ready() -> void:
	name = "LazyDeck"
	custom_minimum_size = Vector2(260, 0)
	_build_ui()
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
	_devices_list.item_selected.connect(func(_index: int) -> void: _update_pair_button())
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
		+ "[[device]] entry to devices.toml first, then Connect again."
	)
	root.add_child(discover_note)

	_discovered_list = ItemList.new()
	_discovered_list.custom_minimum_size = Vector2(0, 80)
	root.add_child(_discovered_list)

	root.add_child(HSeparator.new())

	_log = RichTextLabel.new()
	_log.custom_minimum_size = Vector2(0, 100)
	_log.bbcode_enabled = false
	_log.scroll_following = true
	root.add_child(_log)


func _connect() -> void:
	if _client:
		_client.queue_free()
		_client = null

	var located := LazyDeckConnectionLocator.load_connection()
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
		_update_pair_button()
		return

	var data: Dictionary = result.get("data", {})
	var devices: Array = data.get("devices", [])
	for device in devices:
		_devices.append(device)
		_devices_list.add_item("%s (%s)" % [device.get("id", "?"), device.get("machine", "?")])
	_update_pair_button()


func _update_pair_button() -> void:
	_pair_button.disabled = _client == null or _devices_list.get_selected_items().is_empty()


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


func _error_message(result: Dictionary) -> String:
	var err: Dictionary = result.get("error", {})
	return String(err.get("message", "unknown error"))


func _log_line(text: String) -> void:
	if _log:
		_log.text += text + "\n"
