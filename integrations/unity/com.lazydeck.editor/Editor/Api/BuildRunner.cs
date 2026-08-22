using System;
using System.Collections.Generic;
using System.IO;
using System.Text.RegularExpressions;
using UnityEditor;
using UnityEditor.Build.Reporting;
using UnityEngine;

namespace LazyDeck.Editor.Api
{
    /// <summary>
    /// The outcome of one player build. Mirrors the {"ok", "error"} shape the
    /// Godot addon's export_runner.gd returns, so both integrations report
    /// build failures to their UI the same way rather than throwing.
    /// </summary>
    internal readonly struct BuildOutcome
    {
        public readonly bool Ok;
        public readonly string Error;

        private BuildOutcome(bool ok, string error)
        {
            Ok = ok;
            Error = error;
        }

        public static BuildOutcome Success() => new BuildOutcome(true, null);

        public static BuildOutcome Failure(string error) => new BuildOutcome(false, error);
    }

    /// <summary>
    /// Wraps BuildPipeline.BuildPlayer — Unity's documented scriptable build
    /// entry point — as the Unity counterpart of the Godot addon's
    /// api/export_runner.gd.
    ///
    /// Note the deliberate difference from the Godot side: Godot's
    /// EditorExport singleton turned out not to be exposed to GDScript, so
    /// that addon shells out to a second `godot --headless --export-*`
    /// process. Unity has no such limitation — BuildPipeline.BuildPlayer is
    /// the supported in-process API — so nothing is spawned here.
    ///
    /// BuildPlayer runs synchronously and blocks the editor for the duration
    /// of the build; that is normal for scripted Unity builds, but it does
    /// mean the LazyDeck window will not repaint until the build finishes.
    /// </summary>
    internal static class BuildRunner
    {
        /// <summary>
        /// Build targets this integration offers. Linux64 is the Steam Deck's
        /// native target; Windows64 is included because Proton runs Windows
        /// builds on the Deck, so it is a legitimate thing to deploy.
        /// </summary>
        public static readonly BuildTarget[] SupportedTargets =
        {
            BuildTarget.StandaloneLinux64,
            BuildTarget.StandaloneWindows64,
        };

        /// <summary>
        /// The extension Unity expects in locationPathName for a target. Unity
        /// keys parts of its output layout off this, so it is supplied rather
        /// than left to the user to remember.
        /// </summary>
        public static string ExecutableExtension(BuildTarget target)
        {
            switch (target)
            {
                case BuildTarget.StandaloneWindows64:
                    return ".exe";
                case BuildTarget.StandaloneLinux64:
                    return ".x86_64";
                default:
                    return string.Empty;
            }
        }

        /// <summary>
        /// A reasonable default executable name: the project's product name,
        /// sanitized to characters that are safe in a file name, plus the
        /// target's expected extension. Falls back to a fixed name when the
        /// product name is empty or sanitizes away to nothing.
        /// </summary>
        public static string DefaultExecutableName(BuildTarget target)
        {
            string sanitized = Regex.Replace(
                (Application.productName ?? string.Empty).ToLowerInvariant(),
                "[^a-z0-9_-]+",
                "_"
            );
            sanitized = sanitized.Trim('_');
            if (string.IsNullOrEmpty(sanitized))
            {
                sanitized = "game";
            }
            return sanitized + ExecutableExtension(target);
        }

        /// <summary>
        /// Builds the enabled scenes in Build Settings as
        /// outputDirectory/executableName, with everything else Unity writes
        /// alongside it (the _Data folder, platform libraries) landing in
        /// outputDirectory — which is why the caller deploys the directory
        /// rather than the executable alone.
        /// </summary>
        public static BuildOutcome Build(
            BuildTarget target,
            string outputDirectory,
            string executableName,
            bool development
        )
        {
            if (string.IsNullOrEmpty(outputDirectory) || !Path.IsPathRooted(outputDirectory))
            {
                return BuildOutcome.Failure("output directory must be an absolute path");
            }
            if (string.IsNullOrEmpty(executableName))
            {
                return BuildOutcome.Failure("executable name is required");
            }
            // Must be a bare file name. Path.Combine silently discards
            // outputDirectory when the second argument is rooted, and a
            // relative name like "../game.x86_64" escapes it — either way the
            // player lands somewhere the caller then doesn't deploy, because
            // the caller uploads outputDirectory. Failing here beats shipping
            // a deployment that's missing the build.
            // Checked with explicit separator tests rather than
            // Path.GetFileName, which historically threw on invalid path
            // characters — this method's contract is to return a failure, not
            // to throw.
            if (Path.IsPathRooted(executableName)
                || executableName.IndexOf(Path.DirectorySeparatorChar) >= 0
                || executableName.IndexOf(Path.AltDirectorySeparatorChar) >= 0
                || executableName == "."
                || executableName == "..")
            {
                return BuildOutcome.Failure(
                    "executable name must be a bare file name, not a path "
                        + $"(got \"{executableName}\")"
                );
            }

            string[] scenes = EnabledScenePaths();
            if (scenes.Length == 0)
            {
                return BuildOutcome.Failure(
                    "no enabled scenes in Build Settings (File > Build Settings...); "
                        + "add at least one before building"
                );
            }

            try
            {
                Directory.CreateDirectory(outputDirectory);
            }
            catch (Exception e) when (e is IOException || e is UnauthorizedAccessException)
            {
                return BuildOutcome.Failure($"could not create {outputDirectory}: {e.Message}");
            }

            var options = new BuildPlayerOptions
            {
                scenes = scenes,
                locationPathName = Path.Combine(outputDirectory, executableName),
                target = target,
                targetGroup = BuildPipeline.GetBuildTargetGroup(target),
                options = development ? BuildOptions.Development : BuildOptions.None,
            };

            BuildReport report;
            try
            {
                report = BuildPipeline.BuildPlayer(options);
            }
            catch (Exception e)
            {
                return BuildOutcome.Failure($"build threw: {e.Message}");
            }

            if (report == null)
            {
                return BuildOutcome.Failure("build returned no report");
            }

            BuildSummary summary = report.summary;
            if (summary.result != BuildResult.Succeeded)
            {
                return BuildOutcome.Failure(
                    $"build {summary.result.ToString().ToLowerInvariant()} "
                        + $"with {summary.totalErrors} error(s)"
                );
            }

            return BuildOutcome.Success();
        }

        private static string[] EnabledScenePaths()
        {
            var paths = new List<string>();
            foreach (EditorBuildSettingsScene scene in EditorBuildSettings.scenes)
            {
                if (scene.enabled && !string.IsNullOrEmpty(scene.path))
                {
                    paths.Add(scene.path);
                }
            }
            return paths.ToArray();
        }
    }
}
