"""Readiness for bounded descriptor probes. This never loads package source."""
import importlib.util
import os
from pathlib import Path
import stat
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from actions.descriptor import exact, identifier, prerequisites
from actions.envelope import COUNTER, DIGEST, UUID, matches
from actions.trust import read_policy
from contracts.json_values import decode, encode

FIELDS = {"grant_id", "installation_id", "content_sha256", "descriptor_sha256", "id",
          "version", "revision", "origin", "execution_class", "prerequisites"}


def inspect_probe(value):
    exact(value, FIELDS)
    if (not matches(UUID, value["grant_id"]) or not matches(UUID, value["installation_id"]) or
            not matches(DIGEST, value["content_sha256"]) or not matches(DIGEST, value["descriptor_sha256"]) or
            not matches(COUNTER, value["revision"]) or type(value["version"]) is not int or
            not 1 <= value["version"] <= 65535 or value["execution_class"] not in {"unprivileged", "host_admin"}):
        raise ValueError("action_probe_invalid")
    identifier(value["id"])
    exact(value["origin"], {"github_id", "owner_id"})
    if any(not matches(COUNTER, v) for v in value["origin"].values()):
        raise ValueError("action_probe_invalid")
    prerequisites(value["prerequisites"])


def check(values, policy):
    if not isinstance(values, list) or len(values) > 32:
        raise ValueError("action_probe_invalid")
    try:
        origins = read_policy(policy)["origins"]
    except FileNotFoundError:
        origins = []
    results, seen = [], set()
    for value in values:
        inspect_probe(value)
        if value["grant_id"] in seen:
            raise ValueError("action_probe_invalid")
        seen.add(value["grant_id"])
        allowed = any(o["github_id"] == value["origin"]["github_id"] and
            o["owner_id"] == value["origin"]["owner_id"] and value["execution_class"] in o["classes"] for o in origins)
        missing = []
        for item in value["prerequisites"]:
            if item["kind"] == "python_module":
                available = importlib.util.find_spec(item["module"]) is not None
            else:
                try:
                    available = stat.S_ISREG(os.stat(item["path"]).st_mode) and os.access(item["path"], os.X_OK)
                except OSError:
                    available = False
            if not available:
                missing.append(item["id"])
        results.append({**{key: value[key] for key in ("grant_id", "installation_id", "content_sha256", "descriptor_sha256", "id", "revision")},
            "ready": allowed and not missing,
            "reason": "action_permission_required" if not allowed else "action_prerequisites" if missing else None,
            "missing": missing})
    return {"origins": origins, "actions": results}


if __name__ == "__main__":
    try:
        value = decode(sys.stdin.buffer.read(512 * 1024 + 1), 512 * 1024)
        exact(value, {"probes"})
        result = check(value["probes"], sys.argv[1])
    except BaseException:
        result = {"error": "action_readiness_unavailable"}
    sys.stdout.buffer.write(encode(result, 65536))
