extends SceneTree

# Headless integration test for the Godot plugin's HTTP client
# (integrations/godot/addons/lazydeck/api/*.gd), run against a real
# `lazydeck serve --fixture` instance instead of the real devkit bridge --
# see scripts/godot-integration-test.sh for how this is wired up in CI/
# containers. Exercises the same request sequence a plugin user's export
# workflow does: locate the connection file -> health/capabilities/devices
# -> export via LazyDeckExportRunner -> submit a deployment -> poll it ->
# cancel it -> confirm the cancellation is observed.
#
# Run with:
#   godot --headless --path examples/godot-demo \
#     --script ../../integrations/godot/tests/integration_test.gd
#
# Configuration (all optional, via environment variables):
#   LAZYDECK_TEST_EXPORT_DIR   where to write the export (default: a temp dir)
#   LAZYDECK_TEST_DEVICE_ID    device name from devices.toml (default: fixture-deck)
#   LAZYDECK_TEST_GAME_ID      game id passed to the deploy API (default: lazydeck_integration)


func _initialize() -> void:
	call_deferred("_run")


func _fail(message: String) -> void:
	push_error(message)
	quit(1)


func _run() -> void:
	var presets := LazyDeckExportRunner.list_preset_names()
	if not presets.has("Linux"):
		_fail("Linux export preset was not found in export_presets.cfg")
		return

	var export_directory := OS.get_environment("LAZYDECK_TEST_EXPORT_DIR")
	if export_directory == "":
		export_directory = OS.get_cache_dir().path_join("lazydeck-godot-integration-test")
	var device_id := OS.get_environment("LAZYDECK_TEST_DEVICE_ID")
	if device_id == "":
		device_id = "fixture-deck"
	var game_id := OS.get_environment("LAZYDECK_TEST_GAME_ID")
	if game_id == "":
		# No '-': deploy's game_id validation rejects it, since a '-'
		# anywhere in game_id breaks the remote Steam client's
		# shortcut-registration step on real hardware (see
		# docs/DEVICE_LAUNCH.md's "Known caveat" section).
		game_id = "lazydeck_integration"

	var export_result := LazyDeckExportRunner.export_preset(
		"Linux", export_directory, "lazydeck-godot-demo.x86_64", false
	)
	if not export_result.get("ok", false):
		_fail("Godot export failed: %s" % export_result.get("error", "unknown error"))
		return
	print("export: succeeded (%s)" % export_directory)

	var located := LazyDeckConnectionLocator.load_connection()
	if not located.get("ok", false):
		_fail("connection discovery failed: %s" % located.get("error", "unknown error"))
		return

	var client := LazyDeckClient.new(located["info"])
	root.add_child(client)

	for request in [
		["health", client.get_health],
		["capabilities", client.get_capabilities],
		["devices", client.list_devices],
	]:
		var result: Dictionary = await request[1].call()
		if not result.get("ok", false):
			_fail(
				(
					"%s request failed (status %s): %s"
					% [
						request[0],
						result.get("status", 0),
						result.get("error", {}).get("message", "unknown error"),
					]
				)
			)
			return
		print("%s: status %s" % [request[0], result.get("status", 0)])

	var deploy_result := await client.submit_deployment(device_id, game_id, export_directory)
	if not deploy_result.get("ok", false):
		_fail("deploy submission failed: %s" % deploy_result)
		return
	var job_id: String = deploy_result.get("data", {}).get("job", {}).get("id", "")
	if job_id == "":
		_fail("deploy response did not include a job id")
		return
	print("deploy: queued %s" % job_id)

	var cancel_result := await client.cancel_job(job_id)
	if not cancel_result.get("ok", false):
		_fail("job cancellation failed: %s" % cancel_result)
		return

	for attempt in 20:
		var job_result := await client.get_job(job_id)
		if not job_result.get("ok", false):
			_fail("job polling failed: %s" % job_result)
			return
		var status: String = job_result.get("data", {}).get("job", {}).get("status", "")
		if status == "cancelled":
			print("job: cancelled")
			quit(0)
			return
		await create_timer(0.1).timeout

	_fail("job did not reach cancelled state within 2s")
