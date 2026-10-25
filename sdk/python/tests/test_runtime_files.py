"""Generic runtime-file permissions preserve authorization and no-follow IO."""
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from contracts.grants import inspect_grant, permits, safe_path
from host.broker import Broker
from host.files import observe


class RuntimeFiles(unittest.TestCase):
    def test_unrelated_runtime_files_require_all_three_exact_grants(self):
        path = "/run/example-provider/status"
        grant = {"operation": "files.read", "paths": [path], "roots": [], "max_bytes": 64}
        inspect_grant(grant)
        args = {"path": path, "max_bytes": 64}
        self.assertTrue(permits(grant, "files.read", args))
        self.assertFalse(permits(grant, "files.read", {**args, "path": path + ".secret"}))
        request = {"sequence": 1, "operation": "files.read", "arguments": args}
        result = (0, json.dumps({"raw_bytes": 2, "value": {"text": "ok", "truncated": False}}).encode(), b"")
        for denied in range(3):
            layers = [[grant], [grant], [grant]]
            layers[denied] = []
            with patch("host.broker.capture") as capture, self.assertRaises(RuntimeError):
                Broker(*layers, None, {}).handle(request, 10)
            capture.assert_not_called()
        with patch("host.broker.capture", return_value=result):
            self.assertEqual(Broker([grant], [grant], [grant], None, {}).handle(request, 10)["value"]["text"], "ok")
        with patch("host.broker.capture") as capture, self.assertRaises(RuntimeError):
            Broker([grant], [grant], [grant], None, {}, protected_paths=["/run/example-provider"]).handle(request, 10)
        capture.assert_not_called()

    def test_runtime_roots_and_protected_state_are_denied(self):
        for path in ("/run", "/run/credentials/service/key", "/run/secrets/key", "/run/user/1000/key",
                     "/run/tinywarden-agent/secret", "/run/tinywarden/secret", "/var/run/provider/status"):
            with self.subTest(path=path):
                self.assertFalse(safe_path(path))
        self.assertTrue(safe_path("/run/unrelated/status"))
        self.assertFalse(safe_path("/run/unrelated", True))

    def test_actual_runtime_file_access_rejects_links_directories_and_special_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            provider = root / "run/provider"
            provider.mkdir(parents=True)
            file = provider / "status"
            file.write_text("ready")
            original_open = os.open

            def rooted_open(path, flags, *args, **kwargs):
                return original_open(directory if path == "/" else path, flags, *args, **kwargs)

            with patch("host.files.os.open", side_effect=rooted_open):
                args = {"path": "/run/provider/status", "max_bytes": 64}
                self.assertEqual(observe("files.read", args)["text"], "ready")
                file.unlink()
                file.symlink_to(provider / "target")
                with self.assertRaises(OSError):
                    observe("files.read", args)
                file.unlink()
                os.mkfifo(file)
                with self.assertRaises(RuntimeError):
                    observe("files.read", args)
                file.unlink()
                file.mkdir()
                with self.assertRaises(RuntimeError):
                    observe("files.stat", args)


if __name__ == "__main__":
    unittest.main()
