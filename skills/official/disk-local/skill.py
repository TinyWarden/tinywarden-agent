"""Local filesystems; host-view capacity comes from the generic broker snapshot."""
import hashlib
MOUNT_PROBLEMS = {"mount_disappeared", "mount_inaccessible", "mount_unverifiable", "unsupported_type"}
CHECK_PROBLEMS = {"inventory_unavailable", "inventory_malformed", "inventory_overflow", "records_overflow", "topology_changed"}
def validate_settings(settings):
    if settings["warning_percent"] >= settings["critical_percent"]:
        return [{"field": "critical_percent", "message": {"key": "invalid_thresholds", "params": {}}}]
    return []


def collect(settings, host):
    snapshot = host.request("filesystems.snapshot")
    # Missing byte values are represented by absent fields, never guessed zeros.
    snapshot["mounts"] = [{key: value for key, value in mount.items() if value is not None}
                          for mount in snapshot["mounts"] if mount["writable"]]
    return snapshot


def reduce(context):
    return {}


def evaluate(context):
    observation, settings = context["observation"], context["settings"]
    rows, details, issues, status, worst = [], [], [], "healthy", None
    truncated = False
    issues_truncated = False
    for mount in observation["mounts"]:
        if not mount["writable"]:
            continue
        percent = None
        classification = "unknown"
        capacity = None
        if mount["kind"] == "local" and mount["reason"] == "none" and all(key in mount for key in ("total_bytes", "free_bytes", "available_bytes")):
            raw = [mount[key] for key in ("total_bytes", "free_bytes", "available_bytes")]
            if all(value.isdigit() and str(int(value)) == value for value in raw):
                total, free, available = map(int, raw)
                used, denominator = total - free, total - free + available
                if 0 <= available <= free <= total <= (1 << 64) - 1 and denominator > 0:
                    capacity = (used, denominator)
                    percent = (used * 2000 + denominator) // (2 * denominator) / 10
        if capacity and mount["writable"]:
            used, denominator = capacity
            if worst is None or used * worst[2] > worst[3] * denominator:
                worst = (percent, mount, denominator, used)
            if used * 100 >= settings["critical_percent"] * denominator:
                classification = status = "critical"
            elif used * 100 >= settings["warning_percent"] * denominator:
                classification = "warning"
                if status != "critical": status = "warning"
            else:
                classification = "healthy"
        short = lambda value: value.encode("utf-8")[:192].decode("utf-8", "ignore")
        row = {"mount_id": mount["mount_id"], "path": short(mount["mount_path"]),
               "total": mount.get("total_bytes", ""), "free": mount.get("free_bytes", ""), "available": mount.get("available_bytes", ""),
               "classification": classification, "used": "" if percent is None else str(percent),
               "series": hashlib.sha256((mount["mount_path"] + "\0" + mount["mount_root"] + "\0" + mount["filesystem_type"]).encode()).hexdigest()}
        if capacity is None:
            problem = mount["reason"] if mount["reason"] in MOUNT_PROBLEMS else "capacity_unavailable"
            issues.append({"path": row["path"], "problem": problem})
            issues_truncated |= row["path"] != mount["mount_path"]
        details.append({"mount_id": mount["mount_id"], "root": short(mount["mount_root"]),
               "filesystem": mount["filesystem_type"], "kind": mount["kind"], "writable": mount["writable"],
               "shared": mount["shared_capacity"], "reason": mount["reason"]})
        truncated |= row["path"] != mount["mount_path"] or details[-1]["root"] != mount["mount_root"]
        rows.append(row)
    reason = {"key": "disk_usage", "params": {"path": worst[1]["mount_path"][:512], "percent": worst[0]}} if worst else {"key": "disk_empty", "params": {}}
    if status == "healthy" and (observation["coverage"] != "complete" or worst is None):
        status, reason = "unknown", {"key": "disk_incomplete", "params": {}}
    notice = ""
    if observation["coverage"] != "complete":
        notice = observation["reason"] if observation["reason"] in CHECK_PROBLEMS else "incomplete"
    elif not rows:
        notice = "no_filesystems"
    columns = [("mount_id", "number"), ("path", "text"), ("total", "text"), ("free", "text"), ("available", "text"), ("classification", "text"), ("used", "text"), ("series", "text")]
    detail_columns = [("mount_id", "number"), ("root", "text"), ("filesystem", "text"), ("kind", "text"), ("writable", "boolean"), ("shared", "boolean"), ("reason", "text")]
    facts = [{"key": "coverage", "label_key": "coverage_label", "kind": "text", "value": observation["coverage"]},
             {"key": "excluded_kernel", "label_key": "excluded_kernel_label", "kind": "number", "value": observation["excluded_kernel"]},
             {"key": "excluded_remote", "label_key": "excluded_remote_label", "kind": "number", "value": observation["excluded_remote"]},
             {"key": "filesystems", "label_key": "filesystems_label", "kind": "table", "columns": [
                 {"key": key, "label_key": key + "_label", "kind": kind} for key, kind in columns], "rows": rows, "truncated": truncated},
             {"key": "mount_details", "label_key": "mount_details_label", "kind": "table", "columns": [
                 {"key": key, "label_key": key + "_label", "kind": kind} for key, kind in detail_columns], "rows": details, "truncated": truncated},
             {"key": "collection_notice", "label_key": "collection_notice_label", "kind": "text", "value": notice},
             {"key": "collection_issues", "label_key": "collection_issues_title", "kind": "table", "columns": [
                 {"key": "path", "label_key": "path_label", "kind": "text"},
                 {"key": "problem", "label_key": "collection_problem_label", "kind": "text"}],
              "rows": sorted(issues, key=lambda issue: issue["path"].casefold()), "truncated": issues_truncated}]
    if worst:
        mount = worst[1]
        facts.append({"key": "worst_mount", "label_key": "worst_mount_label", "kind": "table", "columns": [
            {"key": key, "label_key": key + "_label", "kind": kind} for key,kind in [("mount_id","number"),("path","text"),("total","text"),("available","text"),("shared","boolean"),("classification","text")]],
            "rows": [{"mount_id":mount["mount_id"],"path":mount["mount_path"][:1024],"total":mount["total_bytes"],"available":mount["available_bytes"],"shared":mount["shared_capacity"],"classification":next(row["classification"] for row in rows if row["mount_id"]==mount["mount_id"])}], "truncated":len(mount["mount_path"])>1024})
    return [{"from": context["now"], "status": status, "reason": reason, "facts": facts}]
