// Package client shells out to the vendored Python devkit CLI (python/cli.py)
// via `uv run`, so lazydeck never has to reimplement steamos-devkit's
// pairing / SSH / rsync protocol in Go. Each call returns a decoded JSON
// envelope: {"ok": bool, "data": any, "error": string}.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Client drives the headless Python CLI for one lazydeck process.
type Client struct {
	PythonDir string // directory containing pyproject.toml + cli.py
	Timeout   time.Duration
}

type envelope struct {
	OK        bool            `json:"ok"`
	Data      json.RawMessage `json:"data"`
	Error     string          `json:"error"`
	ErrorKind string          `json:"error_kind"`
}

// CLIError wraps a failure reported by cli.py along with its coarse
// category (auth-failed / unreachable / invalid-input / script-error /
// unknown), so callers like the TUI can react differently (e.g. color an
// "unreachable" device differently from an "auth-failed" one) without
// string-matching error messages.
type CLIError struct {
	Kind    string
	Message string
}

func (e *CLIError) Error() string { return e.Message }

// New locates the vendored python/ directory, preferring $LAZYDECK_PYTHON_DIR,
// then a "python" sibling of the running binary (installed layout), then a
// "python" sibling found by walking up from the current source file (dev
// layout, i.e. running via `go run ./cmd/lazydeck` inside the repo).
func New() (*Client, error) {
	if dir := os.Getenv("LAZYDECK_PYTHON_DIR"); dir != "" {
		return &Client{PythonDir: dir, Timeout: 60 * time.Second}, nil
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "python")
		if isPythonDir(candidate) {
			return &Client{PythonDir: candidate, Timeout: 60 * time.Second}, nil
		}
	}

	// Dev layout: running via `go run ./cmd/lazydeck` inside the repo.
	// runtime.Caller(0) resolves to this source file's compile-time path,
	// so we can walk internal/client/client.go -> repo root -> python/.
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Join(filepath.Dir(thisFile), "..", "..", "python")
		if isPythonDir(candidate) {
			return &Client{PythonDir: candidate, Timeout: 60 * time.Second}, nil
		}
	}

	return nil, fmt.Errorf("could not locate the python/ directory; set LAZYDECK_PYTHON_DIR")
}

func isPythonDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cli.py"))
	return err == nil
}

// run invokes `uv run --project PythonDir python cli.py <args...>` and
// decodes the JSON envelope it prints on stdout.
func (c *Client) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	full := append([]string{"run", "--project", c.PythonDir, "python", "cli.py"}, args...)
	cmd := exec.CommandContext(ctx, "uv", full...)
	cmd.Dir = c.PythonDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	return parseEnvelope(stdout.Bytes(), stderr.Bytes(), runErr)
}

// parseEnvelope decodes cli.py's {"ok":bool,"data":any,"error":string}
// JSON envelope from raw stdout/stderr, translating subprocess failures and
// malformed output into clear errors instead of panicking or silently
// returning zero values. Split out from run() so it can be unit tested
// without invoking a real `uv`/subprocess.
func parseEnvelope(stdout, stderr []byte, runErr error) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &env); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("uv run failed: %w\nstderr: %s", runErr, stderr)
		}
		return nil, fmt.Errorf("could not parse cli.py output: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !env.OK {
		if env.Error == "" {
			return nil, fmt.Errorf("cli.py reported failure with no error message")
		}
		return nil, &CLIError{Kind: env.ErrorKind, Message: env.Error}
	}
	return env.Data, nil
}

// ConnectionInfo resolves the login/address the devkit client would use,
// plus the local devkit SSH private key path, so callers (the TUI) can open
// a real interactive `ssh` session without going through paramiko.
type ConnectionInfo struct {
	Address string `json:"address"`
	Login   string `json:"login"`
	KeyPath string `json:"key_path"`
}

func (c *Client) ConnectionInfo(ctx context.Context, machine, login string) (*ConnectionInfo, error) {
	args := []string{"connection-info", "--machine", machine}
	if login != "" {
		args = append(args, "--login", login)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var info ConnectionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Register pairs this workstation's SSH key with the given machine.
func (c *Client) Register(ctx context.Context, machine string) error {
	_, err := c.run(ctx, "register", "--machine", machine)
	return err
}

// DiscoveredDevice is one devkit found via mDNS/Bonjour browsing.
type DiscoveredDevice struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// Discover browses `_steamos-devkit._tcp.local.` for the given duration and
// returns whatever devkits announced themselves on the LAN in that window.
// Useful for finding a Steam Deck's address without knowing it ahead of
// time, e.g. right after joining the same Wi-Fi network.
func (c *Client) Discover(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	seconds := timeout.Seconds()
	discoverer := *c
	if buffer := timeout + 15*time.Second; buffer > discoverer.Timeout {
		discoverer.Timeout = buffer
	}
	data, err := discoverer.run(ctx, "discover", "--timeout", fmt.Sprintf("%.1f", seconds))
	if err != nil {
		return nil, err
	}
	var found []DiscoveredDevice
	if err := json.Unmarshal(data, &found); err != nil {
		return nil, err
	}
	return found, nil
}

// Status is the parsed output of `steamos-get-status --json` on the devkit.
type Status struct {
	Raw map[string]any
}

func (c *Client) Status(ctx context.Context, machine, login string) (*Status, error) {
	args := []string{"status", "--machine", machine}
	if login != "" {
		args = append(args, "--login", login)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &Status{Raw: m}, nil
}

// ListGames returns the games currently installed on the target devkit.
func (c *Client) ListGames(ctx context.Context, machine, login string) ([]any, error) {
	args := []string{"list-games", "--machine", machine}
	if login != "" {
		args = append(args, "--login", login)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var games []any
	if err := json.Unmarshal(data, &games); err != nil {
		return nil, err
	}
	return games, nil
}

// Deploy rsyncs a local build directory to the devkit and registers it as a
// launchable Steam shortcut named gameID.
func (c *Client) Deploy(ctx context.Context, machine, login, gameID, directory string, deleteExtraneous bool) error {
	args := []string{"deploy", "--machine", machine, "--name", gameID, "--directory", directory}
	if login != "" {
		args = append(args, "--login", login)
	}
	if deleteExtraneous {
		args = append(args, "--delete-extraneous")
	}
	_, err := c.run(ctx, args...)
	return err
}

// Delete removes a previously deployed title from the devkit.
func (c *Client) Delete(ctx context.Context, machine, login, gameID string) error {
	args := []string{"delete", "--machine", machine, "--name", gameID}
	if login != "" {
		args = append(args, "--login", login)
	}
	_, err := c.run(ctx, args...)
	return err
}

// SyncLogs pulls a title's logs down into a local directory.
func (c *Client) SyncLogs(ctx context.Context, machine, login, gameID, directory string) error {
	args := []string{"sync-logs", "--machine", machine, "--name", gameID, "--directory", directory}
	if login != "" {
		args = append(args, "--login", login)
	}
	_, err := c.run(ctx, args...)
	return err
}

// RunCommand executes a single non-interactive command over SSH on the devkit.
type CommandResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitStatus int    `json:"exit_status"`
}

func (c *Client) RunCommand(ctx context.Context, machine, login, remoteCmd string) (*CommandResult, error) {
	args := []string{"run", "--machine", machine, "--cmd", remoteCmd}
	if login != "" {
		args = append(args, "--login", login)
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var res CommandResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
