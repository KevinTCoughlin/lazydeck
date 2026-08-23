using System;
using System.IO;
using System.Threading;
using LazyDeck.Editor.Api;
using UnityEditor;
using UnityEngine;

namespace LazyDeck.Editor.Cli
{
    /// <summary>
    /// Batch-mode entry points for
    /// `Unity -batchmode -quit -executeMethod LazyDeck.Editor.Cli.LazyDeckCli.BuildAndDeploy`,
    /// the scripted-CI counterpart of LazyDeckWindow's "Build &amp; deploy" and
    /// "Sync logs" buttons — for a CI job that needs to build a player and
    /// ship it to a paired devkit, or pull its logs, without opening the
    /// Unity Editor UI at all. Same motivation as the Godot integration
    /// shelling out to `godot --headless --export-*`.
    ///
    /// Every option is read from `-lazydeck<Name>` command-line flags (see
    /// CliArgs) rather than from EditorPrefs/window state, since there is no
    /// window in batch mode. Progress and failures go to Debug.Log, which
    /// Unity's own `-logFile` captures; a failure calls EditorApplication.Exit
    /// with a nonzero code so the invoking script can detect it, since a
    /// batch-mode process otherwise exits 0 whether or not the method's body
    /// actually succeeded.
    /// </summary>
    internal static class LazyDeckCli
    {
        private const int DefaultJobTimeoutSeconds = 600;
        private const int ConnectPollAttempts = 20;
        private const double ConnectPollIntervalSeconds = 0.25;

        /// <summary>
        /// -lazydeckDevice, -lazydeckGame, -lazydeckOutput (an absolute
        /// path), and -lazydeckLaunchArgs are required — the deploy API has
        /// nothing to launch and always fails once the rsync step finishes
        /// without an argv (see api/openapi.yaml's deployments.argv and
        /// internal/client.Client.Deploy's doc comment), so this fails fast
        /// rather than spending a build and a rsync on a job that cannot
        /// succeed. -lazydeckTarget (default StandaloneLinux64),
        /// -lazydeckExecutable (default derived from the product name),
        /// -lazydeckDevelopment (a flag, no value), and
        /// -lazydeckTimeoutSeconds (default 600) are optional.
        /// </summary>
        public static void BuildAndDeploy()
        {
            RunGuarded(() =>
            {
                CliArgs args = CliArgs.Parse(Environment.GetCommandLineArgs());
                BuildTarget target = ResolveTarget(args);
                string deviceId = args.Require("Device");
                string gameId = args.Require("Game");
                string outputDirectory = args.Require("Output");
                string executableName = args.GetOrDefault(
                    "Executable",
                    BuildRunner.DefaultExecutableName(target)
                );
                bool development = args.Flag("Development");
                string[] argv = ParseArgv(args.Require("LaunchArgs"));
                if (argv.Length == 0)
                {
                    throw new LazyDeckCliException(
                        "-lazydeckLaunchArgs must contain at least one token"
                    );
                }
                int timeoutSeconds = args.GetIntOrDefault("TimeoutSeconds", DefaultJobTimeoutSeconds);

                if (!Path.IsPathRooted(outputDirectory))
                {
                    throw new LazyDeckCliException("-lazydeckOutput must be an absolute path");
                }

                Log(
                    $"Building {target} ({(development ? "development" : "release")}) "
                        + $"to {outputDirectory}..."
                );
                BuildOutcome build = BuildRunner.Build(target, outputDirectory, executableName, development);
                if (!build.Ok)
                {
                    throw new LazyDeckCliException($"build failed: {build.Error}");
                }
                Log("Build finished.");

                using LazyDeckCliClient client = Connect();
                Log($"Deploying {outputDirectory} to {deviceId}...");
                string jobId = client.SubmitDeployment(deviceId, gameId, outputDirectory, argv);
                RunJobToCompletion(client, jobId, "Deploy", timeoutSeconds);
            });
        }

        /// <summary>
        /// -lazydeckDevice and -lazydeckLogsDirectory (an absolute path) are
        /// required; -lazydeckTimeoutSeconds is optional (default 600).
        /// </summary>
        public static void SyncLogs()
        {
            RunGuarded(() =>
            {
                CliArgs args = CliArgs.Parse(Environment.GetCommandLineArgs());
                string deviceId = args.Require("Device");
                string logsDirectory = args.Require("LogsDirectory");
                int timeoutSeconds = args.GetIntOrDefault("TimeoutSeconds", DefaultJobTimeoutSeconds);

                if (!Path.IsPathRooted(logsDirectory))
                {
                    throw new LazyDeckCliException("-lazydeckLogsDirectory must be an absolute path");
                }

                using LazyDeckCliClient client = Connect();
                Log($"Syncing logs from {deviceId} to {logsDirectory}...");
                string jobId = client.SubmitLogsSync(deviceId, logsDirectory);
                RunJobToCompletion(client, jobId, "Log sync", timeoutSeconds);
            });
        }

        private static BuildTarget ResolveTarget(CliArgs args)
        {
            string raw = args.GetOrDefault("Target", null);
            if (string.IsNullOrEmpty(raw))
            {
                return BuildRunner.SupportedTargets[0];
            }
            if (
                !Enum.TryParse(raw, ignoreCase: true, out BuildTarget target)
                || Array.IndexOf(BuildRunner.SupportedTargets, target) < 0
            )
            {
                string supported = string.Join(
                    ", ",
                    Array.ConvertAll(BuildRunner.SupportedTargets, t => t.ToString())
                );
                throw new LazyDeckCliException(
                    $"-lazydeckTarget must be one of {supported} (got \"{raw}\")"
                );
            }
            return target;
        }

        /// <summary>
        /// Locates a running `lazydeck serve` and, failing that, starts one
        /// via ServerLauncher exactly as LazyDeckWindow.ConnectAsync does,
        /// then confirms it with a capabilities request before handing back
        /// a client.
        /// </summary>
        private static LazyDeckCliClient Connect()
        {
            ConnectionLocator.Result located = ConnectionLocator.Load();
            if (!located.Ok)
            {
                if (!ServerLauncher.StartIfNeeded(Log))
                {
                    throw new LazyDeckCliException($"no running lazydeck serve: {located.Error}");
                }
                for (int attempt = 0; attempt < ConnectPollAttempts && !located.Ok; attempt++)
                {
                    Thread.Sleep(TimeSpan.FromSeconds(ConnectPollIntervalSeconds));
                    // There is no EditorWindow polling DrainMessages every OnGUI
                    // here, so a startup failure's stderr — the most actionable
                    // diagnostic available — would otherwise sit undrained and
                    // never reach the batch-mode log.
                    ServerLauncher.DrainMessages(Log);
                    located = ConnectionLocator.Load();
                }
                ServerLauncher.DrainMessages(Log);
                if (!located.Ok)
                {
                    throw new LazyDeckCliException($"lazydeck serve did not start in time: {located.Error}");
                }
            }

            var client = new LazyDeckCliClient(located.Info);
            string apiVersion = client.GetCapabilitiesApiVersion();
            Log($"Connected to lazydeck serve at {located.Info.base_url} ({apiVersion}).");
            return client;
        }

        /// <summary>
        /// Polls jobId to a terminal state, logging only on status changes,
        /// the same shape as LazyDeckWindow.PollJobAsync — but blocking this
        /// thread with Thread.Sleep instead of an Awaitable delay, since
        /// there is no window/repaint loop to yield to in batch mode.
        /// Throws on failure/cancellation or on exceeding timeoutSeconds, so
        /// callers can let RunGuarded turn it into a nonzero exit code.
        /// </summary>
        private static void RunJobToCompletion(
            LazyDeckCliClient client,
            string jobId,
            string label,
            int timeoutSeconds
        )
        {
            Log($"{label} job {jobId} queued.");
            string lastStatus = "";
            DateTime deadline = DateTime.UtcNow.AddSeconds(timeoutSeconds);
            while (true)
            {
                LazyDeckCliClient.JobEntry job = client.GetJob(jobId);
                if (job.status != lastStatus)
                {
                    Log($"Job {jobId}: {job.status}");
                    lastStatus = job.status;
                }

                if (job.status == "succeeded")
                {
                    Log($"{label} complete.");
                    return;
                }
                if (job.status == "failed" || job.status == "cancelled")
                {
                    string detail = job.error?.message;
                    if (string.IsNullOrEmpty(detail))
                    {
                        detail = job.last_message ?? "";
                    }
                    throw new LazyDeckCliException($"{label} did not succeed ({job.status}): {detail}");
                }

                if (DateTime.UtcNow >= deadline)
                {
                    throw new LazyDeckCliException(
                        $"{label} job {jobId} did not finish within {timeoutSeconds}s "
                            + "(raise -lazydeckTimeoutSeconds if it just needs longer)"
                    );
                }
                Thread.Sleep(TimeSpan.FromSeconds(1));
            }
        }

        private static void RunGuarded(Action action)
        {
            try
            {
                action();
            }
            catch (LazyDeckCliException e)
            {
                Log($"lazydeck: {e.Message}");
                EditorApplication.Exit(1);
            }
            catch (Exception e)
            {
                Log($"lazydeck: unexpected error: {e}");
                EditorApplication.Exit(1);
            }
        }

        private static void Log(string message) => Debug.Log($"[LazyDeck] {message}");

        /// <summary>
        /// Splits a whitespace-separated launch command into argv tokens,
        /// mirroring LazyDeckWindow.ParseArgv (see that method's doc comment
        /// for why no quoting support is offered).
        /// </summary>
        private static string[] ParseArgv(string launchCommand)
        {
            if (string.IsNullOrWhiteSpace(launchCommand))
            {
                return Array.Empty<string>();
            }
            return launchCommand.Split((char[])null, StringSplitOptions.RemoveEmptyEntries);
        }
    }
}
