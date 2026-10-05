"""Readonly systemd snapshots; timestamps cannot establish same-second ordering."""
import datetime
import re
import time

TIMER = ["Id", "LoadState", "ActiveState", "UnitFileState", "LastTriggerUSec", "NextElapseUSecRealtime", "ConditionResult", "ConditionTimestamp"]
SERVICE = ["Id", "LoadState", "ActiveState", "Result", "ExecMainCode", "ExecMainStatus", "ExecMainStartTimestamp", "ExecMainExitTimestamp", "ConditionResult", "ConditionTimestamp"]
BOOT = "/proc/sys/kernel/random/boot_id"
MAX_EPOCH = 253402300799


def timestamp(value):
    if not value:
        return None
    match = re.fullmatch(r"@(0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?", value)
    if match:
        second = int(match[1])
    else:
        date = datetime.datetime.strptime(value, "%a %Y-%m-%d %H:%M:%S UTC").replace(tzinfo=datetime.UTC)
        if date.strftime("%a %Y-%m-%d %H:%M:%S UTC") != value:
            raise ValueError("timestamp")
        second = int(date.timestamp())
    if not 0 <= second <= MAX_EPOCH:
        raise ValueError("timestamp")
    return second or None


def condition(values):
    if values["ConditionResult"] not in {"yes", "no"}:
        raise ValueError("condition")
    checked = timestamp(values["ConditionTimestamp"])
    return {} if checked is None else {"passed": values["ConditionResult"] == "yes", "checked_at": checked}


def number(value, maximum):
    if not re.fullmatch(r"[0-9]{1,3}", value) or int(value) > maximum:
        raise ValueError("number")
    return int(value)


def parse(timer, service, started, finished, before, after):
    if finished < started or finished - started > 31:
        return {"problem": "clock_uncertain"}
    if not re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", before) or before != after:
        return {"problem": "boot_uncertain"}
    try:
        for values, fields, unit in [(timer, TIMER, "fstrim.timer"), (service, SERVICE, "fstrim.service")]:
            if set(values) != set(fields) or values["Id"] != unit:
                raise ValueError("properties")
            if values["LoadState"] not in {"loaded", "not-found", "error", "bad-setting", "masked", "merged", "stub"} or values["ActiveState"] not in {"active", "reloading", "inactive", "failed", "activating", "deactivating", "refreshing", "maintenance"}:
                raise ValueError("states")
        if timer["UnitFileState"] not in {"", "enabled", "enabled-runtime", "linked", "linked-runtime", "alias", "static", "indirect", "disabled", "masked", "masked-runtime", "generated", "transient", "bad"} or service["Result"] not in {"success", "resources", "timeout", "exit-code", "signal", "core-dump", "watchdog", "start-limit-hit", "protocol", "exec-condition", "oom-kill"}:
            raise ValueError("states")
        last, next_at = timestamp(timer["LastTriggerUSec"]), timestamp(timer["NextElapseUSecRealtime"])
        start, end = timestamp(service["ExecMainStartTimestamp"]), timestamp(service["ExecMainExitTimestamp"])
        tc, sc = condition(timer), condition(service)
        code, status = number(service["ExecMainCode"], 6), number(service["ExecMainStatus"], 255)
    except (ValueError, TypeError, KeyError, OverflowError):
        return {"problem": "output_unsupported"}
    for at in [last, start, end, tc.get("checked_at"), sc.get("checked_at")]:
        if at is not None and at >= int(started):
            return {"problem": "clock_uncertain" if at > int(finished) else "snapshot_changed"}
    if start is not None and end is not None and start > end or next_at is not None and next_at <= int(finished) or code == 0 and status != 0 or code in {2, 3} and not 1 <= status <= 64:
        return {"problem": "evidence_inconsistent"}
    clean = lambda values: {key: value for key, value in values.items() if value is not None}
    return {"problem": "none", "observed_at": int(finished), "reclamation_verified": False,
            "timer": clean({"load_state": timer["LoadState"], "active_state": timer["ActiveState"], "unit_file_state": timer["UnitFileState"], "last_trigger": last, "next_elapse": next_at, "condition": tc}),
            "service": clean({"load_state": service["LoadState"], "active_state": service["ActiveState"], "result": service["Result"], "exit_kind": code, "exit_status": status, "started_at": start, "finished_at": end, "condition": sc})}


def observe(host):
    started = time.time()
    before = host.request("files.read", {"path": BOOT, "max_bytes": 128})["text"].strip()
    timer = host.request("systemd.properties", {"unit": "fstrim.timer", "properties": TIMER})
    service = host.request("systemd.properties", {"unit": "fstrim.service", "properties": SERVICE})
    after = host.request("files.read", {"path": BOOT, "max_bytes": 128})["text"].strip()
    return parse(timer, service, started, time.time(), before, after)
