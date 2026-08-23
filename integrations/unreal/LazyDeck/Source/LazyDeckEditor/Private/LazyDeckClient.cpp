#include "LazyDeckClient.h"

#include "Dom/JsonObject.h"
#include "GenericPlatform/GenericPlatformHttp.h"
#include "HttpModule.h"
#include "Interfaces/IHttpRequest.h"
#include "Interfaces/IHttpResponse.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"
#include "Serialization/JsonWriter.h"

// Every request this client makes is a short, non-streaming call (connect,
// devices, pair, discover); a fixed bound guarantees the request delegate
// always fires instead of leaving a caller's busy flag stuck true if serve
// accepts a request and then stops responding. Matches the Unity client's
// RequestTimeoutSeconds.
static constexpr float LazyDeckRequestTimeoutSeconds = 30.0f;

FLazyDeckClient::FLazyDeckClient(const FLazyDeckConnectionInfo& InConnection) : BaseUrl(InConnection.BaseUrl), Token(InConnection.Token) {}

void FLazyDeckClient::Request(const FString& Verb, const FString& Path, const FString& JsonBody, FLazyDeckApiResultDelegate OnComplete) const
{
	const TSharedRef<IHttpRequest> HttpRequest = FHttpModule::Get().CreateRequest();
	HttpRequest->SetURL(BaseUrl + Path);
	HttpRequest->SetVerb(Verb);
	HttpRequest->SetHeader(TEXT("Authorization"), TEXT("Bearer ") + Token);
	HttpRequest->SetTimeout(LazyDeckRequestTimeoutSeconds);
	if (!JsonBody.IsEmpty())
	{
		HttpRequest->SetHeader(TEXT("Content-Type"), TEXT("application/json"));
		HttpRequest->SetContentAsString(JsonBody);
	}

	HttpRequest->OnProcessRequestComplete().BindLambda(
		[OnComplete](FHttpRequestPtr, FHttpResponsePtr Response, bool bConnectedSuccessfully)
		{
			FLazyDeckApiResult Result;
			if (!bConnectedSuccessfully || !Response.IsValid())
			{
				Result.bOk = false;
				Result.ErrorKind = TEXT("unreachable");
				Result.ErrorMessage = TEXT("request did not complete");
				OnComplete.ExecuteIfBound(Result);
				return;
			}

			Result.StatusCode = Response->GetResponseCode();
			Result.Body = Response->GetContentAsString();

			if (Result.StatusCode >= 200 && Result.StatusCode < 300)
			{
				Result.bOk = true;
				OnComplete.ExecuteIfBound(Result);
				return;
			}

			Result.bOk = false;
			Result.ErrorKind = TEXT("unknown");
			Result.ErrorMessage = FString::Printf(TEXT("request failed with status %d"), Result.StatusCode);

			TSharedPtr<FJsonObject> JsonObject;
			TSharedRef<TJsonReader<>> Reader = TJsonReaderFactory<>::Create(Result.Body);
			if (FJsonSerializer::Deserialize(Reader, JsonObject) && JsonObject.IsValid())
			{
				const TSharedPtr<FJsonObject>* ErrorObject;
				if (JsonObject->TryGetObjectField(TEXT("error"), ErrorObject))
				{
					(*ErrorObject)->TryGetStringField(TEXT("kind"), Result.ErrorKind);
					(*ErrorObject)->TryGetStringField(TEXT("message"), Result.ErrorMessage);
				}
			}
			OnComplete.ExecuteIfBound(Result);
		});

	HttpRequest->ProcessRequest();
}

void FLazyDeckClient::GetHealth(FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), TEXT("/v1/health"), FString(), OnComplete);
}

void FLazyDeckClient::GetCapabilities(FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), TEXT("/v1/capabilities"), FString(), OnComplete);
}

void FLazyDeckClient::ListDevices(FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), TEXT("/v1/devices"), FString(), OnComplete);
}

void FLazyDeckClient::DiscoverDevices(float TimeoutSeconds, FLazyDeckApiResultDelegate OnComplete) const
{
	const TSharedRef<FJsonObject> JsonObject = MakeShared<FJsonObject>();
	JsonObject->SetNumberField(TEXT("timeout_seconds"), TimeoutSeconds);
	FString Body;
	const TSharedRef<TJsonWriter<>> Writer = TJsonWriterFactory<>::Create(&Body);
	FJsonSerializer::Serialize(JsonObject, Writer);
	Request(TEXT("POST"), TEXT("/v1/devices/discover"), Body, OnComplete);
}

void FLazyDeckClient::PairDevice(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("POST"), FString::Printf(TEXT("/v1/devices/%s/pair"), *FGenericPlatformHttp::UrlEncode(DeviceId)), FString(), OnComplete);
}

void FLazyDeckClient::GetDeviceStatus(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), FString::Printf(TEXT("/v1/devices/%s/status"), *FGenericPlatformHttp::UrlEncode(DeviceId)), FString(), OnComplete);
}

void FLazyDeckClient::ListGames(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), FString::Printf(TEXT("/v1/devices/%s/games"), *FGenericPlatformHttp::UrlEncode(DeviceId)), FString(), OnComplete);
}

void FLazyDeckClient::SubmitDeployment(const FString& DeviceId, const FString& GameId, const FString& Directory, bool bDeleteExtraneous,
									   FLazyDeckApiResultDelegate OnComplete) const
{
	const TSharedRef<FJsonObject> JsonObject = MakeShared<FJsonObject>();
	JsonObject->SetStringField(TEXT("game_id"), GameId);
	JsonObject->SetStringField(TEXT("directory"), Directory);
	JsonObject->SetBoolField(TEXT("delete_extraneous"), bDeleteExtraneous);
	FString Body;
	const TSharedRef<TJsonWriter<>> Writer = TJsonWriterFactory<>::Create(&Body);
	FJsonSerializer::Serialize(JsonObject, Writer);
	Request(TEXT("POST"), FString::Printf(TEXT("/v1/devices/%s/deployments"), *FGenericPlatformHttp::UrlEncode(DeviceId)), Body, OnComplete);
}

void FLazyDeckClient::SyncLogs(const FString& DeviceId, const FString& Directory, const FString& GameId, FLazyDeckApiResultDelegate OnComplete) const
{
	const TSharedRef<FJsonObject> JsonObject = MakeShared<FJsonObject>();
	JsonObject->SetStringField(TEXT("directory"), Directory);
	if (!GameId.IsEmpty())
	{
		JsonObject->SetStringField(TEXT("game_id"), GameId);
	}
	FString Body;
	const TSharedRef<TJsonWriter<>> Writer = TJsonWriterFactory<>::Create(&Body);
	FJsonSerializer::Serialize(JsonObject, Writer);
	Request(TEXT("POST"), FString::Printf(TEXT("/v1/devices/%s/logs/sync"), *FGenericPlatformHttp::UrlEncode(DeviceId)), Body, OnComplete);
}

void FLazyDeckClient::GetJob(const FString& JobId, FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("GET"), FString::Printf(TEXT("/v1/jobs/%s"), *FGenericPlatformHttp::UrlEncode(JobId)), FString(), OnComplete);
}

void FLazyDeckClient::CancelJob(const FString& JobId, FLazyDeckApiResultDelegate OnComplete) const
{
	Request(TEXT("DELETE"), FString::Printf(TEXT("/v1/jobs/%s"), *FGenericPlatformHttp::UrlEncode(JobId)), FString(), OnComplete);
}
