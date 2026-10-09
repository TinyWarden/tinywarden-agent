"""The marker is one local signal, not an all-services reboot assurance."""
import re

MARKER = "/run/reboot-required"
PACKAGES = "/run/reboot-required.pkgs"
NAME = re.compile(r"[a-z0-9][a-z0-9+.-]*(?::[a-z0-9][a-z0-9-]*)?")


def package_list(result):
    if not result["available"]:
        return [], "unavailable", False
    lines = result["text"].split("\n")
    partial = result["truncated"]
    if partial:
        lines = lines[:-1]
    names = set()
    for line in lines:
        name = line.strip(" \t\r")
        if not name:
            continue
        if len(name) > 128 or not NAME.fullmatch(name):
            partial = True
            continue
        names.add(name)
    truncated = len(names) > 100
    partial = partial or truncated
    return sorted(names)[:100], "unavailable" if not names else "partial" if partial else "reported", truncated


def validate_settings(settings):
    return []


def collect(settings, host):
    observed = host.request("files.stat", {"path": MARKER})["exists"]
    packages, status, truncated = [], "not_applicable", False
    if observed:
        result = host.request("files.read", {"path": PACKAGES, "max_bytes": 16384, "optional": True})
        observed = host.request("files.stat", {"path": MARKER})["exists"]
        if observed:
            packages, status, truncated = package_list(result)
    return {"marker_observed": observed, "assurance": "limited", "packages": packages,
            "package_list_status": status, "package_list_truncated": truncated}


def reduce(context):
    return {}


def evaluate(context):
    observation = context["observation"]
    observed = observation["marker_observed"]
    names = observation.get("packages", []) if observed else []
    status = observation.get("package_list_status", "unavailable") if observed else "not_applicable"
    truncated = bool(names) and observation.get("package_list_truncated", False)
    notice = "partial" if names and status == "partial" and not truncated else "unavailable" if observed and not names else ""
    return [{"from": context["now"], "status": "warning" if observed else "healthy",
             "reason": {"key": "reboot_marker_present" if observed else "reboot_marker_absent", "params": {}},
             "facts": [{"key": "marker", "label_key": "marker_label", "kind": "boolean", "value": observed},
                       {"key": "assurance", "label_key": "assurance_label", "kind": "text", "value": observation["assurance"]},
                       {"key": "reboot_request", "label_key": "reboot_request_label", "kind": "text", "value": "requested" if observed else "absent"},
                       {"key": "packages", "label_key": "packages_label", "kind": "table", "truncated": truncated,
                        "columns": [{"key": "package", "label_key": "package_label", "kind": "text"}], "rows": [{"package": name} for name in names]},
                       {"key": "package_notice", "label_key": "package_notice_label", "kind": "text", "value": notice}]}]
