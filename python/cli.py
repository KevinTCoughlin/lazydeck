#!/usr/bin/env python3
"""Headless CLI wrapper around Valve's steamos-devkit ``devkit_client`` library.

This exists because upstream only ships an interactive PySDL2/imgui GUI
(``devkit-gui.py``); there is no scriptable entrypoint. `devkit-tui` (the Go
TUI in the parent project) shells out to this script via ``uv run`` and
parses JSON on stdout, so it can drive pairing / deploy / status / logs for
several devkits (Steam Machine, Steam Deck, ...) without reimplementing the
SSH + rsync + mDNS pairing protocol.

Every subcommand prints a single JSON object to stdout:
    {"ok": true, "data": ...}
    {"ok": false, "error": "message"}
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from types import SimpleNamespace

VENDOR_DIR = Path(__file__).resolve().parent / "vendor"
sys.path.insert(0, str(VENDOR_DIR))

import devkit_client as dk  # noqa: E402  (path must be set up first)


class NullSignal:
    """Stand-in for the Qt/GUI cancel_signal expected by new_or_ensure_game."""

    def connect(self, _fn):
        return None


def _machine_args(machine: str, login: str | None) -> SimpleNamespace:
    return SimpleNamespace(
        machine=machine,
        login=login,
        machine_name_type=dk.MachineNameType.GUESS,
        http_port=dk.DEFAULT_DEVKIT_SERVICE_HTTP,
    )


def emit(ok: bool, data=None, error: str | None = None) -> None:
    payload = {"ok": ok}
    if ok:
        payload["data"] = data
    else:
        payload["error"] = error
    print(json.dumps(payload, default=str))


def cmd_register(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, None)
    dk.register(ns)
    emit(True, {"registered": args.machine})


def cmd_status(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, args.login)
    status = dk.steamos_get_status(ns)
    if status is None:
        raise RuntimeError("no response (is the machine paired and reachable?)")
    emit(True, status)


def cmd_connection_info(args: argparse.Namespace) -> None:
    """Resolve login/address + the devkit SSH private key path, so the Go
    side can open a real interactive `ssh` session itself (for a live
    remote shell) without reimplementing resolve_machine()."""
    ns = _machine_args(args.machine, args.login)
    machine = dk.resolve_machine(
        ns.machine,
        login=ns.login,
        need_login=True,
        need_devkit1=False,
        name_type=ns.machine_name_type,
        http_port=ns.http_port,
    )
    _, key_path, _ = dk.ensure_devkit_key()
    emit(True, {"address": machine.address, "login": machine.login, "key_path": key_path})


def cmd_list_games(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, args.login)
    games = dk.list_games(ns)
    emit(True, games)


def cmd_deploy(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, args.login)
    ns.name = args.name
    ns.directory = args.directory
    ns.delete_extraneous = args.delete_extraneous
    ns.skip_newer_files = False
    ns.verify_checksums = False
    ns.filter_args = None
    ns.steam_play_debug = dk.SteamPlayDebug.Disabled
    ns.argv = args.argv or []
    ns.clear_settings = False
    ns.settings_file = []
    ns.set_json = []
    ns.set = []
    ns.deps = None
    ns.cancel_signal = NullSignal()
    dk.new_or_ensure_game(ns)
    emit(True, {"deployed": args.name, "directory": args.directory})


def cmd_delete(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, args.login)
    ns.name = args.name
    dk.delete_title(ns)
    emit(True, {"deleted": args.name})


def cmd_sync_logs(args: argparse.Namespace) -> None:
    ns = _machine_args(args.machine, args.login)
    ns.name = args.name
    ns.directory = args.directory
    dk.sync_logs(ns)
    emit(True, {"synced_to": args.directory})


def cmd_shell_command(args: argparse.Namespace) -> None:
    """Run a single remote command over SSH and return its output (non-interactive)."""
    ns = _machine_args(args.machine, args.login)
    ssh = dk._open_ssh_for_args(ns)
    out_text, err_text, exit_status = dk._simple_ssh(ssh, args.cmd, silent=True)
    emit(True, {"stdout": out_text, "stderr": err_text, "exit_status": exit_status})


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)

    def add(name, func, needs_login=True):
        sp = sub.add_parser(name)
        sp.add_argument("--machine", required=True, help="hostname, IP, or mDNS service name")
        if needs_login:
            sp.add_argument("--login", default=None, help="remote username override")
        sp.set_defaults(func=func)
        return sp

    add("register", cmd_register, needs_login=False)
    add("status", cmd_status)
    add("connection-info", cmd_connection_info)
    add("list-games", cmd_list_games)

    sp = add("deploy", cmd_deploy)
    sp.add_argument("--name", required=True, help="gameid / shortcut name")
    sp.add_argument("--directory", required=True, help="local build directory to rsync up")
    sp.add_argument("--delete-extraneous", action="store_true")
    sp.add_argument("--argv", nargs="*", default=None)

    sp = add("delete", cmd_delete)
    sp.add_argument("--name", required=True)

    sp = add("sync-logs", cmd_sync_logs)
    sp.add_argument("--name", required=True)
    sp.add_argument("--directory", required=True, help="local directory to receive logs")

    sp = add("run", cmd_shell_command)
    sp.add_argument("--cmd", required=True, help="remote shell command to execute")

    return p


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    try:
        args.func(args)
    except Exception as exc:  # noqa: BLE001 - surfaced to caller as JSON
        emit(False, error=str(exc))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
