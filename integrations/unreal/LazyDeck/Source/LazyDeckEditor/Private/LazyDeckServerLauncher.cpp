#include "LazyDeckServerLauncher.h"

#include "HAL/PlatformProcess.h"
#include "LazyDeckConnectionLocator.h"
#include "Misc/Paths.h"

bool FLazyDeckServerLauncher::IsAutoStartEnabled()
{
	const FString Env = FPlatformMisc::GetEnvironmentVariable(TEXT("LAZYDECK_AUTOSTART"));
	if (Env == TEXT("0") || Env.Equals(TEXT("false"), ESearchCase::IgnoreCase))
	{
		return false;
	}
	return true;
}

FString FLazyDeckServerLauncher::GetExecutable()
{
	const FString Env = FPlatformMisc::GetEnvironmentVariable(TEXT("LAZYDECK_BIN"));
	return Env.IsEmpty() ? TEXT("lazydeck") : Env;
}

bool FLazyDeckServerLauncher::StartIfNeeded(const TFunction<void(const FString&)>& LogFn)
{
	if (FPaths::FileExists(FLazyDeckConnectionLocator::DefaultPath()))
	{
		return false;
	}
	if (!IsAutoStartEnabled())
	{
		LogFn(TEXT("lazydeck serve is not running; auto-start is disabled."));
		return false;
	}

	const FString Executable = GetExecutable();
	if (Executable.IsEmpty())
	{
		LogFn(TEXT("lazydeck serve auto-start failed: no executable was configured."));
		return false;
	}

	// Not tracked with a FProcHandle the plugin waits on: the process is
	// meant to outlive the editor, matching the Godot/Unity launchers'
	// intentionally fire-and-forget semantics.
	const FProcHandle Handle = FPlatformProcess::CreateProc(*Executable, TEXT("serve"),
															/*bLaunchDetached=*/true, /*bLaunchHidden=*/true, /*bLaunchReallyHidden=*/true, nullptr,
															/*PriorityModifier=*/0, nullptr, nullptr);

	if (!Handle.IsValid())
	{
		LogFn(FString::Printf(TEXT("lazydeck serve auto-start failed: could not start %s."), *Executable));
		return false;
	}

	LogFn(FString::Printf(TEXT("Starting lazydeck serve (%s)..."), *Executable));
	return true;
}
