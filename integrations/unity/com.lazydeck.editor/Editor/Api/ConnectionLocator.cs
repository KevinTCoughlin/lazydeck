using System;
using System.IO;
using UnityEngine;

namespace LazyDeck.Editor.Api
{
    /// <summary>
    /// Finds and parses the connection file `lazydeck serve` writes on
    /// startup. Mirrors internal/server/connection.go's connectionFilePath()
    /// exactly (same env var, same fallback, same relative path) so this
    /// package locates the same file the Go service itself considers
    /// authoritative, without needing to invoke `lazydeck` at all.
    /// </summary>
    internal static class ConnectionLocator
    {
        /// <summary>
        /// The outcome of locating and parsing a connection file: either
        /// Ok with the parsed Info and the Path it was read from, or a
        /// human-readable Error. A struct rather than throwing, since a
        /// missing connection file (lazydeck serve simply isn't running
        /// yet) is an expected, common outcome for the caller to handle,
        /// not an exceptional one.
        /// </summary>
        public readonly struct Result
        {
            public readonly bool Ok;
            public readonly ConnectionInfo Info;
            public readonly string Path;
            public readonly string Error;

            private Result(bool ok, ConnectionInfo info, string path, string error)
            {
                Ok = ok;
                Info = info;
                Path = path;
                Error = error;
            }

            public static Result Success(ConnectionInfo info, string path) =>
                new Result(true, info, path, null);

            public static Result Failure(string error) => new Result(false, null, null, error);
        }

        /// <summary>
        /// Prefers $XDG_RUNTIME_DIR (matches the Go side's preference for a
        /// tmpfs-backed, per-session, already-private location for a file
        /// holding a bearer token) and falls back to the OS cache directory
        /// otherwise (matches Go's os.UserCacheDir()).
        /// </summary>
        public static string DefaultPath()
        {
            string runtimeDir = Environment.GetEnvironmentVariable("XDG_RUNTIME_DIR");
            if (!string.IsNullOrEmpty(runtimeDir))
            {
                return Path.Combine(runtimeDir, "lazydeck", "serve.json");
            }
            return Path.Combine(CacheDirectory(), "lazydeck", "serve.json");
        }

        /// <summary>
        /// Approximates Go's os.UserCacheDir() on the platforms lazydeck
        /// itself ships for (see .goreleaser.yml): ~/Library/Caches on
        /// macOS, and Linux's $XDG_CACHE_HOME (defaulting to ~/.cache,
        /// matching Go's implementation exactly when unset).
        /// </summary>
        private static string CacheDirectory()
        {
            string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            if (Application.platform == RuntimePlatform.OSXEditor)
            {
                return Path.Combine(home, "Library", "Caches");
            }
            string xdgCache = Environment.GetEnvironmentVariable("XDG_CACHE_HOME");
            return !string.IsNullOrEmpty(xdgCache) ? xdgCache : Path.Combine(home, ".cache");
        }

        /// <summary>
        /// Reads and parses the connection file at path (the default
        /// location from <see cref="DefaultPath"/> when path is null or
        /// empty).
        /// </summary>
        public static Result Load(string path = null)
        {
            string resolvedPath = string.IsNullOrEmpty(path) ? DefaultPath() : path;
            if (!File.Exists(resolvedPath))
            {
                return Result.Failure(
                    $"no connection file at {resolvedPath} (is `lazydeck serve` running?)"
                );
            }

            string text;
            try
            {
                text = File.ReadAllText(resolvedPath);
            }
            catch (IOException e)
            {
                return Result.Failure($"could not read {resolvedPath}: {e.Message}");
            }

            ConnectionInfo info;
            try
            {
                info = JsonUtility.FromJson<ConnectionInfo>(text);
            }
            catch (ArgumentException e)
            {
                return Result.Failure($"malformed connection file at {resolvedPath}: {e.Message}");
            }

            if (info == null || string.IsNullOrEmpty(info.base_url))
            {
                return Result.Failure($"malformed connection file at {resolvedPath}");
            }

            return Result.Success(info, resolvedPath);
        }
    }
}
