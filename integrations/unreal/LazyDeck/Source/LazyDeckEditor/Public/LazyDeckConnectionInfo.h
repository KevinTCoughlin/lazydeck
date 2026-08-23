#pragma once

#include "CoreMinimal.h"

/**
 * Mirrors internal/server.ConnectionInfo's JSON shape (see
 * internal/server/connection.go): the connection file `lazydeck serve`
 * writes on startup so a client that didn't spawn the process itself can
 * still discover it, Jupyter-connection-file style. Field names match the
 * JSON keys exactly, the same convention the Godot (connection_info.gd) and
 * Unity (ConnectionInfo.cs) counterparts use, since both are parsed by
 * exact-name JSON mapping rather than attribute-based renaming.
 */
struct FLazyDeckConnectionInfo
{
	int32 Pid = 0;
	int32 Port = 0;
	FString BaseUrl;
	FString Token;
	FString ApiVersion;

	bool IsValid() const { return !BaseUrl.IsEmpty() && !Token.IsEmpty(); }
};
