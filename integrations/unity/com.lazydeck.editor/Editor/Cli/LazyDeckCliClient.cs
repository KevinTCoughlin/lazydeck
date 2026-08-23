using System;
using System.Net.Http;
using System.Text;
using System.Threading.Tasks;
using LazyDeck.Editor.Api;
using UnityEngine;

namespace LazyDeck.Editor.Cli
{
    /// <summary>
    /// A synchronous counterpart to LazyDeckClient (see
    /// Editor/Api/LazyDeckClient.cs), used only by the batch-mode CLI entry
    /// points in this folder.
    ///
    /// LazyDeckClient's requests are UnityWebRequest calls awaited through
    /// Awaitable, which — per that class's own doc comment and this
    /// package's README validation notes — depend on EditorApplication.update
    /// or the player frame loop to make progress, neither of which this
    /// integration has been able to confirm ticks reliably around
    /// `-executeMethod` in batch mode. A stalled request there would hang a
    /// CI job with no feedback. System.Net.Http.HttpClient has no such
    /// dependency: SendAsync's continuation is forced off the calling
    /// SynchronizationContext with ConfigureAwait(false), so blocking on it
    /// with GetAwaiter().GetResult() cannot deadlock against a context that
    /// never resumes it, and the call otherwise blocks the batch-mode
    /// process's single thread of execution directly, which is exactly the
    /// behavior a CLI entry point wants.
    /// </summary>
    internal sealed class LazyDeckCliClient : IDisposable
    {
        private const int RequestTimeoutSeconds = 30;

        private readonly HttpClient _http;
        private readonly string _baseUrl;

        public LazyDeckCliClient(ConnectionInfo connection)
        {
            _baseUrl = connection.base_url;
            _http = new HttpClient { Timeout = TimeSpan.FromSeconds(RequestTimeoutSeconds) };
            _http.DefaultRequestHeaders.Authorization =
                new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", connection.token);
        }

        public void Dispose() => _http.Dispose();

        public string GetCapabilitiesApiVersion()
        {
            string body = Request(HttpMethod.Get, "/v1/capabilities", null);
            CapabilitiesResponse parsed;
            try
            {
                parsed = JsonUtility.FromJson<CapabilitiesResponse>(body);
            }
            catch (ArgumentException e)
            {
                throw new LazyDeckCliException($"capabilities response was not valid JSON: {e.Message}");
            }
            if (parsed == null || string.IsNullOrEmpty(parsed.api_version))
            {
                throw new LazyDeckCliException("capabilities response was missing an api_version");
            }
            return parsed.api_version;
        }

        /// <summary>
        /// Submits a deploy job, mirroring LazyDeckClient.SubmitDeploymentAsync
        /// (see that method's doc comment for the argv/directory contract),
        /// and returns the queued job's id.
        /// </summary>
        public string SubmitDeployment(string deviceId, string gameId, string directory, string[] argv)
        {
            string body =
                argv != null && argv.Length > 0
                    ? JsonUtility.ToJson(
                        new DeploymentRequestWithArgv
                        {
                            game_id = gameId,
                            directory = directory,
                            argv = argv,
                        }
                    )
                    : JsonUtility.ToJson(new DeploymentRequest { game_id = gameId, directory = directory });
            string response = Request(
                HttpMethod.Post,
                $"/v1/devices/{Uri.EscapeDataString(deviceId)}/deployments",
                body
            );
            return RequireJobId(response);
        }

        public string SubmitLogsSync(string deviceId, string directory)
        {
            string body = JsonUtility.ToJson(new LogsSyncRequest { directory = directory });
            string response = Request(
                HttpMethod.Post,
                $"/v1/devices/{Uri.EscapeDataString(deviceId)}/logs/sync",
                body
            );
            return RequireJobId(response);
        }

        public JobEntry GetJob(string jobId)
        {
            string response = Request(HttpMethod.Get, $"/v1/jobs/{Uri.EscapeDataString(jobId)}", null);
            JobResponse parsed;
            try
            {
                parsed = JsonUtility.FromJson<JobResponse>(response);
            }
            catch (ArgumentException e)
            {
                throw new LazyDeckCliException($"could not parse job {jobId} response: {e.Message}");
            }
            if (parsed?.job == null)
            {
                throw new LazyDeckCliException($"job {jobId} response named no job");
            }
            return parsed.job;
        }

        private string Request(HttpMethod method, string path, string jsonBody)
        {
            using var request = new HttpRequestMessage(method, _baseUrl + path);
            if (jsonBody != null)
            {
                request.Content = new StringContent(jsonBody, Encoding.UTF8, "application/json");
            }

            HttpResponseMessage response;
            string body;
            try
            {
                response = _http.SendAsync(request).ConfigureAwait(false).GetAwaiter().GetResult();
                body = response.Content.ReadAsStringAsync().ConfigureAwait(false).GetAwaiter().GetResult();
            }
            catch (Exception e) when (e is HttpRequestException || e is TaskCanceledException)
            {
                throw new LazyDeckCliException($"request to {path} did not complete: {e.Message}");
            }

            if (response.IsSuccessStatusCode)
            {
                return body;
            }

            string message = $"request failed with status {(int)response.StatusCode}";
            if (!string.IsNullOrEmpty(body))
            {
                try
                {
                    ApiErrorEnvelope envelope = JsonUtility.FromJson<ApiErrorEnvelope>(body);
                    if (envelope?.error != null && !string.IsNullOrEmpty(envelope.error.message))
                    {
                        message = envelope.error.message;
                    }
                }
                catch (ArgumentException)
                {
                    // Body wasn't a parseable error envelope; keep the generic message.
                }
            }
            throw new LazyDeckCliException(message);
        }

        private static string RequireJobId(string body)
        {
            JobResponse parsed;
            try
            {
                parsed = JsonUtility.FromJson<JobResponse>(body);
            }
            catch (ArgumentException e)
            {
                throw new LazyDeckCliException($"could not parse submission response: {e.Message}");
            }
            if (parsed?.job == null || string.IsNullOrEmpty(parsed.job.id))
            {
                throw new LazyDeckCliException("submission response named no job to poll");
            }
            return parsed.job.id;
        }

        [Serializable]
        private sealed class CapabilitiesResponse
        {
            public string api_version;
        }

        [Serializable]
        private sealed class DeploymentRequest
        {
            public string game_id;
            public string directory;
        }

        [Serializable]
        private sealed class DeploymentRequestWithArgv
        {
            public string game_id;
            public string directory;
            public string[] argv;
        }

        [Serializable]
        private sealed class LogsSyncRequest
        {
            public string directory;
        }

        [Serializable]
        internal sealed class JobEntry
        {
            public string id;
            public string status;
            public string last_message;
            public ApiErrorPayload error;
        }

        [Serializable]
        private sealed class JobResponse
        {
            public JobEntry job;
        }
    }
}
