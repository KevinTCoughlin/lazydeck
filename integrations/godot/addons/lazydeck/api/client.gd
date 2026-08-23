@tool
class_name LazyDeckClient
extends Node

## Thin HTTP client for the lazydeck local service API (see #13 /
## api/openapi.yaml). Every method awaits its HTTPRequest and returns a
## plain Dictionary — {"ok": true, "status": int, "data": Variant} on
## success, or {"ok": false, "status": int, "error": {"kind": String,
## "message": String}} on failure — instead of GDScript classes mirroring
## every response shape, since callers here are just UI code that reads a
## couple of fields.
##
## An HTTPRequest can only have one request in flight at a time; concurrent
## calls on the same LazyDeckClient are rejected with a "busy" error rather
## than silently queuing or corrupting each other's response.

var base_url: String
var _token: String
var _http: HTTPRequest
var _busy := false


func _init(connection: LazyDeckConnectionInfo) -> void:
	base_url = connection.base_url
	_token = connection.token
	# Built here (off-tree) rather than in _ready(), so _http exists the
	# instant this object is constructed and callers never have to worry
	# about _ready() ordering relative to their first request() call.
	_http = HTTPRequest.new()
	add_child(_http)


## Performs one request and awaits its completion. path is a /v1/... route;
## body, when non-empty, is JSON-encoded as the request body.
func request(method: HTTPClient.Method, path: String, body: Dictionary = {}) -> Dictionary:
	if _busy:
		return {
			"ok": false,
			"status": 0,
			"error":
			{"kind": "internal", "message": "a request is already in flight on this client"}
		}

	var headers := PackedStringArray(
		[
			"Authorization: Bearer %s" % _token,
			"Content-Type: application/json",
		]
	)
	var body_text := "" if body.is_empty() else JSON.stringify(body)

	_busy = true
	var start_err := _http.request(base_url + path, headers, method, body_text)
	if start_err != OK:
		_busy = false
		return {
			"ok": false,
			"status": 0,
			"error":
			{"kind": "internal", "message": "request setup failed: %s" % error_string(start_err)}
		}

	var result: Array = await _http.request_completed
	_busy = false

	var http_result: int = result[0]
	var status: int = result[1]
	var raw_body: PackedByteArray = result[3]

	if http_result != HTTPRequest.RESULT_SUCCESS:
		return {
			"ok": false,
			"status": status,
			"error":
			{
				"kind": "unreachable",
				"message": "HTTP request did not complete (result code %d)" % http_result
			}
		}

	var response_text := raw_body.get_string_from_utf8()
	var parsed = JSON.parse_string(response_text)
	if status >= 200 and status < 300:
		# JSON.parse_string returns null both for an empty body and for
		# genuinely malformed JSON (e.g. an HTML error page from a proxy in
		# front of the service); only the former is a legitimate "no body"
		# response, so a non-empty body that failed to parse must not be
		# reported as ok with a silently-null payload.
		if response_text != "" and parsed == null:
			return {
				"ok": false,
				"status": status,
				"error": {"kind": "internal", "message": "response body was not valid JSON"}
			}
		return {"ok": true, "status": status, "data": parsed}

	var api_error: Dictionary = {
		"kind": "unknown", "message": "request failed with status %d" % status
	}
	if typeof(parsed) == TYPE_DICTIONARY and parsed.has("error"):
		api_error = parsed["error"]
	return {"ok": false, "status": status, "error": api_error}


func get_health() -> Dictionary:
	return await request(HTTPClient.METHOD_GET, "/v1/health")


func get_capabilities() -> Dictionary:
	return await request(HTTPClient.METHOD_GET, "/v1/capabilities")


func list_devices() -> Dictionary:
	return await request(HTTPClient.METHOD_GET, "/v1/devices")


func discover_devices(timeout_seconds: float = 5.0) -> Dictionary:
	return await request(
		HTTPClient.METHOD_POST, "/v1/devices/discover", {"timeout_seconds": timeout_seconds}
	)


func pair_device(device_id: String) -> Dictionary:
	return await request(HTTPClient.METHOD_POST, "/v1/devices/%s/pair" % device_id.uri_encode())


func get_device_status(device_id: String) -> Dictionary:
	return await request(HTTPClient.METHOD_GET, "/v1/devices/%s/status" % device_id.uri_encode())


## Submits a deploy job for device_id. directory must be an absolute path
## on this workstation (the API rejects a relative one — see
## api/openapi.yaml's deployments endpoint). argv, when non-empty, is the
## command-line the resulting Steam shortcut launches with (see
## api/openapi.yaml's deployments endpoint); leave it empty to omit it from
## the request body. Returns immediately with the queued job's snapshot
## ({"ok": true, "data": {"job": {...}}}); poll get_job() to observe
## progress.
func submit_deployment(
	device_id: String,
	game_id: String,
	directory: String,
	delete_extraneous: bool = false,
	argv: PackedStringArray = PackedStringArray()
) -> Dictionary:
	var body := {
		"game_id": game_id,
		"directory": directory,
		"delete_extraneous": delete_extraneous,
	}
	if not argv.is_empty():
		body["argv"] = Array(argv)
	return await request(
		HTTPClient.METHOD_POST, "/v1/devices/%s/deployments" % device_id.uri_encode(), body
	)


## Submits a log-sync job for device_id. game_id is accepted for forward
## compatibility but currently unused by the backend (it always fetches
## the device's complete Steam logs/minidumps) — see api/openapi.yaml.
func submit_logs_sync(device_id: String, directory: String, game_id: String = "") -> Dictionary:
	var body := {"directory": directory}
	if game_id != "":
		body["game_id"] = game_id
	return await request(
		HTTPClient.METHOD_POST, "/v1/devices/%s/logs/sync" % device_id.uri_encode(), body
	)


func get_job(job_id: String) -> Dictionary:
	return await request(HTTPClient.METHOD_GET, "/v1/jobs/%s" % job_id.uri_encode())


func cancel_job(job_id: String) -> Dictionary:
	return await request(HTTPClient.METHOD_DELETE, "/v1/jobs/%s" % job_id.uri_encode())
