package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kevintcoughlin/lazydeck/internal/mcpapi"
)

// serverName/serverVersion identify this MCP server to connecting clients
// (Claude Desktop, VS Code Copilot, etc.). Version is set from main's own
// build metadata by Run's caller so `lazydeck mcp` reports the same
// version as `lazydeck version`.
const serverName = "lazydeck"

// Options configures Run.
type Options struct {
	// AllowMutations enables tools that change device or job state
	// (deploy, pair_device, sync_logs, cancel_job, launch_game,
	// stop_game). Off by default: see registerMutatingTools' doc comment.
	AllowMutations bool
	// Version is reported to connecting MCP clients as this server's
	// implementation version (typically main.version).
	Version string
	// FixtureBackend, when true, tells EnsureServing's auto-started
	// `lazydeck serve` to run against the in-memory fake backend
	// (`serve --fixture`) instead of a real devkit fleet — useful for
	// trying the MCP server out, and for this package's own tests,
	// without any real hardware.
	FixtureBackend bool
}

// Run discovers or starts `lazydeck serve` (see EnsureServing), builds an
// MCP server wrapping its /v1 API as tools, and serves it over stdio until
// ctx is cancelled or the client disconnects.
func Run(ctx context.Context, opts Options) error {
	info, err := EnsureServing(ctx, opts.FixtureBackend)
	if err != nil {
		return fmt.Errorf("connecting to lazydeck serve: %w", err)
	}

	cli := mcpapi.New(info.BaseURL, info.Token)
	if _, err := cli.Health(ctx); err != nil {
		return fmt.Errorf("lazydeck serve at %s did not respond to a final health check: %w", info.BaseURL, err)
	}

	impl := &mcp.Implementation{Name: serverName, Version: opts.Version}
	server := mcp.NewServer(impl, nil)
	registerTools(server, cli, opts.AllowMutations)

	return server.Run(ctx, &mcp.StdioTransport{})
}
