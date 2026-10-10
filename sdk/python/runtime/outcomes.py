"""Validate all package-owned display data and engine-bounded time transitions."""
import re
import json
from json_values import encode

KINDS = {"text", "number", "boolean", "duration", "time", "percent"}


def message(value, catalog):
    if not isinstance(value, dict) or set(value) != {"key", "params"}:
        raise ValueError("message")
    key, params = value["key"], value["params"]
    if key not in catalog or not isinstance(params, dict) or set(params) != set(catalog[key]["parameters"]):
        raise ValueError("message")
    for name, kind in catalog[key]["parameters"].items():
        actual = params[name]
        if kind == "string" and (not isinstance(actual, str) or len(actual) > 512):
            raise ValueError("parameter")
        if kind == "number" and type(actual) not in {int, float}:
            raise ValueError("parameter")
        if kind == "boolean" and type(actual) is not bool:
            raise ValueError("parameter")
    rendered = re.sub(r"\{([a-zA-Z_][a-zA-Z0-9_]*)\}",
                      lambda match: str(params[match[1]]), catalog[key]["text"])
    if len(json.dumps(rendered, ensure_ascii=False).encode()) > 4096:
        raise ValueError("message_size")


def scalar(kind, value):
    if kind == "text":
        return isinstance(value, str) and len(value) <= 1024
    if kind == "boolean":
        return type(value) is bool
    if kind == "time":
        return type(value) is int and 0 <= value <= 8640000000000000
    if kind == "duration":
        return type(value) is int and value >= 0
    if kind == "percent":
        return type(value) in {int, float} and 0 <= value <= 100
    return kind == "number" and type(value) in {int, float}


def label(key, catalog):
    if not isinstance(key, str) or key not in catalog or catalog[key]["parameters"]:
        raise ValueError("label")


def facts(values, catalog):
    if not isinstance(values, list) or len(values) > 64:
        raise ValueError("facts")
    seen = set()
    for fact in values:
        if not isinstance(fact, dict) or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", fact.get("key", "")):
            raise ValueError("fact")
        if fact["key"] in seen:
            raise ValueError("fact")
        seen.add(fact["key"])
        label(fact.get("label_key"), catalog)
        kind = fact.get("kind")
        if kind == "table":
            if set(fact) != {"key", "label_key", "kind", "columns", "rows", "truncated"} or type(fact["truncated"]) is not bool:
                raise ValueError("table")
            columns, rows = fact["columns"], fact["rows"]
            if not isinstance(columns, list) or not 1 <= len(columns) <= 12 or not isinstance(rows, list) or len(rows) > 128:
                raise ValueError("table")
            column_keys = set()
            for column in columns:
                if set(column) != {"key", "label_key", "kind"} or column["kind"] not in KINDS or not re.fullmatch(r"[a-z][a-z0-9_]{0,63}", column["key"]):
                    raise ValueError("column")
                if column["key"] in column_keys:
                    raise ValueError("column")
                column_keys.add(column["key"])
                label(column["label_key"], catalog)
            for row in rows:
                if not isinstance(row, dict) or set(row) != column_keys:
                    raise ValueError("row")
                if any(not scalar(column["kind"], row[column["key"]]) for column in columns):
                    raise ValueError("row")
        elif set(fact) != {"key", "label_key", "kind", "value"} or not scalar(kind, fact["value"]):
            raise ValueError("fact")


def timeline(entries, context, catalog):
    now, expires = context["now"], context["evidence_expires_at"]
    if type(now) is not int or type(expires) is not int or now >= expires:
        raise ValueError("timeline_clock")
    if not isinstance(entries, list) or not 1 <= len(entries) <= 8:
        raise ValueError("timeline")
    previous = now - 1
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict) or set(entry) != {"from", "status", "reason", "facts"}:
            raise ValueError("assessment")
        at = entry["from"]
        if type(at) is not int or not previous < at < expires or index == 0 and at != now:
            raise ValueError("timeline_clock")
        previous = at
        if entry["status"] not in {"healthy", "warning", "critical", "unknown"}:
            raise ValueError("assessment_status")
        message(entry["reason"], catalog)
        facts(entry["facts"], catalog)
    encode(entries, 256 * 1024)


def errors(values, metadata):
    encode(values, 65536)
    if not isinstance(values, list) or len(values) > 64:
        raise ValueError("errors")
    for error in values:
        if not isinstance(error, dict) or set(error) != {"field", "message"}:
            raise ValueError("error")
        if error["field"] not in metadata["schemas"]["settings"]["properties"]:
            raise ValueError("field")
        message(error["message"], metadata["catalog"])
