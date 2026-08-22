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
