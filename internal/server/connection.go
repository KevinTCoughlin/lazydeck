package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConnectionInfo is what `lazydeck serve` writes to disk so an engine
// plugin that did not spawn the process itself (issue #14's "connect to an
// already-running service" case) can still discover it, Jupyter-connection-
// file style, instead of scraping stdout.
type ConnectionInfo struct {
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	BaseURL    string `json:"base_url"`
	Token      string `json:"token"`
	APIVersion string `json:"api_version"`
}

// ConnectionFilePath returns the path `lazydeck serve` writes its
// ConnectionInfo to. It is exported so other same-module clients (e.g.
// internal/mcp's launcher) can discover a running server the same way an
// out-of-process engine plugin would, without duplicating the XDG/cache-dir
// resolution logic.
func ConnectionFilePath() (string, error) {
	return connectionFilePath()
}

// ReadConnectionInfo reads and parses the connection file at path. It is
// exported for the same reason as ConnectionFilePath: same-module clients
// like internal/mcp need to read what `lazydeck serve` wrote without
// reimplementing the JSON shape.
func ReadConnectionInfo(path string) (ConnectionInfo, error) {
	return readConnectionInfo(path)
}

// connectionFilePath returns the path `lazydeck serve` writes its
// ConnectionInfo to. It prefers $XDG_RUNTIME_DIR (tmpfs-backed, per-session,
// already 0700 on a systemd-managed Linux desktop) since the file holds a
// bearer token and should not outlive the session or land somewhere synced
// to a backup/cloud drive; it falls back to the user's cache directory
// (matching internal/client's use of os.UserCacheDir for the Python venv)
// on platforms without XDG_RUNTIME_DIR, e.g. macOS.
func connectionFilePath() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "lazydeck", "serve.json"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving connection file location: %w", err)
	}
	return filepath.Join(dir, "lazydeck", "serve.json"), nil
}

// acquireConnectionFile opens path (creating it if needed) and takes an
// exclusive, non-blocking OS file lock on it for the life of the returned
// *os.File, so a second `lazydeck serve` can never race the first to bind a
// port and overwrite the connection file: unlike a check-then-act PID
// liveness check (this file's previous approach), the lock is held
// atomically by the OS and is automatically released if this process dies
// or is killed, with no staleness heuristic required. Callers must Close
// the returned file (which releases the lock) when the server shuts down.
func acquireConnectionFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := lockExclusiveNonblocking(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lazydeck serve is already running (connection file: %s): %w", path, err)
	}
	// The file may have pre-existed with a different mode (e.g. from a
	// build before this file held a bearer token); enforce 0600 now that
	// we hold the lock, since O_CREATE's mode argument only applies when
	// the file is newly created.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return f, nil
}

// writeConnectionInfo overwrites f's contents with info, encoded as JSON.
// f must already be open for writing (see acquireConnectionFile) and is
// truncated and rewritten from offset 0 in place: since f is a single
// already-locked, already-0600 file descriptor for the whole server
// lifetime, there's no separate temp file to make atomic — the lock itself
// is what a concurrent reader/writer must respect.
func writeConnectionInfo(f *os.File, info ConnectionInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating connection file: %w", err)
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		return fmt.Errorf("writing connection file: %w", err)
	}
	return f.Sync()
}

// readConnectionInfo reads a previously written connection file. Used by
// tests to verify what acquireConnectionFile/writeConnectionInfo produced;
// a real engine-plugin reader lives outside this Go codebase.
func readConnectionInfo(path string) (ConnectionInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ConnectionInfo{}, err
	}
	var info ConnectionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return ConnectionInfo{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return info, nil
}
