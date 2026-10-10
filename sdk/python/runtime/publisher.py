"""Freeze validated source into a private content store without following links."""
import fcntl
import os
import shutil
import stat
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from json_values import decode, encode
from packages import content, load


from package_files import source_file

def publish(request):
    source, store = Path(request["package"]), Path(request["store"])
    if source.is_symlink() or store.is_symlink() or not store.is_absolute():
        raise ValueError("package_path")
    store.mkdir(mode=0o700, parents=True, exist_ok=True)
    mode = store.stat()
    if mode.st_uid != os.getuid() or mode.st_mode & 0o077:
        raise ValueError("package_store")
    with open(store / ".lock", "a+b") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        files, expected, _ = content(source)
        if request.get("content_sha256") and request["content_sha256"] != expected:
            raise ValueError("package_digest")
        root_fd = os.open(source, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        staging = Path(tempfile.mkdtemp(prefix=".staging-", dir=request.get("workspace", store)))
        try:
            total = 0
            for name in files:
                fd = source_file(root_fd, name)
                try:
                    with os.fdopen(fd, "rb") as opened:
                        data = opened.read(8 * 1024 * 1024 + 1)
                    total += len(data)
                    if len(data) > 8 * 1024 * 1024 or total > 20 * 1024 * 1024:
                        raise ValueError("package_size")
                    target = staging / name
                    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                    with target.open("xb") as output:
                        output.write(data)
                        output.flush()
                        os.fsync(output.fileno())
                except BaseException:
                    # fdopen owns fd after construction.
                    raise
            metadata = load(staging, expected, request.get("official") is True)
            final = store / expected
            versions = [p for p in store.iterdir() if p.is_dir() and len(p.name) == 64]
            used = sum(p.stat().st_size for version in versions for p in version.rglob("*") if p.is_file())
            used += sum(p.stat().st_size for p in (store / ".archives").glob("*.zip"))
            transient = sum(p.stat().st_size for workspace in store.glob(".admission-*")
                            if workspace.is_dir() for p in workspace.rglob("*") if p.is_file())
            if not final.exists() and (len(versions) >= 100 or used + transient + metadata["size"] + 10 * 1024 * 1024 > 1024 * 1024 * 1024):
                raise ValueError("package_quota")
            from archives import canonical_archive
            metadata["archive"] = canonical_archive(store, expected, files, request.get("workspace"))
            if final.exists():
                load(final, expected, request.get("official") is True)
                return metadata
            for item in staging.rglob("*"):
                item.chmod(0o500 if item.is_dir() else 0o400)
            os.rename(staging, final)
            final.chmod(0o500)
            directory_fd = os.open(store, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
            return metadata
        finally:
            os.close(root_fd)
            if staging.exists():
                for item in staging.rglob("*"):
                    if item.is_dir():
                        item.chmod(0o700)
                staging.chmod(0o700)
                shutil.rmtree(staging)


if __name__ == "__main__":
    try:
        result = publish(decode(sys.stdin.buffer.read(65537), 65536))
    except (ValueError, TypeError, KeyError, OSError, RecursionError, SyntaxError):
        result = {"error": "package_rejected"}
    sys.stdout.buffer.write(encode(result))
