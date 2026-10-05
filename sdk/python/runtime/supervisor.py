"""Trusted host process. Package imports occur only in launcher.py inside bwrap."""
import os
import sys
import time
import signal
from pathlib import Path

ASSETS = Path(__file__).resolve().parent
sys.path.insert(0, str(ASSETS))
from capture import capture
from cgroups import BudgetGroup
from json_values import decode, encode
from schema import validate
from seccomp import policy_fd
from sandbox import python_command
from outcomes import timeline, errors

ERRORS = {"runtime_unavailable", "capability_denied", "broker_limit", "output_exceeded",
          "deadline_exceeded", "resource_exhausted", "execution_failed", "output_invalid", "cleanup_failed",
          "observation_unavailable"}


def inspect_package(request, group, timeout=5):
    payload = {key: request[key] for key in ("package", "content_sha256", "official", "store", "archive", "archive_sha256") if key in request}
    script = {"publish": "publisher.py", "publish_archive": "archives.py"}.get(request.get("action"), "inspector.py")
    code, out, _ = capture(["/usr/bin/python3.13", "-I", "-S", "-B", str(ASSETS / script)],
                           encode(payload, 65536), group, timeout)
    if code != 0:
        raise RuntimeError("package_rejected")
    metadata = decode(out)
    if "error" in metadata:
        raise ValueError("package_rejected")
    return metadata


def invoke(request):
    package = Path(request["package"]).absolute()
    function = request["function"]
    if function not in {"validate_settings", "collect", "reduce", "evaluate"}:
        raise ValueError("entrypoint")
    arguments = request["arguments"]
    pure = function != "collect"
    try:
        group = BudgetGroup(pure)
    except (OSError, StopIteration):
        raise RuntimeError("runtime_unavailable") from None
    descriptors = []
    started = time.monotonic()
    try:
        metadata = inspect_package(request, group)
        if function in {"collect", "validate_settings"}:
            validate(metadata["schemas"]["settings"], arguments)
        policy = policy_fd()
        descriptors.append(policy)
        broker, channels = None, None
        guest_read, guest_write = None, None
        if not pure:
            from broker import Broker
            guest_read, parent_write = os.pipe()
            parent_read, guest_write = os.pipe()
            descriptors += [guest_read, parent_write, parent_read, guest_write]
            broker = Broker(metadata["manifest"]["capabilities"], request.get("grants", []),
                            request.get("ceiling", []), group, policy, request.get("filesystem_helper"), request.get("protected_paths", [])).handle
            channels = (parent_read, parent_write)
        command = python_command(ASSETS, package, policy, pure, guest_read, guest_write)
        passed = (policy,) if pure else (policy, guest_read, guest_write)
        collector_wall = metadata["manifest"]["limits"]["wall_seconds"]
        if not pure and isinstance(arguments, dict) and type(arguments.get("timeout_seconds")) is int:
            collector_wall = min(collector_wall, arguments["timeout_seconds"])
        wall = (5 if pure else collector_wall) - (time.monotonic() - started)
        if wall <= 0:
            raise RuntimeError("deadline_exceeded")
        code, output, _ = capture(command, encode({"function": function, "arguments": arguments}),
                                  group, wall, pass_fds=passed, broker=broker, channels=channels)
        if code != 0:
            raise RuntimeError("execution_failed" if code == 70 else "runtime_unavailable")
        try:
            envelope = decode(output)
            if set(envelope) != {"result"}:
                raise ValueError("output")
            result = envelope["result"]
            if function == "collect":
                validate(metadata["schemas"]["observation"], result)
            elif function == "reduce":
                validate(metadata["schemas"]["state"], result)
                encode(result, 16384)
            elif function == "evaluate":
                timeline(result, arguments, metadata["catalog"])
            elif function == "validate_settings":
                errors(result, metadata)
            return {"result": result, "content_sha256": metadata["content_sha256"]}
        except (ValueError, TypeError, KeyError):
            raise RuntimeError("output_invalid") from None
    finally:
        try:
            group.close()
        finally:
            for descriptor in descriptors:
                os.close(descriptor)


def main():
    request = decode(sys.stdin.buffer.read(1024 * 1024 + 1))
    if request.get("action") in {"inspect", "publish", "publish_archive"}:
        try:
            group = BudgetGroup(True)
        except (OSError, StopIteration):
            raise RuntimeError("runtime_unavailable") from None
        try:
            return inspect_package(request, group)
        finally:
            group.close()
    if request.get("action") == "run":
        return invoke(request)
    raise ValueError("action")


if __name__ == "__main__":
    def interrupted(_signal, _frame):
        raise RuntimeError("deadline_exceeded")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    try:
        response = main()
    except RuntimeError as error:
        reason = str(error)
        response = {"error": reason if reason in ERRORS else "runtime_unavailable"}
    except (ValueError, TypeError, KeyError, OSError, StopIteration, RecursionError):
        response = {"error": "package_rejected"}
    sys.stdout.buffer.write(encode(response) + b"\n")
