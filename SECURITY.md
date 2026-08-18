# Security policy

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository. Do not open a
public issue containing exploit details, private hostnames, addresses, SSH keys,
or device logs. Include the affected version, host platform, impact, and a
minimal reproduction. Security reports are handled on a best-effort basis; no
formal response-time SLA is offered.

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
