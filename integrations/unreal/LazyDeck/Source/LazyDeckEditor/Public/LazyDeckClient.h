#pragma once

#include "CoreMinimal.h"
#include "LazyDeckConnectionInfo.h"

/**
 * The outcome of one LazyDeckClient request: either bOk with the raw JSON
 * response body (callers parse it themselves into whatever shape that route
 * returns, since response shapes vary per endpoint), or a structured error
 * with a stable ErrorKind, mirroring the API's own {"error": {"kind",
 * "message"}} envelope (see api/openapi.yaml's ApiErrorEnvelope).
 */
struct FLazyDeckApiResult
{
	bool bOk = false;
	int32 StatusCode = 0;
	FString Body;
	FString ErrorKind;
	FString ErrorMessage;
};

DECLARE_DELEGATE_OneParam(FLazyDeckApiResultDelegate, FLazyDeckApiResult);

/**
 * Thin HTTP client for the lazydeck local service API (api/openapi.yaml),
 * the Unreal counterpart of the Godot addon's api/client.gd and the Unity
 * package's Editor/Api/LazyDeckClient.cs. Every method issues one async
 * FHttpModule request and invokes OnComplete on the game thread with an
 * FLazyDeckApiResult rather than typed response classes per endpoint, since
 * callers here are just editor UI code reading a couple of fields out of the
 * JSON body.
 */
class FLazyDeckClient
{
public:
	explicit FLazyDeckClient(const FLazyDeckConnectionInfo& InConnection);

	void GetHealth(FLazyDeckApiResultDelegate OnComplete) const;
	void GetCapabilities(FLazyDeckApiResultDelegate OnComplete) const;
	void ListDevices(FLazyDeckApiResultDelegate OnComplete) const;
	void DiscoverDevices(float TimeoutSeconds, FLazyDeckApiResultDelegate OnComplete) const;
	void PairDevice(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const;
	void GetDeviceStatus(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const;
	void ListGames(const FString& DeviceId, FLazyDeckApiResultDelegate OnComplete) const;

	/**
	 * Submits a deploy job for DeviceId. Directory must be an absolute path
	 * on this workstation (the API rejects a relative one — see
	 * api/openapi.yaml's deployments endpoint). Returns immediately (via
	 * OnComplete) with the queued job's snapshot; poll GetJob to observe
	 * progress.
	 */
	void SubmitDeployment(
		const FString& DeviceId,
		const FString& GameId,
		const FString& Directory,
		bool bDeleteExtraneous,
		FLazyDeckApiResultDelegate OnComplete) const;

	/**
	 * Submits a log-sync job for DeviceId. GameId is accepted for forward
	 * compatibility but currently unused by the backend (it always fetches
	 * the device's complete Steam logs/minidumps) — see api/openapi.yaml.
	 * Pass an empty GameId to omit it from the request body.
	 */
	void SyncLogs(
		const FString& DeviceId,
		const FString& Directory,
		const FString& GameId,
		FLazyDeckApiResultDelegate OnComplete) const;

	void GetJob(const FString& JobId, FLazyDeckApiResultDelegate OnComplete) const;
	void CancelJob(const FString& JobId, FLazyDeckApiResultDelegate OnComplete) const;

private:
	void Request(
		const FString& Verb,
		const FString& Path,
		const FString& JsonBody,
		FLazyDeckApiResultDelegate OnComplete) const;

	FString BaseUrl;
	FString Token;
};
