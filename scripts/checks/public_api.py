#!/usr/bin/env python3
"""Keep the public call inventory synchronized with implemented entry points."""
import ast
import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def assigned(path, name):
    for node in ast.parse(path.read_text()).body:
        if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == name for t in node.targets):
            return ast.literal_eval(node.value)
    raise ValueError(name)


def inventory(root=ROOT):
    runtime = root / "sdk/python/runtime"
    routes = set()
    expansions = {"/api/v2/agent/skill-packages/": "/api/v2/agent/skill-packages/{assignment_id}",
                  "/api/v1/agent/maintenance-jobs/": "/api/v1/agent/maintenance-jobs/{id}/package"}
    for path in (root / "internal").rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        for value in re.findall(r'"(/api/[^"\s]*)"', path.read_text()):
            if value in {"/api/", "/api/v1/maintenance-recovery/"}:
                continue
            route = expansions.get(value, value)
            if route.endswith("/"):
                raise ValueError(f"Document the new dynamic HTTP route: {value}")
            routes.add(route)
    monitor = []
    for node in ast.walk(ast.parse((runtime / "supervisor.py").read_text())):
        if isinstance(node, ast.Compare) and isinstance(node.left, ast.Name) and node.left.id == "function":
            if len(node.comparators) == 1 and isinstance(node.comparators[0], ast.Set):
                monitor = sorted(ast.literal_eval(node.comparators[0]))
                break
    if not monitor:
        raise ValueError("Cannot inventory monitoring entry points")
    context = next(n for n in ast.parse((runtime / "actions/guest.py").read_text()).body
                   if isinstance(n, ast.ClassDef) and n.name == "Context")
    methods = sorted(n.name for n in context.body if isinstance(n, ast.FunctionDef) and not n.name.startswith("_"))
    commands, flags = set(), set()
    for node in ast.walk(ast.parse((root / "sdk/python/skill_tool.py").read_text())):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.args and isinstance(node.args[0], ast.Constant):
            if node.func.attr == "add_parser":
                commands.add(node.args[0].value)
            if node.func.attr == "add_argument" and str(node.args[0].value).startswith("--"):
                flags.add(node.args[0].value)
        if isinstance(node, ast.For) and isinstance(node.target, ast.Name) and node.target.id == "name" and isinstance(node.iter, ast.Tuple):
            commands.update(ast.literal_eval(node.iter))
    return {"format": 1, "runtime_artifact_sha256": hashlib.sha256((runtime / "artifact.json").read_bytes()).hexdigest(),
            "agent_http_paths": sorted(routes), "host_operations": sorted(assigned(runtime / "packages/admission.py", "OPERATIONS")),
            "monitor_functions": monitor, "action_functions": sorted(assigned(runtime / "actions/envelope.py", "PHASES")),
            "action_context_methods": methods, "action_exception": "ActionFailure",
            "author_commands": sorted(commands), "author_flags": sorted(flags)}


def main():
    if sys.argv[1:] not in ([], ["--write"]):
        raise SystemExit("usage: public_api.py [--write]")
    result = json.dumps(inventory(), indent=2) + "\n"
    path = ROOT / "docs/public-api.json"
    if sys.argv[1:] == ["--write"]:
        path.write_text(result)
    elif not path.exists() or path.read_text() != result:
        raise SystemExit("Public API inventory changed: regenerate it and update the human API/SDK reference and consumers")
    print("Public API source inventory verified")


if __name__ == "__main__":
    main()
