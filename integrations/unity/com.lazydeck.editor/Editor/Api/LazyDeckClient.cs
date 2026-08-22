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
        /// Uses a plain `while (!operation.isDone) await
        /// Awaitable.NextFrameAsync();` poll rather than an event-based
        /// bridge: this is the most widely-documented Awaitable pattern
        /// and the piece of this client most worth double-checking against
        /// a real Editor session outside Play mode, since this package was
        /// written without one available (see
        /// integrations/unity/README.md). If NextFrameAsync turns out not
        /// to pump reliably in Editor-only context on the version you're
        /// testing with, wiring UnityWebRequestAsyncOperation.completed
        /// into an AwaitableCompletionSource is the fix.
        /// </summary>
        public async Awaitable<ApiResult> RequestAsync(string method, string path, string jsonBody = null)
        {
            using var request = new UnityWebRequest(_baseUrl + path, method);
            request.SetRequestHeader("Authorization", "Bearer " + _token);
            request.downloadHandler = new DownloadHandlerBuffer();
            if (!string.IsNullOrEmpty(jsonBody))
            {
                request.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(jsonBody));
                request.SetRequestHeader("Content-Type", "application/json");
            }

            UnityWebRequestAsyncOperation operation = request.SendWebRequest();
            while (!operation.isDone)
            {
                await Awaitable.NextFrameAsync();
            }

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

        [Serializable]
        private sealed class DiscoverRequest
        {
            public float timeout_seconds;
        }
    }
}
