import argparse
import contextlib
import io
import tempfile
import unittest
from pathlib import Path
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


if __name__ == "__main__":
    unittest.main()
