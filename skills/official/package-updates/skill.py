"""Bounded, read-only APT plan. Cached indexes are not a freshness guarantee."""
import re

INPUTS = ["/etc/apt/sources.list", "/etc/apt/sources.list.d", "/etc/apt/preferences", "/etc/apt/preferences.d",
          "/var/lib/apt/lists", "/var/lib/apt/extended_states", "/var/lib/dpkg/status", "/usr/share/dpkg"]
SUMMARY = re.compile(r"^(0|[1-9][0-9]{0,6}) upgraded, (0|[1-9][0-9]{0,6}) newly installed, (0|[1-9][0-9]{0,6}) to remove and (0|[1-9][0-9]{0,6}) not upgraded\.$")


def validate_settings(settings):
    return []


def collect(settings, host):
    argv = ["--simulate", "upgrade"] if settings["package_mode"] == "upgrade" else ["--simulate", "--with-new-pkgs", "upgrade"]
    result = host.request("command.capture", {"executable": "/usr/bin/apt-get", "argv": argv, "inputs": INPUTS,
                                            "helpers": ["/usr/bin/dpkg"], "empty_directories": ["/etc/apt/apt.conf.d"]})
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
    return {"problem": "none", "mode": settings["package_mode"], **dict(zip(["upgraded", "installed", "removed", "held_back"], counts)),
            "index_freshness": "unverified", "state_consistency": "unverified"}


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
    return [{"from": context["now"], "status": "warning" if changes else "healthy", "reason": {"key": reason, "params": {}}, "facts": facts}]
