#!/usr/bin/env python3
"""Create the complete signed Debian 13/amd64 platform release from reviewed source."""
import argparse
import base64
import hashlib
import json
import os
import re
import subprocess
import tarfile
import tempfile
from pathlib import Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "checks"))
from source_check import ROOT, check, git, tracked_files


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


parser = argparse.ArgumentParser()
parser.add_argument("--output", required=True)
parser.add_argument("--signing-key", required=True)
parser.add_argument("--source-commit", required=True)
parser.add_argument("--source-min", default="0.0.8")
parser.add_argument("--source-max")
args = parser.parse_args()
version_pattern = r"(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})"
version = re.search(r'const Version = "([^"]+)"', (ROOT / "internal/release/version.go").read_text())[1]
args.source_max = args.source_max or version
if not all(re.fullmatch(version_pattern, v) for v in [version, args.source_min, args.source_max]):
    raise SystemExit("Invalid release/source version")
if not re.fullmatch(r"[a-f0-9]{40}", args.source_commit):
    raise SystemExit("Use the exact source commit")
if git("rev-parse", args.source_commit + "^{tree}").strip() != git("write-tree").strip():
    raise SystemExit("Source commit must match the staged tree")
output = Path(args.output)
key = Path(args.signing_key)
if not output.is_absolute() or output.exists():
    raise SystemExit("Use a new absolute release output directory")
if not key.is_file() or key.is_symlink() or key.stat().st_mode & 0o077:
    raise SystemExit("Signing key must be a protected regular file")
expected_key = (ROOT / "internal/release/release-public-key.txt").read_text().strip()
public = subprocess.check_output(["openssl", "pkey", "-in", str(key), "-pubout", "-outform", "DER"])
if base64.b64encode(public[-32:]).decode() != expected_key:
    raise SystemExit("Signing key does not match the pinned public key")
check()
if git("ls-files", "--others", "--exclude-standard").strip():
    raise SystemExit("Stage reviewed source before packaging")
files = tracked_files()
fingerprints = {name: digest(ROOT / name) for name in files}
tree = git("write-tree").decode().strip()
for name in files:
    if (ROOT / name).read_bytes() != git("show", tree + ":" + name):
        raise SystemExit("Source differs from staged tree: " + name)
os.umask(0o077)
output.mkdir()
with tempfile.TemporaryDirectory(prefix="tinywarden-release-") as temporary:
    work = Path(temporary)
    binary = work / "tinywarden-agent"
    run("go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/tinywarden-agent", cwd=ROOT,
        env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64", "GOTOOLCHAIN": "local"})
    headers = subprocess.check_output(["readelf", "-l", str(binary)]).decode()
    if "INTERP" in headers:
        raise SystemExit("Release binary must not require a dynamic loader")
    if not subprocess.check_output([str(binary), "--version"]).decode().strip().endswith(" " + version):
        raise SystemExit("Compiled version mismatch")
    descriptor = {"format": 1, "version": version, "source_commit": args.source_commit,
                  "binary_sha256": digest(binary), "runtime_sha256": digest(ROOT / "sdk/python/runtime/artifact.json")}
    descriptor_path = work / "platform.json"
    descriptor_path.write_text(json.dumps(descriptor, separators=(",", ":")) + "\n")
    asset = "tinywarden-agent-" + version + "-linux-amd64.tar.gz"
    members = [(binary, "tinywarden-agent"), (descriptor_path, "platform.json")]
    members += [(ROOT / name, name.removeprefix("sdk/python/")) for name in files if name.startswith("sdk/python/runtime/")]
    members += [(ROOT / "deploy/systemd/managed" / name, "systemd/" + name) for name in
                ["tinywarden-agent.service", "tinywarden-agent-update.service", "tinywarden-agent-update.timer"]]
    members += [(ROOT / name, name) for name in ["LICENSE", "THIRD_PARTY_NOTICES.md"]]
    with tarfile.open(output / asset, "x:gz") as archive:
        for path, name in sorted(members, key=lambda item: item[1]):
            info = archive.gettarinfo(str(path), arcname=name)
            info.uid = info.gid = 0
            info.uname = info.gname = "root"
            info.mode = 0o755 if name == "tinywarden-agent" else 0o644
            info.mtime = 0
            with path.open("rb") as handle:
                archive.addfile(info, handle)
    manifest = {"format": 1, "repository": "TinyWarden/tinywarden-agent", "tag": "v" + version,
                "version": version, "source_commit": args.source_commit, "channel": "stable",
                "os": "debian", "os_version": "13", "architecture": "amd64",
                "source_min": args.source_min, "source_max": args.source_max, "automatic": True,
                "required_app_capabilities": ["agent_updates.v1", "agent_update_requests.v1"],
                "binary_sha256": descriptor["binary_sha256"], "runtime_sha256": descriptor["runtime_sha256"],
                "descriptor_sha256": digest(descriptor_path),
                "asset": {"name": asset, "size": (output / asset).stat().st_size, "sha256": digest(output / asset)}}
    manifest_path = output / "release.json"
    manifest_path.write_text(json.dumps(manifest, separators=(",", ":")) + "\n")
    signature = work / "signature"
    run("openssl", "pkeyutl", "-sign", "-rawin", "-inkey", str(key), "-in", str(manifest_path), "-out", str(signature))
    (output / "release.json.sig").write_text(base64.b64encode(signature.read_bytes()).decode() + "\n")
    (output / "install.sh").write_bytes((ROOT / "deploy/install.sh").read_bytes())
    hashes = "".join(digest(path) + "  " + path.name + "\n" for path in sorted(output.iterdir()))
    (output / "SHA256SUMS").write_text(hashes)
for name, expected in fingerprints.items():
    if digest(ROOT / name) != expected:
        raise SystemExit("Source changed during packaging: " + name)
print(json.dumps({"version": version, "sourceCommit": args.source_commit, "sourceTree": tree, "output": str(output)}))
