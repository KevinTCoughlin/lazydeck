#pragma once

#include "CoreMinimal.h"

/**
 * The outcome of one cook/package run. Mirrors the {"ok", "error"} shape
 * Unity's BuildRunner.BuildOutcome and the Godot addon's export_runner.gd
 * both use, so all three integrations report packaging failures to their UI
 * the same way rather than throwing.
 */
struct FLazyDeckCookOutcome
{
	bool bOk = false;
	FString Error;

	static FLazyDeckCookOutcome Success()
	{
		FLazyDeckCookOutcome Outcome;
		Outcome.bOk = true;
		return Outcome;
	}

	static FLazyDeckCookOutcome Failure(FString InError)
	{
		FLazyDeckCookOutcome Outcome;
		Outcome.bOk = false;
		Outcome.Error = MoveTemp(InError);
		return Outcome;
	}
};

DECLARE_DELEGATE_OneParam(FLazyDeckCookCompleteDelegate, FLazyDeckCookOutcome);

/**
 * Drives Unreal's BuildCookRun through UAT (UnrealAutomationTool), the
 * Unreal counterpart of Unity's Editor/Api/BuildRunner.cs (in-process
 * BuildPipeline.BuildPlayer) and the Godot addon's api/export_runner.gd
 * (headless `godot --export-*` subprocess). Unreal has neither of those
 * in-process/simple-subprocess options: packaging a project is UAT's
 * BuildCookRun command, the same one the Editor's own
 * File > Package Project menu shells out to, run asynchronously via
 * IUATHelperModule so it doesn't block the editor UI thread.
 *
 * This targets the UAT command-line surface and IUATHelperModule signature
 * as of UE 5.3-5.4; both have shifted across 5.x releases before, so treat
 * build breaks on other 5.x versions as an API-drift fix, not evidence the
 * approach is wrong (see the plugin README's "Validation status" section).
 */
class FLazyDeckCookRunner
{
public:
	/**
	 * Platforms this integration offers to BuildCookRun's -platform= flag.
	 * Linux is the Steam Deck/Steam Machine's native target; Win64 is
	 * included because Proton runs Windows builds on the Deck, mirroring
	 * Unity's BuildRunner.SupportedTargets (StandaloneLinux64/Windows64).
	 */
	static const TArray<FString>& SupportedPlatforms();

	/**
	 * Runs `RunUAT BuildCookRun` for the currently open project, cooking,
	 * staging, packaging, and archiving into OutputDirectory. Returns
	 * immediately; OnComplete fires on the game thread once UAT exits (or
	 * fails to launch).
	 *
	 * Platform must be one of SupportedPlatforms(). OutputDirectory must be
	 * an absolute path; it is created if it does not already exist.
	 * bDevelopment selects the Development client config (mirroring the
	 * `development` flag Unity's BuildRunner.Build takes); otherwise the
	 * Shipping config is used.
	 */
	static void CookAndPackage(const FString& Platform, const FString& OutputDirectory, bool bDevelopment, FLazyDeckCookCompleteDelegate OnComplete);

private:
	static FString BuildCommandLine(const FString& ProjectPath, const FString& Platform, const FString& OutputDirectory, bool bDevelopment);
};
