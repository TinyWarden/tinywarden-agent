"""Diagnostics stay bounded, inert and independent of action authority."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from actions.logging import ActionLogger, NAME, failure_code
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
        for _ in range(150):
            self.log.diagnostic("phase_started")
        entries = self.entries()
        self.assertEqual(len(entries), 128)
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
