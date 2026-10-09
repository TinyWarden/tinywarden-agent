"""Bounded, read-only APT plan. Cached indexes are not a freshness guarantee."""
import re

INPUTS = ["/etc/apt/sources.list", "/etc/apt/sources.list.d", "/etc/apt/preferences", "/etc/apt/preferences.d",
          "/var/lib/apt/lists", "/var/lib/apt/extended_states", "/var/lib/dpkg/status", "/usr/share/dpkg", "/etc/apt/mirrors"]
HELPERS = ["/usr/bin/dpkg", "/usr/lib/apt/methods/mirror+file", "/usr/lib/apt/methods/file",
           "/usr/lib/apt/methods/http", "/usr/lib/apt/methods/https"]
SUMMARY = re.compile(r"^(0|[1-9][0-9]{0,6}) upgraded, (0|[1-9][0-9]{0,6}) newly installed, (0|[1-9][0-9]{0,6}) to remove and (0|[1-9][0-9]{0,6}) not upgraded\.$")
NAME = r"[a-z0-9][a-z0-9+.-]*(?::[a-z0-9][a-z0-9-]*)?"
VERSION = r"[A-Za-z0-9.+:~_-]+"
INSTALL = re.compile(rf"^Inst ({NAME})(?: \[({VERSION})\])? \(({VERSION})(?: .*)?\)$")
REMOVE = re.compile(rf"^Remv ({NAME})(?: \[({VERSION})\])?(?: .*)?$")
ROW_LIMIT = 100


def package_details(lines, counts):
    """Only extract APT's action records; never publish arbitrary command output."""
    rows, seen, found = [], set(), dict.fromkeys(("upgraded", "installed", "removed", "held_back"), 0)
    complete, held = True, False
    for line in lines:
        candidates = []
        if line == "The following packages have been kept back:":
            held = True
            continue
        if held and line.startswith("  "):
            for name in line.split():
                if re.fullmatch(NAME, name):
                    candidates.append((name, "", "", "held_back"))
                else:
                    complete = False
        else:
            held = False
            if line.startswith("Inst "):
                match = INSTALL.fullmatch(line)
                if match:
                    name, before, after = match.groups()
                    candidates.append((name, before or "", after, "upgraded" if before else "installed"))
                else:
                    complete = False
            elif line.startswith("Remv "):
                match = REMOVE.fullmatch(line)
                if match:
                    candidates.append((match[1], match[2] or "", "", "removed"))
                else:
                    complete = False
        for name, before, after, action in candidates:
            if max(len(name), len(before), len(after)) > 128 or (name, action) in seen:
                complete = False
                continue
            seen.add((name, action))
            found[action] += 1
            if len(rows) < ROW_LIMIT:
                rows.append({"package": name, "installed_version": before, "available_version": after, "action": action})
    # A presentation we cannot reconcile must not invent extra package actions.
    rows = [row for row in rows if found[row["action"]] <= counts[row["action"]]]
    complete = complete and found == counts
    return {"packages": rows, "package_list_complete": complete,
            "package_list_truncated": sum(found.values()) > ROW_LIMIT}


def validate_settings(settings):
    return []


def collect(settings, host):
    argv = ["--simulate", "upgrade"] if settings["package_mode"] == "upgrade" else ["--simulate", "--with-new-pkgs", "upgrade"]
    result = host.request("command.capture", {"executable": "/usr/bin/apt-get", "argv": argv, "inputs": INPUTS,
                                            "helpers": HELPERS, "empty_directories": ["/etc/apt/apt.conf.d"]})
    if result["exit_code"] != 0 or result["stderr_bytes"] or not result["stdout"].endswith("\n"):
        return {"problem": "package_output_unavailable"}
    counts = None
    for line in result["stdout"].splitlines():
        if any(ord(char) < 32 and char != "\t" or ord(char) == 127 for char in line) or line.startswith(("E:", "W:")) or "not fully installed or removed" in line:
            return {"problem": "package_output_unavailable"}
        match = SUMMARY.fullmatch(line)
        if match:
            if counts is not None:
                return {"problem": "package_output_unavailable"}
            counts = [int(value) for value in match.groups()]
    if counts is None or sum(counts) > 1000000:
        return {"problem": "package_output_unavailable"}
    counts = dict(zip(["upgraded", "installed", "removed", "held_back"], counts))
    return {"problem": "none", "mode": settings["package_mode"], **counts,
            "index_freshness": "unverified", "state_consistency": "unverified",
            **package_details(result["stdout"].splitlines(), counts)}


def reduce(context):
    return {}


def evaluate(context):
    observation = context["observation"]
    if observation["problem"] != "none":
        return [{"from": context["now"], "status": "unknown", "reason": {"key": observation["problem"], "params": {}}, "facts": []}]
    changes = sum(observation[key] for key in ("upgraded", "installed", "removed", "held_back"))
    reason = "package_removals" if observation["removed"] else "package_changes" if changes else "package_plan_clear"
    facts = [{"key": key, "label_key": key + "_label", "kind": "number", "value": observation[key]}
             for key in ("upgraded", "installed", "removed", "held_back")]
    facts += [{"key": key, "label_key": key + "_label", "kind": "text", "value": observation[key]}
              for key in ("mode", "index_freshness", "state_consistency")]
    facts.append({"key": "packages", "label_key": "package_list_title", "kind": "table",
                  "columns": [{"key": key, "label_key": key + "_label", "kind": "text"}
                              for key in ("package", "installed_version", "available_version", "action")],
                  "rows": observation.get("packages", []), "truncated": observation.get("package_list_truncated", False)})
    notice = "partial" if not observation.get("package_list_complete", False) else ""
    facts.append({"key": "package_list_note", "label_key": "package_list_note_label", "kind": "text", "value": notice})
    return [{"from": context["now"], "status": "warning" if changes else "healthy", "reason": {"key": reason, "params": {}}, "facts": facts}]
