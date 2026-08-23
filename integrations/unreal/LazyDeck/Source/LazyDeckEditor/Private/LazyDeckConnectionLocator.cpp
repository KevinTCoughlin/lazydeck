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
	return FPaths::Combine(CacheDirectory(), TEXT("lazydeck"), TEXT("serve.json"));
}

FString FLazyDeckConnectionLocator::CacheDirectory()
{
#if PLATFORM_MAC
	return FPaths::Combine(FPlatformProcess::UserHomeDir(), TEXT("Library"), TEXT("Caches"));
#elif PLATFORM_WINDOWS
	// Go's os.UserCacheDir() returns %LocalAppData% on Windows.
	const FString LocalAppData = FPlatformMisc::GetEnvironmentVariable(TEXT("LOCALAPPDATA"));
	if (!LocalAppData.IsEmpty())
	{
		return LocalAppData;
	}
	return FPaths::Combine(FPlatformProcess::UserHomeDir(), TEXT("AppData"), TEXT("Local"));
#else
	// Linux (and other XDG-following platforms): $XDG_CACHE_HOME, defaulting
	// to $HOME/.cache when unset -- matches Go's implementation exactly.
	const FString XdgCache = FPlatformMisc::GetEnvironmentVariable(TEXT("XDG_CACHE_HOME"));
	if (!XdgCache.IsEmpty())
	{
		return XdgCache;
	}
	return FPaths::Combine(FPlatformProcess::UserHomeDir(), TEXT(".cache"));
#endif
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
	// FJsonObject::TryGetNumberField's non-template overload writes to a
	// double (Unreal JSON numbers are represented as doubles); parse into
	// one explicitly and narrow to int32 rather than relying on a numeric
	// out-param overload that isn't guaranteed across engine versions.
	double PidValue = 0.0;
	double PortValue = 0.0;
	JsonObject->TryGetNumberField(TEXT("pid"), PidValue);
	JsonObject->TryGetNumberField(TEXT("port"), PortValue);
	Info.Pid = static_cast<int32>(PidValue);
	Info.Port = static_cast<int32>(PortValue);
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
