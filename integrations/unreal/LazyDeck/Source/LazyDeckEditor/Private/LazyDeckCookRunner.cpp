#include "LazyDeckCookRunner.h"

#include "HAL/FileManager.h"
#include "IUATHelperModule.h"
#include "Misc/Paths.h"

namespace
{
/**
 * BuildCookRun's -platform= names for the targets this integration offers.
 * These are UAT/UBT platform identifiers, not marketing names ("Linux" and
 * "Win64" specifically, not "Linux64"/"Windows64" as Unity's BuildTarget
 * enum spells them).
 */
const TArray<FString> GSupportedPlatforms = {TEXT("Linux"), TEXT("Win64")};
}

const TArray<FString>& FLazyDeckCookRunner::SupportedPlatforms()
{
	return GSupportedPlatforms;
}

FString FLazyDeckCookRunner::BuildCommandLine(const FString& ProjectPath, const FString& Platform, const FString& OutputDirectory, bool bDevelopment)
{
	const TCHAR* Config = bDevelopment ? TEXT("Development") : TEXT("Shipping");

	// Mirrors the command line the Editor's own File > Package Project menu
	// issues: cook, stage, pak, and archive into OutputDirectory in one UAT
	// invocation. -noP4 skips Perforce integration (irrelevant here); the
	// -client/-serverconfig pair is required even for a client-only package
	// because BuildCookRun validates both.
	return FString::Printf(TEXT("BuildCookRun -project=\"%s\" -noP4 -platform=%s -clientconfig=%s -serverconfig=%s ")
							   TEXT("-cook -allmaps -build -stage -pak -archive -archivedirectory=\"%s\" -utf8output"),
						   *ProjectPath, *Platform, Config, Config, *OutputDirectory);
}

void FLazyDeckCookRunner::CookAndPackage(const FString& Platform, const FString& OutputDirectory, bool bDevelopment, FLazyDeckCookCompleteDelegate OnComplete)
{
	if (!GSupportedPlatforms.Contains(Platform))
	{
		OnComplete.ExecuteIfBound(FLazyDeckCookOutcome::Failure(FString::Printf(TEXT("unsupported platform \"%s\""), *Platform)));
		return;
	}
	if (OutputDirectory.IsEmpty() || FPaths::IsRelative(OutputDirectory))
	{
		OnComplete.ExecuteIfBound(FLazyDeckCookOutcome::Failure(TEXT("output directory must be an absolute path")));
		return;
	}

	const FString ProjectPath = FPaths::ConvertRelativePathToFull(FPaths::GetProjectFilePath());
	if (ProjectPath.IsEmpty())
	{
		OnComplete.ExecuteIfBound(FLazyDeckCookOutcome::Failure(TEXT("no project is currently open")));
		return;
	}

	IFileManager::Get().MakeDirectory(*OutputDirectory, /*Tree=*/true);

	const FString CommandLine = BuildCommandLine(ProjectPath, Platform, OutputDirectory, bDevelopment);

	// IUATHelperModule runs UAT out-of-process and asynchronously, posting
	// progress to the Output Log and a slate notification on its own, then
	// invoking this callback on the game thread with UAT's final status
	// string ("Completed" on success) once the process exits -- unlike
	// Unity's synchronous, in-process BuildPipeline.BuildPlayer, so callers
	// must not assume packaging has finished when CookAndPackage returns.
	IUATHelperModule::Get().CreateUatTask(CommandLine, FText::FromString(Platform), FText::FromString(TEXT("Cook and package for LazyDeck")),
										  FText::FromString(TEXT("LazyDeck Package")), nullptr,
										  [OnComplete](FString Result, double) mutable
										  {
											  if (Result == TEXT("Completed"))
											  {
												  OnComplete.ExecuteIfBound(FLazyDeckCookOutcome::Success());
											  }
											  else
											  {
												  OnComplete.ExecuteIfBound(
													  FLazyDeckCookOutcome::Failure(FString::Printf(TEXT("UAT BuildCookRun did not complete (%s)"), *Result)));
											  }
										  });
}
