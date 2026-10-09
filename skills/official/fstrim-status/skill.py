"""Schedule-aware trim monitoring. No trimming or service action is performed."""
from collector import observe
from context import advance, assess, retained, GRACE, RUNNING


def validate_settings(settings):
    return []


def collect(settings, host):
    return observe(host)


def reduce(context):
    return advance(context)


def presentation(observation, state, status, reason, at):
    last = retained(state, at)
    automatic = "unconfirmed"
    next_run = None
    if observation["problem"] == "none":
        timer, service = observation["timer"], observation["service"]
        if timer["unit_file_state"] in {"disabled", "masked", "masked-runtime"}:
            automatic = "disabled"
        elif timer["load_state"] == service["load_state"] == "loaded":
            if timer["active_state"] in {"inactive", "failed"}:
                automatic = "inactive"
            elif (timer["active_state"] == "active"
                  and timer["unit_file_state"] in {"enabled", "enabled-runtime"}
                  and timer["condition"].get("passed") is True
                  and service["condition"].get("passed") is not False):
                automatic = "running" if service["active_state"] in RUNNING else "enabled"
                if timer.get("next_elapse", 0) * 1000 > at:
                    next_run = timer["next_elapse"] * 1000
    facts = [
        {"key": "trim_last_result", "label_key": "trim_last_result_label", "kind": "text",
         "value": last["outcome"] if last else "unrecorded"},
        {"key": "trim_automatic", "label_key": "trim_automatic_label", "kind": "text", "value": automatic},
        {"key": "trim_problem", "label_key": "trim_problem_label", "kind": "text",
         "value": "" if status == "healthy" else reason},
        {"key": "trim_due", "label_key": "trim_due_label", "kind": "table", "truncated": False,
         "columns": [{"key": "scheduled_for", "label_key": "trim_scheduled_for_label", "kind": "time"},
                     {"key": "result", "label_key": "trim_due_result_label", "kind": "text"}],
         "rows": [{"scheduled_for": state["expected_at"] * 1000, "result": "unconfirmed"}]
                 if reason == "fstrim_result_overdue" and "expected_at" in state else []},
    ]
    if last and "finished_at" in last:
        facts.append({"key": "trim_completed_at", "label_key": "trim_completed_at_label",
                      "kind": "time", "value": last["finished_at"] * 1000})
    if next_run is not None:
        facts.append({"key": "trim_next_at", "label_key": "trim_next_at_label", "kind": "time", "value": next_run})
    return facts


def evaluate(context):
    observation, state = context["observation"], context["state"]
    facts = []
    if observation["problem"] == "none":
        for group, keys in [("timer", ["load_state", "active_state", "unit_file_state"]), ("service", ["load_state", "active_state", "result", "exit_kind", "exit_status"])]:
            for key in keys:
                value = observation[group][key]
                facts.append({"key": group + "_" + key, "label_key": group + "_" + key + "_label", "kind": "number" if type(value) is int else "text", "value": value})
        for group, keys in [("timer", ["last_trigger", "next_elapse"]), ("service", ["started_at", "finished_at"])]:
            for key in keys:
                if key in observation[group]:
                    facts.append({"key": group + "_" + key, "label_key": group + "_" + key + "_label", "kind": "time", "value": observation[group][key] * 1000})
        for group in ("timer", "service"):
            condition = observation[group]["condition"]
            if "passed" in condition:
                facts.append({"key": group + "_condition", "label_key": group + "_condition_label", "kind": "boolean", "value": condition["passed"]})
            if "checked_at" in condition:
                facts.append({"key": group + "_condition_at", "label_key": group + "_condition_at_label", "kind": "time", "value": condition["checked_at"] * 1000})
        facts.append({"key": "reclamation", "label_key": "reclamation_label", "kind": "boolean", "value": False})
    last = state.get("last_execution")
    if last:
        facts.append({"key": "last_outcome", "label_key": "last_outcome_label", "kind": "text", "value": last["outcome"]})
        facts.append({"key": "last_observed", "label_key": "last_observed_label", "kind": "time", "value": last["observed_at"] * 1000})
    if "expected_at" in state:
        facts.append({"key": "expected", "label_key": "expected_label", "kind": "time", "value": state["expected_at"] * 1000})
    instants = [context["now"]]
    if "expected_at" in state:
        instants += [at for at in [state["expected_at"] * 1000, (state["expected_at"] + GRACE) * 1000]
                     if context["now"] < at < context["evidence_expires_at"]]
    results = []
    for at in sorted(set(instants)):
        status, reason = assess(observation, state, at / 1000)
        results.append({"from": at, "status": status, "reason": {"key": reason, "params": {}},
                        "facts": facts + presentation(observation, state, status, reason, at)})
    return results
