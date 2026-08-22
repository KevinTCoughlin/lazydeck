package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kevintcoughlin/lazydeck/internal/mcpapi"
)

// jobPollInterval matches the ~1s cadence the Godot/Unity integrations
// already poll job progress at.
const jobPollInterval = 1 * time.Second

// deployToolTimeout/syncLogsToolTimeout bound how long the deploy/sync_logs
// tools block waiting for the job to finish before returning control to
// the MCP client with the job still running; these match the server's own
// per-operation deadlines (internal/server/handlers.go's deployTimeout/
// logsSyncTimeout) plus a little headroom for the poll cadence above, so a
// tool call that hits its own timeout is reporting "still running", not
// racing the server's harder deadline.
const (
	deployToolTimeout   = 11 * time.Minute
	syncLogsToolTimeout = 3 * time.Minute
)

// toolAPIError converts an *mcpapi.APIError (or other error) into a
// CallToolResult with IsError set, so a failed devkit operation is
// reported back to the calling agent as tool-call output it can reason
// about, rather than a protocol-level error that aborts the exchange.
func toolAPIError(err error) (*mcp.CallToolResult, any, error) {
	var apiErr *mcpapi.APIError
	msg := err.Error()
	if errors.As(err, &apiErr) {
		msg = fmt.Sprintf("%s: %s", apiErr.Kind, apiErr.Message)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

// toolJSONResult renders v (typically a map[string]any or mcpapi.Job) as
// the tool's textual result, matching how the /v1 API itself returns JSON.
func toolJSONResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encoding tool result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, v, nil
}

// registerReadOnlyTools registers the tools available regardless of
// --allow-mutations: they only ever read devkit/job state.
func registerReadOnlyTools(s *mcp.Server, cli *mcpapi.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "health",
		Description: "Check that lazydeck serve is reachable and report its API version.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := cli.Health(ctx)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_capabilities",
		Description: "Report which devkit operations this lazydeck serve version supports (see GET /v1/capabilities).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := cli.Capabilities(ctx)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_devices",
		Description: "List the devkits configured in devices.toml.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		devices, err := cli.ListDevices(ctx)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(map[string]any{"devices": devices})
	})

	type discoverArgs struct {
		TimeoutSeconds float64 `json:"timeout_seconds,omitempty" jsonschema:"how long to browse the LAN for devkits, in seconds (default 5, max 300)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_devices",
		Description: "Browse the LAN for devkits announcing themselves over mDNS. Does not require a devices.toml entry.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args discoverArgs) (*mcp.CallToolResult, any, error) {
		timeout := args.TimeoutSeconds
		if timeout <= 0 {
			timeout = 5
		}
		devices, err := cli.Discover(ctx, timeout)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(map[string]any{"devices": devices})
	})

	type deviceArgs struct {
		DeviceID string `json:"device_id" jsonschema:"the configured device id, from list_devices"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_status",
		Description: "Fetch a configured device's current steamos-get-status output.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args deviceArgs) (*mcp.CallToolResult, any, error) {
		out, err := cli.Status(ctx, args.DeviceID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_games",
		Description: "List games currently installed/deployed on a configured device.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args deviceArgs) (*mcp.CallToolResult, any, error) {
		games, err := cli.ListGames(ctx, args.DeviceID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(map[string]any{"games": games})
	})

	type jobArgs struct {
		JobID string `json:"job_id" jsonschema:"the job id returned by deploy or sync_logs"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_job",
		Description: "Fetch a job's current state (queued/running/succeeded/failed/cancelled).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args jobArgs) (*mcp.CallToolResult, any, error) {
		job, err := cli.GetJob(ctx, args.JobID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(map[string]any{"job": job})
	})
}

// registerMutatingTools registers tools that change device or job state.
// Only called when --allow-mutations is set: an LLM-driven agent calling
// deploy/pair/sync-logs is a materially different trust model than a human
// clicking a button in an editor, so these are opt-in rather than on by
// default (see docs/mcp.md).
func registerMutatingTools(s *mcp.Server, cli *mcpapi.Client) {
	type pairArgs struct {
		DeviceID string `json:"device_id" jsonschema:"the configured device id to pair this workstation's SSH key with"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pair_device",
		Description: "Pair this workstation's SSH key with a configured devkit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args pairArgs) (*mcp.CallToolResult, any, error) {
		out, err := cli.Pair(ctx, args.DeviceID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})

	type deployArgs struct {
		DeviceID         string `json:"device_id" jsonschema:"the configured device id to deploy to"`
		GameID           string `json:"game_id" jsonschema:"identifier for the deployed title; letters, digits, '.', '_', '-' only"`
		Directory        string `json:"directory" jsonschema:"absolute path to the exported build on this workstation"`
		DeleteExtraneous bool   `json:"delete_extraneous,omitempty" jsonschema:"mirror the remote directory exactly (rsync --delete) instead of only adding/updating files"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "deploy",
		Description: "Deploy a local build directory to a devkit and wait for it to finish (up to " +
			deployToolTimeout.String() + "). Returns the job id regardless, so a slow deploy can still be polled with get_job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args deployArgs) (*mcp.CallToolResult, any, error) {
		job, err := cli.Deploy(ctx, args.DeviceID, args.GameID, args.Directory, args.DeleteExtraneous)
		if err != nil {
			return toolAPIError(err)
		}
		return waitAndResult(ctx, cli, job, deployToolTimeout)
	})

	type syncLogsArgs struct {
		DeviceID  string `json:"device_id" jsonschema:"the configured device id to pull logs from"`
		Directory string `json:"directory" jsonschema:"absolute local directory to sync logs into"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "sync_logs",
		Description: "Pull a devkit's Steam logs and minidumps into a local directory and wait for it to finish (up to " +
			syncLogsToolTimeout.String() + "). Returns the job id regardless, so a slow sync can still be polled with get_job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args syncLogsArgs) (*mcp.CallToolResult, any, error) {
		job, err := cli.SyncLogs(ctx, args.DeviceID, "", args.Directory)
		if err != nil {
			return toolAPIError(err)
		}
		return waitAndResult(ctx, cli, job, syncLogsToolTimeout)
	})

	type jobArgs struct {
		JobID string `json:"job_id" jsonschema:"the job id to cancel"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "cancel_job",
		Description: "Request cancellation of a queued or running job. A no-op if it has already finished.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args jobArgs) (*mcp.CallToolResult, any, error) {
		job, err := cli.CancelJob(ctx, args.JobID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(map[string]any{"job": job})
	})

	type gameArgs struct {
		DeviceID string `json:"device_id" jsonschema:"the configured device id"`
		GameID   string `json:"game_id" jsonschema:"the deployed title's game id"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "launch_game",
		Description: "Launch a deployed title. Currently always returns \"unsupported\": the SteamOS devkit protocol " +
			"has no remote launch primitive yet; start the title from the device's Steam UI. Check get_capabilities first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args gameArgs) (*mcp.CallToolResult, any, error) {
		out, err := cli.Launch(ctx, args.DeviceID, args.GameID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "stop_game",
		Description: "Stop a running title. Currently always returns \"unsupported\": the SteamOS devkit protocol " +
			"has no remote stop primitive yet; stop the title from the device's Steam UI. Check get_capabilities first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args gameArgs) (*mcp.CallToolResult, any, error) {
		out, err := cli.Stop(ctx, args.DeviceID, args.GameID)
		if err != nil {
			return toolAPIError(err)
		}
		return toolJSONResult(out)
	})
}

// waitAndResult blocks on cli.WaitForJob up to timeout, returning whatever
// snapshot it has (running or terminal) as the tool result: a timeout is
// reported as a still-running job, not a tool error, since the operation
// itself hasn't failed — the caller can keep polling with get_job.
func waitAndResult(ctx context.Context, cli *mcpapi.Client, job mcpapi.Job, timeout time.Duration) (*mcp.CallToolResult, any, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	final, err := cli.WaitForJob(waitCtx, job.ID, jobPollInterval)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return toolAPIError(err)
	}
	return toolJSONResult(map[string]any{"job": final})
}

// registerTools wires up read-only tools unconditionally and mutating
// tools only when allowMutations is true.
func registerTools(s *mcp.Server, cli *mcpapi.Client, allowMutations bool) {
	registerReadOnlyTools(s, cli)
	if allowMutations {
		registerMutatingTools(s, cli)
	}
}
