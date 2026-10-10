"""Trusted host IO child. Even blocked host reads share the invocation budget."""
import os
import sys
from pathlib import Path

ASSETS = Path(__file__).resolve().parent
sys.path.insert(0, str(ASSETS))
from capture import capture
from cgroups import AttachedBudget
from files import observe, open_path
from json_values import decode, encode
from sandbox import native_command
from seccomp import policy_fd


def command(args, group, timeout):
    policy = policy_fd()
    mounts, descriptors = [], [policy]
    try:
        for path in args["inputs"]:
            try:
                fd = open_path(path, directory=Path(path).is_dir())
            except FileNotFoundError:
                continue
            descriptors.append(fd)
            mounts.append((fd, path))
        argv = native_command(args["executable"], args["argv"], [], policy)
        marker = argv.index("--chdir")
        from sandbox import dependencies, mounts as system_mounts, system_path
        helpers = args.get("helpers", [])
        for helper in helpers:
            system_path(helper)
        argv[marker:marker] = ([item for target in args.get("empty_directories", []) for item in ("--dir", target)] +
                               system_mounts([path for helper in helpers for path in [helper, *dependencies(helper)]]) +
                               [item for fd, target in mounts for item in ("--ro-bind-fd", str(fd), target)])
        code, out, err = capture(argv, b"", group, timeout, pass_fds=tuple(descriptors), output_limit=65536)
        return {"exit_code": code, "stdout": out.decode("utf-8"), "stderr_bytes": len(err)}, len(out) + len(err)
    finally:
        for fd in descriptors:
            os.close(fd)


def run(request):
    operation, args = request["operation"], request["arguments"]
    group = AttachedBudget()
    timeout = request["timeout"]
    if operation.startswith("files."):
        value = observe(operation, args)
        return {"value": value, "raw_bytes": len(encode(value))}
    if operation == "command.capture":
        value, raw = command(args, group, timeout)
        return {"value": value, "raw_bytes": raw}
    if operation == "systemd.properties":
        argv = ["/usr/bin/systemctl", "--system", "--no-pager", "--no-ask-password", "--all",
                "--timestamp=unix", "show", "--property=" + ",".join(args["properties"]), "--", args["unit"]]
        code, out, err = capture(argv, b"", group, timeout, output_limit=65536)
        if code != 0:
            raise RuntimeError("observation_unavailable")
        value = {}
        for line in out.decode().splitlines():
            key, separator, content = line.partition("=")
            if not separator or key in value or len(content) > 512 or any(ord(c) < 32 or ord(c) == 127 for c in content):
                raise RuntimeError("observation_unavailable")
            value[key] = content
        if set(value) != set(args["properties"]):
            raise RuntimeError("observation_unavailable")
        return {"value": value, "raw_bytes": len(out) + len(err)}
    if operation == "filesystems.snapshot":
        helper = Path(request["filesystem_helper"]).resolve(strict=True)
        mode = helper.stat()
        if mode.st_uid not in {0, os.getuid()} or mode.st_mode & 0o022:
            raise RuntimeError("runtime_unavailable")
        code, out, _ = capture([str(helper), "__collect-disk-v1"], b"", group, timeout)
        if code != 0:
            raise RuntimeError("observation_unavailable")
        return {"value": decode(out), "raw_bytes": 0}
    raise RuntimeError("capability_denied")


if __name__ == "__main__":
    try:
        result = run(decode(sys.stdin.buffer.read(65537), 65536))
    except RuntimeError as error:
        code = str(error)
        result = {"error": code if code in {"capability_denied", "broker_limit", "output_exceeded",
                  "deadline_exceeded", "resource_exhausted", "runtime_unavailable"} else "observation_unavailable"}
    except (OSError, ValueError, TypeError, KeyError):
        result = {"error": "observation_unavailable"}
    sys.stdout.buffer.write(encode(result))
