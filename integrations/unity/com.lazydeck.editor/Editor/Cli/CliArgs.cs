using System;
using System.Collections.Generic;

namespace LazyDeck.Editor.Cli
{
    /// <summary>
    /// Parses this integration's own `-lazydeck<Name>` command-line flags out
    /// of the full argv Unity hands the editor (Environment.GetCommandLineArgs()),
    /// which also contains Unity's own flags (-batchmode, -quit, -executeMethod,
    /// ...) and anything else the invoking script passed. The `-lazydeck` prefix
    /// keeps this parser from ever mistaking one of Unity's own flags for one of
    /// ours.
    /// </summary>
    internal readonly struct CliArgs
    {
        private const string Prefix = "-lazydeck";

        // Flags this integration reads as booleans (present/absent), never as a
        // "name value" pair.
        private static readonly HashSet<string> BooleanFlags = new HashSet<string>(
            StringComparer.OrdinalIgnoreCase
        )
        {
            "Development",
        };

        private readonly Dictionary<string, string> _values;
        private readonly HashSet<string> _flags;

        private CliArgs(Dictionary<string, string> values, HashSet<string> flags)
        {
            _values = values;
            _flags = flags;
        }

        public static CliArgs Parse(string[] argv)
        {
            var values = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            var flags = new HashSet<string>(StringComparer.OrdinalIgnoreCase);

            for (int i = 0; i < argv.Length; i++)
            {
                string arg = argv[i];
                if (!arg.StartsWith(Prefix, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                string name = arg.Substring(Prefix.Length);
                if (name.Length == 0)
                {
                    continue;
                }

                if (BooleanFlags.Contains(name))
                {
                    flags.Add(name);
                    continue;
                }

                if (i + 1 >= argv.Length)
                {
                    throw new LazyDeckCliException($"{Prefix}{name} requires a value");
                }
                values[name] = argv[++i];
            }

            return new CliArgs(values, flags);
        }

        public string Require(string name)
        {
            if (!_values.TryGetValue(name, out string value) || value.Length == 0)
            {
                throw new LazyDeckCliException($"{Prefix}{name} is required");
            }
            return value;
        }

        public string GetOrDefault(string name, string fallback) =>
            _values.TryGetValue(name, out string value) && value.Length > 0 ? value : fallback;

        public int GetIntOrDefault(string name, int fallback)
        {
            if (!_values.TryGetValue(name, out string value) || value.Length == 0)
            {
                return fallback;
            }
            if (!int.TryParse(value, out int parsed))
            {
                throw new LazyDeckCliException(
                    $"{Prefix}{name} must be an integer (got \"{value}\")"
                );
            }
            return parsed;
        }

        public bool Flag(string name) => _flags.Contains(name);
    }
}
