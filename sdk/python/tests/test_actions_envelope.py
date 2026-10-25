"""Exact byte identities and phase scope cannot be widened by JSON decoding."""
import copy
import hashlib
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from actions.envelope import invocation

FIXTURE = Path(__file__).parent / "fixtures/skill-actions-v1.json"


class ActionEnvelope(unittest.TestCase):
    def setUp(self):
        self.value = json.loads(FIXTURE.read_text())["invocation"]

    def test_exact_invocation_and_cross_language_number_bytes(self):
        self.assertEqual(invocation(self.value)["input"], {})
        self.value["input_json"] = '{"number":1.0}'
        self.value["binding"]["input_sha256"] = hashlib.sha256(self.value["input_json"].encode()).hexdigest()
        self.assertEqual(invocation(self.value)["input"], {"number": 1.0})
        self.value["input_json"] = '{"number":1}'
        with self.assertRaises(ValueError):
            invocation(self.value)

    def test_changed_unknown_scope_and_authority_are_rejected(self):
        cases = [
            lambda v: v.update(schema_version=True),
            lambda v: v.update(extra="ignored"),
            lambda v: v["binding"].update(command="run"),
            lambda v: v["binding"].update(permission_revision="0"),
            lambda v: v["binding"].update(action_version=True),
            lambda v: v["binding"].update(content_sha256="missing"),
            lambda v: v["binding"]["origin"].update(owner_id=""),
            lambda v: v["scope"].update(generation=True),
            lambda v: v.update(plan_json="{}"),
            lambda v: v.update(receipt_json="{}"),
            lambda v: v.update(input_json='{"one":1,"one":2}'),
        ]
        for change in cases:
            value = copy.deepcopy(self.value)
            change(value)
            with self.subTest(value=value), self.assertRaises(ValueError):
                invocation(value)

    def test_apply_requires_exact_plan_bytes_and_job_start(self):
        self.value.update(phase="apply", plan_json="{}", plan_sha256=hashlib.sha256(b'{}').hexdigest())
        self.value["start"] = {"job_id": self.value["job_id"],
                               "attempt_id": "00000005-1111-4111-8111-111111111111",
                               "boot_id": "00000006-1111-4111-8111-111111111111", "not_after_ms": 100}
        invocation(self.value)
        self.value["start"]["job_id"] = self.value["start"]["attempt_id"]
        with self.assertRaises(ValueError):
            invocation(self.value)
        self.value["start"]["job_id"] = self.value["job_id"]
        self.value["plan_json"] = "{ }"
        with self.assertRaises(ValueError):
            invocation(self.value)


if __name__ == "__main__":
    unittest.main()
