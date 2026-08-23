#include "LazyDeckConnectionLocator.h"

#include "Dom/JsonObject.h"
#include "HAL/PlatformProcess.h"
#include "Misc/FileHelper.h"
#include "Misc/Paths.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"

FString FLazyDeckConnectionLocator::DefaultPath()
{
	FString RuntimeDir = FPlatformMisc::GetEnvironmentVariable(TEXT("XDG_RUNTIME_DIR"));
	if (!RuntimeDir.IsEmpty())
	{
		return FPaths::Combine(RuntimeDir, TEXT("lazydeck"), TEXT("serve.json"));
	}
	// FPlatformProcess::UserSettingsDir() approximates Go's os.UserCacheDir()
	// on the platforms lazydeck itself ships for (~/.cache or
	// ~/Library/Application Support on macOS) closely enough for locating a
	// file another process already wrote; it does not need to match byte for
	// byte since this plugin never writes here itself.
	return FPaths::Combine(FPlatformProcess::UserSettingsDir(), TEXT("lazydeck"), TEXT("serve.json"));
}

bool FLazyDeckConnectionLocator::Load(const FString& InPath, FLazyDeckConnectionInfo& OutInfo, FString& OutError)
{
	const FString ResolvedPath = InPath.IsEmpty() ? DefaultPath() : InPath;

	if (!FPaths::FileExists(ResolvedPath))
	{
		OutError = FString::Printf(TEXT("no connection file at %s (is `lazydeck serve` running?)"), *ResolvedPath);
		return false;
	}

	FString Text;
	if (!FFileHelper::LoadFileToString(Text, *ResolvedPath))
	{
		OutError = FString::Printf(TEXT("could not read %s"), *ResolvedPath);
		return false;
	}

	TSharedPtr<FJsonObject> JsonObject;
	TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Text);
	if (!FJsonSerializer::Deserialize(Reader, JsonObject) || !JsonObject.IsValid())
	{
		OutError = FString::Printf(TEXT("malformed connection file at %s"), *ResolvedPath);
		return false;
	}

	FLazyDeckConnectionInfo Info;
	JsonObject->TryGetNumberField(TEXT("pid"), Info.Pid);
	JsonObject->TryGetNumberField(TEXT("port"), Info.Port);
	JsonObject->TryGetStringField(TEXT("base_url"), Info.BaseUrl);
	JsonObject->TryGetStringField(TEXT("token"), Info.Token);
	JsonObject->TryGetStringField(TEXT("api_version"), Info.ApiVersion);

	if (!Info.IsValid())
	{
		OutError = FString::Printf(TEXT("malformed connection file at %s"), *ResolvedPath);
		return false;
	}

	OutInfo = Info;
	return true;
}
