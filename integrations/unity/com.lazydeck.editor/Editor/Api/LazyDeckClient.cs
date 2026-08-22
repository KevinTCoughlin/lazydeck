using System;
using System.Text;
using UnityEngine;
using UnityEngine.Networking;

namespace LazyDeck.Editor.Api
{
    [Serializable]
    internal sealed class ApiErrorPayload
    {
        public string kind;
        public string message;
    }

    [Serializable]
    internal sealed class ApiErrorEnvelope
    {
        public ApiErrorPayload error;
    }

    /// <summary>
    /// The outcome of one LazyDeckClient request: either Ok with the raw
    /// JSON response body (callers deserialize with JsonUtility themselves
    /// into whatever shape that endpoint returns, since response shapes
    /// vary per route), or a structured error with a stable Kind, mirroring
    /// the API's own {"error": {"kind", "message"}} envelope (see
    /// api/openapi.yaml's ApiErrorEnvelope).
    /// </summary>
    internal readonly struct ApiResult
    {
        public readonly bool Ok;
        public readonly long Status;
        public readonly string Body;
        public readonly string ErrorKind;
        public readonly string ErrorMessage;

        private ApiResult(bool ok, long status, string body, string errorKind, string errorMessage)
        {
            Ok = ok;
            Status = status;
            Body = body;
            ErrorKind = errorKind;
            ErrorMessage = errorMessage;
        }

        public static ApiResult Success(long status, string body) =>
            new ApiResult(true, status, body, null, null);

        public static ApiResult Failure(long status, string kind, string message) =>
            new ApiResult(false, status, null, kind, message);
    }

    /// <summary>
    /// Thin HTTP client for the lazydeck local service API (see #13 /
    /// api/openapi.yaml), the Unity counterpart of the Godot plugin's
    /// api/client.gd (#14/#16). Every method awaits its UnityWebRequest and
    /// returns an ApiResult rather than typed response classes per
    /// endpoint, since callers here are just editor UI code reading a
    /// couple of fields out of the JSON body.
    /// </summary>
    internal sealed class LazyDeckClient
    {
        // UnityWebRequest.timeout defaults to 0 (no timeout); every request
        // this client makes is a short, non-streaming call (connect,
        // devices, pair, discover), so a fixed bound is enough to guarantee
        // RequestAsync always completes instead of leaving a caller's
        // _busy flag stuck true if serve accepts a request and then stops
        // responding.
        private const int RequestTimeoutSeconds = 30;

        private readonly string _baseUrl;
        private readonly string _token;

        public LazyDeckClient(ConnectionInfo connection)
        {
            _baseUrl = connection.base_url;
            _token = connection.token;
        }

        /// <summary>
        /// Performs one request and awaits its completion. path is a
        /// /v1/... route; jsonBody, when non-null, is sent as the request
        /// body with a JSON content type.
        ///
        /// Bridges UnityWebRequestAsyncOperation.completed into an
        /// Awaitable via AwaitableCompletionSource, rather than polling
        /// with `while (!operation.isDone) await Awaitable.NextFrameAsync();`
        /// — that poll is tied to the player-frame loop, which an
        /// EditorWindow invokes outside Play mode only on its own repaint
        /// cadence, so it could leave a request pending indefinitely in an
        /// editor-only context. This class was written without a Unity
        /// Editor available to verify either approach against (see
        /// integrations/unity/README.md); this is the more
        /// editor-context-safe of the two per Unity's own Awaitable docs.
        /// </summary>
        public async Awaitable<ApiResult> RequestAsync(string method, string path, string jsonBody = null)
        {
            using var request = new UnityWebRequest(_baseUrl + path, method);
            request.SetRequestHeader("Authorization", "Bearer " + _token);
            request.downloadHandler = new DownloadHandlerBuffer();
            request.timeout = RequestTimeoutSeconds;
            if (!string.IsNullOrEmpty(jsonBody))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(jsonBody));
                request.SetRequestHeader("Content-Type", "application/json");
            }

            UnityWebRequestAsyncOperation operation = request.SendWebRequest();
            var completionSource = new AwaitableCompletionSource();
            operation.completed += _ => completionSource.SetResult();
            await completionSource.Awaitable;

            if (
                request.result == UnityWebRequest.Result.ConnectionError
                || request.result == UnityWebRequest.Result.DataProcessingError
            )
            {
                return ApiResult.Failure(0, "unreachable", $"request did not complete: {request.error}");
            }

            long status = request.responseCode;
            string body = request.downloadHandler.text;

            if (status >= 200 && status < 300)
            {
                return ApiResult.Success(status, body);
            }

            string kind = "unknown";
            string message = $"request failed with status {status}";
            if (!string.IsNullOrEmpty(body))
            {
                try
                {
                    ApiErrorEnvelope envelope = JsonUtility.FromJson<ApiErrorEnvelope>(body);
                    if (envelope?.error != null)
                    {
                        kind = envelope.error.kind;
                        message = envelope.error.message;
                    }
                }
                catch (ArgumentException)
                {
                    // Body wasn't a parseable error envelope; keep the generic message.
                }
            }
            return ApiResult.Failure(status, kind, message);
        }

        public Awaitable<ApiResult> GetHealthAsync() => RequestAsync("GET", "/v1/health");

        public Awaitable<ApiResult> GetCapabilitiesAsync() => RequestAsync("GET", "/v1/capabilities");

        public Awaitable<ApiResult> ListDevicesAsync() => RequestAsync("GET", "/v1/devices");

        public Awaitable<ApiResult> DiscoverDevicesAsync(float timeoutSeconds = 5f)
        {
            string body = JsonUtility.ToJson(new DiscoverRequest { timeout_seconds = timeoutSeconds });
            return RequestAsync("POST", "/v1/devices/discover", body);
        }

        public Awaitable<ApiResult> PairDeviceAsync(string deviceId) =>
            RequestAsync("POST", $"/v1/devices/{UnityWebRequest.EscapeURL(deviceId)}/pair");

        public Awaitable<ApiResult> GetDeviceStatusAsync(string deviceId) =>
            RequestAsync("GET", $"/v1/devices/{UnityWebRequest.EscapeURL(deviceId)}/status");

        /// <summary>
        /// Submits a deploy job for deviceId. directory must be an absolute
        /// path on this workstation (the API rejects a relative one — see
        /// api/openapi.yaml's deployments endpoint). Returns immediately with
        /// the queued job's snapshot; poll GetJobAsync to observe progress.
        /// </summary>
        public Awaitable<ApiResult> SubmitDeploymentAsync(
            string deviceId,
            string gameId,
            string directory,
            bool deleteExtraneous = false
        )
        {
            string body = JsonUtility.ToJson(
                new DeploymentRequest
                {
                    game_id = gameId,
                    directory = directory,
                    delete_extraneous = deleteExtraneous,
                }
            );
            return RequestAsync(
                "POST",
                $"/v1/devices/{UnityWebRequest.EscapeURL(deviceId)}/deployments",
                body
            );
        }

        /// <summary>
        /// Submits a log-sync job for deviceId. gameId is accepted for forward
        /// compatibility but currently unused by the backend (it always fetches
        /// the device's complete Steam logs/minidumps) — see api/openapi.yaml.
        ///
        /// Two request DTOs rather than one nullable field: JsonUtility always
        /// serializes every public field, so an empty gameId on a shared DTO
        /// would send `"game_id": ""` instead of omitting the key, which is a
        /// different request than the Godot client sends.
        /// </summary>
        public Awaitable<ApiResult> SubmitLogsSyncAsync(
            string deviceId,
            string directory,
            string gameId = ""
        )
        {
            string body = string.IsNullOrEmpty(gameId)
                ? JsonUtility.ToJson(new LogsSyncRequest { directory = directory })
                : JsonUtility.ToJson(
                    new LogsSyncRequestWithGame { directory = directory, game_id = gameId }
                );
            return RequestAsync(
                "POST",
                $"/v1/devices/{UnityWebRequest.EscapeURL(deviceId)}/logs/sync",
                body
            );
        }

        public Awaitable<ApiResult> GetJobAsync(string jobId) =>
            RequestAsync("GET", $"/v1/jobs/{UnityWebRequest.EscapeURL(jobId)}");

        public Awaitable<ApiResult> CancelJobAsync(string jobId) =>
            RequestAsync("DELETE", $"/v1/jobs/{UnityWebRequest.EscapeURL(jobId)}");

        [Serializable]
        private sealed class DiscoverRequest
        {
            public float timeout_seconds;
        }

        [Serializable]
        private sealed class DeploymentRequest
        {
            public string game_id;
            public string directory;
            public bool delete_extraneous;
        }

        [Serializable]
        private sealed class LogsSyncRequest
        {
            public string directory;
        }

        [Serializable]
        private sealed class LogsSyncRequestWithGame
        {
            public string directory;
            public string game_id;
        }
    }
}
