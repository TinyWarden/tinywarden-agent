"""Descriptor-relative no-follow access, including intermediate path components."""
import os
import stat
from grants import safe_path


def open_path(path, directory=False):
    if not safe_path(path, directory):
        raise RuntimeError("capability_denied")
    parts = path.split("/")[1:]
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        for index, part in enumerate(parts):
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK
            if index < len(parts) - 1 or directory:
                flags |= os.O_DIRECTORY
            child = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = child
        info = os.fstat(fd)
        if not (stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)):
            raise RuntimeError("capability_denied")
        return fd
    except BaseException:
        os.close(fd)
        raise


def observe(operation, arguments):
    path = arguments["path"]
    directory = operation == "files.list"
    try:
        fd = open_path(path, directory)
    except FileNotFoundError:
        if operation == "files.stat":
            return {"exists": False}
        raise RuntimeError("observation_unavailable") from None
    try:
        info = os.fstat(fd)
        if operation == "files.stat":
            return {"exists": True, "size": min(info.st_size, (1 << 53) - 1)}
        if operation == "files.list":
            maximum = arguments.get("max_entries", 128)
            names = []
            with os.scandir(fd) as entries:
                for entry in entries:
                    names.append(entry.name)
                    if len(names) > 4096 or len(entry.name) > 255:
                        raise RuntimeError("broker_limit")
            names.sort()
            return {"entries": names[:maximum], "truncated": len(names) > maximum}
        maximum = arguments.get("max_bytes", 65536)
        data = os.read(fd, maximum + 1)
        return {"text": data[:maximum].decode("utf-8", errors="strict"), "truncated": len(data) > maximum}
    finally:
        os.close(fd)
