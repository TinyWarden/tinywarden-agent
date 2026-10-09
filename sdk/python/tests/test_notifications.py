"""Static Details contracts: limits, types, formatting and old-runtime compatibility."""
import copy
import json
import sys
import unittest
from pathlib import Path
ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from notifications import inspect_notifications
from packages import load


class NotificationAdmission(unittest.TestCase):
    def setUp(self):
        self.metadata = load(ROOT / "skills/official/disk-local", official=True)
        self.value = copy.deepcopy(self.metadata["notifications"])

    def inspect(self, value):
        return inspect_notifications(json.dumps(value).encode(), self.metadata["catalog"], self.metadata["schemas"]["settings"])

    def test_official_definitions_and_independent_package(self):
        for p in (ROOT / "skills/official").iterdir():
            self.assertEqual(load(p, official=True)["notifications"]["format"], 1)
        m = load(ROOT / "skills/examples/memory-pressure")
        m["catalog"]["notify.community"] = {"text": "Local use is {used}%.", "parameters": {"used": "number"}}
        reason = next(iter(m["catalog"]))
        value = {"format": 1, "rules": [{"reason": reason, "states": ["warning"], "message_key": "notify.community",
            "parameters": {"used": {"source": {"fact": "used", "type": "number"}, "marks": ["bold", "italic", "underline"]}}}]}
        self.assertEqual(inspect_notifications(json.dumps(value).encode(), m["catalog"], m["schemas"]["settings"]), value)

    def test_closed_rule_and_binding_and_duplicate_coverage(self):
        for mutate in [lambda v: v.update(html="bad"), lambda v: v.update(format=True),
                       lambda v: v["rules"].append(v["rules"][0]),
                       lambda v: v["rules"][0].update(states=["unknown"]),
                       lambda v: v["rules"][0].update(marks=["bold", "bold"]),
                       lambda v: v["rules"][0]["parameters"]["used"].update(precision=True),
                       lambda v: v["rules"][0]["parameters"]["used"].update(source={"setting": "absent"}),
                       lambda v: v["rules"][0]["parameters"]["mount"].update(source={"fact": "path", "type": "number"}),
                       lambda v: v["rules"][0]["parameters"]["used"].update(html="bad")]:
            value = copy.deepcopy(self.value); mutate(value)
            with self.assertRaises(ValueError): self.inspect(value)

    def test_plural_type_precision_and_catalog_validation(self):
        value = self.value["rules"][0]["parameters"]["used"]
        value["plural"] = {"one_key": "name", "other_key": "name"}
        with self.assertRaises(ValueError): self.inspect(self.value)
        del value["plural"]
        for text in ("Visit https://example.test", "javascript:bad", "Read www.example.test", "bad\nline", "bad\u202evalue"):
            self.metadata["catalog"]["notify.disk.warning"]["text"] = text
            with self.assertRaises(ValueError): self.inspect(self.value)

    def test_payload_bounds_and_duplicate_json(self):
        for data in (b" " * 16385, b'{"format":1,"format":1,"rules":[]}'):
            with self.assertRaises(ValueError): inspect_notifications(data, self.metadata["catalog"], self.metadata["schemas"]["settings"])
        self.value["rules"] = []
        with self.assertRaises(ValueError): self.inspect(self.value)
        self.value["rules"] = [self.metadata["notifications"]["rules"][0]] * 33
        with self.assertRaises(ValueError): self.inspect(self.value)


if __name__ == "__main__": unittest.main()
