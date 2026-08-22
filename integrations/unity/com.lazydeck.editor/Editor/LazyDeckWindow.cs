using System;
using System.IO;
using System.Text;
using LazyDeck.Editor.Api;
using UnityEditor;
using UnityEngine;

namespace LazyDeck.Editor
{
    /// <summary>
    /// The LazyDeck editor window: connects to a running `lazydeck serve`,
    /// lists configured devices, discovers devkits on the LAN, and pairs a
    /// configured device. Build/deploy/log-sync are deliberately out of
    /// scope for this window — see integrations/unity/README.md.
    /// </summary>
    public sealed class LazyDeckWindow : EditorWindow
    {
        // Classes, not structs: JsonUtility's struct support has had more
        // edge cases historically than plain classes, and there's no
        // Unity Editor available to verify against here (see
        // integrations/unity/README.md) — no reason to take on that risk
        // for DTOs that are otherwise free to make classes.
        [Serializable]
        private sealed class DeviceEntry
        {
            public string id;
            public string machine;
            public string login;
        }

        [Serializable]
        private sealed class DeviceListResponse
        {
            public DeviceEntry[] devices;
        }

        [Serializable]
        private sealed class DiscoveredDeviceEntry
        {
            public string name;
            public string address;
            public int port;
        }

        [Serializable]
        private sealed class DiscoveredDeviceListResponse
        {
            public DiscoveredDeviceEntry[] devices;
        }

        [Serializable]
        private sealed class CapabilitiesResponse
        {
            public string api_version;
        }

        [Serializable]
        private sealed class JobEntry
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

        private LazyDeckClient _client;
        private string _statusText = "Not connected";
        private DeviceEntry[] _devices = Array.Empty<DeviceEntry>();
        private int _selectedDeviceIndex = -1;
        private DiscoveredDeviceEntry[] _discovered = Array.Empty<DiscoveredDeviceEntry>();
        private readonly StringBuilder _log = new StringBuilder();
        private Vector2 _logScroll;
        private bool _busy;

        private int _selectedTargetIndex;
        private bool _developmentBuild = true;
        private string _gameId = "";
        private string _outputDirectory = "";
        private string _executableName = "";
        private string _logsDirectory = "";
        private string _currentJobId = "";

        [MenuItem("Window/LazyDeck")]
        public static void ShowWindow()
        {
            LazyDeckWindow window = GetWindow<LazyDeckWindow>();
            window.titleContent = new GUIContent("LazyDeck");
        }

        private void OnEnable()
        {
            SeedBuildDefaults();
            _ = ConnectAsync();
        }

        /// <summary>
        /// Fills the build/deploy fields with workable defaults so the common
        /// case is "pick a device, type a game ID, click Build &amp; deploy"
        /// rather than filling in four paths by hand. Only ever seeds empty
        /// fields, so a value the user typed survives a window reopen.
        /// </summary>
        private void SeedBuildDefaults()
        {
            BuildTarget target = SelectedTarget();
            if (string.IsNullOrEmpty(_outputDirectory))
            {
                // Beside Assets/, not inside it: build output under Assets/
                // would be imported as project assets on the next refresh.
                _outputDirectory = Path.Combine(
                    Path.GetDirectoryName(Application.dataPath) ?? "",
                    "Build"
                );
            }
            if (string.IsNullOrEmpty(_executableName))
            {
                _executableName = BuildRunner.DefaultExecutableName(target);
            }
            if (string.IsNullOrEmpty(_logsDirectory))
            {
                _logsDirectory = Path.Combine(
                    Path.GetDirectoryName(Application.dataPath) ?? "",
                    "LazyDeckLogs"
                );
            }
        }

        private BuildTarget SelectedTarget()
        {
            BuildTarget[] targets = BuildRunner.SupportedTargets;
            if (_selectedTargetIndex < 0 || _selectedTargetIndex >= targets.Length)
            {
                return targets[0];
            }
            return targets[_selectedTargetIndex];
        }

        private void OnGUI()
        {
            EditorGUILayout.LabelField(_statusText, EditorStyles.boldLabel);

            using (new EditorGUI.DisabledScope(_busy))
            {
                if (GUILayout.Button("Connect"))
                {
                    _ = ConnectAsync();
                }
            }

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Configured devices (devices.toml)", EditorStyles.boldLabel);

            if (_devices.Length == 0)
            {
                EditorGUILayout.HelpBox("No devices configured, or not connected.", MessageType.Info);
            }
            else
            {
                if (_selectedDeviceIndex < 0 || _selectedDeviceIndex >= _devices.Length)
                {
                    _selectedDeviceIndex = 0;
                }
                string[] labels = new string[_devices.Length];
                for (int i = 0; i < _devices.Length; i++)
                {
                    labels[i] = $"{_devices[i].id} ({_devices[i].machine})";
                }
                _selectedDeviceIndex = EditorGUILayout.Popup("Device", _selectedDeviceIndex, labels);
            }

            bool hasDeviceSelected =
                _selectedDeviceIndex >= 0 && _selectedDeviceIndex < _devices.Length;
            bool canPair = !_busy && _client != null && hasDeviceSelected;
            using (new EditorGUI.DisabledScope(!canPair))
            {
                if (GUILayout.Button("Pair selected device"))
                {
                    _ = PairSelectedDeviceAsync();
                }
            }

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Discover on LAN", EditorStyles.boldLabel);
            EditorGUILayout.HelpBox(
                "Discovered devices aren't automatically pairable: add a matching [[device]] "
                    + "entry to devices.toml and restart lazydeck serve first (it only reads "
                    + "devices.toml at startup), then Connect again.",
                MessageType.Info
            );

            using (new EditorGUI.DisabledScope(_busy || _client == null))
            {
                if (GUILayout.Button("Discover"))
                {
                    _ = DiscoverAsync();
                }
            }

            foreach (DiscoveredDeviceEntry device in _discovered)
            {
                EditorGUILayout.LabelField($"{device.name} @ {device.address}:{device.port}");
            }

            DrawBuildAndDeploy(hasDeviceSelected);
            DrawLogSync(hasDeviceSelected);

            EditorGUILayout.Space();
            using (new EditorGUI.DisabledScope(_client == null || string.IsNullOrEmpty(_currentJobId)))
            {
                if (GUILayout.Button("Cancel current job"))
                {
                    _ = CancelCurrentJobAsync();
                }
            }

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Log", EditorStyles.boldLabel);
            _logScroll = EditorGUILayout.BeginScrollView(_logScroll, GUILayout.Height(150));
            EditorGUILayout.TextArea(_log.ToString(), GUILayout.ExpandHeight(true));
            EditorGUILayout.EndScrollView();
        }

        private void DrawBuildAndDeploy(bool hasDeviceSelected)
        {
            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Build & deploy", EditorStyles.boldLabel);

            BuildTarget[] targets = BuildRunner.SupportedTargets;
            string[] targetLabels = new string[targets.Length];
            for (int i = 0; i < targets.Length; i++)
            {
                targetLabels[i] = targets[i].ToString();
            }

            int previousTargetIndex = _selectedTargetIndex;
            _selectedTargetIndex = EditorGUILayout.Popup(
                "Target",
                Mathf.Clamp(_selectedTargetIndex, 0, targets.Length - 1),
                targetLabels
            );
            if (_selectedTargetIndex != previousTargetIndex)
            {
                // The expected extension differs per target, so re-derive the
                // name instead of leaving a .exe queued for a Linux build.
                _executableName = BuildRunner.DefaultExecutableName(SelectedTarget());
            }

            _developmentBuild = EditorGUILayout.Toggle("Development build", _developmentBuild);
            _gameId = EditorGUILayout.TextField("Game ID", _gameId);
            _outputDirectory = EditorGUILayout.TextField("Output directory", _outputDirectory);
            _executableName = EditorGUILayout.TextField("Executable name", _executableName);

            EditorGUILayout.HelpBox(
                "Builds the scenes enabled in File > Build Settings, then deploys the whole "
                    + "output directory so the executable's _Data folder goes with it. The "
                    + "editor is unresponsive while the build runs.",
                MessageType.Info
            );

            bool canDeploy = !_busy && _client != null && hasDeviceSelected;
            using (new EditorGUI.DisabledScope(!canDeploy))
            {
                if (GUILayout.Button("Build & deploy"))
                {
                    _ = BuildAndDeployAsync();
                }
            }
        }

        private void DrawLogSync(bool hasDeviceSelected)
        {
            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Sync logs", EditorStyles.boldLabel);
            _logsDirectory = EditorGUILayout.TextField("Local logs directory", _logsDirectory);

            bool canSync = !_busy && _client != null && hasDeviceSelected;
            using (new EditorGUI.DisabledScope(!canSync))
            {
                if (GUILayout.Button("Sync logs from selected device"))
                {
                    _ = SyncLogsAsync();
                }
            }
        }

        private DeviceEntry SelectedDevice()
        {
            if (_selectedDeviceIndex < 0 || _selectedDeviceIndex >= _devices.Length)
            {
                return null;
            }
            return _devices[_selectedDeviceIndex];
        }

        private async Awaitable BuildAndDeployAsync()
        {
            DeviceEntry device = SelectedDevice();
            if (_client == null || device == null)
            {
                return;
            }
            string outputDirectory = _outputDirectory.Trim();
            string executableName = _executableName.Trim();
            string gameId = _gameId.Trim();
            if (
                outputDirectory.Length == 0
                || executableName.Length == 0
                || gameId.Length == 0
            )
            {
                LogLine("Output directory, executable name, and game ID are all required.");
                return;
            }
            if (!Path.IsPathRooted(outputDirectory))
            {
                LogLine("Output directory must be an absolute path.");
                return;
            }

            LazyDeckClient client = _client;
            BuildTarget target = SelectedTarget();
            bool development = _developmentBuild;
            _busy = true;
            try
            {
                LogLine(
                    $"Building {target} "
                        + $"({(development ? "development" : "release")}) to {outputDirectory}..."
                );
                BuildOutcome build = BuildRunner.Build(
                    target,
                    outputDirectory,
                    executableName,
                    development
                );
                if (this == null)
                {
                    return;
                }
                if (!build.Ok)
                {
                    LogLine($"Build failed: {build.Error}");
                    return;
                }

                LogLine($"Build finished. Deploying {outputDirectory} to {device.id}...");
                ApiResult submit = await client.SubmitDeploymentAsync(
                    device.id,
                    gameId,
                    outputDirectory
                );
                if (this == null || _client != client)
                {
                    return;
                }
                if (!submit.Ok)
                {
                    LogLine($"Deploy submission failed: {submit.ErrorMessage}");
                    return;
                }

                await TrackJobAsync(client, submit, "Deploy");
            }
            finally
            {
                if (this != null)
                {
                    _busy = false;
                    _currentJobId = "";
                    Repaint();
                }
            }
        }

        private async Awaitable SyncLogsAsync()
        {
            DeviceEntry device = SelectedDevice();
            if (_client == null || device == null)
            {
                return;
            }
            string logsDirectory = _logsDirectory.Trim();
            if (logsDirectory.Length == 0)
            {
                LogLine("Local logs directory is required.");
                return;
            }
            if (!Path.IsPathRooted(logsDirectory))
            {
                LogLine("Local logs directory must be an absolute path.");
                return;
            }

            LazyDeckClient client = _client;
            _busy = true;
            try
            {
                LogLine($"Syncing logs from {device.id} to {logsDirectory}...");
                ApiResult submit = await client.SubmitLogsSyncAsync(device.id, logsDirectory);
                if (this == null || _client != client)
                {
                    return;
                }
                if (!submit.Ok)
                {
                    LogLine($"Log sync submission failed: {submit.ErrorMessage}");
                    return;
                }

                await TrackJobAsync(client, submit, "Log sync");
            }
            finally
            {
                if (this != null)
                {
                    _busy = false;
                    _currentJobId = "";
                    Repaint();
                }
            }
        }

        /// <summary>
        /// Adopts the job named in a submission response as the window's
        /// current job, polls it to a terminal state, and reports the outcome.
        /// Shared by deploy and log sync, which differ only in their label.
        /// </summary>
        private async Awaitable TrackJobAsync(
            LazyDeckClient client,
            ApiResult submitResult,
            string label
        )
        {
            JobEntry queued = ParseJob(submitResult.Body);
            if (queued == null || string.IsNullOrEmpty(queued.id))
            {
                LogLine($"{label} was submitted but the response named no job to poll.");
                return;
            }

            _currentJobId = queued.id;
            LogLine($"{label} job {queued.id} queued.");
            Repaint();

            JobEntry final = await PollJobAsync(client, queued.id);
            if (this == null || _client != client || final == null)
            {
                return;
            }

            if (final.status == "succeeded")
            {
                LogLine($"{label} complete.");
            }
            else
            {
                string detail = final.error?.message;
                if (string.IsNullOrEmpty(detail))
                {
                    detail = final.last_message ?? "";
                }
                LogLine($"{label} did not succeed ({final.status}): {detail}");
            }
        }

        /// <summary>
        /// Polls one job to a terminal state, logging only on status changes so
        /// a long deploy doesn't flood the log with identical lines. Returns
        /// null when the poll failed or this window/client was superseded.
        /// </summary>
        private async Awaitable<JobEntry> PollJobAsync(LazyDeckClient client, string jobId)
        {
            string lastStatus = "";
            while (true)
            {
                ApiResult result = await client.GetJobAsync(jobId);
                if (this == null || _client != client)
                {
                    return null;
                }
                if (!result.Ok)
                {
                    LogLine($"Failed to poll job {jobId}: {result.ErrorMessage}");
                    return null;
                }

                JobEntry job = ParseJob(result.Body);
                if (job == null)
                {
                    LogLine($"Failed to parse the status of job {jobId}.");
                    return null;
                }

                if (job.status != lastStatus)
                {
                    LogLine($"Job {jobId}: {job.status}");
                    lastStatus = job.status;
                    Repaint();
                }
                if (job.status == "succeeded" || job.status == "failed" || job.status == "cancelled")
                {
                    return job;
                }

                await EditorDelay.ForSecondsAsync(1.0);
                if (this == null || _client != client)
                {
                    return null;
                }
            }
        }

        private async Awaitable CancelCurrentJobAsync()
        {
            if (_client == null || string.IsNullOrEmpty(_currentJobId))
            {
                return;
            }
            // Deliberately does not clear _busy or _currentJobId: the in-flight
            // PollJobAsync owns both and will observe the cancelled status on
            // its next tick, exactly as the Godot dock does.
            LazyDeckClient client = _client;
            string jobId = _currentJobId;
            LogLine($"Cancelling job {jobId}...");
            ApiResult result = await client.CancelJobAsync(jobId);
            if (this == null || _client != client)
            {
                return;
            }
            if (!result.Ok)
            {
                LogLine($"Failed to cancel job {jobId}: {result.ErrorMessage}");
            }
            Repaint();
        }

        private JobEntry ParseJob(string body)
        {
            if (string.IsNullOrEmpty(body))
            {
                return null;
            }
            try
            {
                return JsonUtility.FromJson<JobResponse>(body)?.job;
            }
            catch (ArgumentException)
            {
                return null;
            }
        }

        private async Awaitable ConnectAsync()
        {
            if (_busy)
            {
                return;
            }
            _busy = true;
            try
            {
                _client = null;
                _devices = Array.Empty<DeviceEntry>();
                _selectedDeviceIndex = -1;
                _discovered = Array.Empty<DiscoveredDeviceEntry>();
                // Reconnecting invalidates any job tracked against the old
                // client — there is nothing left to poll or cancel.
                _currentJobId = "";

                ConnectionLocator.Result located = ConnectionLocator.Load();
                if (!located.Ok)
                {
                    _statusText = "Not connected";
                    LogLine($"Could not find a running lazydeck serve: {located.Error}");
                    return;
                }

                var client = new LazyDeckClient(located.Info);
                ApiResult caps = await client.GetCapabilitiesAsync();
                if (this == null)
                {
                    return; // window closed while the request was in flight
                }
                if (!caps.Ok)
                {
                    _statusText = "Not connected";
                    LogLine($"Found a connection file but the request failed: {caps.ErrorMessage}");
                    return;
                }

                // caps.Ok only reflects the HTTP status; validate that the body
                // actually is a capabilities payload with an api_version before
                // treating this endpoint as a compatible lazydeck serve.
                CapabilitiesResponse parsedCaps;
                try
                {
                    parsedCaps = JsonUtility.FromJson<CapabilitiesResponse>(caps.Body);
                }
                catch (ArgumentException e)
                {
                    _statusText = "Not connected";
                    LogLine($"Found a connection file but the capabilities response was not valid JSON: {e.Message}");
                    return;
                }
                if (parsedCaps == null || string.IsNullOrEmpty(parsedCaps.api_version))
                {
                    _statusText = "Not connected";
                    LogLine("Found a connection file but the capabilities response was missing an api_version.");
                    return;
                }

                _client = client;
                _statusText =
                    $"Connected: {located.Info.api_version} (pid {located.Info.pid}, port {located.Info.port})";
                LogLine($"Connected to lazydeck serve at {located.Info.base_url}");
                await RefreshDevicesAsync(client);
            }
            finally
            {
                if (this != null)
                {
                    _busy = false;
                    Repaint();
                }
            }
        }

        private async Awaitable RefreshDevicesAsync(LazyDeckClient client)
        {
            ApiResult result = await client.ListDevicesAsync();
            if (this == null)
            {
                return; // window closed while the request was in flight
            }
            if (_client != client)
            {
                return; // superseded by a later Connect click; that call owns the UI now
            }

            if (!result.Ok)
            {
                LogLine($"Failed to list devices: {result.ErrorMessage}");
                return;
            }

            DeviceListResponse parsed;
            try
            {
                parsed = JsonUtility.FromJson<DeviceListResponse>(result.Body);
            }
            catch (ArgumentException e)
            {
                LogLine($"Failed to parse device list: {e.Message}");
                return;
            }
            _devices = parsed?.devices ?? Array.Empty<DeviceEntry>();
        }

        private async Awaitable PairSelectedDeviceAsync()
        {
            if (_client == null || _selectedDeviceIndex < 0 || _selectedDeviceIndex >= _devices.Length)
            {
                return;
            }
            LazyDeckClient client = _client;
            DeviceEntry device = _devices[_selectedDeviceIndex];
            _busy = true;
            try
            {
                LogLine($"Pairing {device.id}...");
                ApiResult result = await client.PairDeviceAsync(device.id);
                if (this == null)
                {
                    return; // window closed while the request was in flight
                }
                if (_client != client)
                {
                    return;
                }
                LogLine(
                    result.Ok
                        ? $"Paired {device.id}."
                        : $"Failed to pair {device.id}: {result.ErrorMessage}"
                );
            }
            finally
            {
                if (this != null)
                {
                    _busy = false;
                    Repaint();
                }
            }
        }

        private async Awaitable DiscoverAsync()
        {
            if (_client == null)
            {
                return;
            }
            LazyDeckClient client = _client;
            _busy = true;
            try
            {
                _discovered = Array.Empty<DiscoveredDeviceEntry>();
                LogLine("Discovering devkits on the LAN...");
                ApiResult result = await client.DiscoverDevicesAsync();
                if (this == null)
                {
                    return; // window closed while the request was in flight
                }
                if (_client != client)
                {
                    return;
                }
                if (!result.Ok)
                {
                    LogLine($"Discover failed: {result.ErrorMessage}");
                    return;
                }
                DiscoveredDeviceListResponse parsed;
                try
                {
                    parsed = JsonUtility.FromJson<DiscoveredDeviceListResponse>(result.Body);
                }
                catch (ArgumentException e)
                {
                    LogLine($"Failed to parse discovered devices: {e.Message}");
                    return;
                }
                _discovered = parsed?.devices ?? Array.Empty<DiscoveredDeviceEntry>();
                if (_discovered.Length == 0)
                {
                    LogLine("No devkits found.");
                }
            }
            finally
            {
                if (this != null)
                {
                    _busy = false;
                    Repaint();
                }
            }
        }

        private void LogLine(string text)
        {
            _log.Append(text).Append('\n');
        }
    }
}
