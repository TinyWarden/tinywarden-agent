"""Schedule-aware trim monitoring. No trimming or service action is performed."""
from collector import observe
from context import advance, assess, GRACE


def validate_settings(settings):
    return []


def collect(settings, host):
    return observe(host)


def reduce(context):
    return advance(context)


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
        results.append({"from": at, "status": status, "reason": {"key": reason, "params": {}}, "facts": facts})
    return results
