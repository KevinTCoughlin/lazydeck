import argparse
import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import cli


class DeployTests(unittest.TestCase):
    def test_deploy_passes_iterable_filter_args(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                machine="steamdeck.local",
                login="deck",
                name="Gridlock",
                directory=directory,
                delete_extraneous=True,
                argv=["gridlock.x86_64"],
            )

            with (
                mock.patch.object(cli.dk, "new_or_ensure_game") as deploy,
                contextlib.redirect_stdout(io.StringIO()),
            ):
                cli.cmd_deploy(args)

            namespace = deploy.call_args.args[0]
            self.assertEqual(namespace.filter_args, [])
            self.assertEqual(namespace.argv, ["gridlock.x86_64"])
            self.assertEqual(namespace.directory, str(Path(directory)))
            self.assertEqual(namespace.env_vars, {})
            self.assertEqual(namespace.force_appid, "")

    def test_argv_parses_flag_like_values_via_real_parser(self):
        """Regression test for a PR #30 review comment: --argv used
        nargs="*", so argparse stopped consuming as soon as it saw a token
        that looked like an option (e.g. "--fullscreen") and errored out on
        it as unrecognized instead of treating it as an argv value. This
        drives the *real* argparse parser (not a hand-built Namespace, like
        the test above) to prove dash-prefixed argv entries now parse
        correctly. --argv must stay the last flag on the command line:
        REMAINDER captures every token after it verbatim, so anything after
        --argv is never re-parsed as its own flag."""
        with tempfile.TemporaryDirectory() as directory:
            parser = cli.build_parser()
            parsed = parser.parse_args(
                [
                    "deploy",
                    "--machine",
                    "steamdeck.local",
                    "--name",
                    "Gridlock",
                    "--directory",
                    directory,
                    "--argv",
                    "gridlock.x86_64",
                    "--fullscreen",
                    "-w",
                    "1920",
                ]
            )
            self.assertEqual(parsed.argv, ["gridlock.x86_64", "--fullscreen", "-w", "1920"])


class DeleteTests(unittest.TestCase):
    def test_delete_maps_title_to_vendor_gameid(self):
        args = argparse.Namespace(
            machine="steamdeck.local",
            login="deck",
            name="GridLock",
        )

        with (
            mock.patch.object(cli.dk, "delete_title") as delete,
            contextlib.redirect_stdout(io.StringIO()),
        ):
            cli.cmd_delete(args)

        namespace = delete.call_args.args[0]
        self.assertEqual(namespace.gameid, "GridLock")
        self.assertFalse(namespace.delete_all)
        self.assertFalse(namespace.reset_steam_client)


class ConnectionInfoTests(unittest.TestCase):
    def test_connection_info_exposes_dedicated_host_key_policy(self):
        args = argparse.Namespace(machine="steamdeck.local", login="deck")
        machine = SimpleNamespace(address="10.0.0.5", login="deck")
        with (
            mock.patch.object(cli.dk, "resolve_machine", return_value=machine),
            mock.patch.object(cli.dk, "ensure_devkit_key", return_value=(None, "/key", None)),
            mock.patch.object(cli.dk, "devkit_known_hosts_path", return_value="/known_hosts"),
            mock.patch.object(cli.dk, "ssh_strict_host_keys", return_value=True),
            contextlib.redirect_stdout(io.StringIO()) as output,
        ):
            cli.cmd_connection_info(args)

        payload = json.loads(output.getvalue())
        self.assertEqual(payload["data"]["known_hosts_path"], "/known_hosts")
        self.assertTrue(payload["data"]["strict_host_keys"])


class SyncLogsTests(unittest.TestCase):
    """Regression coverage for finding #1: cmd_sync_logs must hand the vendored
    devkit_client.sync_logs a namespace it actually understands. The vendor
    reads ``local_folder`` (not ``directory``) and honors ``login``; the old
    code set ``directory`` and dropped ``login``, so logs never synced and any
    login override was ignored."""

    def test_sync_logs_sets_local_folder_and_preserves_login(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                machine="steamdeck.local",
                login="gamer",
                name="Gridlock",
                directory=directory,
            )

            with (
                mock.patch.object(cli.dk, "sync_logs") as sync,
                contextlib.redirect_stdout(io.StringIO()),
            ):
                cli.cmd_sync_logs(args)

            namespace = sync.call_args.args[0]
            # The attribute the vendored API actually consumes.
            self.assertEqual(namespace.local_folder, str(Path(directory)))
            # login must survive so a per-call override reaches resolve_machine.
            self.assertEqual(namespace.login, "gamer")
            self.assertEqual(namespace.machine, "steamdeck.local")
            # The old, wrong attribute must not be what we rely on: the vendor
            # never reads ``directory``, so ensure we didn't only set that.
            self.assertTrue(hasattr(namespace, "local_folder"))

    def test_sync_logs_integration_uses_local_folder_and_login(self):
        """Drive the *real* vendored sync_logs with resolve_machine and the
        DevkitClient rsync mocked, proving the namespace attributes line up
        end-to-end (local_folder consumed, login forwarded)."""
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                machine="steamdeck.local",
                login="gamer",
                name="Gridlock",
                directory=directory,
            )

            resolved = SimpleNamespace(login="gamer", address="10.0.0.5")
            with (
                mock.patch.object(
                    cli.dk, "resolve_machine", return_value=resolved
                ) as resolve,
                mock.patch.object(cli.dk, "DevkitClient") as client_cls,
                contextlib.redirect_stdout(io.StringIO()),
            ):
                cli.cmd_sync_logs(args)

            # login must be forwarded to resolve_machine (it was dropped before).
            self.assertEqual(resolve.call_args.kwargs.get("login"), "gamer")

            client = client_cls.return_value
            # rsync_transfer should be invoked with local folders rooted at the
            # requested directory (steam_logs + minidump), proving local_folder
            # was honored rather than raising AttributeError on ``directory``.
            local_targets = [c.args[0] for c in client.rsync_transfer.call_args_list]
            self.assertIn(str(Path(directory) / "steam_logs"), local_targets)
            self.assertIn(str(Path(directory) / "minidump"), local_targets)


class GameNameValidationTests(unittest.TestCase):
    """Finding #5: game names cross the bridge into a remote shell command. We
    reject clearly unsafe/misparsed names early while keeping ordinary Steam
    shortcut names (spaces, Unicode, punctuation) valid."""

    def test_accepts_ordinary_names(self):
        for name in ["Gridlock", "My Game 2", "Café Deluxe", "Rock & Roll!", "游戏"]:
            self.assertEqual(cli.validate_game_name(name), name)

    def test_rejects_empty_or_whitespace(self):
        for name in ["", "   ", "\t"]:
            with self.assertRaises(ValueError):
                cli.validate_game_name(name)

    def test_rejects_control_characters(self):
        for name in ["bad\nname", "nul\x00byte", "tab\tname", "del\x7f"]:
            with self.assertRaises(ValueError):
                cli.validate_game_name(name)

    def test_rejects_leading_dash(self):
        with self.assertRaises(ValueError):
            cli.validate_game_name("--delete-all-titles")

    def test_rejects_overlong(self):
        with self.assertRaises(ValueError):
            cli.validate_game_name("x" * 256)

    def test_deploy_rejects_invalid_name_before_ssh(self):
        with tempfile.TemporaryDirectory() as directory:
            args = argparse.Namespace(
                machine="steamdeck.local",
                login="deck",
                name="bad\nname",
                directory=directory,
                delete_extraneous=False,
                argv=[],
            )
            with mock.patch.object(cli.dk, "new_or_ensure_game") as deploy:
                with self.assertRaises(ValueError):
                    cli.cmd_deploy(args)
            deploy.assert_not_called()

    def test_deploy_rejects_embedded_dash_before_ssh(self):
        """Real-hardware testing found that a '-' anywhere in the name (not
        just a leading one) makes the remote Steam client's
        create-shortcut protocol fail with a generic "missing/invalid
        arguments" error. deploy rejects it locally with a clear message
        instead of round-tripping to the device to fail opaquely. This is
        deploy-specific: list/delete/sync-logs don't go through
        create-shortcut, so they keep allowing '-' (see
        test_delete_rejects_invalid_name_before_ssh's "-rf", which is
        rejected for the unrelated leading-dash reason, not this one)."""
        with tempfile.TemporaryDirectory() as directory:
            for name in ["smoke-test", "lazydeck-mcp-smoketest", "my-game"]:
                args = argparse.Namespace(
                    machine="steamdeck.local",
                    login="deck",
                    name=name,
                    directory=directory,
                    delete_extraneous=False,
                    argv=[],
                )
                with mock.patch.object(cli.dk, "new_or_ensure_game") as deploy:
                    with self.assertRaises(ValueError):
                        cli.cmd_deploy(args)
                deploy.assert_not_called()

    def test_delete_rejects_invalid_name_before_ssh(self):
        args = argparse.Namespace(
            machine="steamdeck.local",
            login="deck",
            name="-rf",
        )
        with mock.patch.object(cli.dk, "delete_title") as delete:
            with self.assertRaises(ValueError):
                cli.cmd_delete(args)
        delete.assert_not_called()


if __name__ == "__main__":
    unittest.main()
