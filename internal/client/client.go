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
	"strings"
	"time"
)

// Client drives the headless Python CLI for one lazydeck process.
type Client struct {
	PythonDir string // directory containing pyproject.toml + cli.py
	UVPath    string // resolved uv executable
	// Timeout is a fallback deadline applied only when a caller invokes an
	// operation with a context that has no deadline of its own. Callers with
	// operation-appropriate deadlines (the TUI sets e.g. 10m for deploy, 20s
	// for status) are respected as-is and never clamped to this value.
	Timeout time.Duration
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
	uvPath, err := findUV()
	if err != nil {
		return nil, fmt.Errorf("uv is required to run the bundled Python bridge: %w", err)
	}

	if dir := os.Getenv("LAZYDECK_PYTHON_DIR"); dir != "" {
		if !isPythonDir(dir) {
			return nil, fmt.Errorf("LAZYDECK_PYTHON_DIR=%q does not contain cli.py, pyproject.toml, and uv.lock", dir)
		}
		return &Client{PythonDir: dir, UVPath: uvPath, Timeout: 60 * time.Second}, nil
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil {
		executables := []string{exe}
		if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil && resolved != exe {
			executables = append(executables, resolved)
		}
		for _, executable := range executables {
			binDir := filepath.Dir(executable)
			candidates = append(candidates,
				filepath.Join(binDir, "python"),
				filepath.Join(binDir, "..", "share", "lazydeck", "python"),
				filepath.Join(binDir, "..", "libexec", "python"),
			)
		}
	}

	if dataDir := os.Getenv("XDG_DATA_HOME"); dataDir != "" {
		candidates = append(candidates, filepath.Join(dataDir, "lazydeck", "python"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "share", "lazydeck", "python"),
			filepath.Join(home, "Library", "Application Support", "lazydeck", "python"),
		)
	}

	if _, thisFile, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(thisFile), "..", "..", "python"))
	}

	checked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		checked = append(checked, candidate)
		if isPythonDir(candidate) {
			return &Client{PythonDir: candidate, UVPath: uvPath, Timeout: 60 * time.Second}, nil
		}
	}

	return nil, fmt.Errorf("could not locate a complete Python runtime (checked %s); set LAZYDECK_PYTHON_DIR", strings.Join(checked, ", "))
}

func findUV() (string, error) {
	if path := os.Getenv("LAZYDECK_UV"); path != "" {
		if isExecutable(path) {
			return path, nil
		}
		return "", fmt.Errorf("LAZYDECK_UV=%q is not an executable file", path)
	}
	if path, err := exec.LookPath("uv"); err == nil {
		return path, nil
	}
	if exe, err := os.Executable(); err == nil {
		executables := []string{exe}
		if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil && resolved != exe {
			executables = append(executables, resolved)
		}
		for _, executable := range executables {
			binDir := filepath.Dir(executable)
			for _, candidate := range []string{
				filepath.Join(binDir, "uv"),
				filepath.Join(binDir, "..", "libexec", "lazydeck", "uv"),
				filepath.Join(binDir, "..", "libexec", "uv"),
			} {
				if isExecutable(candidate) {
					return filepath.Clean(candidate), nil
				}
			}
		}
	}
	return "", exec.ErrNotFound
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func isPythonDir(dir string) bool {
	for _, name := range []string{"cli.py", "pyproject.toml", "uv.lock"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// run invokes `uv run --project PythonDir python cli.py <args...>` and
// decodes the JSON envelope it prints on stdout.
// Preview renders the exact command line a call with these args would
// invoke, without running anything. Modeled on lazydocker's practice of
// always showing the real underlying command it's about to run rather than
// only a human-friendly paraphrase — useful for trust/debugging when an
// operation (like delete) touches real hardware over SSH.
func (c *Client) Preview(args ...string) string {
	full := append([]string{"run", "--project", c.PythonDir, "python", "cli.py"}, args...)
	return "uv " + strings.Join(full, " ")
}

func (c *Client) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	ctx, cancel := c.withFallbackTimeout(ctx)
	defer cancel()

	full := append([]string{"run", "--project", c.PythonDir, "python", "cli.py"}, args...)
	cmd := exec.CommandContext(ctx, c.UVPath, full...)
	cmd.Dir = c.PythonDir
	cmd.Env = os.Environ()
	if os.Getenv("UV_PROJECT_ENVIRONMENT") == "" {
		if cacheDir, err := os.UserCacheDir(); err == nil {
			cmd.Env = append(cmd.Env, "UV_PROJECT_ENVIRONMENT="+filepath.Join(cacheDir, "lazydeck", "python"))
		}
	}
	// uv spawns python, which in turn spawns ssh/rsync; the default
	// CommandContext behavior only kills uv on cancel, orphaning those
	// descendants. configureCancellation puts the child in its own process
	// group and kills the whole group with a bounded WaitDelay backstop.
	configureCancellation(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	return parseEnvelope(stdout.Bytes(), stderr.Bytes(), runErr)
}

// withFallbackTimeout respects the caller's deadline: operations like deploy
// legitimately run for minutes, so run must not clamp every call to c.Timeout.
// It only imposes c.Timeout when the caller supplied a context with no
// deadline of its own (and c.Timeout > 0); otherwise it returns ctx unchanged
// with a no-op cancel.
func (c *Client) withFallbackTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline || c.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.Timeout)
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
	Address        string `json:"address"`
	Login          string `json:"login"`
	KeyPath        string `json:"key_path"`
	KnownHostsPath string `json:"known_hosts_path"`
	StrictHostKeys bool   `json:"strict_host_keys"`
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
//
// mDNS/multicast socket setup can fail transiently (e.g. a network
// interface still coming up right after joining Wi-Fi), so a real error
// (as opposed to a clean "found nothing") is retried once after a short
// backoff before being surfaced to the caller.
func (c *Client) Discover(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	seconds := timeout.Seconds()
	discoverer := *c
	if buffer := timeout + 15*time.Second; buffer > discoverer.Timeout {
		discoverer.Timeout = buffer
	}
	runner := func() (json.RawMessage, error) {
		return discoverer.run(ctx, "discover", "--timeout", fmt.Sprintf("%.1f", seconds))
	}
	return discoverWithRetry(ctx, runner, 2, time.Second)
}

// discoverWithRetry retries runner up to maxAttempts times (with a delay
// between attempts) when it returns an error, and parses the resulting JSON
// on success. Split out from Discover so the retry/backoff behavior is
// unit-testable with a fake runner, without invoking a real subprocess.
func discoverWithRetry(ctx context.Context, runner func() (json.RawMessage, error), maxAttempts int, delay time.Duration) ([]DiscoveredDevice, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		data, err := runner()
		if err != nil {
			lastErr = err
			if attempt < maxAttempts {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
				continue
			}
			return nil, fmt.Errorf("discover failed after %d attempts: %w", maxAttempts, lastErr)
		}
		var found []DiscoveredDevice
		if err := json.Unmarshal(data, &found); err != nil {
			return nil, err
		}
		return found, nil
	}
	return nil, lastErr
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
// launchable Steam shortcut named gameID. argv, if non-empty, is the
// command-line the shortcut launches with (see python/cli.py's cmd_deploy
// --argv); without it, steam-client-create-shortcut has nothing to launch
// and the deploy fails once it reaches that step, even though the rsync
// itself succeeds.
func (c *Client) Deploy(ctx context.Context, machine, login, gameID, directory string, deleteExtraneous bool, argv []string) error {
	args := []string{"deploy", "--machine", machine, "--name", gameID, "--directory", directory}
	if login != "" {
		args = append(args, "--login", login)
	}
	if deleteExtraneous {
		args = append(args, "--delete-extraneous")
	}
	if len(argv) > 0 {
		args = append(args, "--argv")
		args = append(args, argv...)
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
