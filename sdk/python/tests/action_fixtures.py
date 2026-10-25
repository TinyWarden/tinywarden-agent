"""Action packages are independent test inputs, never compiled provider entries."""
import json
from fixtures import package


def object_schema(properties):
    return {"type": "object", "properties": properties,
            "required": list(properties), "additionalProperties": False}


def action_package(root):
    root = package(root)
    (root / "actions").mkdir()
    schema = object_schema({"complete": {"type": "boolean", "nullable": True}})
    for name in ("input", "plan", "result", "recovery"):
        (root / f"actions/{name}.json").write_text(json.dumps(schema))
    source = "raise RuntimeError('action code imported during admission')\n"
    source += "\n".join(f"def {phase}(request, context):\n    return {{'complete': True}}\n"
                        for phase in ("prepare", "fetch", "apply", "verify", "inspect"))
    (root / "actions/entry.py").write_text(source)
    descriptor = {
        "id": "example.check.v1", "version": 1, "name_key": "name",
        "description_key": "description", "execution_class": "unprivileged",
        "requires_approval": True, "requires_monitoring": False,
        "prepare_effect": "none", "entrypoint": "actions/entry.py",
        "prerequisites": [],
        "schemas": {name: f"actions/{name}.json" for name in ("input", "plan", "result", "recovery")},
        "review": {"facts": [{"key": "complete", "label_key": "ok", "kind": "boolean"}], "tables": []},
        "result_facts": [{"key": "complete", "label_key": "ok", "kind": "boolean"}],
        "recovery_facts": [{"key": "complete", "label_key": "ok", "kind": "boolean"}],
    }
    manifest = json.loads((root / "skill.json").read_text())
    manifest.update(format=3, sdk=3, actions=[descriptor])
    (root / "skill.json").write_text(json.dumps(manifest))
    return root
