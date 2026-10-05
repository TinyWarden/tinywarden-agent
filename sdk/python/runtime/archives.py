"""Strict, bounded ZIP transport. No package code runs during extraction."""
import hashlib
import os
import re
import shutil
import stat
import struct
import sys
import tempfile
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from json_values import decode, encode
from packages import path_name

MAX_ARCHIVE = 10 * 1024 * 1024


def canonical_archive(store, digest, files):
    """Freeze one deterministic container per content identity; ZIP identity is separate."""
    folder = store / ".archives"
    folder.mkdir(mode=0o700, exist_ok=True)
    if folder.is_symlink() or folder.stat().st_uid != os.getuid() or folder.stat().st_mode & 0o077:
        raise ValueError("package_store")
    final = folder / (digest + ".zip")
    if not final.exists():
        fd, name = tempfile.mkstemp(prefix=".zip-", dir=folder)
        staging = Path(name)
        try:
            with os.fdopen(fd, "w+b") as stream:
                with zipfile.ZipFile(stream, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6, allowZip64=False) as archive:
                    for path, data in sorted(files.items()):
                        entry = zipfile.ZipInfo(path, (1980, 1, 1, 0, 0, 0))
                        entry.compress_type = zipfile.ZIP_DEFLATED
                        entry.external_attr = (stat.S_IFREG | 0o400) << 16
                        archive.writestr(entry, data)
                        if stream.tell() > MAX_ARCHIVE:
                            raise ValueError("archive_size")
                stream.flush()
                os.fsync(stream.fileno())
            if staging.stat().st_size > MAX_ARCHIVE:
                raise ValueError("archive_size")
            staging.chmod(0o400)
            os.rename(staging, final)
            fd = os.open(folder, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        finally:
            staging.unlink(missing_ok=True)
    fd = os.open(final, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or not 1 <= info.st_size <= MAX_ARCHIVE:
            raise ValueError("archive_size")
        data = stream.read(MAX_ARCHIVE + 1)
    return {"sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}


def extra_fields(data):
    while data:
        if len(data) < 4:
            raise ValueError("archive_extra")
        tag, length = struct.unpack_from("<HH", data)
        if length > len(data) - 4 or tag in {1, 0x7075}:
            raise ValueError("archive_extra")
        data = data[4 + length:]


def records(data):
    # No ZIP64, multiple disks, prepended executables, overlapping records or trailing payload.
    end = data.rfind(b"PK\x05\x06", max(0, len(data) - 65557))
    if end < 0 or end + 22 > len(data):
        raise ValueError("archive_end")
    _, disk, central_disk, disk_count, count, size, offset, comment = struct.unpack_from("<4s4H2IH", data, end)
    if disk or central_disk or disk_count != count or not 1 <= count <= 512 or offset + size != end or end + 22 + comment != len(data):
        raise ValueError("archive_end")
    pos, result, names, spelling = offset, [], set(), {}
    for _ in range(count):
        if pos + 46 > end:
            raise ValueError("archive_record")
        h = struct.unpack_from("<4s6H3I5H2I", data, pos)
        sig, made, version, flags, method, tm, date, crc, compressed, unpacked, nlen, elen, clen, start_disk, internal, external, local = h
        finish = pos + 46 + nlen + elen + clen
        if sig != b"PK\x01\x02" or finish > end or start_disk or version > 20 or flags & ~0x80E or method not in {0, 8} or method == 0 and flags & 6:
            raise ValueError("archive_record")
        raw_name = data[pos + 46:pos + 46 + nlen]
        name = raw_name.decode("ascii")
        directory = name.endswith("/")
        normalized = name[:-1] if directory else name
        if not normalized or not re.fullmatch(r"[A-Za-z0-9_.\-/]+", normalized) or len(normalized) > 200 or len(normalized.split("/")) > 8 or any(p in {"", ".", ".."} for p in normalized.split("/")) or normalized.lower() in names:
            raise ValueError("archive_path")
        names.add(normalized.lower())
        for i in range(1, len(normalized.split("/")) + 1):
            prefix = "/".join(normalized.split("/")[:i])
            if spelling.setdefault(prefix.lower(), prefix) != prefix:
                raise ValueError("archive_path")
        mode = external >> 16 if made >> 8 == 3 else 0
        kind = stat.S_IFMT(mode)
        if kind not in ({0, stat.S_IFDIR} if directory else {0, stat.S_IFREG}) or external & 0x10 and not directory:
            raise ValueError("archive_file")
        if directory and (unpacked or crc) or unpacked > 8 * 1024 * 1024:
            raise ValueError("archive_size")
        if not directory:
            path_name(name)
        extra_fields(data[pos + 46 + nlen:pos + 46 + nlen + elen])
        result.append((local, raw_name, directory, version, flags, method, tm, date, crc, compressed, unpacked))
        pos = finish
    if pos != end or sum(not row[2] for row in result) > 256:
        raise ValueError("archive_size")
    boundary = 0
    for local, name, directory, version, flags, method, tm, date, crc, compressed, unpacked in sorted(result):
        if local != boundary or local + 30 > offset:
            raise ValueError("archive_overlap")
        h = struct.unpack_from("<4s5H3I2H", data, local)
        sig, lv, lf, lm, lt, ld, lc, lcs, lus, nl, el = h
        begin = local + 30 + nl + el
        if sig != b"PK\x03\x04" or (lv, lf, lm, lt, ld) != (version, flags, method, tm, date) or begin + compressed > offset or data[local + 30:local + 30 + nl] != name:
            raise ValueError("archive_local")
        extra_fields(data[local + 30 + nl:begin])
        boundary = begin + compressed
        if flags & 8:
            if (lc, lcs, lus) not in {(0, 0, 0), (crc, compressed, unpacked)}:
                raise ValueError("archive_descriptor")
            if data[boundary:boundary + 4] == b"PK\x07\x08":
                boundary += 4
            if boundary + 12 > offset or struct.unpack_from("<3I", data, boundary) != (crc, compressed, unpacked):
                raise ValueError("archive_descriptor")
            boundary += 12
        elif (lc, lcs, lus) != (crc, compressed, unpacked):
            raise ValueError("archive_local")
    if boundary != offset:
        raise ValueError("archive_overlap")
    return result


def extract(source, staging, expected=None):
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as opened:
        info = os.fstat(opened.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or not 1 <= info.st_size <= MAX_ARCHIVE:
            raise ValueError("archive_size")
        data = opened.read(MAX_ARCHIVE + 1)
    if len(data) > MAX_ARCHIVE or expected and hashlib.sha256(data).hexdigest() != expected:
        raise ValueError("archive_digest")
    rows = records(data)
    import io
    total = 0
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        if len(archive.infolist()) != len(rows):
            raise ValueError("archive_record")
        for row, entry in zip(rows, archive.infolist()):
            name, directory = row[1].decode("ascii"), row[2]
            if entry.orig_filename != name or entry.header_offset != row[0]:
                raise ValueError("archive_record")
            target = staging / name
            if directory:
                target.mkdir(mode=0o700, parents=True, exist_ok=True)
                continue
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            count = 0
            with archive.open(entry) as stream, target.open("xb") as output:
                while chunk := stream.read(65536):
                    count += len(chunk)
                    total += len(chunk)
                    if count > 8 * 1024 * 1024 or total > 20 * 1024 * 1024:
                        raise ValueError("archive_size")
                    output.write(chunk)
            if count != row[10]:
                raise ValueError("archive_size")


def publish_archive(request):
    from publisher import publish
    store = Path(request["store"])
    store.mkdir(mode=0o700, parents=True, exist_ok=True)
    if store.is_symlink() or store.stat().st_uid != os.getuid() or store.stat().st_mode & 0o077:
        raise ValueError("package_store")
    staging = Path(tempfile.mkdtemp(prefix=".incoming-", dir=store))
    try:
        extract(request["archive"], staging, request.get("archive_sha256"))
        return publish({**request, "package": str(staging)})
    finally:
        shutil.rmtree(staging)


if __name__ == "__main__":
    try:
        result = publish_archive(decode(sys.stdin.buffer.read(65537), 65536))
    except (ValueError, TypeError, KeyError, OSError, RecursionError, SyntaxError, UnicodeError, zipfile.BadZipFile):
        result = {"error": "package_rejected"}
    sys.stdout.buffer.write(encode(result))
