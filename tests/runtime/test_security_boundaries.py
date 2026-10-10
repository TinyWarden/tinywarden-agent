"""Regression checks for shared admission/output boundaries (no guest code)."""
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from grants import inspect_grant, permits
from outcomes import scalar, message, errors
from supervisor import response_bytes, admission_workspace
from packages import load
from fixtures import package


class SecurityBoundaries(unittest.TestCase):
    def test_service_scope_is_a_visible_list_and_never_an_option(self):
        grant = {"operation": "systemd.properties", "units": ["fstrim.service"],
                 "properties": ["Result"]}
        inspect_grant(grant)
        args = {"unit": "fstrim.service", "properties": ["Result"]}
        self.assertTrue(permits(grant, grant["operation"], args))
        for units in ({"fstrim.service": True}, ["-Hexample.service"], [None]):
            with self.subTest(units=units), self.assertRaises(ValueError):
                inspect_grant({**grant, "units": units})
            self.assertFalse(permits({**grant, "units": units}, grant["operation"],
                                     {**args, "unit": "-Hexample.service"}))
        import worker
        with patch.object(worker, "AttachedBudget"), patch.object(worker, "capture", return_value=(0, b"Result=success\n", b"")) as command:
            self.assertEqual(worker.run({"operation": grant["operation"], "arguments": args, "timeout": 1})["value"], {"Result": "success"})
            self.assertEqual(command.call_args.args[0][-2:], ["--", "fstrim.service"])

    def test_complete_response_budget_includes_identity_and_newline(self):
        result = "x" * (1024 * 1024 - 20)
        self.assertLessEqual(len(response_bytes({"result": result})), 1024 * 1024)
        self.assertEqual(json.loads(response_bytes({"result": result, "content_sha256": "a"*64})),
                         {"error": "output_exceeded"})
        self.assertEqual(json.loads(response_bytes({"result": {"ok": True}, "content_sha256": "a"*64}))["result"], {"ok": True})

    def test_timestamp_and_rendered_message_bounds(self):
        self.assertTrue(scalar("time", 0))
        self.assertTrue(scalar("time", 8640000000000000))
        self.assertFalse(scalar("time", 8640000000000001))
        self.assertTrue(scalar("duration", 8640000000000001))
        catalog = {"reason": {"text": "{value}"*9, "parameters": {"value": "string"}}}
        with self.assertRaises(ValueError):
            message({"key": "reason", "params": {"value": "x"*512}}, catalog)
        message({"key": "reason", "params": {"value": "ok"}}, catalog)
        with self.assertRaises(ValueError):
            errors([{"field": "x", "message": {"key": "reason", "params": {"value": "🙂"*512}}}]*64,
                   {"catalog": catalog, "schemas": {"settings": {"properties": {"x": {}}}}})

    def test_cadence_is_an_engine_invariant_and_unicode_names_are_bounded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = package(Path(tmp)/"skill")
            manifest = json.loads((root/"skill.json").read_text())
            for schema, value in (({"type": "boolean"}, True), ({"type": "integer", "minimum": 0, "maximum": 86400}, 0)):
                (root/"settings.schema.json").write_text(json.dumps({"type": "object", "properties": {"interval_seconds": schema}, "required": ["interval_seconds"], "additionalProperties": False}))
                manifest["defaults"] = {"interval_seconds": value}
                manifest["fields"] = {"interval_seconds": {"label_key": "name", "help_key": "description", "unit": "seconds", "order": 0}}
                (root/"skill.json").write_text(json.dumps(manifest))
                with self.assertRaises(ValueError): load(root)
            (root/"settings.schema.json").write_text(json.dumps({"type": "object", "properties": {}, "required": [], "additionalProperties": False}))
            manifest["defaults"] = {}; manifest["fields"] = {}
            (root/"skill.json").write_text(json.dumps(manifest))
            load(root)
            catalog = json.loads((root/"messages/en.json").read_text()); catalog["name"]["text"] = "🙂"*1100
            (root/"messages/en.json").write_text(json.dumps(catalog))
            with self.assertRaises(ValueError): load(root)

    def test_parent_removes_staging_after_child_failure_and_recovers_orphans(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = Path(tmp)/"store"; store.mkdir(mode=0o700)
            orphan = store/".admission-orphan"; orphan.mkdir(); (orphan/"data").write_bytes(b"x")
            with self.assertRaises(RuntimeError):
                with admission_workspace(store) as (workspace, _):
                    self.assertFalse(orphan.exists())
                    nested = workspace/"partial"; nested.mkdir(); (nested/"data").write_bytes(b"x")
                    nested.chmod(0o500)
                    raise RuntimeError("resource_exhausted")
            self.assertFalse(any(p.is_dir() for p in store.glob(".admission-*")))

    def test_parent_removes_workspace_after_sigkill(self):
        from capture import capture
        class Group:
            def enter(self): pass
            def exhausted(self): return False
        with tempfile.TemporaryDirectory() as tmp:
            store = Path(tmp)/"store"; store.mkdir(mode=0o700)
            with self.assertRaises(RuntimeError):
                with admission_workspace(store) as (workspace, lock_fd):
                    code, _, _ = capture([sys.executable, "-c",
                        "import os,sys,signal; from pathlib import Path; Path(sys.argv[1]).write_bytes(b'x'*65536); os.kill(os.getpid(),signal.SIGKILL)",
                        str(workspace/"partial")], b"", Group(), 2, pass_fds=(lock_fd,))
                    self.assertLess(code,0)
                    raise RuntimeError("package_rejected")
            self.assertFalse(any(p.is_dir() for p in store.glob(".admission-*")))


if __name__ == "__main__":
    unittest.main()
