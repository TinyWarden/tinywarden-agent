"""Copy a working starter and package exact, statically admitted source bytes."""
import json
import os
import re
import shutil
import tempfile
from contextlib import contextmanager
from pathlib import Path

from archives import canonical_archive, extract
from json_values import decode
from packages import content, load

ROOT = Path(__file__).resolve().parents[3]
STARTER = ROOT / "skills/examples/memory-pressure"


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def initialize(destination, identity, publisher):
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}/[a-z][a-z0-9-]{0,63}", identity) or identity.startswith("tinywarden/"):
        raise ValueError("author_id")
    if not isinstance(publisher, str) or not 1 <= len(publisher) <= 200:
        raise ValueError("author_publisher")
    destination = Path(destination).absolute()
    # Refuse even an empty existing directory; cleanup can only remove our own work.
    destination.mkdir(mode=0o700)
    try:
        files, _, _ = content(STARTER)
        for name, data in files.items():
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
        manifest = decode((destination / "skill.json").read_bytes())
        manifest.update(id=identity, publisher=publisher)
        write_json(destination / "skill.json", manifest)
        shutil.copyfile(ROOT / "sdk/python/authoring/starter-cases.json", destination / "tests.json")
        return load(destination)
    except BaseException:
        shutil.rmtree(destination)
        raise


@contextmanager
def snapshot(source):
    """Pin one checked byte set; never import authored code or follow source links."""
    with tempfile.TemporaryDirectory(prefix="tw-author-") as temporary:
        directory = Path(temporary) / "package"
        directory.mkdir(mode=0o700)
        source = Path(source).absolute()
        if source.is_dir() and not source.is_symlink():
            files, _, _ = content(source)
            for name, data in files.items():
                target = directory / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
        else:
            extract(source, directory)
        yield directory, load(directory)


def summary(metadata):
    manifest = metadata["manifest"]
    return {"id": manifest["id"], "version": manifest["version"],
            "runtime": manifest["runtime"], "sdk": manifest["sdk"],
            "content_sha256": metadata["content_sha256"], "unpacked_bytes": metadata["size"],
            "capabilities": manifest["capabilities"], "compatibility": manifest["compatibility"]}


def create_zip(directory, metadata, destination):
    destination = Path(destination).absolute()
    if directory == destination or directory in destination.parents:
        raise ValueError("archive_output")
    files, digest, _ = content(directory)
    if digest != metadata["content_sha256"]:
        raise ValueError("package_changed")
    with tempfile.TemporaryDirectory(prefix="tw-author-zip-") as temporary:
        store = Path(temporary)
        archive = canonical_archive(store, digest, files)
        path = store / ".archives" / (digest + ".zip")
        # Exercise the production ZIP reader and loader against the output once.
        with snapshot(path) as (_, packed):
            if packed["content_sha256"] != digest:
                raise ValueError("archive_digest")
        data = path.read_bytes()
        exclusive_write(destination, data)
    return {**summary(metadata), "archive_sha256": archive["sha256"], "archive_bytes": len(data)}


def exclusive_write(path, data):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC, 0o600)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
    except BaseException:
        Path(path).unlink(missing_ok=True)
        raise
