"""Display admission rejects executable/unbounded descriptions without imports."""
import copy
import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from display import inspect_display
from packages import load


class DisplayAdmission(unittest.TestCase):
    def setUp(self):
        self.metadata = load(ROOT / "skills/examples/memory-pressure")
        self.display = copy.deepcopy(self.metadata["display"])
        self.display["format"] = 1
        for section in self.display["sections"]:
            section.pop("role", None)
        widgets = [widget for section in self.display["sections"] for widget in section["widgets"]]
        self.display["sections"] = [{**self.display["sections"][0], "widgets": widgets}]

    def inspect(self, value):
        return inspect_display(json.dumps(value).encode(), self.metadata["catalog"], self.metadata["schemas"]["settings"])

    def test_all_five_packages_are_static_and_valid(self):
        for directory in (ROOT / "skills/official").iterdir():
            self.assertIn(load(directory, official=True)["display"]["format"], (1, 2))
        self.assertEqual(self.inspect(self.display), self.display)

    def test_unknown_root_type_and_markup_rejected(self):
        for field, value in (("html", "<script>bad()</script>"), ("url", "https://example.test"), ("format", 3)):
            invalid = copy.deepcopy(self.display)
            invalid[field] = value
            with self.assertRaises(ValueError):
                self.inspect(invalid)
        self.display["sections"][0]["widgets"][0]["type"] = "html"
        with self.assertRaises(ValueError):
            self.inspect(self.display)

    def test_format_two_roles_and_closed_properties(self):
        value = copy.deepcopy(self.display)
        value["format"] = 2
        section = value["sections"][0]
        current, graphs = [], []
        for widget in section["widgets"]:
            widget.pop("default_window", None)
            (graphs if widget["type"] in ("line_chart", "sparkline") else current).append(widget)
        value["sections"] = [dict(id="current", role="current", title_key=section["title_key"], widgets=current),
                             dict(id="graph", role="graph", title_key=section["title_key"], widgets=graphs)]
        self.assertEqual(self.inspect(value), value)
        for role in ("details", "current", "unknown"):
            invalid = copy.deepcopy(value)
            invalid["sections"][1]["role"] = role
            with self.assertRaises(ValueError):
                self.inspect(invalid)
        for field, setting in (("collapsed", False), ("disclosure", "open")):
            invalid = copy.deepcopy(value)
            invalid["sections"][0][field] = setting
            with self.assertRaises(ValueError):
                self.inspect(invalid)
        invalid = copy.deepcopy(value)
        invalid["sections"][1]["widgets"][0]["default_window"] = "24h"
        with self.assertRaises(ValueError):
            self.inspect(invalid)
        invalid = copy.deepcopy(value)
        invalid["sections"][0]["role"] = "details"
        with self.assertRaises(ValueError):
            self.inspect(invalid)

    def test_broken_reference_and_label_rejected(self):
        for property, value in (("source", "absent"), ("title_key", "absent")):
            invalid = copy.deepcopy(self.display)
            invalid["sections"][0]["widgets"][0][property] = value
            with self.assertRaises(ValueError):
                self.inspect(invalid)

    def test_bad_units_and_boolean_precision_rejected(self):
        for declaration in ({"kind": "percent", "unit": "bytes"}, {"kind": "text", "unit": "percent"},
                            {"kind": "number", "precision": True}, {"kind": "text", "encoding": "javascript"}):
            self.display["sources"]["used"] = declaration
            with self.assertRaises(ValueError):
                self.inspect(self.display)

    def test_history_limit_and_row_escape_rejected(self):
        self.display["metrics"]["used_percent"]["max_series"] = 33
        with self.assertRaises(ValueError):
            self.inspect(self.display)
        self.display["metrics"]["used_percent"]["max_series"] = 1
        self.display["sections"][0]["widgets"][0]["columns"][1]["meter"]["min"] = {"fact": "memory", "column": "absent"}
        with self.assertRaises(ValueError):
            self.inspect(self.display)

    def test_duplicate_ids_and_unknown_widget_properties_rejected(self):
        self.display["sections"][0]["widgets"][1]["id"] = "memory_table"
        with self.assertRaises(ValueError):
            self.inspect(self.display)
        self.display["sections"][0]["widgets"][1]["id"] = "memory_trend"
        self.display["sections"][0]["widgets"][0]["onclick"] = "bad()"
        with self.assertRaises(ValueError):
            self.inspect(self.display)

    def test_disclosure_and_muted_enum_are_closed_and_optional(self):
        self.display["sections"][0]["disclosure"] = "open"
        self.assertEqual(self.inspect(self.display)["sections"][0]["disclosure"], "open")
        for invalid in ("sometimes", True, []):
            self.display["sections"][0]["disclosure"] = invalid
            with self.assertRaises(ValueError):
                self.inspect(self.display)
        self.display["sections"][0]["disclosure"] = "closed"
        self.display["sections"][0]["collapsed"] = False
        with self.assertRaises(ValueError):
            self.inspect(self.display)
        del self.display["sections"][0]["collapsed"]
        label = next(k for k, v in self.metadata["catalog"].items() if not v["parameters"])
        self.display["sources"]["certainty"] = {"kind": "text", "enum": {"unverified": label}, "muted_values": ["unverified"]}
        self.assertEqual(self.inspect(self.display), self.display)
        for invalid in ([], ["absent"], ["unverified", "unverified"], [False], "unverified"):
            self.display["sources"]["certainty"]["muted_values"] = invalid
            with self.assertRaises(ValueError):
                self.inspect(self.display)

    def test_oversized_and_duplicate_json_rejected(self):
        for data in (b" " * 65537, b'{"format":1,"format":1}'):
            with self.assertRaises(ValueError):
                inspect_display(data, self.metadata["catalog"], self.metadata["schemas"]["settings"])


if __name__ == "__main__":
    unittest.main()
