using System;

namespace LazyDeck.Editor.Cli
{
    /// <summary>
    /// A batch-mode CLI failure with a message meant to be logged and turned
    /// into a nonzero process exit code, not surfaced as a raw stack trace.
    /// </summary>
    internal sealed class LazyDeckCliException : Exception
    {
        public LazyDeckCliException(string message)
            : base(message) { }
    }
}
