"""Retain observed executions and pending schedule expectations across reboots."""
GRACE = 86400
RUNNING = {"active", "activating", "deactivating", "reloading"}


def retained(context, now):
    execution = context.get("last_execution")
    return execution if execution and execution["recorded_at"] >= now - 90 * 86400000 else None


def advance(context):
    observation = context["observation"]
    previous = context["previous_state"] or {}
    state = {**previous}
    old = retained(previous, context["received_at"])
    if old:
        state["last_execution"] = old
    else:
        state.pop("last_execution", None)
    if observation["problem"] != "none":
        return state
    at = observation["observed_at"]
    state["as_of"] = max(state.get("as_of", 0), at)
    timer, service = observation["timer"], observation["service"]
    failure = service["active_state"] == "failed" or service["result"] != "success" or "finished_at" in service and (service["exit_kind"] != 1 or service["exit_status"] != 0)
    success = service["active_state"] == "inactive" and service["result"] == "success" and service["exit_kind"] == 1 and service["exit_status"] == 0 and "started_at" in service and "finished_at" in service and service["condition"].get("passed") is True and service["condition"].get("checked_at", at + 1) <= service.get("started_at", 0)
    candidate = None
    if (failure or success) and service["load_state"] == "loaded" and service["condition"].get("passed") is not False:
        candidate = {"observed_at": at, "recorded_at": context["received_at"], "outcome": "failure" if failure else "success"}
        for key in ("started_at", "finished_at"):
            if key in service:
                candidate[key] = service[key]
        if "last_trigger" in timer and timer["last_trigger"] <= service.get("started_at", 0):
            candidate["trigger_at"] = timer["last_trigger"]
        same = old and all(candidate.get(key) == old.get(key) for key in ("outcome", "started_at", "finished_at"))
        later_success = candidate["outcome"] == "success" and (not old or old["outcome"] != "failure" or candidate.get("finished_at", 0) > old.get("finished_at", old["observed_at"]))
        if not same and (candidate["outcome"] == "failure" or later_success):
            state["last_execution"] = candidate
    last = state.get("last_execution")
    if last and last["outcome"] == "success" and candidate and candidate["outcome"] == "success" and candidate.get("finished_at") == last.get("finished_at"):
        trigger = candidate.get("trigger_at")
        advanced = trigger is not None and trigger > state.get("covered_trigger", 0)
        if "expected_at" in state and (last["finished_at"] >= state["expected_at"] or advanced and trigger >= state["expected_at"] - GRACE):
            state.pop("expected_at")
        if trigger is not None:
            state["covered_trigger"] = max(state.get("covered_trigger", 0), trigger)
    if previous and "last_trigger" in timer and timer["last_trigger"] > state.get("last_trigger", 0) and timer["last_trigger"] > state.get("covered_trigger", 0):
        state["expected_at"] = min(state.get("expected_at", timer["last_trigger"]), timer["last_trigger"])
    if "last_trigger" in timer:
        state["last_trigger"] = max(state.get("last_trigger", 0), timer["last_trigger"])
    if timer["load_state"] == "loaded" and timer["active_state"] == "active" and timer["unit_file_state"] in {"enabled", "enabled-runtime"} and timer["condition"].get("passed") is True:
        if "next_elapse" in timer:
            state["expected_at"] = min(state.get("expected_at", timer["next_elapse"]), timer["next_elapse"])
        if service["active_state"] in RUNNING:
            state["expected_at"] = min(state.get("expected_at", at), at)
    return state


def assess(observation, state, now):
    if observation["problem"] != "none":
        return "unknown", observation["problem"]
    if state.get("as_of") != observation["observed_at"] or now < state.get("as_of", 0):
        return "unknown", "fstrim_context_unavailable"
    timer, service = observation["timer"], observation["service"]
    if timer["unit_file_state"] in {"disabled", "masked", "masked-runtime"}:
        return "warning", "fstrim_timer_disabled"
    if timer["load_state"] != "loaded" or service["load_state"] != "loaded":
        return "unknown", "fstrim_unit_unavailable"
    if timer["condition"].get("passed") is False or service["condition"].get("passed") is False:
        return "unknown", "fstrim_condition_skipped"
    if timer["active_state"] == "failed":
        return "warning", "fstrim_timer_failed"
    if timer["active_state"] == "inactive":
        return "warning", "fstrim_timer_inactive"
    running = service["active_state"] in RUNNING
    if timer["active_state"] != "active" or service["active_state"] not in {"inactive", "failed"} and not running:
        return "unknown", "fstrim_running"
    if timer["unit_file_state"] not in {"enabled", "enabled-runtime"} or "next_elapse" not in timer and not running or "expected_at" not in state:
        return "unknown", "fstrim_schedule_unverified"
    if "checked_at" not in timer["condition"] or timer["condition"].get("passed") is not True:
        return "unknown", "fstrim_condition_unverified"
    if "started_at" in service and "finished_at" not in service and not running or "finished_at" in service and ("started_at" not in service or service["exit_kind"] == 0 or service["condition"].get("checked_at", 0) > service.get("started_at", 0)):
        return "unknown", "evidence_inconsistent"
    last = retained(state, int(now * 1000))
    if service["active_state"] == "failed" or service["result"] != "success" or last and last["outcome"] == "failure":
        return "warning", "fstrim_service_failed"
    if now >= state["expected_at"] + GRACE:
        return "warning", "fstrim_result_overdue"
    if running or now >= state["expected_at"] or state.get("last_trigger", 0) > state.get("covered_trigger", 0) and state.get("last_trigger", 0) >= state["expected_at"] - GRACE:
        return "healthy", "fstrim_awaiting_result"
    return ("healthy", "fstrim_observed_success") if last and last["outcome"] == "success" else ("healthy", "fstrim_scheduled")
