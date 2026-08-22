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

	var parsed = JSON.parse_string(raw_body.get_string_from_utf8())
	if status >= 200 and status < 300:
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
