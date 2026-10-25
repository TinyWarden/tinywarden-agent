#!/usr/bin/env python3
"""Enforce the reviewed production dependency and shipped-runtime boundary."""
import json
import os
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MODULE = "github.com/TinyWarden/tinywarden-agent/"
INPUT_FIELDS = ("GoFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles", "HFiles",
                "FFiles", "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles", "EmbedFiles")
FORBIDDEN = re.compile(
    r"disk-local|package-updates|reboot-required|fstrim-status|fstrim\.(?:timer|service)"
    r"|\bapt_pkg\b|\bimport apt\b|packages\.(?:refresh|upgrade_conservative)"
)


def production_graph(root=ROOT, architecture="amd64"):
    raw = subprocess.check_output(
        ["go", "list", "-deps", "-json", "./cmd/tinywarden-agent"], cwd=root, text=True, env={**os.environ, "GOOS": "linux", "GOARCH": architecture})
    decoder = json.JSONDecoder()
    packages = []
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        if not item.get("Standard", False):
            packages.append(item)
    return packages


def check_inventory(actual, allowed, label):
    extra = set(actual) - set(allowed)
    missing = set(allowed) - set(actual)
    if extra or missing:
        raise ValueError(f"{label} boundary changed; review ownership: added={sorted(extra)}, removed={sorted(missing)}")


def check_content(name, text):
    if FORBIDDEN.search(text):
        raise ValueError(f"Skill-owned identity, recipe or executor in platform: {name}")


def check(root=ROOT):
    policy = json.loads((root / "scripts/checks/platform-boundary.json").read_text())
    if policy.get("format") != 1:
        raise ValueError("Unsupported architecture policy")
    graphs = [production_graph(root, architecture) for architecture in ("amd64", "arm64")]
    for architecture, graph in zip(("amd64", "arm64"), graphs):
        check_inventory([p["ImportPath"].removeprefix(MODULE) for p in graph], policy["go_packages"], f"{architecture} production dependency")
    files = {}
    for graph in graphs:
        for item in graph:
            package = item["ImportPath"].removeprefix(MODULE)
            for field in INPUT_FIELDS:
                for name in item.get(field, []):
                    files[f"{package}/{name}"] = Path(item["Dir"]) / name
    check_inventory(files, policy["production_files"], "Production input file")
    for path in sorted(set(files.values())):
        check_content(str(path), path.read_text())
    checked = len(files)
    runtime = root / "sdk/python/runtime"
    artifact = json.loads((runtime / "artifact.json").read_text())
    actual = {p.relative_to(runtime).as_posix() for p in runtime.rglob("*")
              if p.is_file() and "__pycache__" not in p.parts and p.name != "artifact.json"}
    check_inventory(actual, policy["runtime_files"], "Shipped runtime")
    check_inventory(artifact["files"], policy["runtime_files"], "Runtime manifest")
    for name in sorted(actual):
        check_content(name, (runtime / name).read_text())
    if (root / "internal/legacy").exists() or (root / "internal/maintenance/apt").exists():
        raise ValueError("Retired skill implementation remains in agent source")
    if (root / "sdk/python/tests/fixtures/official-packages.json").exists():
        raise ValueError("Copied official skill payload belongs in the skills repository")
    print(f"Neutrality boundary passed: {len(graph)} production packages, {checked} production input files, {len(actual)} runtime files")


if __name__ == "__main__":
    check()
