using System;
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

        private LazyDeckClient _client;
        private string _statusText = "Not connected";
        private DeviceEntry[] _devices = Array.Empty<DeviceEntry>();
        private int _selectedDeviceIndex = -1;
        private DiscoveredDeviceEntry[] _discovered = Array.Empty<DiscoveredDeviceEntry>();
        private string _log = "";
        private Vector2 _logScroll;
        private bool _busy;

        [MenuItem("Window/LazyDeck")]
        public static void ShowWindow()
        {
            LazyDeckWindow window = GetWindow<LazyDeckWindow>();
            window.titleContent = new GUIContent("LazyDeck");
        }

        private void OnEnable()
        {
            _ = ConnectAsync();
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

            bool canPair =
                !_busy && _client != null && _selectedDeviceIndex >= 0 && _selectedDeviceIndex < _devices.Length;
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
                    + "entry to devices.toml first, then Connect again.",
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

            EditorGUILayout.Space();
            EditorGUILayout.LabelField("Log", EditorStyles.boldLabel);
            _logScroll = EditorGUILayout.BeginScrollView(_logScroll, GUILayout.Height(150));
            EditorGUILayout.TextArea(_log, GUILayout.ExpandHeight(true));
            EditorGUILayout.EndScrollView();
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

                ConnectionLocator.Result located = ConnectionLocator.Load();
                if (!located.Ok)
                {
                    _statusText = "Not connected";
                    LogLine($"Could not find a running lazydeck serve: {located.Error}");
                    return;
                }

                var client = new LazyDeckClient(located.Info);
                ApiResult caps = await client.GetCapabilitiesAsync();
                if (!caps.Ok)
                {
                    _statusText = "Not connected";
                    LogLine($"Found a connection file but the request failed: {caps.ErrorMessage}");
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
                _busy = false;
                Repaint();
            }
        }

        private async Awaitable RefreshDevicesAsync(LazyDeckClient client)
        {
            ApiResult result = await client.ListDevicesAsync();
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
                _busy = false;
                Repaint();
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
                _busy = false;
                Repaint();
            }
        }

        private void LogLine(string text)
        {
            _log += text + "\n";
        }
    }
}
