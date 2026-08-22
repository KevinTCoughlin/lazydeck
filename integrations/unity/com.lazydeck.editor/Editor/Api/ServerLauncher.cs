using System;
using System.ComponentModel;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.IO;
using UnityEditor;

namespace LazyDeck.Editor.Api
{
    /// <summary>
    /// Starts a local LazyDeck service when the editor cannot find one. The
    /// process is deliberately not owned by the editor: one service can serve
    /// both engine integrations and survive an editor restart, just as when a
    /// developer starts it in a terminal.
    /// </summary>
    internal static class ServerLauncher
    {
        private const string AutoStartPreference = "LazyDeck.AutoStartServer";
        private const string ExecutablePreference = "LazyDeck.ServerExecutable";
        private static readonly ConcurrentQueue<string> StderrLines = new ConcurrentQueue<string>();

        public static bool AutoStartEnabled
        {
            get
            {
                string environment = Environment.GetEnvironmentVariable("LAZYDECK_AUTOSTART");
                if (environment == "0" || string.Equals(environment, "false", StringComparison.OrdinalIgnoreCase))
                {
                    return false;
                }
                return EditorPrefs.GetBool(AutoStartPreference, true);
            }
            set => EditorPrefs.SetBool(AutoStartPreference, value);
        }

        public static string Executable
        {
            get
            {
                string environment = Environment.GetEnvironmentVariable("LAZYDECK_BIN");
                if (!string.IsNullOrEmpty(environment))
                {
                    return environment;
                }
                return EditorPrefs.GetString(ExecutablePreference, "lazydeck");
            }
            set => EditorPrefs.SetString(ExecutablePreference, value);
        }

        /// <summary>
        /// Starts `lazydeck serve` only when no connection file exists. A
        /// malformed or inaccessible existing file is reported to the caller
        /// rather than spawning over a potentially running service.
        /// </summary>
        public static bool StartIfNeeded(Action<string> log)
        {
            if (File.Exists(ConnectionLocator.DefaultPath()))
            {
                return false;
            }
            if (!AutoStartEnabled)
            {
                log("lazydeck serve is not running; auto-start is disabled.");
                return false;
            }

            string executable = Executable;
            if (string.IsNullOrWhiteSpace(executable))
            {
                log("lazydeck serve auto-start failed: no executable was configured.");
                return false;
            }

            var startInfo = new ProcessStartInfo
            {
                FileName = executable,
                Arguments = "serve",
                UseShellExecute = false,
                RedirectStandardError = true,
                CreateNoWindow = true,
            };
            try
            {
                Process process = Process.Start(startInfo);
                if (process == null)
                {
                    log($"lazydeck serve auto-start failed: could not start {executable}.");
                    return false;
                }
                process.ErrorDataReceived += (_, eventArgs) =>
                {
                    if (!string.IsNullOrEmpty(eventArgs.Data))
                    {
                        StderrLines.Enqueue($"lazydeck serve: {eventArgs.Data}");
                    }
                };
                process.BeginErrorReadLine();
                log($"Starting lazydeck serve ({executable}, pid {process.Id})...");
                return true;
            }
            catch (Win32Exception e)
            {
                log($"lazydeck serve auto-start failed for {executable}: {e.Message}");
                return false;
            }
            catch (InvalidOperationException e)
            {
                log($"lazydeck serve auto-start failed for {executable}: {e.Message}");
                return false;
            }
        }

        /// <summary>
        /// Delivers child-process stderr on Unity's main thread. Process
        /// callbacks run on a worker thread and must not mutate editor UI
        /// state directly.
        /// </summary>
        public static void DrainMessages(Action<string> log)
        {
            while (StderrLines.TryDequeue(out string line))
            {
                log(line);
            }
        }
    }
}
