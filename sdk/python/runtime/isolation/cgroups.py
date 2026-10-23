"""Every guest and broker child enters an enforced subtree before exec."""
import os
import ctypes
import resource
import time
import uuid
from pathlib import Path


class BudgetGroup:
    def __init__(self, pure):
        line = next(x for x in Path("/proc/self/cgroup").read_text().splitlines() if x.startswith("0::"))
        leaf = Path("/sys/fs/cgroup" + line[3:])
        # DelegateSubgroup=supervisor is mandatory: the parent must contain no processes.
        if leaf.name != "supervisor":
            raise RuntimeError("runtime_unavailable")
        parent = leaf.parent
        if not {"cpu", "memory", "pids"} <= set((parent / "cgroup.controllers").read_text().split()):
            raise RuntimeError("runtime_unavailable")
        (parent / "cgroup.subtree_control").write_text("+cpu +memory +pids")
        self.path = parent / ("skill-" + uuid.uuid4().hex)
        self.path.mkdir()
        try:
            for name, value in {"memory.max": str((128 if pure else 256) * 1024 * 1024),
                                "memory.swap.max": "0", "pids.max": "16" if pure else "32",
                                "cpu.max": "100000 100000", "memory.oom.group": "1"}.items():
                (self.path / name).write_text(value)
            self.cpu_limit = 2_000_000 if pure else 15_000_000
        except BaseException:
            self.path.rmdir()
            raise

    def enter(self):
        (self.path / "cgroup.procs").write_text(str(os.getpid()))
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
        resource.setrlimit(resource.RLIMIT_NOFILE, (128, 128))
        # A transport archive may be 10 MiB; individual extracted files remain 8 MiB.
        resource.setrlimit(resource.RLIMIT_FSIZE, (10 * 1024 * 1024, 10 * 1024 * 1024))
        if ctypes.CDLL(None).prctl(38, 1, 0, 0, 0) != 0:
            raise RuntimeError("runtime_unavailable")

    def exhausted(self):
        stat = dict(line.split() for line in (self.path / "cpu.stat").read_text().splitlines())
        events = dict(line.split() for line in (self.path / "memory.events").read_text().splitlines())
        pids = dict(line.split() for line in (self.path / "pids.events").read_text().splitlines())
        return int(stat.get("usage_usec", 0)) > self.cpu_limit or int(events.get("oom", 0)) > 0 or int(pids.get("max", 0)) > 0

    def close(self):
        (self.path / "cgroup.kill").write_text("1")
        until = time.monotonic() + 2
        while "populated 1" in (self.path / "cgroup.events").read_text():
            if time.monotonic() > until:
                raise RuntimeError("cleanup_failed")
            time.sleep(.01)
        self.path.rmdir()


class AttachedBudget(BudgetGroup):
    def __init__(self):
        line = next(x for x in Path("/proc/self/cgroup").read_text().splitlines() if x.startswith("0::"))
        self.path = Path("/sys/fs/cgroup" + line[3:])
        if not self.path.name.startswith("skill-"):
            raise RuntimeError("runtime_unavailable")
        self.cpu_limit = 15_000_000
