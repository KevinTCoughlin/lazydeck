package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// writeConnectionInfo persists info to the connection file with 0600
// permissions (it contains the bearer token) and returns the path written.
func writeConnectionInfo(info ConnectionInfo) (string, error) {
	path, err := connectionFilePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// readConnectionInfo reads a previously written connection file, used to
// detect a still-running `lazydeck serve` before starting a second one.
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

// processAlive reports whether pid names a live process on this machine,
// used to tell a genuinely running `lazydeck serve` apart from a stale
// connection file left behind by one that crashed or was killed.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 performs no-op existence/permission checks without actually
	// sending a signal; this only works on Unix, which matches the rest of
	// the codebase's process-group handling (see internal/client/process_unix.go).
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// checkNoOtherInstance errors out if the connection file names a PID that is
// still alive, so two `lazydeck serve` instances never race to write the
// same file or bind overlapping state. A stale file (dead PID, or unreadable
// because it doesn't exist yet) is not an error.
func checkNoOtherInstance() error {
	path, err := connectionFilePath()
	if err != nil {
		return err
	}
	info, err := readConnectionInfo(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return nil // unreadable/corrupt connection file: treat as stale, not fatal
	}
	if processAlive(info.PID) {
		return fmt.Errorf("lazydeck serve already running as pid %d on port %d (connection file: %s)", info.PID, info.Port, path)
	}
	return nil
}
