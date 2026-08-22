using UnityEditor;
using UnityEngine;

namespace LazyDeck.Editor.Api
{
    /// <summary>
    /// A timed wait an EditorWindow can await outside Play mode.
    ///
    /// Deliberately not Awaitable.WaitForSecondsAsync: that is driven by the
    /// player frame loop, which does not run on its own in an editor-only
    /// context — the same failure mode #18's validation round found with
    /// Awaitable.NextFrameAsync in LazyDeckClient, where the fix was to bridge
    /// off a callback that the editor genuinely raises. EditorApplication.update
    /// ticks whenever the editor does, so it is the reliable clock here, and
    /// EditorApplication.timeSinceStartup keeps ticking across domain-reload-free
    /// editor idle time.
    /// </summary>
    internal static class EditorDelay
    {
        public static Awaitable ForSecondsAsync(double seconds)
        {
            var completionSource = new AwaitableCompletionSource();
            double deadline = EditorApplication.timeSinceStartup + seconds;

            // Held in an explicit variable rather than written as a local
            // function so the delegate removed inside the callback is provably
            // the same instance that was added.
            EditorApplication.CallbackFunction tick = null;
            tick = () =>
            {
                if (EditorApplication.timeSinceStartup < deadline)
                {
                    return;
                }
                EditorApplication.update -= tick;
                completionSource.SetResult();
            };
            EditorApplication.update += tick;

            return completionSource.Awaitable;
        }
    }
}
