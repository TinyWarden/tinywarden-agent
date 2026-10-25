"""Typed local prerequisites and trust readiness; never import action code."""
import importlib.util
import os
from pathlib import Path
import stat
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from actions.trust import admit
from contracts.json_values import decode, encode


def probe(package, policy, request):
    try:
        action, _, _, _ = admit(package, policy, request)
    except FileNotFoundError:
        return {"ready": False, "reason": "action_permission_required", "missing": []}
    except (ValueError, OSError, TypeError, KeyError):
        return {"ready": False, "reason": "action_not_authorized", "missing": []}
    missing = []
    for item in action["descriptor"]["prerequisites"]:
        if item["kind"] == "executable":
            try:
                info = os.stat(item["path"])
                available = stat.S_ISREG(info.st_mode) and os.access(item["path"], os.X_OK)
            except OSError:
                available = False
        else:
            available = importlib.util.find_spec(item["module"]) is not None
        if not available:
            missing.append(item["id"])
    return {"ready": not missing, "reason": "action_prerequisites" if missing else None, "missing": missing}


if __name__ == "__main__":
    try:
        request = decode(sys.stdin.buffer.read(17 * 1024 * 1024 + 1), 17 * 1024 * 1024)
        value = probe(sys.argv[1], sys.argv[2], request)
    except BaseException:
        value = {"ready": False, "reason": "action_runtime_unavailable", "missing": []}
    sys.stdout.buffer.write(encode(value, 65536))
