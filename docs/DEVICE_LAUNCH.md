# Launching deployed titles

LazyDeck intentionally does **not** expose remote launch or stop operations.
After deployment, LazyDeck registers the title with the Steam client using the
supported SteamOS devkit protocol. Start and stop the title from the Steam UI
on the Steam Deck or Steam Machine.

The protocol's supported helper scripts include registration, upload, listing,
deletion, status, pairing-related setup, and log collection. It has no remote
launch or stop operation. Implementing one with arbitrary SSH commands such as
`steam -applaunch` or `pkill` would be an unsupported Steam-client integration:
it would need to persist generated shortcut app IDs, could change without
notice across SteamOS updates, and a stop command could terminate the wrong
process.

Accordingly, `/v1/capabilities` advertises `launch` and `stop` as `false`,
and their stable API routes return `501 unsupported`. Engine integrations must
respect those flags rather than presenting controls that cannot work
reliably. This policy can be revisited only if Valve adds supported devkit
primitives that can be validated against real hardware.

## Known caveat: avoid `-` in `game_id` for real deploys

On real hardware, a `-` in `game_id` reliably makes the *remote* Steam
client reject shortcut registration during `deploy` with a generic
`missing/invalid arguments` script error, regardless of `argv` — this was
reproduced with several dash-containing ids and went away once the dash
was removed. The failure happens inside Valve's own on-device devkit-utils
protocol handler (`steam-client-create-shortcut`, installed on the Steam
Deck/Steam Machine itself, not part of this repository), which builds an
unencoded `create-shortcut?...&gameid=...` command string from the raw
`game_id` and hands it to the Steam client over a local IPC pipe. Until
that's confirmed fixed on Valve's side, `deploy` rejects any `game_id`
containing `-` locally, before it ever reaches SSH or the device, with a
clear `invalid-input` error pointing back here (`internal/server/handlers.go`'s
`validateDeployGameID`, `python/cli.py`'s `cmd_deploy`). `/v1`'s general
`game_id` pattern (`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`) still allows `-`
for `list-games`/`delete`/`sync-logs`, which don't go through
`create-shortcut` and aren't affected — only `deploy` enforces the
stricter `[A-Za-z0-9._]` rule. If you already have a device with a
dash-containing shortcut from before this check existed, `delete` it and
redeploy under a dash-free name.

## Investigated: `steam://` IPC pipe as a launch/stop primitive (not viable)

Valve's own on-device devkit-utils (`devkit_utils.execute_steam_client_command`
in `~/.local/share/steamos-devkit/client/devkit-utils/devkit_utils/__init__.py`
on the Steam Deck/Steam Machine itself) writes raw `steam://...` commands to a
local IPC pipe (`~/.steam/steam.pipe`), scoped by a session token read from
`~/.steam/steam.token`, in the form:

```
devkit-1 steam://devkit-1/<session_token>/<cmd>
```

`steam-client-create-shortcut` — the tool this project already relies on for
`deploy` — uses exactly this channel to send
`create-shortcut?response=...&gameid=...`. In principle the same pipe could
carry other `steam://` verbs, such as `steam://rungameid/<appid>` to launch a
title, giving `launch_game`/`stop_game` a real implementation.

This was investigated and is **not viable** to ship as a supported feature:

- The pipe protocol is undocumented and closed-source on Steam's end. Valve's
  own comment describing the `create-shortcut` hack over this pipe literally
  calls it "a hack" — there is no guarantee any other verb (`rungameid` or a
  stop/quit equivalent) is accepted by the `devkit-1` session-token-scoped
  handler, as opposed to the normal `steam://` URI handler.
- There is no supported devkit protocol primitive for remote launch/stop at
  all (see `python/vendor/devkit_client/__init__.py`); this would be layering
  an unsupported, reverse-engineered integration on top of Valve's own
  internal tooling, with the same risks called out above for `-applaunch`/
  `pkill`: it could change or break without notice across SteamOS updates,
  and a malformed or unsupported command written to the pipe risks
  destabilizing the Steam client or gamescope session on real hardware, with
  no clean rollback path once a bad command is written.
- Validating this safely requires careful, deliberate prototyping directly
  against real Steam Deck/Machine hardware, which is out of scope for this
  change.

Accordingly, `launch_game`/`stop_game` remain permanent `501 unsupported`
stubs (see `internal/server/handlers.go`'s `handleLaunch`/`handleStop`), and
`/v1/capabilities` continues to advertise `launch`/`stop` as `false`. This
finding is recorded here so future contributors don't have to rediscover it;
revisiting this would require a maintainer with real hardware access to
prototype the pipe commands directly, observe the Steam client's behavior,
and have a clear abort/rollback plan before any code ships.
