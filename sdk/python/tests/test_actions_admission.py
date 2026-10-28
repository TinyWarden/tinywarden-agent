"""Unknown privileges and executable metadata fail before any package import."""
import copy
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from actions.descriptor import canonical
from actions.schema import inspect, validate
from packages.admission import load
from action_fixtures import action_package, object_schema


class ActionAdmission(unittest.TestCase):
    def test_explicit_verification_policy_is_bound_and_old_descriptors_remain_unchanged(self):
        original = load(self.root)["actions"][0]
        self.assertNotIn("verification", original["descriptor"])
        for mode in ("local", "host_health"):
            self.manifest(lambda v: v["actions"][0].update(verification=mode))
            admitted = load(self.root)["actions"][0]
            self.assertEqual(admitted["descriptor"]["verification"], mode)
            self.assertNotEqual(admitted["descriptor_sha256"], original["descriptor_sha256"])
        for mode in ("none", "skip", None, 0, {}, []):
            self.manifest(lambda v: v["actions"][0].update(verification=mode))
            with self.assertRaises(ValueError):
                load(self.root)

    def test_action_schema_labels_are_data_and_numeric_enums_match_json_numbers(self):
        schema={"type":"number","minimum":0,"maximum":10,"enum":[1.0],"label_key":"amount"}
        inspect(schema)
        validate(schema,1)
        with self.assertRaises(ValueError):
            inspect({"type":"object","properties":{},"additionalProperties":False,"enum":[{}]})
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = action_package(Path(self.tmp.name) / "package")

    def manifest(self, mutate):
        path = self.root / "skill.json"
        data = json.loads(path.read_text())
        mutate(data)
        path.write_text(json.dumps(data))

    def test_static_admission_binds_descriptor_schemas_and_code(self):
        value = load(self.root)
        action = value["actions"][0]
        self.assertEqual(hashlib.sha256(canonical({k: action[k] for k in ("descriptor", "schemas")})).hexdigest(), action["descriptor_sha256"])
        # Both source files deliberately would fail if imported by admission.
        (self.root / "skill.py").write_text("raise RuntimeError('monitor imported')\n" + (self.root / "skill.py").read_text())
        second = load(self.root)
        self.assertNotEqual(value["content_sha256"], second["content_sha256"])
        path = self.root / "actions/entry.py"
        path.write_text(path.read_text() + "\n# changed domain behavior\n")
        with self.assertRaises(ValueError):
            load(self.root, expected=second["content_sha256"])

    def test_old_formats_cannot_smuggle_action_authority(self):
        for version in (1, 2):
            with self.subTest(version=version):
                self.manifest(lambda v: v.update(format=version, sdk=version))
                with self.assertRaises(ValueError):
                    load(self.root)

    def test_closed_descriptor_privileges_paths_labels_and_entrypoints(self):
        original = json.loads((self.root / "skill.json").read_text())
        mutations = [
            lambda a: a.update(execution_class="root_shell"),
            lambda a: a.update(command="apt-get update"),
            lambda a: a.update(entrypoint="../entry.py"),
            lambda a: a.update(entrypoint="skill.py"),
            lambda a: a.update(requires_approval=1),
            lambda a: a.update(name_key="missing"),
            lambda a: a.update(prepare_effect="packages_changed"),
            lambda a: a.update(version=True),
            lambda a: a.update(prerequisites=[{"kind": "shell", "id": "run"}]),
            lambda a: a.update(prerequisites=[{"kind": "executable", "id": "test.run", "path": "/tmp/exec", "package": "test"}]),
            lambda a: a.update(prerequisites=[{"kind": "python_module", "id": "test.module", "module": "one.two", "package": "test"}]),
            lambda a: a["review"]["facts"][0].update(key="unknown"),
            lambda a: a["result_facts"][0].update(kind="text"),
        ]
        for mutate in mutations:
            manifest = copy.deepcopy(original)
            mutate(manifest["actions"][0])
            (self.root / "skill.json").write_text(json.dumps(manifest))
            with self.subTest(manifest=manifest["actions"][0]), self.assertRaises(ValueError):
                load(self.root)
        (self.root / "skill.json").write_text(json.dumps(original))
        (self.root / "actions/entry.py").write_text("def prepare(request, context): pass\n")
        with self.assertRaises(ValueError):
            load(self.root)

    def test_duplicate_actions_and_schema_tampering_rejected(self):
        self.manifest(lambda v: v["actions"].append(copy.deepcopy(v["actions"][0])))
        with self.assertRaises(ValueError):
            load(self.root)
        self.manifest(lambda v: v["actions"].pop())
        path = self.root / "actions/result.json"
        path.write_text('{"$ref":"https://example.com/code"}')
        with self.assertRaises(ValueError):
            load(self.root)

    def test_action_schema_limits_and_exact_types(self):
        rows = {"type": "array", "items": {"type": "integer", "minimum": 0, "maximum": 10}, "maxItems": 5000}
        schema = object_schema({"rows": rows, "optional": {"type": "boolean", "nullable": True}})
        inspect(schema)
        validate(schema, {"rows": [1] * 5000, "optional": None})
        for value in ({"rows": [1] * 5001, "optional": True}, {"rows": [True], "optional": False},
                      {"rows": [], "optional": None, "extra": 1}):
            with self.assertRaises(ValueError):
                validate(schema, value)
        for value in ({**rows, "maxItems": 5001}, {**rows, "$ref": "code"},
                      {"type": "number", "minimum": 0, "maximum": float("inf")}):
            with self.assertRaises(ValueError):
                inspect(value)
        with self.assertRaises(ValueError):
            validate({"type": "integer", "minimum": 0, "maximum": 1, "enum": [1]}, True)

    def test_expanded_metadata_has_a_total_budget(self):
        path = self.root / "skill.json"
        manifest = json.loads(path.read_text())
        original = manifest["actions"][0]
        manifest["actions"] = [{**copy.deepcopy(original), "id": "example.action_" + str(i)} for i in range(16)]
        path.write_text(json.dumps(manifest))
        schema = object_schema({"complete": {"type": "boolean"}, "notes": {
            "type": "string", "maxLength": 2048, "enum": ["a" * 1024 + str(i) for i in range(8)]}})
        for name in ("input", "plan", "result", "recovery"):
            (self.root / f"actions/{name}.json").write_text(json.dumps(schema))
        with self.assertRaisesRegex(ValueError, "json_size"):
            load(self.root)

    def test_review_tables_require_present_nonnull_arrays_and_rows(self):
        row = object_schema({"complete": {"type": "boolean", "nullable": True}})
        schema = object_schema({"rows": {"type": "array", "items": row, "maxItems": 3}})
        self.manifest(lambda v: v["actions"][0].update(review={"facts": [], "tables": [{
            "id": "rows", "label_key": "ok", "source": "rows", "columns": [
                {"key": "complete", "label_key": "ok", "kind": "boolean"}]}]}))
        path = self.root / "actions/plan.json"
        for mutation in (lambda s: s.update(required=[]),
                         lambda s: s["properties"]["rows"].update(nullable=True),
                         lambda s: s["properties"]["rows"]["items"].update(nullable=True)):
            invalid = copy.deepcopy(schema)
            mutation(invalid)
            # These remain valid value schemas; only their table binding is refused.
            inspect(invalid)
            path.write_text(json.dumps(invalid))
            with self.subTest(schema=invalid), self.assertRaisesRegex(ValueError, "action_review_table_schema"):
                load(self.root)
        path.write_text(json.dumps(schema))
        admitted = load(self.root)["actions"][0]
        for value in ({"rows": []}, {"rows": [{"complete": None}]}):
            validate(admitted["schemas"]["plan"], value)
        for value in ({}, {"rows": None}, {"rows": [None]}):
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate(admitted["schemas"]["plan"], value)


if __name__ == "__main__":
    unittest.main()
