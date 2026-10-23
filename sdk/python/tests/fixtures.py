"""Synthetic skill authoring fixtures, shared by admission and isolation tests."""
import json
from pathlib import Path

EMPTY = {"type": "object", "properties": {}, "required": [], "additionalProperties": False}


def package(root, code=None, capabilities=None):
    root = Path(root)
    (root / "messages").mkdir(parents=True)
    manifest = {"format": 1, "id": "example/probe", "version": "1.0.0", "runtime": "python-3.13-v1",
                "sdk": 1, "state_version": 1, "license": "Apache-2.0", "publisher": "Example",
                "name_key": "name", "description_key": "description", "category": "system",
                "compatibility": {"os": ["debian:13"], "architectures": ["amd64", "arm64"]},
                "defaults": {}, "schedule": {"minimum_seconds": 60, "maximum_seconds": 86400},
                "capabilities": capabilities or [], "limits": {"wall_seconds": 5}, "fields": {}}
    values = {"skill.json": manifest, "settings.schema.json": EMPTY,
              "observation.schema.json": EMPTY, "state.schema.json": EMPTY,
              "messages/en.json": {key: {"text": text, "parameters": {}} for key, text in
                                   (("name", "Probe"), ("description", "Synthetic probe"), ("ok", "Passed"))}}
    for name, value in values.items():
        (root / name).write_text(json.dumps(value))
    (root / "skill.py").write_text(code or """def validate_settings(settings):
    return []

def collect(settings, host):
    return {}

def reduce(context):
    return {}

def evaluate(context):
    return [{"from": context["now"], "status": "healthy", "reason": {"key": "ok", "params": {}}, "facts": []}]
""")
    (root / "README.md").write_text("Synthetic test fixture.\n")
    (root / "LICENSE").write_text("Apache-2.0\n")
    return root
