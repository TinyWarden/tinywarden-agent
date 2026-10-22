"""Deterministic captured-observation tests, run through production entrypoints."""
from json_values import decode
from schema import validate
from authoring.execution import invoke

KEYS = {"name", "settings", "observation", "previous_state", "captured_at", "received_at",
        "now", "evidence_expires_at", "expect", "resource_epochs", "evaluation_revision"}
EXPECT = {"errors", "state", "statuses", "timeline"}


def read_cases(path):
    with open(path, "rb") as source:
        document = decode(source.read(1024 * 1024 + 1))
    if not isinstance(document, dict) or set(document) != {"format", "cases"} or document["format"] != 1:
        raise ValueError("fixture_format")
    rows = document["cases"]
    if not isinstance(rows, list) or not 1 <= len(rows) <= 32:
        raise ValueError("fixture_cases")
    names = set()
    for row in rows:
        if not isinstance(row, dict) or set(row) - KEYS or not {"name", "expect"} <= row.keys():
            raise ValueError("fixture_case")
        name, expected = row["name"], row["expect"]
        if not isinstance(name, str) or not 1 <= len(name) <= 128 or name in names:
            raise ValueError("fixture_name")
        names.add(name)
        if not isinstance(expected, dict) or not expected or set(expected) - EXPECT:
            raise ValueError("fixture_expect")
        if "errors" in expected and not isinstance(expected["errors"], list):
            raise ValueError("fixture_expect")
        if "statuses" in expected and (not isinstance(expected["statuses"], list) or not expected["statuses"] or
                                      any(s not in {"healthy", "warning", "critical", "unknown"} for s in expected["statuses"])):
            raise ValueError("fixture_expect")
        if not ("errors" in expected and expected["errors"]) and ("observation" not in row or not (set(expected) & {"state", "statuses", "timeline"})):
            raise ValueError("fixture_expect")
    return rows


def run_case(runtime, directory, metadata, case):
    settings = case.get("settings", metadata["manifest"]["defaults"])
    validate(metadata["schemas"]["settings"], settings)
    errors = invoke(runtime, directory, metadata, "validate_settings", settings)
    expected = case["expect"]
    if errors != expected.get("errors", []):
        raise ValueError("fixture_errors_mismatch")
    if errors:
        if set(expected) != {"errors"}:
            raise ValueError("fixture_expect")
        return
    validate(metadata["schemas"]["observation"], case["observation"])
    previous = case.get("previous_state")
    if previous is not None:
        validate(metadata["schemas"]["state"], previous)
    now = case.get("now", 1800000000000)
    captured = case.get("captured_at", now)
    received = case.get("received_at", now)
    if any(type(value) is not int or value < 0 or value > 2 ** 53 - 1 for value in (now, captured, received)):
        raise ValueError("fixture_clock")
    expires = case.get("evidence_expires_at", min(captured, received) + 600000)
    if type(expires) is not int or expires < 0 or expires > 2 ** 53 - 1:
        raise ValueError("fixture_clock")
    # Match the platform's admission/evaluation contract for both SDK versions.
    # An explicit later now still supports future schedule-boundary fixtures.
    now = max(now, captured, received)
    if captured > received + 5000 or now >= expires:
        raise ValueError("fixture_clock")
    context = {"identity": {"installation_id": "11111111-1111-4111-8111-111111111111",
                            "assignment_id": "22222222-2222-4222-8222-222222222222",
                            "content_sha256": metadata["content_sha256"], "generation": 1},
               "settings": settings, "observation": case["observation"], "previous_state": previous,
               "state": None, "captured_at": captured, "received_at": received,
               "now": now, "evidence_expires_at": expires}
    if metadata["manifest"]["sdk"] == 2:
        context["resource_epochs"] = case.get("resource_epochs", {
            key: {target: "fixture-" + str(i) for i, target in enumerate(settings[spec["selection_field"]])}
            for key, spec in metadata["manifest"]["resource_collections"].items()})
        context["evaluation_revision"] = case.get("evaluation_revision", "fixture-evaluation-v1")
    state = invoke(runtime, directory, metadata, "reduce", context)
    context["state"] = state
    timeline = invoke(runtime, directory, metadata, "evaluate", context)
    if "state" in expected and state != expected["state"]:
        raise ValueError("fixture_state_mismatch")
    statuses = [subject["status"] for entry in timeline for subject in entry["subjects"]] if metadata["manifest"]["sdk"] == 2 else [entry["status"] for entry in timeline]
    if "statuses" in expected and statuses != expected["statuses"]:
        raise ValueError("fixture_status_mismatch")
    if "timeline" in expected and timeline != expected["timeline"]:
        raise ValueError("fixture_timeline_mismatch")


def run_cases(runtime, directory, metadata, path):
    results = []
    for case in read_cases(path):
        try:
            run_case(runtime, directory, metadata, case)
            results.append({"name": case["name"], "passed": True})
        except (ValueError, KeyError, TypeError) as error:
            # Error text is our contract's machine code, never a guest traceback.
            results.append({"name": case["name"], "passed": False, "error": str(error)[:200]})
            if str(error).startswith("runtime:"):
                break
    return {"passed": all(row["passed"] for row in results), "cases": results,
            "content_sha256": metadata["content_sha256"]}
