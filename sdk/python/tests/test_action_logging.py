"""Diagnostics stay bounded, inert and independent of action authority."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from actions.logging import ActionLogger, NAME, MAX_ENTRIES, failure_code
from actions.guest import Context


class ActionLogging(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.fd = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY)
        self.catalog = {"step": {"text": "Checked {count} items.", "parameters": {"count": "number"}}}
        self.log = ActionLogger(self.fd, "prepare", self.catalog)

    def tearDown(self):
        os.close(self.fd)
        self.temp.cleanup()

    def entries(self):
        return [json.loads(line) for line in (self.root / NAME).read_bytes().splitlines()]

    def test_catalogue_values_survive_phase_changes_without_execution_fields(self):
        self.assertTrue(self.log.log({"key": "step", "params": {"count": 1.5}}))
        ActionLogger(self.fd, "verify", self.catalog).diagnostic("phase_completed")
        self.assertEqual([r["sequence"] for r in self.entries()], [1, 2])
        self.assertEqual([r["phase"] for r in self.entries()], ["prepare", "verify"])
        self.assertEqual(set(self.entries()[0]), {"sequence", "phase", "level", "message"})

    def test_public_context_accepts_documented_message_keyword(self):
        request = {"binding": {}, "scope": {}, "job_id": "synthetic", "phase": "prepare", "start": None, "plan_sha256": None}
        action = {"descriptor": {"execution_class": "host_admin"}}
        context = Context(request, lambda value: None, action, {}, self.fd, self.root, self.catalog)
        self.assertTrue(context.log(message={"key": "step", "params": {"count": 2}}, level="warning"))
        self.assertEqual(self.entries()[0]["level"], "warning")

    def test_invalid_messages_and_unavailable_storage_do_not_raise(self):
        self.assertFalse(self.log.log({"key": "unknown", "params": {}}))
        self.assertFalse(self.log.log({"key": "step", "params": {"count": float("nan")}}))
        os.close(self.fd)
        self.fd = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY)
        self.assertFalse(ActionLogger(-1, "prepare", self.catalog).diagnostic("phase_started"))

    def test_fifo_symlink_and_multiple_links_are_refused_without_blocking(self):
        target = self.root / NAME
        os.mkfifo(target, 0o600)
        self.assertFalse(self.log.diagnostic("phase_started"))
        target.unlink()
        private = self.root / "unrelated"
        private.write_text("private")
        target.symlink_to(private)
        self.assertFalse(self.log.diagnostic("phase_started"))
        self.assertEqual(private.read_text(), "private")
        target.unlink()
        os.link(private, target)
        self.assertFalse(self.log.diagnostic("phase_started"))

    def test_spam_is_capped_and_reports_truncation(self):
        for _ in range(MAX_ENTRIES + 20):
            self.log.diagnostic("phase_started")
        entries = self.entries()
        self.assertEqual(len(entries), MAX_ENTRIES)
        self.assertEqual(entries[-1]["code"], "log_truncated")

    def test_partial_last_write_can_resume_without_overwriting_complete_entries(self):
        self.log.diagnostic("phase_started")
        with (self.root / NAME).open("ab") as out:
            out.write(b'{"partial":')
        self.assertTrue(self.log.diagnostic("phase_completed"))
        self.assertEqual([r["sequence"] for r in self.entries()], [1, 2])

    def test_raw_exception_text_and_custom_errors_never_become_diagnostics(self):
        self.assertEqual(failure_code(ValueError("action_schema_value")), "action_schema_value")
        self.assertEqual(failure_code(ValueError("credential=private")), "unexpected_exception")
        self.assertEqual(failure_code(ValueError(["secret"])), "unexpected_exception")
        self.assertEqual(failure_code(RuntimeError("private")), "unexpected_exception")

    def test_public_output_retains_inert_text_and_splits_utf8_with_exact_limits(self):
        request = {"binding": {}, "scope": {}, "job_id": "synthetic", "phase": "apply", "start": None, "plan_sha256": None}
        context = Context(request, lambda value: None, {"descriptor": {"execution_class": "host_admin"}}, {}, self.fd, self.root, self.catalog)
        text = "<script>alert('literal')</script>\n" + "測試 😀\t" * 400
        self.assertTrue(context.output(text, stream="stderr"))
        entries = self.entries()
        self.assertTrue(all(len(json.dumps(r, ensure_ascii=False, separators=(",", ":")).encode()) <= 1023 for r in entries))
        self.assertEqual("".join(r["output"]["text"] for r in entries), text)
        self.assertTrue(all(r["output"]["stream"] == "stderr" for r in entries))

    def test_output_redacts_common_credentials_and_removes_terminal_controls(self):
        text = "\x1b[31mE: https://user:pass@example.test/ failed\x1b[0m\nAuthorization: Bearer abc123\npassword='two words' token=secret123\x00\u202e"
        self.assertTrue(self.log.output(text))
        result = "".join(r["output"]["text"] for r in self.entries())
        for secret in ["user:pass", "abc123", "two words", "secret123", "\x1b", "\x00", "\u202e"]:
            self.assertNotIn(secret, result)
        self.assertIn("E: https://[redacted]@example.test/ failed", result)

    def test_output_rejects_invalid_values_without_changing_the_action(self):
        self.assertFalse(self.log.output(b"binary"))
        self.assertFalse(self.log.output(""))
        self.assertFalse(self.log.output("text", stream="execute"))
        self.assertFalse(self.log.output("text", stream=[]))
        self.assertFalse(ActionLogger(-1, "apply", {}).output("unavailable"))

    def test_successful_and_failed_native_exit_levels_are_distinct(self):
        self.assertTrue(self.log.output("0", "exit"))
        self.assertTrue(self.log.output("100", "exit"))
        self.assertEqual([r["level"] for r in self.entries()], ["info", "error"])
        self.assertEqual([r["output"]["text"] for r in self.entries()], ["[exit 0]\n", "[exit 100]\n"])
