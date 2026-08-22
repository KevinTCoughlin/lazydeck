using System;

namespace LazyDeck.Editor.Api
{
    /// <summary>
    /// Mirrors internal/server.ConnectionInfo's JSON shape (see
    /// internal/server/connection.go): the connection file `lazydeck serve`
    /// writes on startup so a client that didn't spawn the process itself
    /// can still discover it, Jupyter-connection-file style.
    ///
    /// Field names match the JSON keys exactly (pid, port, base_url, token,
    /// api_version) rather than following C# naming conventions, because
    /// UnityEngine.JsonUtility maps JSON fields to C# fields by exact name
    /// with no attribute-based renaming available.
    /// </summary>
    [Serializable]
    internal sealed class ConnectionInfo
    {
        public int pid;
        public int port;
        public string base_url;
        public string token;
        public string api_version;
    }
}
