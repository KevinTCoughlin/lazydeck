// Package mcp implements `lazydeck mcp`: a Model Context Protocol server
// that exposes lazydeck's devkit fleet operations as MCP tools for LLM
// agents (Claude Desktop, VS Code Copilot, etc.), built on the official
// github.com/modelcontextprotocol/go-sdk.
//
// Like the Godot and Unity editor integrations, this package is a client of
// the existing /v1 HTTP+SSE API (internal/server, documented in
// api/openapi.yaml) rather than a reimplementation of the devkit protocol:
// it discovers (or, if necessary, starts) a `lazydeck serve` process the
// same way those integrations do, then wraps internal/mcpapi's typed HTTP
// client as MCP tools. See docs/mcp.md for the user-facing configuration
// story and the "not yet hardware-validated" caveat that applies to every
// lazydeck surface today.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/mcpapi"
	"github.com/kevintcoughlin/lazydeck/internal/server"
)

// healthCheckTimeout bounds a single "is this connection file's server
// actually alive" probe, kept short since it only needs to rule out a
// stale/crashed process before deciding whether to spawn a new one.
const healthCheckTimeout = 2 * time.Second

// spawnWaitTimeout bounds how long EnsureServing waits for a newly spawned
// `lazydeck serve` to write its connection file and answer /v1/health,
// generous enough to cover process startup and the Python venv/bridge
// warmup internal/client performs on first use.
const spawnWaitTimeout = 15 * time.Second

// spawnPollInterval is how often EnsureServing re-checks for the
// connection file/health after spawning `lazydeck serve`.
const spawnPollInterval = 200 * time.Millisecond

// autoStartEnvVar mirrors the Godot plugin's LAZYDECK_AUTOSTART override
// (integrations/godot/addons/lazydeck/api/server_launcher.gd) so disabling
// auto-start is one consistent knob across every lazydeck client.
const autoStartEnvVar = "LAZYDECK_AUTOSTART"

// binEnvVar mirrors the Godot plugin's LAZYDECK_BIN override, letting a
// user point auto-start at a non-PATH lazydeck binary.
const binEnvVar = "LAZYDECK_BIN"

// autoStartEnabled reports whether EnsureServing may spawn `lazydeck
// serve` itself. Defaults to enabled, matching the Godot plugin's default
// (ProjectSettings "lazydeck/server/autostart" defaults to true); an MCP
// client has no project-settings equivalent, so the environment variable
// is the only override here.
func autoStartEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(autoStartEnvVar)))
	return v != "0" && v != "false"
}

// executablePath returns the lazydeck binary to spawn for auto-start:
// LAZYDECK_BIN if set (matching the Godot plugin), else this same running
// binary's own path, since `lazydeck mcp` and `lazydeck serve` are the same
// executable — no separate install/PATH lookup is needed in the common
// case.
func executablePath() (string, error) {
	if bin := os.Getenv(binEnvVar); bin != "" {
		return bin, nil
	}
	return os.Executable()
}

// EnsureServing returns connection info for a running `lazydeck serve`
// instance, reusing one already on disk if it answers /v1/health, and
// otherwise spawning one (unless auto-start is disabled via
// LAZYDECK_AUTOSTART), mirroring
// integrations/godot/addons/lazydeck/api/server_launcher.gd's
// start_if_needed(): an existing-but-unhealthy connection file is
// deliberately left alone rather than overwritten, since a live process's
// diagnostic state would otherwise be lost. useFixture, if true and a
// spawn is needed, starts `lazydeck serve --fixture` (the in-memory fake
// backend) instead of a real devkit fleet.
func EnsureServing(ctx context.Context, useFixture bool) (server.ConnectionInfo, error) {
	path, err := server.ConnectionFilePath()
	if err != nil {
		return server.ConnectionInfo{}, fmt.Errorf("resolving connection file location: %w", err)
	}

	if info, err := server.ReadConnectionInfo(path); err == nil {
		if healthy(ctx, info) {
			return info, nil
		}
		return server.ConnectionInfo{}, fmt.Errorf(
			"found a connection file at %s but %s did not answer /v1/health; "+
				"if lazydeck serve crashed, remove that file and retry", path, info.BaseURL)
	}

	if !autoStartEnabled() {
		return server.ConnectionInfo{}, errors.New(
			"lazydeck serve is not running and auto-start is disabled (LAZYDECK_AUTOSTART=0); " +
				"start it yourself with `lazydeck serve`")
	}

	return spawnAndWait(ctx, path, useFixture)
}

// healthy reports whether info's BaseURL answers GET /v1/health within
// healthCheckTimeout.
func healthy(ctx context.Context, info server.ConnectionInfo) bool {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	cli := mcpapi.New(info.BaseURL, info.Token)
	_, err := cli.Health(ctx)
	return err == nil
}

// spawnAndWait starts `lazydeck serve` as a detached child process — it
// intentionally outlives this MCP server the same way the Godot plugin's
// spawned process outlives the editor session, since it may end up serving
// other clients too — then polls for the connection file to appear and
// answer /v1/health. If useFixture is true, it starts `lazydeck serve
// --fixture` instead of talking to real hardware.
func spawnAndWait(ctx context.Context, connPath string, useFixture bool) (server.ConnectionInfo, error) {
	bin, err := executablePath()
	if err != nil {
		return server.ConnectionInfo{}, fmt.Errorf("resolving lazydeck executable to auto-start: %w", err)
	}

	args := []string{"serve"}
	if useFixture {
		args = append(args, "--fixture")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return server.ConnectionInfo{}, fmt.Errorf("auto-starting %s serve: %w", bin, err)
	}
	// Deliberately not Wait()-ed: lazydeck serve is a long-running daemon,
	// not a subprocess whose exit this call should block on. Releasing it
	// avoids leaking a zombie-reaping goroutine responsibility onto a
	// caller that only wants to know once it's ready.
	_ = cmd.Process.Release()

	deadline := time.Now().Add(spawnWaitTimeout)
	for time.Now().Before(deadline) {
		if info, err := server.ReadConnectionInfo(connPath); err == nil && healthy(ctx, info) {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return server.ConnectionInfo{}, ctx.Err()
		case <-time.After(spawnPollInterval):
		}
	}
	return server.ConnectionInfo{}, fmt.Errorf(
		"auto-started %s serve but it did not become healthy within %s; check its logs", bin, spawnWaitTimeout)
}
