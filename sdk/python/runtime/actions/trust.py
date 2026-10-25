"""Root-owned local ceiling and pinned package checks before any action import."""
import os
from pathlib import Path
import stat
from actions.descriptor import exact
from actions.envelope import invocation, matches, COUNTER
from actions.schema import validate
from contracts.json_values import decode
from contracts.schema import validate as validate_settings
from packages.admission import content, load


def protected_read(path, limit, directory=False):
    path = Path(path)
    if not path.is_absolute() or str(path) != os.path.normpath(path):
        raise ValueError("action_path_untrusted")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        for index, part in enumerate(path.parts[1:]):
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK
            if index < len(path.parts) - 2 or directory:
                flags |= os.O_DIRECTORY
            next_fd = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = next_fd
            info = os.fstat(fd)
            if info.st_uid != 0 or info.st_mode & 0o022:
                raise ValueError("action_path_untrusted")
        info = os.fstat(fd)
        if directory:
            if not stat.S_ISDIR(info.st_mode):
                raise ValueError("action_path_untrusted")
            return None
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
            raise ValueError("action_path_untrusted")
        data = os.read(fd, limit + 1)
        if len(data) > limit:
            raise ValueError("action_path_untrusted")
        return data
    finally:
        os.close(fd)


def read_policy(path):
    value = decode(protected_read(path, 65536), 65536)
    exact(value, {"schema_version", "origins"})
    if type(value["schema_version"]) is not int or value["schema_version"] != 1:
        raise ValueError("action_policy_invalid")
    if not isinstance(value["origins"], list) or len(value["origins"]) > 64:
        raise ValueError("action_policy_invalid")
    seen = set()
    for origin in value["origins"]:
        exact(origin, {"github_id", "owner_id", "classes"})
        if any(not matches(COUNTER, origin[key]) for key in ("github_id", "owner_id")):
            raise ValueError("action_policy_invalid")
        key = (origin["github_id"], origin["owner_id"])
        classes = origin["classes"]
        if (key in seen or not isinstance(classes, list) or
                any(not isinstance(c, str) or c not in {"unprivileged", "host_admin"} for c in classes) or
                len(classes) != len(set(classes))):
            raise ValueError("action_policy_invalid")
        seen.add(key)
    return value


def admit(package, policy, request):
    if os.geteuid() != 0:
        raise ValueError("action_root_supervisor_required")
    arguments = invocation(request)
    bound = request["binding"]
    protected_read(package, 0, directory=True)
    # Admission reads syntax/metadata only; it imports no authored module.
    metadata = load(package, expected=bound["content_sha256"], official=True)
    files, digest, _ = content(package)
    if digest != bound["content_sha256"] or metadata["manifest"]["id"] != bound["skill_id"]:
        raise ValueError("action_binding")
    for name, data in files.items():
        if protected_read(Path(package) / name, len(data)) != data:
            raise ValueError("action_path_untrusted")
    candidates = [a for a in metadata.get("actions", []) if
                  a["descriptor"]["id"] == bound["action_id"] and
                  a["descriptor"]["version"] == bound["action_version"] and
                  a["descriptor_sha256"] == bound["descriptor_sha256"]]
    if len(candidates) != 1:
        raise ValueError("action_binding")
    action = candidates[0]
    ceiling = read_policy(policy)
    origin = bound["origin"]
    if not any(o["github_id"] == origin["github_id"] and o["owner_id"] == origin["owner_id"] and
               action["descriptor"]["execution_class"] in o["classes"] for o in ceiling["origins"]):
        raise ValueError("action_permission_required")
    validate_settings(metadata["schemas"]["settings"], arguments["settings"])
    validate(action["schemas"]["input"], arguments["input"])
    if arguments["plan"] is not None:
        validate(action["schemas"]["plan"], arguments["plan"])
    return action, metadata, files, arguments
