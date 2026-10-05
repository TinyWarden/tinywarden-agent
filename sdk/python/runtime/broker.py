"""Capability intersection and bounded supervision of trusted host IO children."""
from pathlib import Path
from capture import capture
from grants import inspect_grant, permits
from json_values import decode, encode


class Broker:
    def __init__(self, requested, approved, ceiling, group, policy, filesystem_helper=None, protected_paths=None):
        self.layers = [requested, approved, ceiling]
        for layer in self.layers:
            if not isinstance(layer, list) or len(layer) > 32:
                raise RuntimeError("capability_denied")
            for grant in layer:
                inspect_grant(grant)
        self.group = group
        self.filesystem_helper = filesystem_helper
        self.protected_paths = protected_paths or []
        self.calls, self.raw, self.sequence = 0, 0, 0

    def handle(self, request, remaining):
        if not isinstance(request, dict) or set(request) != {"sequence", "operation", "arguments"}:
            raise RuntimeError("capability_denied")
        self.sequence += 1
        if request["sequence"] != self.sequence or type(request["sequence"]) is not int:
            raise RuntimeError("capability_denied")
        self.calls += 1
        if self.calls > 64:
            raise RuntimeError("broker_limit")
        operation, args = request["operation"], request["arguments"]
        if not isinstance(args, dict) or not isinstance(operation, str):
            raise RuntimeError("capability_denied")
        paths = [args.get("path")] if operation.startswith("files.") else args.get("inputs", [])
        if operation == "command.capture" and isinstance(paths, list):
            paths = paths + args.get("helpers", []) + args.get("empty_directories", [])
        if not isinstance(paths, list) or any(not isinstance(p, str) or any(p == root or p.startswith(root.rstrip('/') + '/') or operation == "command.capture" and root.startswith(p.rstrip('/') + '/') for root in self.protected_paths if root) for p in paths):
            raise RuntimeError("capability_denied")
        matches = [[grant for grant in layer if permits(grant, operation, args)] for layer in self.layers]
        if any(not layer for layer in matches):
            raise RuntimeError("capability_denied")
        timeout = min(remaining, 3)
        if operation == "filesystems.snapshot":
            if not self.filesystem_helper:
                raise RuntimeError("runtime_unavailable")
            timeout = min(remaining, 10)
        elif operation == "command.capture":
            timeout = min(remaining, *(min(g["timeout_seconds"] for g in layer) for layer in matches))
        payload = {"operation": operation, "arguments": args, "timeout": timeout,
                   "filesystem_helper": self.filesystem_helper}
        worker = Path(__file__).resolve().with_name("worker.py")
        code, out, _ = capture(["/usr/bin/python3.13", "-I", "-S", "-B", str(worker)],
                               encode(payload, 65536), self.group, timeout)
        if code != 0:
            raise RuntimeError("observation_unavailable")
        result = decode(out)
        if "error" in result:
            raise RuntimeError(result["error"])
        self.raw += result["raw_bytes"]
        if self.raw > 256 * 1024:
            raise RuntimeError("broker_limit")
        return {"sequence": self.sequence, "value": result["value"]}
