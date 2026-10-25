"""Closed invocation identity; authority is checked by the parent before import."""
import hashlib
import re
from actions.descriptor import exact, identifier
from contracts.json_values import decode

BINDING = {"installation_id", "skill_id", "content_sha256", "action_id", "action_version",
           "descriptor_sha256", "permission_revision", "settings_revision",
           "settings_sha256", "input_sha256", "origin"}
INVOCATION = {"schema_version", "binding", "scope", "job_id", "phase", "input_json",
              "settings_json", "plan_json", "plan_sha256", "receipt_json", "start"}
PHASES = {"prepare", "fetch", "apply", "verify", "inspect"}
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
COUNTER = re.compile(r"[1-9][0-9]{0,15}\Z")


def matches(pattern, value):
    return isinstance(value, str) and pattern.fullmatch(value) is not None


def digest_text(value):
    if not matches(DIGEST, value):
        raise ValueError("action_binding")


def binding(value):
    exact(value, BINDING)
    if (not matches(UUID, value["installation_id"]) or
            not isinstance(value["skill_id"], str) or
            not re.fullmatch(r"[a-z][a-z0-9-]{0,63}/[a-z][a-z0-9-]{0,63}", value["skill_id"])):
        raise ValueError("action_binding")
    identifier(value["action_id"])
    if type(value["action_version"]) is not int or not 1 <= value["action_version"] <= 65535:
        raise ValueError("action_binding")
    for key in ("content_sha256", "descriptor_sha256", "settings_sha256", "input_sha256"):
        digest_text(value[key])
    for key in ("permission_revision", "settings_revision"):
        if not matches(COUNTER, value[key]) or int(value[key]) > 2**53-1:
            raise ValueError("action_binding")
    exact(value["origin"], {"github_id", "owner_id"})
    if any(not matches(COUNTER, v) for v in value["origin"].values()):
        raise ValueError("action_binding")


def json_value(raw, expected=None):
    if not isinstance(raw, str):
        raise ValueError("action_envelope")
    data = raw.encode("utf-8")
    if expected is not None and hashlib.sha256(data).hexdigest() != expected:
        raise ValueError("action_binding")
    return decode(data, 4 * 1024 * 1024)


def invocation(value):
    exact(value, INVOCATION)
    if type(value["schema_version"]) is not int or value["schema_version"] != 1:
        raise ValueError("action_envelope")
    binding(value["binding"])
    exact(value["scope"], {"host_id", "agent_id", "generation"})
    if (not matches(UUID, value["scope"]["host_id"]) or
            not matches(UUID, value["scope"]["agent_id"]) or
            type(value["scope"]["generation"]) is not int or
            not 1 <= value["scope"]["generation"] <= 2**53-1 or
            not matches(UUID, value["job_id"]) or value["phase"] not in PHASES):
        raise ValueError("action_envelope")
    args = json_value(value["input_json"], value["binding"]["input_sha256"])
    settings = json_value(value["settings_json"], value["binding"]["settings_sha256"])
    if any(len(value[key].encode("utf-8")) > 65536 for key in ("input_json", "settings_json")):
        raise ValueError("action_envelope")
    if not isinstance(args, dict) or not isinstance(settings, dict):
        raise ValueError("action_envelope")
    plan, receipt = None, None
    if value["phase"] in {"prepare", "inspect"}:
        if value["plan_json"] is not None or value["plan_sha256"] is not None:
            raise ValueError("action_envelope")
    else:
        digest_text(value["plan_sha256"])
        plan = json_value(value["plan_json"], value["plan_sha256"])
        if not isinstance(plan, dict):
            raise ValueError("action_envelope")
    if value["receipt_json"] is not None:
        receipt = json_value(value["receipt_json"])
    if value["phase"] == "verify" and receipt is None:
        raise ValueError("action_envelope")
    if value["phase"] != "verify" and receipt is not None:
        raise ValueError("action_envelope")
    if value["phase"] == "apply":
        exact(value["start"], {"job_id", "attempt_id", "boot_id", "not_after_ms"})
        start = value["start"]
        if (start["job_id"] != value["job_id"] or
                any(not matches(UUID, start[k]) for k in ("attempt_id", "boot_id")) or
                type(start["not_after_ms"]) is not int or not 0 < start["not_after_ms"] <= 2**53-1):
            raise ValueError("action_envelope")
    elif value["start"] is not None:
        raise ValueError("action_envelope")
    return {"input": args, "settings": settings, "plan": plan, "receipt": receipt}
