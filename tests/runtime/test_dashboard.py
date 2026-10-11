"""Static dashboard metadata cannot execute code or reduce source tables."""
import copy
import json
import sys
import unittest
from pathlib import Path
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from display import inspect_display


class Dashboard(unittest.TestCase):
    def setUp(self):
        root = ROOT / "skills/examples/memory-pressure"
        self.display = json.loads((root / "display.json").read_bytes())
        self.catalog = json.loads((root / "messages/en.json").read_bytes())
        self.settings = json.loads((root / "settings.schema.json").read_bytes())

    def inspect(self, value):
        return inspect_display(json.dumps(value).encode(), self.catalog, self.settings)

    def test_old_and_new_descriptors(self):
        self.inspect(self.display)
        self.display.pop("dashboard")
        self.inspect(self.display)

    def test_reject_unsafe_and_ambiguous_fields(self):
        original = self.display["dashboard"]["fields"][0]
        for change in ({"html": "<script/>"}, {"value": {"fact": "memory", "column": "used"}},
                       {"context": {"fact": "used"}}, {"value": {"fact": "unknown"}}):
            value = copy.deepcopy(self.display)
            value["dashboard"]["fields"][0].update(change)
            with self.assertRaises(ValueError): self.inspect(value)
        for fields in ([original, original], [dict(original, id="field" + str(n)) for n in range(9)]):
            value = copy.deepcopy(self.display)
            value["dashboard"]["fields"] = fields
            with self.assertRaises(ValueError): self.inspect(value)

    def test_all_official_declarations(self):
        for root in (ROOT / "skills/official").iterdir():
            if not root.is_dir(): continue
            self.catalog = json.loads((root / "messages/en.json").read_bytes())
            self.settings = json.loads((root / "settings.schema.json").read_bytes())
            self.inspect(json.loads((root / "display.json").read_bytes()))


if __name__ == "__main__": unittest.main()
