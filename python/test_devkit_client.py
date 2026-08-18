"""Regression tests for lazydeck's changes at the vendored devkit_client
boundary: remote-shell quoting of user-supplied game names (finding #5),
thread-safe discovery snapshots (finding #7), and the persistent
trust-on-first-use SSH host-key policy (SSH finding).

We import ``cli`` first: it inserts the vendored ``python/vendor`` directory
onto ``sys.path`` as a side effect, which is how the bridge itself loads the
library, so tests exercise exactly what ships."""

import os
import shlex
import socket
import tempfile
import threading
import unittest
from types import SimpleNamespace
from unittest import mock

import cli  # noqa: F401  (import side effect: puts vendor/ on sys.path)

dk = cli.dk
import paramiko  # noqa: E402  (only importable after cli sets sys.path)


class GameNameQuotingTests(unittest.TestCase):
    """Finding #5: user-supplied game names are interpolated into remote shell
    commands run over SSH. They must be shell-quoted so a name can never inject
    additional commands on the device."""

    def test_new_or_ensure_game_quotes_gameid(self):
        dangerous = "game; rm -rf ~"
        args = SimpleNamespace(
            name=dangerous,
            machine="deck",
            directory="/local/dir",
            delete_extraneous=False,
            skip_newer_files=False,
            verify_checksums=False,
            filter_args=[],
            steam_play_debug=dk.SteamPlayDebug.Disabled,
            cancel_signal=SimpleNamespace(connect=lambda fn: None),
            argv=[],
            env_vars={},
            force_appid="",
            deps=None,
        )
        ssh = mock.Mock()
        client = mock.Mock()
        machine = SimpleNamespace(address="10.0.0.5")

        def fake_ssh(_ssh, command, **_kwargs):
            if "steamos-prepare-upload" in command:
                return ('{"user": "deck", "directory": "/remote/dir"}', "", 0)
            # steam-client-create-shortcut path
            return ('{"success": "created"}', "", 0)

        with (
            mock.patch.object(
                dk, "_open_ssh_for_args_all", return_value=(ssh, client, machine)
            ),
            mock.patch.object(dk, "_simple_ssh", side_effect=fake_ssh) as simple,
        ):
            dk.new_or_ensure_game(args)

        remote_cmd = simple.call_args_list[0].args[1]
        self.assertIn("steamos-prepare-upload", remote_cmd)
        self.assertIn(shlex.quote(dangerous), remote_cmd)
        # The raw, unquoted injection must not survive into the command line.
        self.assertNotIn("--gameid game; rm", remote_cmd)

    def test_delete_title_quotes_gameid(self):
        dangerous = "title; reboot"
        args = SimpleNamespace(
            gameid=dangerous, delete_all=False, reset_steam_client=False
        )
        with (
            mock.patch.object(dk, "_open_ssh_for_args", return_value=mock.Mock()),
            mock.patch.object(dk, "_simple_ssh", return_value=("", "", 0)) as simple,
        ):
            dk.delete_title(args)

        remote_cmd = simple.call_args.args[1]
        self.assertIn(shlex.quote(dangerous), remote_cmd)
        self.assertNotIn("--delete-title title; reboot", remote_cmd)


class ServiceListenerSnapshotTests(unittest.TestCase):
    """Finding #7: discovery iterated the devkits dict while the zeroconf
    browser thread mutated it. snapshot_devkits() must resolve results
    atomically under the listener lock."""

    def setUp(self):
        dk.g_zeroconf_listener = None

    def tearDown(self):
        dk.g_zeroconf_listener = None

    def _info(self, ip, port):
        return SimpleNamespace(addresses=[socket.inet_aton(ip)], port=port)

    def test_snapshot_resolves_addresses(self):
        listener = dk.ServiceListener(mock.Mock())
        with listener._lock:
            listener.devkits["deck"] = self._info("10.0.0.5", 32000)
            listener.devkits["machine"] = self._info("10.0.0.6", 32001)
        snap = sorted(listener.snapshot_devkits())
        self.assertEqual(
            snap,
            [("deck", "10.0.0.5", 32000), ("machine", "10.0.0.6", 32001)],
        )

    def test_snapshot_survives_concurrent_mutation(self):
        listener = dk.ServiceListener(mock.Mock())
        info = self._info("10.0.0.5", 32000)
        stop = threading.Event()
        errors = []

        def mutate():
            i = 0
            try:
                while not stop.is_set():
                    # add_service/remove_service hold the same lock in real code.
                    with listener._lock:
                        listener.devkits[f"d{i % 64}"] = info
                        if i % 3 == 0 and listener.devkits:
                            listener.devkits.pop(next(iter(listener.devkits)), None)
                    i += 1
            except Exception as exc:  # pragma: no cover - failure path
                errors.append(exc)

        t = threading.Thread(target=mutate)
        t.start()
        try:
            for _ in range(3000):
                # Must never raise "dictionary changed size during iteration".
                listener.snapshot_devkits()
        finally:
            stop.set()
            t.join()
        self.assertEqual(errors, [])


class PersistentHostKeyPolicyTests(unittest.TestCase):
    """SSH finding: dedicated, persistent trust-on-first-use host key policy
    replacing the silent AutoAddPolicy."""

    @classmethod
    def setUpClass(cls):
        # ECDSA generation is fast; reuse a couple of distinct keys.
        cls.key_a = paramiko.ECDSAKey.generate()
        cls.key_b = paramiko.ECDSAKey.generate()

    def _policy(self, path, strict):
        return dk.PersistentHostKeyPolicy(path=path, strict=strict)

    def test_trust_on_first_use_records_and_accepts(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "known_hosts")
            policy = self._policy(path, strict=False)
            policy.missing_host_key(mock.Mock(), "deck.local", self.key_a)
            self.assertTrue(os.path.exists(path))
            saved = paramiko.HostKeys()
            saved.load(path)
            self.assertTrue(saved.check("deck.local", self.key_a))

    def test_matching_key_accepts(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "known_hosts")
            policy = self._policy(path, strict=False)
            policy.missing_host_key(mock.Mock(), "deck.local", self.key_a)
            # Same key again: must not raise.
            policy.missing_host_key(mock.Mock(), "deck.local", self.key_a)

    def test_mismatch_nonstrict_accepts_with_warning(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "known_hosts")
            policy = self._policy(path, strict=False)
            policy.missing_host_key(mock.Mock(), "deck.local", self.key_a)
            with self.assertLogs(dk.logger, level="WARNING") as logs:
                # Different key, non-strict: backward-compatible warn + accept.
                policy.missing_host_key(mock.Mock(), "deck.local", self.key_b)
            self.assertTrue(any("does not match" in m for m in logs.output))

    def test_mismatch_strict_raises(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "known_hosts")
            policy = self._policy(path, strict=True)
            policy.missing_host_key(mock.Mock(), "deck.local", self.key_a)
            with self.assertRaises(paramiko.SSHException):
                policy.missing_host_key(mock.Mock(), "deck.local", self.key_b)

    def test_ssh_strict_env_toggle(self):
        with mock.patch.dict(os.environ, {"LAZYDECK_SSH_STRICT": "1"}):
            self.assertTrue(dk.ssh_strict_host_keys())
        with mock.patch.dict(os.environ, {"LAZYDECK_SSH_STRICT": "on"}):
            self.assertTrue(dk.ssh_strict_host_keys())
        with mock.patch.dict(os.environ, {"LAZYDECK_SSH_STRICT": "off"}):
            self.assertFalse(dk.ssh_strict_host_keys())
        with mock.patch.dict(os.environ, {"LAZYDECK_SSH_STRICT": ""}):
            self.assertFalse(dk.ssh_strict_host_keys())


if __name__ == "__main__":
    unittest.main()
