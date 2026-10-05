"""Execute through the unchanged bounded SDK, with no host import fallback."""
import hashlib
import os
import signal
import subprocess
from pathlib import Path

from json_values import decode, encode

ASSETS = Path(__file__).resolve().parents[1] / "runtime"


def runtime_assets(directory):
    directory = Path(directory).absolute()
    artifact = decode((directory / "artifact.json").read_bytes())
    if artifact.get("format") != 1 or artifact.get("runtime") != "python-3.13-v1" or artifact.get("sdk") != 1:
        raise ValueError("runtime_artifact")
    recorded = artifact.get("files")
    if not isinstance(recorded, dict) or not recorded:
        raise ValueError("runtime_artifact")
    for name, expected in recorded.items():
        if not isinstance(name, str) or name.startswith("/") or any(p in {"", ".", ".."} for p in name.split("/")):
            raise ValueError("runtime_artifact")
        path = directory / name
        if any(p.is_symlink() for p in [path, *path.parents]) or not path.is_file():
            raise ValueError("runtime_artifact")
        if hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise ValueError("runtime_artifact")
    return directory


def invoke(runtime, package, metadata, function, arguments, collect=False, filesystem_helper=None):
    request = {"action": "run", "package": str(package), "function": function,
               "content_sha256": metadata["content_sha256"], "official": False, "arguments": arguments}
    if collect:
        request.update(grants=metadata["manifest"]["capabilities"],
                       ceiling=metadata["manifest"]["capabilities"],
                       protected_paths=[str(runtime), "/etc/tinywarden-agent", "/var/lib/tinywarden-agent"])
        if filesystem_helper:
            request["filesystem_helper"] = str(Path(filesystem_helper).absolute())
    process = subprocess.Popen(["/usr/bin/python3.13", "-I", "-S", "-B", str(runtime / "supervisor.py")],
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               env={"LANG": "C.UTF-8", "PATH": "/usr/bin:/bin"}, cwd="/", start_new_session=True)
    # The trusted supervisor bounds output and kills/reaps its entire cgroup.
    # On timeout first ask it to clean up; never silently retry a failed invocation.
    try:
        output, _ = process.communicate(encode(request), timeout=70 if collect else 12)
    except BaseException:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.communicate(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()
        raise
    if process.returncode != 0:
        raise ValueError("runtime_failed")
    response = decode(output)
    if "error" in response:
        raise ValueError("runtime:" + response["error"])
    if set(response) != {"result", "content_sha256"} or response["content_sha256"] != metadata["content_sha256"]:
        raise ValueError("runtime_response")
    return response["result"]
