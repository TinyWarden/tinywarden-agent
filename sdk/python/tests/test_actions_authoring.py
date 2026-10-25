"""Captured action fixtures validate values without importing privileged code."""
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from authoring.actions import run_action_cases
from packages.admission import load
from action_fixtures import action_package


class ActionCases(unittest.TestCase):
    def test_valid_invalid_and_wrong_action_fixtures(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = action_package(Path(temporary) / "skill")
            metadata = load(root)
            path = Path(temporary) / "cases.json"
            values = {"format": 1, "cases": [
                {"name": "valid", "action": "example.check.v1", "values": {"plan": {"complete": True}}, "valid": True},
                {"name": "wrong type", "action": "example.check.v1", "values": {"result": {"complete": 1}}, "valid": False},
            ]}
            path.write_text(json.dumps(values))
            result = run_action_cases(metadata, path)
            self.assertTrue(result["passed"])
            self.assertFalse(result["code_executed"])
            values["cases"][1]["valid"] = True
            path.write_text(json.dumps(values))
            self.assertFalse(run_action_cases(metadata, path)["passed"])
            values["cases"][0]["action"] = "missing.action"
            path.write_text(json.dumps(values))
            with self.assertRaises(ValueError):
                run_action_cases(metadata, path)


if __name__ == "__main__":
    unittest.main()
