#!/usr/bin/env python3
"""Record trusted SDK bytes; rebuild only when the platform runtime changes."""
import hashlib
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[1] / "sdk/python/runtime"
sys.path.insert(0, str(root))
from packages import content

files = {p.relative_to(root).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
         for p in sorted(root.rglob("*")) if p.is_file() and
         (p.suffix == ".py" or "probe" in p.relative_to(root).parts)}
artifact = {"format": 1, "runtime": "python-3.13-v1", "sdk": 1,
            "source": "TinyWarden/tinywarden-agent/sdk/python/runtime",
            "probe_sha256": content(root / "probe")[1], "files": files}
encoded=json.dumps(artifact, indent=2) + "\n"
if sys.argv[1:] == ["--check"]:
    if (root / "artifact.json").read_text()!=encoded:raise SystemExit("runtime assets differ from recorded artifact")
elif not sys.argv[1:]:
    (root / "artifact.json").write_text(encoded)
else:raise SystemExit("usage: runtime_assets.py [--check]")
