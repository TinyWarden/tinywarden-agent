"""Declarative requests must satisfy package, administrator and local ceilings."""
import re

PSEUDO = {"/proc/meminfo", "/proc/loadavg", "/proc/stat", "/proc/uptime", "/proc/sys/kernel/random/boot_id"}
DENIED = ("/home", "/root", "/sys", "/dev", "/etc/shadow", "/etc/gshadow", "/etc/ssh",
          "/etc/ssl/private", "/etc/apt/auth.conf", "/etc/apt/auth.conf.d",
          "/var/lib/tinywarden", "/var/lib/tinywarden-agent", "/var/run", "/tmp")
PROPERTIES = set("Id LoadState ActiveState UnitFileState LastTriggerUSec NextElapseUSecRealtime "
                 "ConditionResult ConditionTimestamp Result ExecMainCode ExecMainStatus "
                 "ExecMainStartTimestamp ExecMainExitTimestamp SubState Description".split())


def helper_path(path):
    if not isinstance(path, str) or len(path) > 512:
        return False
    if any(part in {"", ".", ".."} for part in path.split("/")[1:]):
        return False
    pattern = r"/usr/(?:bin|sbin)/[A-Za-z0-9_.+-]{1,64}|/usr/lib/(?:[A-Za-z0-9_.+-]{1,64}/)*[A-Za-z0-9_.+-]{1,64}"
    return bool(re.fullmatch(pattern, path)) and path.rsplit("/", 1)[1] not in {
        "sh", "bash", "dash", "zsh", "sudo", "su", "env"}


def safe_path(path, root=False):
    if not isinstance(path, str) or not path.startswith("/") or len(path) > 512:
        return False
    if any(part in {"", ".", ".."} for part in path.split("/")[1:]) or any(ord(c) < 32 for c in path):
        return False
    if path in {"/", "/usr", "/etc", "/var", "/var/lib", "/var/cache", "/run", "/proc"}:
        return False
    if any(path == prefix or path.startswith(prefix + "/") for prefix in DENIED):
        return False
    if path.startswith("/proc/"):
        return not root and path in PSEUDO
    if path.startswith("/run/"):
        return not root and path in {"/run/reboot-required", "/run/reboot-required.pkgs"}
    return True


def inspect_grant(grant):
    operation = grant.get("operation")
    if operation in {"files.read", "files.stat", "files.list"}:
        allowed = {"operation", "paths", "roots", "max_bytes", "max_entries"}
        if set(grant) - allowed or not {"paths", "roots"} <= grant.keys():
            raise ValueError("package_capabilities")
        for key in ("paths", "roots"):
            if not isinstance(grant[key], list) or len(grant[key]) > 32:
                raise ValueError("package_capabilities")
            if any(not safe_path(p, key == "roots") for p in grant[key]):
                raise ValueError("package_capabilities")
        for key, bound in (("max_bytes", 65536), ("max_entries", 128)):
            if key in grant and (type(grant[key]) is not int or not 1 <= grant[key] <= bound):
                raise ValueError("package_capabilities")
    elif operation == "filesystems.snapshot":
        if set(grant) != {"operation"}:
            raise ValueError("package_capabilities")
    elif operation == "systemd.properties":
        if set(grant) != {"operation", "units", "properties"} or not 1 <= len(grant["units"]) <= 16:
            raise ValueError("package_capabilities")
        if any(not re.fullmatch(r"[A-Za-z0-9_.@-]{1,128}\.(service|timer)", x) for x in grant["units"]):
            raise ValueError("package_capabilities")
        if not isinstance(grant["properties"], list) or not 1 <= len(grant["properties"]) <= 32 or not set(grant["properties"]) <= PROPERTIES:
            raise ValueError("package_capabilities")
    elif operation == "command.capture":
        required = {"operation", "executable", "argv", "inputs", "timeout_seconds"}
        if not required <= set(grant) or set(grant) - required - {"helpers", "empty_directories"}:
            raise ValueError("package_capabilities")
        exe = grant["executable"]
        if not re.fullmatch(r"/usr/(bin|sbin)/[A-Za-z0-9_.+-]{1,64}", exe) or exe.rsplit("/", 1)[1] in {"sh", "bash", "dash", "zsh", "sudo", "su", "env"}:
            raise ValueError("package_capabilities")
        if not isinstance(grant["inputs"], list) or len(grant["inputs"]) > 16 or any(not safe_path(x, True) or any(denied.startswith(x.rstrip('/') + '/') for denied in DENIED) for x in grant["inputs"]):
            raise ValueError("package_capabilities")
        for key in ("helpers", "empty_directories"):
            values = grant.get(key, [])
            if not isinstance(values, list) or len(values) > 16 or len(set(values)) != len(values):
                raise ValueError("package_capabilities")
            if key == "helpers":
                if any(not helper_path(x) for x in values):
                    raise ValueError("package_capabilities")
            elif any(not safe_path(x, True) or any(denied.startswith(x.rstrip('/') + '/') for denied in DENIED) for x in values):
                raise ValueError("package_capabilities")
        if type(grant["timeout_seconds"]) is not int or not 1 <= grant["timeout_seconds"] <= 30:
            raise ValueError("package_capabilities")
        if not isinstance(grant["argv"], list) or not 1 <= len(grant["argv"]) <= 16:
            raise ValueError("package_capabilities")
        for alternative in grant["argv"]:
            if not isinstance(alternative, list) or len(alternative) > 32:
                raise ValueError("package_capabilities")
            for slot in alternative:
                if isinstance(slot, str):
                    if len(slot) > 512 or "\x00" in slot:
                        raise ValueError("package_capabilities")
                elif not isinstance(slot, dict) or slot.get("type") not in {"integer", "enum", "path"}:
                    raise ValueError("package_capabilities")
                elif slot["type"] == "integer":
                    if set(slot) != {"type", "minimum", "maximum"} or type(slot["minimum"]) is not int or type(slot["maximum"]) is not int or not -1000000 <= slot["minimum"] <= slot["maximum"] <= 1000000:
                        raise ValueError("package_capabilities")
                else:
                    values = slot.get("values")
                    if set(slot) != {"type", "values"} or not isinstance(values, list) or not 1 <= len(values) <= 32:
                        raise ValueError("package_capabilities")
                    if any(not isinstance(x, str) or len(x) > 512 or (slot["type"] == "path" and not safe_path(x)) for x in values):
                        raise ValueError("package_capabilities")
    else:
        raise ValueError("package_capabilities")


def slot_matches(slot, argument):
    if isinstance(slot, str):
        return slot == argument
    if slot["type"] in {"enum", "path"}:
        return argument in slot["values"]
    if not re.fullmatch(r"-?(0|[1-9][0-9]*)", argument):
        return False
    return slot["minimum"] <= int(argument) <= slot["maximum"]


def permits(grant, operation, args):
    if grant["operation"] != operation:
        return False
    if operation.startswith("files."):
        allowed = {"path", "max_bytes", "max_entries"} | ({"optional"} if operation == "files.read" else set())
        if set(args) - allowed or "optional" in args and type(args["optional"]) is not bool or not safe_path(args.get("path")):
            return False
        path = args["path"]
        if path not in grant["paths"] and not any(path.startswith(root + "/") for root in grant["roots"]):
            return False
        return all(type(args.get(key, bound)) is int and 1 <= args.get(key, bound) <= grant.get(key, bound)
                   for key, bound in (("max_bytes", 65536), ("max_entries", 128)))
    if operation == "filesystems.snapshot":
        return args == {}
    if operation == "systemd.properties":
        return set(args) == {"unit", "properties"} and args["unit"] in grant["units"] and isinstance(args["properties"], list) and 1 <= len(args["properties"]) <= 32 and set(args["properties"]) <= set(grant["properties"])
    if operation == "command.capture":
        if not {"executable", "argv", "inputs"} <= set(args) or set(args) - {"executable", "argv", "inputs", "helpers", "empty_directories"} or args["executable"] != grant["executable"]:
            return False
        if not isinstance(args["argv"], list) or any(not isinstance(x, str) or len(x) > 512 for x in args["argv"]):
            return False
        if not isinstance(args["inputs"], list) or not set(args["inputs"]) <= set(grant["inputs"]):
            return False
        for key in ("helpers", "empty_directories"):
            if not isinstance(args.get(key, []), list) or not set(args.get(key, [])) <= set(grant.get(key, [])):
                return False
        return any(len(alt) == len(args["argv"]) and all(slot_matches(s, a) for s, a in zip(alt, args["argv"])) for alt in grant["argv"])
    return False
