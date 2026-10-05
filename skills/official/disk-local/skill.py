"""Local filesystems; host-view capacity comes from the generic broker snapshot."""
def validate_settings(settings):
    if settings["warning_percent"] >= settings["critical_percent"]:
        return [{"field": "critical_percent", "message": {"key": "invalid_thresholds", "params": {}}}]
    return []


def collect(settings, host):
    snapshot = host.request("filesystems.snapshot")
    # Missing byte values are represented by absent fields, never guessed zeros.
    snapshot["mounts"] = [{key: value for key, value in mount.items() if value is not None}
                          for mount in snapshot["mounts"]]
    return snapshot


def reduce(context):
    return {}


def evaluate(context):
    observation, settings = context["observation"], context["settings"]
    rows, details, status, worst = [], [], "healthy", None
    truncated = False
    for mount in observation["mounts"]:
        percent = None
        classification = "read_only" if not mount["writable"] else "unknown"
        if mount["kind"] == "local" and mount["reason"] == "none" and "total_bytes" in mount and "available_bytes" in mount:
            total, available = int(mount["total_bytes"]), int(mount["available_bytes"])
            if total > 0 and 0 <= available <= total and mount["writable"]:
                percent = (total - available) * 1000 // total / 10
        if percent is not None:
            if worst is None or percent > worst[0]:
                worst = (percent, mount)
            # Integer cross multiplication preserves threshold boundary semantics.
            used = int(mount["total_bytes"]) - int(mount["available_bytes"])
            if used * 100 >= settings["critical_percent"] * int(mount["total_bytes"]):
                classification = status = "critical"
            elif used * 100 >= settings["warning_percent"] * int(mount["total_bytes"]):
                classification = "warning"
                if status != "critical": status = "warning"
            else:
                classification = "healthy"
        short = lambda value: value.encode("utf-8")[:192].decode("utf-8", "ignore")
        row = {"mount_id": mount["mount_id"], "path": short(mount["mount_path"]),
               "total": mount.get("total_bytes", ""), "free": mount.get("free_bytes", ""), "available": mount.get("available_bytes", ""),
               "classification": classification, "used": "" if percent is None else str(percent)}
        details.append({"mount_id": mount["mount_id"], "root": short(mount["mount_root"]),
               "filesystem": mount["filesystem_type"], "kind": mount["kind"], "writable": mount["writable"],
               "shared": mount["shared_capacity"], "reason": mount["reason"]})
        truncated |= row["path"] != mount["mount_path"] or details[-1]["root"] != mount["mount_root"]
        rows.append(row)
    reason = {"key": "disk_usage", "params": {"path": worst[1]["mount_path"][:512], "percent": worst[0]}} if worst else {"key": "disk_empty", "params": {}}
    if status == "healthy" and (observation["coverage"] != "complete" or worst is None):
        status, reason = "unknown", {"key": "disk_incomplete", "params": {}}
    columns = [("mount_id", "number"), ("path", "text"), ("total", "text"), ("free", "text"), ("available", "text"), ("classification", "text"), ("used", "text")]
    detail_columns = [("mount_id", "number"), ("root", "text"), ("filesystem", "text"), ("kind", "text"), ("writable", "boolean"), ("shared", "boolean"), ("reason", "text")]
    facts = [{"key": "coverage", "label_key": "coverage_label", "kind": "text", "value": observation["coverage"]},
             {"key": "excluded_kernel", "label_key": "excluded_kernel_label", "kind": "number", "value": observation["excluded_kernel"]},
             {"key": "excluded_remote", "label_key": "excluded_remote_label", "kind": "number", "value": observation["excluded_remote"]},
             {"key": "filesystems", "label_key": "filesystems_label", "kind": "table", "columns": [
                 {"key": key, "label_key": key + "_label", "kind": kind} for key, kind in columns], "rows": rows, "truncated": truncated},
             {"key": "mount_details", "label_key": "mount_details_label", "kind": "table", "columns": [
                 {"key": key, "label_key": key + "_label", "kind": kind} for key, kind in detail_columns], "rows": details, "truncated": truncated}]
    if worst:
        mount = worst[1]
        facts.append({"key": "worst_mount", "label_key": "worst_mount_label", "kind": "table", "columns": [
            {"key": key, "label_key": key + "_label", "kind": kind} for key,kind in [("mount_id","number"),("path","text"),("total","text"),("available","text"),("shared","boolean"),("classification","text")]],
            "rows": [{"mount_id":mount["mount_id"],"path":mount["mount_path"][:1024],"total":mount["total_bytes"],"available":mount["available_bytes"],"shared":mount["shared_capacity"],"classification":next(row["classification"] for row in rows if row["mount_id"]==mount["mount_id"])}], "truncated":len(mount["mount_path"])>1024})
    return [{"from": context["now"], "status": status, "reason": reason, "facts": facts}]
