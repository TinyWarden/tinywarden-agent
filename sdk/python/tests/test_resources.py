"""Resource fixtures also consumed by the control-plane TypeScript tests."""
import copy
import json
import sys
import unittest
from pathlib import Path
sys.path.insert(0, str(Path(__file__).parents[1] / "runtime"))
from contracts.resources import inventory, timeline, selection, inspect_resources, resource_key, LIMITS
from contracts.schema import inspect
from contracts.json_values import encode


class ResourcesTest(unittest.TestCase):
    def setUp(self):
        self.fixture = json.loads((Path(__file__).parent / "fixtures/contract.json").read_text())
        self.metadata = self.fixture["metadata"]
        self.context = {"settings": self.fixture["settings"], "now": 1000, "evidence_expires_at": 5000}

    def test_valid_contract_and_v1_settings_compatibility(self):
        inspect_resources(self.metadata["manifest"], self.metadata["schemas"], self.metadata["catalog"])
        inspect(self.metadata["schemas"]["settings"], settings=True, sdk=2)
        with self.assertRaises(ValueError):
            inspect(self.metadata["schemas"]["settings"], settings=True)
        inventory(self.fixture["inventory"], self.metadata)
        timeline(self.fixture["timeline"], self.context, self.metadata)

    def test_duplicate_missing_and_unselected_resources_rejected(self):
        bad = copy.deepcopy(self.fixture["inventory"])
        bad[0]["items"].append(bad[0]["items"][0])
        with self.assertRaises(ValueError):
            inventory(bad, self.metadata)
        for kind in ("missing", "duplicate", "unselected"):
            bad = copy.deepcopy(self.fixture["timeline"])
            if kind == "missing":
                bad[0]["subjects"].pop()
            elif kind == "duplicate":
                bad[0]["subjects"][1] = bad[0]["subjects"][0]
            else:
                bad[0]["subjects"][0]["key"] = "not-selected"
            with self.assertRaises(ValueError):
                timeline(bad, self.context, self.metadata)

    def test_utf8_identity_and_incomplete_inventory(self):
        self.assertTrue(resource_key("a" * 255))
        self.assertFalse(resource_key("a" * 256))
        self.assertFalse(resource_key("é" * 128))
        self.assertFalse(resource_key("a\nb"))
        self.assertFalse(resource_key("\ud800"))
        self.assertFalse(resource_key("\u0085"))
        self.assertTrue(resource_key("🚀" * 63))
        bad = copy.deepcopy(self.fixture["inventory"])
        bad[0]["complete"] = False
        with self.assertRaises(ValueError):
            inventory(bad, self.metadata)
        bad[0]["reason"] = {"key": "partial", "params": {}}
        inventory(bad, self.metadata)

    def test_selection_and_budget_edges(self):
        values = {"targets": [str(n) for n in range(64)]}
        self.assertEqual(len(selection(self.metadata, values)), 64)
        for targets in (["a", "a"], [str(n) for n in range(65)]):
            with self.assertRaises(ValueError):
                selection(self.metadata, {"targets": targets})
        for budget in LIMITS.values():
            if budget < 1024:
                continue
            encode("a" * (budget - 2), budget)
            with self.assertRaises(ValueError):
                encode("a" * (budget - 1), budget)
        # Largest independent lanes plus identity/framing stay below transport cap.
        self.assertLess(LIMITS["inventory"] + LIMITS["observation"] + 65536, 1048576)
        self.assertLess(2 * LIMITS["state"] + LIMITS["observation"] + 65536, LIMITS["context"])


if __name__ == "__main__":
    unittest.main()
