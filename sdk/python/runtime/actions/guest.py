"""Package invocation and lock-held callbacks; contains no domain operations."""
import importlib.abc
import importlib.util
import os
from pathlib import Path
import signal
import sys
import time
from actions.descriptor import exact
from actions.schema import validate
from contracts.json_values import decode, encode
from contracts.outcomes import message
from actions.logging import ActionLogger
from actions.output import sync_output


class PackageModules(importlib.abc.MetaPathFinder, importlib.abc.Loader):
    """A pinned in-memory package namespace, also readable after dropping root."""
    def __init__(self, files):
        self.files = files

    def path(self, name):
        return "/".join(name.split(".")[1:])

    def find_spec(self, fullname, path=None, target=None):
        if fullname != "action_package" and not fullname.startswith("action_package."):
            return None
        name = self.path(fullname)
        package = not name or name + "/__init__.py" in self.files
        namespace = not name or any(p.startswith(name + "/") for p in self.files)
        if name + ".py" not in self.files and not package and not namespace:
            raise ModuleNotFoundError(fullname)
        return importlib.util.spec_from_loader(fullname, self, is_package=package or namespace)

    def create_module(self, spec):
        return None

    def exec_module(self, module):
        name = self.path(module.__name__)
        path = name + ("/__init__.py" if module.__spec__.submodule_search_locations is not None else ".py")
        if path in self.files:
            exec(compile(self.files[path], "/action-package/" + path, "exec"), module.__dict__)


class ActionFailure(Exception):
    """A package reason and declared facts; never expose arbitrary exception text."""
    def __init__(self, reason, facts, effect="none", receipt_json=None):
        self.result = {"reason": reason, "facts": facts, "effect": effect,
                       "receipt_json": receipt_json, "local_verified": False, "process_running": False}


class Context:
    def __init__(self, request, emit, action, files, state_fd, state_path, catalog):
        self.binding = request["binding"]
        self.scope = request["scope"]
        self.job_id = request["job_id"]
        self.phase = request["phase"]
        self.start = request["start"]
        self.plan_sha256 = request["plan_sha256"]
        self.guard_fd = 3 if action["descriptor"]["execution_class"] == "host_admin" else None
        self.state_fd=state_fd
        self.state_path=Path(state_path) if self.guard_fd is not None else None
        self._emit, self._action, self.committed = emit, action, False
        self._files = files
        self._logger = ActionLogger(state_fd, self.phase, catalog)

    def log(self, message, level="info"):
        """Record a catalogue message without affecting action success or authority."""
        return self._logger.log(message, level)

    def output(self, text, stream="stdout"):
        """Record command/output/exit text; this method never executes commands."""
        if not isinstance(stream, str):
            return False
        if stream in {"command", "exit"} and not sync_output():
            return False
        return self._logger.output(text, stream)

    def read_package(self, name):
        if not isinstance(name, str) or name not in self._files:
            raise ValueError("action_package_file")
        return self._files[name]

    def check_start(self):
        if self.phase != "apply" or self.start is None:
            raise ValueError("action_start_invalid")
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        now = time.clock_gettime_ns(time.CLOCK_BOOTTIME) // 1000000
        if self.start["boot_id"] != boot or not now < self.start["not_after_ms"] <= now + 60000:
            raise ValueError("action_start_expired")

    def commit(self, facts):
        if self.phase != "inspect" or self.committed:
            raise ValueError("action_inspection_invalid")
        if len(list(Path("/proc/self/task").iterdir())) != 1:
            raise ValueError("action_inspection_threads_active")
        validate(self._action["schemas"]["recovery"], facts)
        encode(facts, 65536)
        self.committed = True
        self._emit({"facts": facts})
        os.write(4, b"\n")  # Durable regular receipt is ready; contents stay off the pipe.
        # Parent commits locally under native locks, then releases this inspector.
        if os.read(0, 1) != b"\n":
            raise ValueError("action_inspection_interrupted")


def result(value, action, catalog):
    exact(value, {"effect", "reason", "local_verified", "process_running", "facts", "receipt_json"})
    if value["effect"] not in {"none", "changed", "unknown"} or any(
            type(value[key]) is not bool for key in ("local_verified", "process_running")):
        raise ValueError("action_result_invalid")
    if value["reason"] is not None:
        message(value["reason"], catalog)
    validate(action["schemas"]["result"], value["facts"])
    if value["receipt_json"] is not None:
        if not isinstance(value["receipt_json"], str):
            raise ValueError("action_result_invalid")
        decode(value["receipt_json"].encode(), 65536)
    # Leave room for the bound event envelope and Go's HTML-safe JSON escaping.
    wire=encode(value,65536)
    for char in (b"<",b">",b"&"):
        wire=wire.replace(char,b"\\u00"+char.hex().encode())
    if len(wire)>49152:
        raise ValueError("action_result_size")
    return value


def dispatch(action, metadata, files, arguments, request, output,state_fd,state_path):
    def emit(value):
        reply = {"schema_version": 1, "binding": request["binding"], "scope": request["scope"],
                 "job_id": request["job_id"], "phase": request["phase"], "result": value}
        data = encode(reply, 9 * 1024 * 1024) + b"\n"
        while data:
            data = data[os.write(output, data):]
        os.fsync(output)

    context = Context(request, emit, action, files,state_fd,state_path,metadata["catalog"])
    context._logger.diagnostic("phase_started")
    if request["phase"] == "apply":
        context.check_start()
        if action["descriptor"]["execution_class"] == "unprivileged":
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
    else:
        def cancelled(signum, frame):
            raise ValueError("action_cancelled")
        signal.signal(signal.SIGTERM, cancelled)
    signal.pthread_sigmask(signal.SIG_UNBLOCK, {signal.SIGTERM})
    sys.meta_path.insert(0, PackageModules(files))
    path = action["descriptor"]["entrypoint"]
    module = __import__("action_package." + path[:-3].replace("/", "."), fromlist=["entrypoint"])
    arguments = {**arguments, "context": {"binding": request["binding"], "scope": request["scope"], "job_id": request["job_id"]}}
    try:
        value = getattr(module, request["phase"])(arguments, context)
    except ActionFailure as error:
        context.log(error.result["reason"], "error")
        emit({"failure": result(error.result, action, metadata["catalog"])})
        return
    phase = request["phase"]
    if phase == "prepare":
        exact(value, {"plan_json", "has_changes", "effect", "result"})
        if type(value["has_changes"]) is not bool or value["effect"] not in {"none", "changed"}:
            raise ValueError("action_result_invalid")
        if value["plan_json"] is not None:
            if not isinstance(value["plan_json"], str):
                raise ValueError("action_result_invalid")
            validate(action["schemas"]["plan"], decode(value["plan_json"].encode(), 4 * 1024 * 1024))
        if value["result"] is not None:
            result(value["result"], action, metadata["catalog"])
        if value["plan_json"] is None or value["has_changes"] and value["result"] is not None:
            raise ValueError("action_result_invalid")
    elif phase == "fetch":
        exact(value, {"downloaded", "plan_sha256"})
        if value["downloaded"] is not True or value["plan_sha256"] != context.plan_sha256:
            raise ValueError("action_result_invalid")
    elif phase == "apply":
        result(value, action, metadata["catalog"])
    elif phase == "verify":
        exact(value, {"verified"})
        if value["verified"] is not True:
            raise ValueError("action_result_invalid")
    elif phase == "inspect":
        if not context.committed or value is not None:
            raise ValueError("action_inspection_invalid")
        context._logger.diagnostic("phase_completed")
        return
    context._logger.diagnostic("phase_completed")
    emit(value)
