"""Read package bytes using bounded descriptor-relative traversal, never links."""
import os
import stat


def source_file(root_fd, name):
    fd = os.dup(root_fd)
    try:
        parts = name.split("/")
        for index, part in enumerate(parts):
            if part in {"", ".", ".."}:
                raise ValueError("package_path")
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK
            if index < len(parts) - 1:
                flags |= os.O_DIRECTORY
            next_fd = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = next_fd
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 8 * 1024 * 1024:
            raise ValueError("package_file")
        return fd
    except BaseException:
        os.close(fd)
        raise


def tree(root, validate_name):
    root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    files, folded = {}, set()
    visited, size = 0, 0

    def walk(fd, prefix, depth):
        nonlocal visited, size
        if depth > 8:
            raise ValueError("package_path")
        with os.scandir(fd) as entries:
            for item in entries:
                visited += 1
                if visited > 512:
                    raise ValueError("package_size")
                name = prefix + item.name
                info = item.stat(follow_symlinks=False)
                if stat.S_ISDIR(info.st_mode):
                    child = os.open(item.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
                    try:
                        walk(child, name + "/", depth + 1)
                    finally:
                        os.close(child)
                    continue
                validate_name(name)
                if name.lower() in folded:
                    raise ValueError("package_file")
                folded.add(name.lower())
                opened = source_file(root_fd, name)
                with os.fdopen(opened, "rb") as stream:
                    data = stream.read(8 * 1024 * 1024 + 1)
                size += len(data)
                files[name] = data
                if len(data) > 8 * 1024 * 1024 or len(files) > 256 or size > 20 * 1024 * 1024:
                    raise ValueError("package_size")
    try:
        walk(root_fd, "", 0)
        return files, size
    finally:
        os.close(root_fd)
