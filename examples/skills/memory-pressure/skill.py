"""Independent SDK v1 example: local memory availability, without host actions."""
def validate_settings(settings):
    if settings["warning_percent"] >= settings["critical_percent"]:
        return [{"field": "critical_percent", "message": {"key": "invalid_thresholds", "params": {}}}]
    return []


def collect(settings, host):
    snapshot = host.request("files.read", {"path": "/proc/meminfo", "max_bytes": 16384})
    if snapshot["truncated"]:
        raise ValueError("incomplete_meminfo")
    values = {}
    for line in snapshot["text"].splitlines():
        name, _, rest = line.partition(":")
        if name in {"MemTotal", "MemAvailable"}:
            parts = rest.split()
            if name in values or len(parts) != 2 or parts[1] != "kB" or not parts[0].isdigit():
                raise ValueError("invalid_meminfo")
            values[name] = int(parts[0]) * 1024
    total, available = values["MemTotal"], values["MemAvailable"]
    if not 0 <= available <= total or total <= 0:
        raise ValueError("invalid_capacity")
    return {"total_bytes": str(total), "available_bytes": str(available),
            "used_percent": round((total - available) * 100 / total, 2)}


def reduce(context):
    return {}


def evaluate(context):
    observation, settings = context["observation"], context["settings"]
    percent = observation["used_percent"]
    status = "critical" if percent >= settings["critical_percent"] else "warning" if percent >= settings["warning_percent"] else "healthy"
    return [{"from": context["now"], "status": status,
             "reason": {"key": "memory_usage", "params": {"percent": percent}},
             "facts": [{"key": "used", "label_key": "used_label", "kind": "percent", "value": percent},
                       {"key": "total", "label_key": "total_label", "kind": "text", "value": observation["total_bytes"]},
                       {"key": "available", "label_key": "available_label", "kind": "text", "value": observation["available_bytes"]},
                       {"key": "memory", "label_key": "memory_label", "kind": "table", "columns": [
                           {"key": "region", "label_key": "region_label", "kind": "text"},
                           {"key": "used", "label_key": "used_label", "kind": "percent"},
                           {"key": "total", "label_key": "total_label", "kind": "text"},
                           {"key": "available", "label_key": "available_label", "kind": "text"}],
                        "rows": [{"region": "memory", "used": percent, "total": observation["total_bytes"],
                                  "available": observation["available_bytes"]}], "truncated": False}]}]
