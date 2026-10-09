"""Explicit namespaces and minimal read-only distro runtime mounts."""
import os
import re
import stat
import subprocess
from pathlib import Path


def system_path(path):
    source = Path(path)
    if not source.is_absolute() or not str(source).startswith(("/usr/bin/", "/usr/sbin/", "/usr/lib/", "/lib/", "/lib64/")):
        raise ValueError("runtime_asset")
    actual = source.resolve(strict=True)
    for item in [actual, *actual.parents]:
        mode = item.stat()
        if mode.st_uid != 0 or mode.st_mode & 0o022:
            raise ValueError("runtime_asset")
    return str(actual)


def dependencies(executable):
    result = subprocess.run(["/usr/bin/ldd", system_path(executable)], env={"LANG": "C", "PATH": "/usr/bin:/bin"},
                            capture_output=True, timeout=2, check=True)
    paths = set()
    for line in result.stdout.decode("ascii").splitlines():
        match = re.search(r"(/[^\s]+)", line)
        if match:
            name = match.group(1)
            system_path(name)
            paths.add(name)
    if not paths:
        raise ValueError("runtime_asset")
    return sorted(paths)


def base(policy, pure):
    args = ["/usr/bin/bwrap", "--unshare-user", "--unshare-pid", "--unshare-ipc",
            "--unshare-uts", "--unshare-net", "--unshare-cgroup", "--disable-userns",
            "--assert-userns-disabled", "--uid", str(os.getuid()), "--gid", str(os.getgid()),
            "--cap-drop", "ALL", "--new-session", "--die-with-parent", "--clearenv",
            "--setenv", "LANG", "C.UTF-8", "--setenv", "LC_ALL", "C.UTF-8",
            "--setenv", "PATH", "/usr/bin:/bin", "--proc", "/proc", "--dev", "/dev",
            "--size", str((8 if pure else 4) * 1024 * 1024), "--tmpfs", "/tmp",
            "--seccomp", str(policy)]
    # Preserve distro aliases in the private root; this exposes no extra host paths.
    for name in ("bin", "sbin", "lib", "lib64"):
        source, target = Path("/" + name), "usr/" + name
        if source.is_symlink() and source.lstat().st_uid == 0 and os.readlink(source) in {target, "/" + target}:
            args += ["--dir", "/" + target, "--symlink", target, "/" + name]
    return args


def mounts(paths):
    arguments = []
    for path in sorted(set(paths)):
        arguments += ["--ro-bind", path, path]
    return arguments


def python_command(assets, package, policy, pure, broker_read=None, broker_write=None):
    executable = "/usr/bin/python3.13"
    if not Path(executable).exists():
        raise RuntimeError("runtime_unavailable")
    args = base(policy, pure)
    # System libraries contain no app configuration. Limit the library mount to the
    # distro ABI directory needed by stdlib extension modules; expose no host binaries.
    architecture = "x86_64-linux-gnu" if os.uname().machine == "x86_64" else "aarch64-linux-gnu"
    system_path("/usr/lib/" + architecture)
    args += mounts([executable, "/usr/lib/python3.13", "/usr/lib/" + architecture,
                    *dependencies(executable)])
    args += ["--ro-bind", str(assets), "/runtime", "--ro-bind", str(package), "/skill", "--chdir", "/skill"]
    if broker_read is not None:
        args += ["--setenv", "TW_BROKER_READ", str(broker_read),
                 "--setenv", "TW_BROKER_WRITE", str(broker_write)]
    return args + ["--", executable, "-I", "-S", "-B", "/runtime/launcher.py"]


def native_command(executable, argv, inputs, policy):
    actual = system_path(executable)
    if not stat.S_ISREG(Path(actual).stat().st_mode):
        raise ValueError("capability_denied")
    args = base(policy, False) + mounts([executable, *dependencies(executable)])
    for source, destination in inputs:
        args += ["--ro-bind", source, destination]
    return args + ["--chdir", "/tmp", "--", executable, *argv]
