#!/usr/bin/env python3
"""Build and package the exact staged source tree without touching an installed agent."""
import argparse
import hashlib
import json
import os
import subprocess
import tarfile
import tempfile
from pathlib import Path
from source_check import ROOT, check, git, tracked_files

parser = argparse.ArgumentParser()
parser.add_argument("--output", required=True)
options = parser.parse_args()
output = Path(options.output)
if not output.is_absolute() or output.exists() or Path(str(output) + ".json").exists():
    raise SystemExit("Output must be a new absolute archive path")
os.umask(0o077)
check()
if git("ls-files", "--others", "--exclude-standard").strip():
    raise SystemExit("Untracked source must be reviewed and staged before packaging")
tree = git("write-tree").decode().strip()
files = tracked_files()
fingerprints = {}
for name in files:
    data = (ROOT / name).read_bytes()
    if data != git("show", tree + ":" + name):
        raise SystemExit("Source differs from staged tree: " + name)
    fingerprints[name] = hashlib.sha256(data).hexdigest()
with tempfile.TemporaryDirectory(prefix="tinywarden-agent-package-") as work:
    binary = Path(work) / "tinywarden-agent"
    subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/tinywarden-agent"], cwd=ROOT, check=True, env={**os.environ, "GOTOOLCHAIN": "local"})
    for name, expected in fingerprints.items():
        if hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != expected:
            raise SystemExit("Source changed during build: " + name)
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    assets = ["infra/systemd/tinywarden-agent.service", "docs/deploy/native.md", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"]
    assets += [name for name in files if name.startswith(("sdk/python/runtime/", "skills/official/"))]
    with tarfile.open(output, "x:gz") as archive:
        archive.add(binary, arcname="tinywarden-agent")
        for name in assets:
            archive.add(ROOT / name, arcname=name)
    metadata = {"sourceTree": tree, "module": "github.com/TinyWarden/tinywarden-agent", "binarySha256": binary_hash,
                "unitSha256": fingerprints[assets[0]], "archiveSha256": hashlib.sha256(output.read_bytes()).hexdigest()}
    with Path(str(output) + ".json").open("x") as handle:
        json.dump(metadata, handle, indent=2)
        handle.write("\n")
    print(json.dumps(metadata))
