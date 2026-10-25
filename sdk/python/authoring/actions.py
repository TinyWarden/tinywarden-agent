"""Action starter and captured-value checks, with no host execution authority."""
import json
from pathlib import Path
from actions.schema import validate
from contracts.json_values import decode

STARTER = '''"""An inert example action. It performs no host mutation."""
def prepare(request, context):
    return {"plan_json": '{"complete":true}', "has_changes": False,
            "effect": "none", "result": {"effect":"none", "reason":None,
            "local_verified":True, "process_running":False,
            "facts":{"complete":True}, "receipt_json":None}}

def fetch(request, context):
    return {"downloaded": True, "plan_sha256": context.plan_sha256}

def apply(request, context):
    context.check_start()
    return {"effect": "none", "reason": None, "local_verified": True,
            "process_running": False, "facts": {"complete": True},
            "receipt_json": '{"complete":true}'}

def verify(request, context):
    return {"verified": request["receipt"] == {"complete": True}}

def inspect(request, context):
    context.commit({"complete": True})
'''


def initialize_actions(destination):
    root = Path(destination)
    path = root / "actions"
    path.mkdir()
    (path / "entry.py").write_text(STARTER, encoding="utf-8")
    schema = {"type": "object", "properties": {"complete": {"type": "boolean", "label_key":"action.complete"}},
              "required": ["complete"], "additionalProperties": False}
    names = ("input", "plan", "result", "recovery")
    for name in names:
        (path / (name + ".schema.json")).write_text(json.dumps(schema, indent=2) + "\n")
    catalog_path = root / "messages/en.json"
    catalog = json.loads(catalog_path.read_text())
    catalog.update({key: {"text": text, "parameters": {}} for key, text in (
        ("action.name", "Check example action"),
        ("action.description", "Demonstrate an action without changing this server."),
        ("action.complete", "Complete"))})
    catalog_path.write_text(json.dumps(catalog, indent=2) + "\n")
    fact = {"key": "complete", "label_key": "action.complete", "kind": "boolean"}
    descriptor = {
        "id": "example.check.v1", "version": 1, "name_key": "action.name",
        "description_key": "action.description", "execution_class": "unprivileged",
        "requires_approval": True, "requires_monitoring": False,
        "prepare_effect": "none", "entrypoint": "actions/entry.py", "prerequisites": [],
        "schemas": {name: f"actions/{name}.schema.json" for name in names},
        "review": {"facts": [fact], "tables": []},
        "result_facts": [fact], "recovery_facts": [fact],
    }
    manifest_path = root / "skill.json"
    manifest = json.loads(manifest_path.read_text())
    manifest.update(format=3, sdk=3, actions=[descriptor])
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    fixtures = {"format": 1, "cases": [{"name": "complete", "action": descriptor["id"],
        "values": {name: {"complete": True} for name in names}, "valid": True}]}
    (root / "action-tests.json").write_text(json.dumps(fixtures, indent=2) + "\n")


def run_action_cases(metadata, path):
    document = decode(Path(path).read_bytes(), 1024 * 1024)
    if (not isinstance(document, dict) or set(document) != {"format", "cases"} or
            type(document["format"]) is not int or document["format"] != 1 or
            not isinstance(document["cases"], list) or not 1 <= len(document["cases"]) <= 64):
        raise ValueError("action_fixture")
    actions = {a["descriptor"]["id"]: a for a in metadata.get("actions", [])}
    results, seen = [], set()
    for case in document["cases"]:
        if not isinstance(case, dict) or set(case) != {"name", "action", "values", "valid"}:
            raise ValueError("action_fixture")
        name = case["name"]
        if not isinstance(name, str) or not 1 <= len(name) <= 128 or name in seen:
            raise ValueError("action_fixture")
        seen.add(name)
        if (not isinstance(case["action"], str) or case["action"] not in actions or
                type(case["valid"]) is not bool or not isinstance(case["values"], dict) or
                not case["values"] or set(case["values"]) - {"input", "plan", "result", "recovery"}):
            raise ValueError("action_fixture")
        valid = True
        try:
            for kind, value in case["values"].items():
                validate(actions[case["action"]]["schemas"][kind], value)
        except ValueError:
            valid = False
        results.append({"name": name, "passed": valid == case["valid"]})
    return {"passed": all(row["passed"] for row in results), "cases": results,
            "validation": "captured_action_values", "code_executed": False}
