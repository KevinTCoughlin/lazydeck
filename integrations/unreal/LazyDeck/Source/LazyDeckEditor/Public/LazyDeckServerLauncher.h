#pragma once

#include "CoreMinimal.h"

/**
 * Starts a local LazyDeck service when the editor cannot find one. The
 * process is deliberately not owned by the editor: one service can serve
 * multiple engine integrations and survive an editor restart, just as when
 * a developer starts it in a terminal. C++ counterpart of the Godot addon's
 * api/server_launcher.gd and the Unity package's Editor/Api/ServerLauncher.cs.
 */
class FLazyDeckServerLauncher
{
public:
	/** LAZYDECK_AUTOSTART=0/false disables auto-start; unset/anything else leaves it enabled. */
	static bool IsAutoStartEnabled();

	/** LAZYDECK_BIN overrides the executable name/path; defaults to "lazydeck" resolved via PATH. */
	static FString GetExecutable();

	/**
	 * Starts `lazydeck serve` only when no connection file exists at
	 * FLazyDeckConnectionLocator::DefaultPath(). Returns true if a process
	 * was launched. A malformed or inaccessible existing file is reported to
	 * the caller via LogFn rather than spawning over a potentially running
	 * service.
	 */
	static bool StartIfNeeded(const TFunction<void(const FString&)>& LogFn);
};
