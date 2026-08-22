@tool
class_name LazyDeckConnectionInfo
extends RefCounted

## Mirrors internal/server.ConnectionInfo's JSON shape (see
## internal/server/connection.go): the connection file `lazydeck serve`
## writes on startup so a client that didn't spawn the process itself can
## still discover it, Jupyter-connection-file style.

var pid: int
var port: int
var base_url: String
var token: String
var api_version: String


static func from_dict(data: Dictionary) -> LazyDeckConnectionInfo:
	var info := LazyDeckConnectionInfo.new()
	info.pid = int(data.get("pid", 0))
	info.port = int(data.get("port", 0))
	info.base_url = String(data.get("base_url", ""))
	info.token = String(data.get("token", ""))
	info.api_version = String(data.get("api_version", ""))
	return info
