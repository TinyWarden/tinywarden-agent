"""Fixed read-only system-manager service adapter; never exposes unit actions."""
import re
import time
from pathlib import Path
from capture import capture
from json_values import decode, encode

PROPERTIES = {"Id", "Names", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState",
              "Type", "RemainAfterExit", "TriggeredBy", "Result", "ExecMainCode", "ExecMainStatus"}
BASE = ["/usr/bin/systemctl", "--system", "--no-pager", "--no-ask-password", "--all"]
NAME = re.compile(r"(?:[A-Za-z0-9:_.@-]|\\x[0-9a-fA-F]{2})+\.service\Z")


def unit_name(value):
    return isinstance(value, str) and len(value.encode()) <= 255 and not value.startswith("-") and bool(NAME.fullmatch(value))


def shortened(value, limit=256):
    return value.encode("utf-8")[:limit].decode("utf-8", errors="ignore")


def query(arguments, group, deadline, maximum=262144):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise RuntimeError("deadline_exceeded")
    return capture(BASE + arguments, b"", group, remaining, output_limit=maximum)


def parse_properties(data):
    rows = []
    for block in data.decode("utf-8").strip().split("\n\n"):
        if not block:
            continue
        row = {}
        for line in block.splitlines():
            key, separator, value = line.partition("=")
            if not separator or key not in PROPERTIES or key in row or len(value.encode()) > 8192 or any(ord(c) < 32 or ord(c) == 127 for c in value):
                raise ValueError("service_properties")
            row[key] = value
        if set(row) != PROPERTIES or not unit_name(row["Id"]):
            raise ValueError("service_properties")
        row["Description"] = shortened(row["Description"])
        rows.append(row)
    return rows


def read_units(units, group, deadline):
    result = []
    for offset in range(0, len(units), 16):
        batch = units[offset:offset + 16]
        templates = [u for u in batch if u.endswith("@.service")]
        result.extend({"requested": u, "available": False, "reason": "template", "properties": {}} for u in templates)
        batch = [u for u in batch if u not in templates]
        if not batch:
            continue
        code, out, _ = query(["show", "--property=" + ",".join(sorted(PROPERTIES)), "--", *batch], group, deadline)
        try:
            rows = parse_properties(out)
        except (ValueError, UnicodeError):
            rows = []
        for requested in batch:
            matches = [r for r in rows if r["Id"] == requested or requested in r["Names"].split()]
            row = matches[0] if matches and all(r == matches[0] for r in matches) else None
            if row is None and len(batch) > 1:
                result.extend(read_units([requested], group, deadline))
                continue
            # A batch may fail for another operand; complete properties stand alone.
            if row is None:
                result.append({"requested": requested, "available": False, "reason": "unreadable", "properties": {}})
            else:
                result.append({"requested": requested, "available": True, "reason": None, "properties": row})
    return result


def run(operation, args, group, timeout):
    # Optional discovery ends before its worker deadline, retaining selected reads.
    deadline = time.monotonic() + (min(8, max(.1, timeout - .5)) if operation == "systemd.services.list" else timeout)
    boot_id = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    uptime = int(float(Path("/proc/uptime").read_text().split()[0]) * 1000)
    if operation == "systemd.services.read":
        return {"boot_id": boot_id, "uptime_ms": uptime, "items": read_units(args["units"], group, deadline)}
    names, complete, reason = set(), True, None
    for command, key in (("list-unit-files", "unit_file"), ("list-units", "unit")):
        try:
            code, out, _ = query([command, "--type=service", "--output=json"], group, deadline)
        except RuntimeError as error:
            if str(error) not in {"output_exceeded", "deadline_exceeded"}:
                raise
            complete, reason = False, "limit" if str(error) == "output_exceeded" else "unavailable"
            if str(error) == "deadline_exceeded":
                break
            continue
        if code != 0:
            complete, reason = False, "unavailable"
            continue
        try:
            values = decode(out, 262144)
            if not isinstance(values, list) or any(not isinstance(v, dict) or not unit_name(v.get(key)) for v in values):
                raise ValueError("service_inventory")
            names.update(v[key] for v in values)
        except (ValueError, UnicodeError):
            complete, reason = False, "unavailable"
    if len(names) > 512:
        complete, reason = False, "limit"
    try:
        rows = read_units(sorted(names)[:512], group, deadline)
    except RuntimeError as error:
        if str(error) not in {"output_exceeded", "deadline_exceeded"}:
            raise
        rows, complete, reason = [], False, "limit" if str(error) == "output_exceeded" else "unavailable"
    normalized, seen = [], set()
    for item in rows:
        if not item["available"]:
            if item["reason"] == "template":
                normalized.append(item)
            else:
                complete, reason = False, reason or "unavailable"
            continue
        props = item["properties"]
        if props["Id"] in seen:
            continue
        seen.add(props["Id"])
        candidate = {"requested": props["Id"], "available": True, "reason": None, "properties": props}
        if len(encode(normalized + [candidate])) > 245760:
            complete, reason = False, "limit"
            break
        normalized.append(candidate)
    return {"boot_id": boot_id, "uptime_ms": uptime, "complete": complete, "reason": reason, "items": normalized}
