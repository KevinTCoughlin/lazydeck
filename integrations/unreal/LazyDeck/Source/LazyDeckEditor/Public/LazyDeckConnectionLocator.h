#pragma once

#include "CoreMinimal.h"
#include "LazyDeckConnectionInfo.h"

/**
 * Finds and parses the connection file `lazydeck serve` writes on startup.
 * Mirrors internal/server/connection.go's connectionFilePath() exactly (same
 * env var, same fallback, same relative path) so this plugin locates the
 * same file the Go service itself considers authoritative, without needing
 * to invoke `lazydeck` at all — the C++ counterpart of the Godot addon's
 * api/connection_locator.gd and the Unity package's Editor/Api/ConnectionLocator.cs.
 */
class FLazyDeckConnectionLocator
{
public:
	/**
	 * Prefers $XDG_RUNTIME_DIR (matches the Go side's preference for a
	 * tmpfs-backed, per-session, already-private location for a file holding
	 * a bearer token) and falls back to the OS cache directory otherwise
	 * (matches Go's os.UserCacheDir(), via FPlatformProcess::UserSettingsDir()
	 * on this plugin's supported desktop platforms).
	 */
	static FString DefaultPath();

	/**
	 * Reads and parses the connection file at InPath (the default location
	 * from DefaultPath() when InPath is empty). Returns true and fills
	 * OutInfo on success; returns false and fills OutError otherwise — a
	 * missing connection file (lazydeck serve simply isn't running yet) is
	 * an expected, common outcome for the caller to handle, not exceptional.
	 */
	static bool Load(const FString& InPath, FLazyDeckConnectionInfo& OutInfo, FString& OutError);
};
