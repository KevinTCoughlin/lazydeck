# Security policy

## Reporting a vulnerability

Use GitHub's
[private vulnerability reporting](https://github.com/KevinTCoughlin/lazydeck/security/advisories/new)
for this repository. Do not open a
public issue containing exploit details, private hostnames, addresses, SSH keys,
or device logs. Include the affected version, host platform, impact, and a
minimal reproduction. Security reports are handled on a best-effort basis; no
formal response-time SLA is offered. Fixes are disclosed through a GitHub
Security Advisory once a patched release is out, crediting the reporter unless
they ask otherwise.

## Supported versions

Security fixes target the latest release and `main`. Older releases are not
maintained.

## Steam devkit LAN trust model

SteamOS devkit pairing occurs over the local network and does not provide an
authenticated SSH host-key exchange. LazyDeck therefore keeps a dedicated
`known_hosts` file under the steamos-devkit user config directory and records
keys on first use. This is separate from the user's normal OpenSSH host database.

By default, an unknown key is accepted and recorded. A changed key produces a
prominent warning but remains backward-compatible with re-imaged devices and
reused DHCP addresses. Set `LAZYDECK_SSH_STRICT=1` to reject changed keys while
still allowing first-use enrollment. Verify a changed key out of band before
continuing, and only pair on a trusted LAN.

The vendored protocol can deploy files and run commands with the paired
device's user privileges. Treat discovered devices, config files, custom
commands, and release artifacts accordingly.

## `lazydeck serve` credentials and audit log

`lazydeck serve` listens on loopback only. It accepts two kinds of bearer
credential: the per-launch session token in its `0600` connection file, which
can do everything, and long-lived API keys from `lazydeck api-key create`,
which can do only what their scopes allow. API keys are stored as SHA-256
digests, never in plain text, and are compared in constant time; revoke one
with `lazydeck api-key revoke`. If the key store can't be read, every API key
is rejected. `serve` refuses to start if it can't open its append-only audit
log, which records mutating requests, scope denials, rejected API keys, and
job outcomes, and never credentials. See the README's "CI, API keys, and the
audit log" section.
