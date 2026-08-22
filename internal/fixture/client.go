// Package fixture implements a fake internal/client.Client for engine
// integration testing (Godot, Unity) and container-based CI, so those
// suites can exercise the full `lazydeck serve` HTTP+SSE surface --
// connect, list, discover, pair, deploy, poll, cancel, log-sync -- without
// installing uv/Python, spawning SSH/rsync, or having access to real Steam
// Deck/Steam Machine hardware. It implements the same method set
// internal/server's devkitClient interface expects, so `lazydeck serve
// --fixture` can substitute it for a real *client.Client with no server
// code changes.
//
// Behavior is controlled entirely through environment variables (see
// NewFromEnv) rather than a config file, so a CI script or container
// entrypoint can shape a scenario (a slow deploy, a failing log sync, a
// canned discover result) with a few `export` lines.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
)

// Client is a fake devkit backend: every method returns configured,
// deterministic data instead of shelling out to uv/Python/ssh/rsync. It
// implements the same method set as *client.Client (and internal/server's
// unexported devkitClient interface), so it is usable anywhere a real
// client is, purely by structural typing.
type Client struct {
	// DiscoverResult is returned by Discover; DiscoverErr, when non-nil,
	// is returned instead.
	DiscoverResult []client.DiscoveredDevice
	DiscoverErr    error

	// RegisterErr, when non-nil, is returned by Register.
	RegisterErr error

	// StatusResult is returned by Status; StatusErr, when non-nil, is
	// returned instead.
	StatusResult map[string]any
	StatusErr    error

	// GamesResult is returned by ListGames; GamesErr, when non-nil, is
	// returned instead.
	GamesResult []any
	GamesErr    error

	// DeployDelay simulates a real deploy's duration; DeployErr, when
	// non-nil, is returned after that delay (or immediately if
	// DeployDelay is 0).
	DeployDelay time.Duration
	DeployErr   error

	// SyncLogsDelay/SyncLogsErr mirror DeployDelay/DeployErr for SyncLogs.
	SyncLogsDelay time.Duration
	SyncLogsErr   error
}

// Discover returns the configured DiscoverResult/DiscoverErr, ignoring
// timeout: a fixture has no real LAN to scan, so it can't meaningfully
// simulate one taking time.
func (c *Client) Discover(_ context.Context, _ time.Duration) ([]client.DiscoveredDevice, error) {
	return c.DiscoverResult, c.DiscoverErr
}

// Register returns the configured RegisterErr (nil on success), ignoring
// machine: the fixture has no real SSH keypair to enroll.
func (c *Client) Register(_ context.Context, _ string) error {
	return c.RegisterErr
}

// Status returns the configured StatusResult/StatusErr.
func (c *Client) Status(_ context.Context, _, _ string) (*client.Status, error) {
	if c.StatusErr != nil {
		return nil, c.StatusErr
	}
	return &client.Status{Raw: c.StatusResult}, nil
}

// ListGames returns the configured GamesResult/GamesErr.
func (c *Client) ListGames(_ context.Context, _, _ string) ([]any, error) {
	return c.GamesResult, c.GamesErr
}

// Deploy waits DeployDelay (or returns early if ctx is cancelled first, so
// job cancellation is exercised the same way a real rsync/ssh cancellation
// would be) and then returns DeployErr.
func (c *Client) Deploy(ctx context.Context, _, _, _, _ string, _ bool, _ []string) error {
	return sleep(ctx, c.DeployDelay, c.DeployErr)
}

// SyncLogs mirrors Deploy's delay/cancellation/error behavior for the
// log-sync operation.
func (c *Client) SyncLogs(ctx context.Context, _, _, _, _ string) error {
	return sleep(ctx, c.SyncLogsDelay, c.SyncLogsErr)
}

// sleep waits for delay or ctx cancellation, whichever comes first, then
// returns err. A real Deploy/SyncLogs blocks on rsync/ssh for the whole
// operation and returns ctx.Err() when its subprocess is killed on
// cancellation; mirroring that here makes /v1/jobs/{id} cancellation
// observable in the same shape a real deploy job would produce.
func sleep(ctx context.Context, delay time.Duration, err error) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return err
		}
	}
	select {
	case <-time.After(delay):
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewFromEnv builds a Client from LAZYDECK_FIXTURE_* environment
// variables, so container entrypoints and CI scripts can shape a scenario
// declaratively instead of needing a Go program of their own:
//
//   - LAZYDECK_FIXTURE_STATUS         JSON object, default {"state":"ready"}
//   - LAZYDECK_FIXTURE_GAMES          JSON array, default []
//   - LAZYDECK_FIXTURE_DISCOVER       JSON array of {name,address,port}, default []
//   - LAZYDECK_FIXTURE_DEPLOY_DELAY   Go duration (e.g. "500ms"), default 0
//   - LAZYDECK_FIXTURE_DEPLOY_FAIL    "1"/"true" to make Deploy fail
//   - LAZYDECK_FIXTURE_LOGS_DELAY     Go duration, default 0
//   - LAZYDECK_FIXTURE_LOGS_FAIL      "1"/"true" to make SyncLogs fail
//
// Malformed JSON in any of these is a misconfigured fixture, not a runtime
// condition callers should have to handle, so NewFromEnv panics rather than
// silently falling back to an empty value that could mask a broken CI
// script.
func NewFromEnv() *Client {
	c := &Client{
		StatusResult: map[string]any{"state": "ready"},
	}

	if raw := os.Getenv("LAZYDECK_FIXTURE_STATUS"); raw != "" {
		var status map[string]any
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			panic(fmt.Sprintf("LAZYDECK_FIXTURE_STATUS is not valid JSON: %v", err))
		}
		c.StatusResult = status
	}

	if raw := os.Getenv("LAZYDECK_FIXTURE_GAMES"); raw != "" {
		var games []any
		if err := json.Unmarshal([]byte(raw), &games); err != nil {
			panic(fmt.Sprintf("LAZYDECK_FIXTURE_GAMES is not valid JSON: %v", err))
		}
		c.GamesResult = games
	}

	if raw := os.Getenv("LAZYDECK_FIXTURE_DISCOVER"); raw != "" {
		var discovered []client.DiscoveredDevice
		if err := json.Unmarshal([]byte(raw), &discovered); err != nil {
			panic(fmt.Sprintf("LAZYDECK_FIXTURE_DISCOVER is not valid JSON: %v", err))
		}
		c.DiscoverResult = discovered
	}

	c.DeployDelay = parseDurationEnv("LAZYDECK_FIXTURE_DEPLOY_DELAY")
	c.SyncLogsDelay = parseDurationEnv("LAZYDECK_FIXTURE_LOGS_DELAY")

	if parseBoolEnv("LAZYDECK_FIXTURE_DEPLOY_FAIL") {
		c.DeployErr = &client.CLIError{Kind: "unreachable", Message: "fixture: deploy configured to fail"}
	}
	if parseBoolEnv("LAZYDECK_FIXTURE_LOGS_FAIL") {
		c.SyncLogsErr = &client.CLIError{Kind: "unreachable", Message: "fixture: log sync configured to fail"}
	}

	return c
}

func parseDurationEnv(name string) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Sprintf("%s=%q is not a valid Go duration: %v", name, raw, err))
	}
	return d
}

func parseBoolEnv(name string) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return false
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		panic(fmt.Sprintf("%s=%q is not a valid bool: %v", name, raw, err))
	}
	return b
}
