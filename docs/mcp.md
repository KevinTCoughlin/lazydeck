# lazydeck MCP server

`lazydeck mcp` exposes lazydeck's devkit fleet operations as a [Model
Context Protocol](https://modelcontextprotocol.io) (MCP) server, so LLM
agents (Claude Desktop, VS Code Copilot, and other MCP-capable clients) can
discover, inspect, and (opt-in) deploy to Steam Deck/Steam Machine devkits
the same way a human would through the TUI or an editor integration.

It is built on the official
[`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
and speaks MCP over stdio — the transport MCP-capable clients use to spawn
and talk to a local server as a subprocess.

## How it fits together

`lazydeck mcp` is a **third client** of the same versioned, loopback-only
HTTP+SSE API (`api/openapi.yaml`) that the Godot and Unity editor
integrations already consume — it does not reimplement any devkit logic
itself. Concretely, on startup it:

1. Looks for an already-running `lazydeck serve` via the connection file
   (`$XDG_RUNTIME_DIR/lazydeck/serve.json`, same discovery mechanism the
   Godot/Unity plugins use) and reuses it if it answers `GET /v1/health`.
2. Otherwise, auto-starts `lazydeck serve` itself (same binary, `serve`
   subcommand) and waits for it to come up — mirroring the Godot plugin's
   `server_launcher.gd`.
3. Registers MCP tools that call the `/v1` API over HTTP, translating
   responses (including polling async deploy/log-sync jobs to completion)
   into MCP tool results.

## Usage

Point an MCP-capable client at the `lazydeck` binary with the `mcp`
subcommand as its stdio server command, for example in a Claude
Desktop/VS Code MCP server config:

```json
{
  "mcpServers": {
    "lazydeck": {
      "command": "lazydeck",
      "args": ["mcp"]
    }
  }
}
```

Or run it directly to see what tools it exposes:

```console
$ lazydeck mcp
```

### Flags and environment

| Flag/env | Effect |
| --- | --- |
| `--allow-mutations` | Also register tools that change device/job state (see below). Off by default. |
| `--fixture` | If `lazydeck mcp` needs to auto-start `lazydeck serve`, start it with `--fixture` (the in-memory fake backend), instead of the real uv/Python/SSH bridge. Useful for trying the MCP server out without real hardware. |
| `LAZYDECK_AUTOSTART=0` | Disable auto-starting `lazydeck serve`; `lazydeck mcp` fails fast instead if none is running. Same variable the Godot plugin honors. |
| `LAZYDECK_BIN=/path/to/lazydeck` | Executable to auto-start as `lazydeck serve`. Defaults to this same running binary. |

## Tools

**Read-only (always registered):**

| Tool | Wraps |
| --- | --- |
| `health` | `GET /v1/health` |
| `get_capabilities` | `GET /v1/capabilities` |
| `list_devices` | `GET /v1/devices` |
| `discover_devices` | `POST /v1/devices/discover` |
| `device_status` | `GET /v1/devices/{id}/status` |
| `list_games` | `GET /v1/devices/{id}/games` |
| `get_job` | `GET /v1/jobs/{id}` |

**Mutating (only registered with `--allow-mutations`):**

| Tool | Wraps |
| --- | --- |
| `pair_device` | `POST /v1/devices/{id}/pair` |
| `deploy` | `POST /v1/devices/{id}/deployments` (blocks up to 11 minutes polling the job to completion, then returns its final snapshot; if it times out first, the job id is still returned so `get_job` can keep polling it). Accepts an optional `argv` array setting the command-line the resulting Steam shortcut launches with; without it the shortcut has nothing to launch and the job fails once rsync finishes. **Avoid `-` in `game_id` for real hardware** — see caveat below. |
| `sync_logs` | `POST /v1/devices/{id}/logs/sync` (same polling behavior, up to 3 minutes) |
| `cancel_job` | `DELETE /v1/jobs/{id}` |
| `launch_game` / `stop_game` | `POST /v1/devices/{id}/games/{gameId}/launch` / `.../stop` — currently always return `"unsupported"`; see [`docs/DEVICE_LAUNCH.md`](DEVICE_LAUNCH.md). |

Mutating tools are opt-in on purpose: an LLM agent calling `deploy` or
`pair_device` is a materially different trust model than a human clicking
a button in an editor. Start with the read-only default and enable
`--allow-mutations` deliberately once you're comfortable with what an
agent in your setup might do with those tools.

## Validation

`lazydeck mcp --fixture` has been run end-to-end on Fedora Linux: a real
MCP client (the official Go SDK's `CommandTransport`) spawned the actual
built `lazydeck` binary as a subprocess, which auto-started `lazydeck
serve --fixture`, listed all 13 tools, and successfully called `health`,
`list_devices`, `discover_devices`, `get_capabilities`, and `deploy`
(polling a fixture job through to `succeeded`) over real stdio and
loopback HTTP.

`lazydeck mcp` (without `--fixture`) has also been validated against a
real Steam Machine devkit on the LAN, both read-only and mutating: real
mDNS `discover_devices`, `device_status`, and `list_games` returned live
SteamOS telemetry and title data; `deploy` (with `argv` set) rsync'd files
and successfully registered a launchable Steam shortcut end-to-end;
`sync_logs` pulled real logs/minidumps down; and `launch_game`/`stop_game`
correctly returned their by-design `"unsupported"` error against a real
devkit. Pairing (`pair_device`) was exercised earlier against the same
device (a re-pair against an already-paired machine correctly reports the
underlying `unreachable`/403 from the pairing script, since pairing isn't
idempotent).

**Caveat found during this validation: avoid `-` in `game_id` for real
deploys.** `/v1`'s own `game_id` pattern (and this tool's schema) permits
letters, digits, `.`, `_`, and `-`, but on real hardware a `-` in the
`game_id` makes the *remote* Steam client reject shortcut registration
with a generic `missing/invalid arguments` script error — reproduced
consistently regardless of `argv`, only going away once the `-` was
removed from the id. This happens in Valve's own on-device devkit-utils
protocol handler (outside this repository), not in lazydeck's request
validation, so it isn't something lazydeck can fix directly; stick to
`[A-Za-z0-9._]` in `game_id` until Valve's tooling is confirmed to accept
`-` reliably.
