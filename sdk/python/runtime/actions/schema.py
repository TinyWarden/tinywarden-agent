"""Closed action value schemas; no executable validators or reference resolution."""
import math

KINDS = {"object", "array", "string", "integer", "number", "boolean"}
COMMON = {"type", "enum", "nullable", "label_key", "help_key"}
KEYS = {
    "object": {"properties", "required", "additionalProperties"},
    "array": {"items", "minItems", "maxItems"},
    "string": {"minLength", "maxLength"},
    "integer": {"minimum", "maximum"},
    "number": {"minimum", "maximum"}, "boolean": set(),
}


def inspect(schema, depth=0, budget=None):
    budget = [256] if budget is None else budget
    budget[0] -= 1
    if not isinstance(schema, dict) or depth > 8 or budget[0] < 0:
        raise ValueError("action_schema")
    kind = schema.get("type")
    if kind not in KINDS or set(schema) - COMMON - KEYS[kind]:
        raise ValueError("action_schema")
    if "nullable" in schema and type(schema["nullable"]) is not bool:
        raise ValueError("action_schema")
    if any(not isinstance(schema[key],str) for key in ("label_key","help_key") if key in schema):
        raise ValueError("action_schema")
    if kind == "object":
        props, required = schema.get("properties"), schema.get("required", [])
        if (not isinstance(props, dict) or len(props) > 64 or
                schema.get("additionalProperties") is not False or
                not isinstance(required, list) or
                any(not isinstance(x, str) for x in required) or
                len(required) != len(set(required)) or set(required) - props.keys()):
            raise ValueError("action_schema")
        for key, child in props.items():
            if (not isinstance(key, str) or not key.isascii() or
                    not key.replace("_", "").isalnum() or len(key) > 64):
                raise ValueError("action_schema")
            inspect(child, depth + 1, budget)
    elif kind == "array":
        low, high = schema.get("minItems", 0), schema.get("maxItems")
        if type(low) is not int or type(high) is not int or not 0 <= low <= high <= 5000:
            raise ValueError("action_schema")
        inspect(schema.get("items"), depth + 1, budget)
    elif kind == "string":
        low, high = schema.get("minLength", 0), schema.get("maxLength")
        if type(low) is not int or type(high) is not int or not 0 <= low <= high <= 65536:
            raise ValueError("action_schema")
    elif kind in {"integer", "number"}:
        low, high = schema.get("minimum"), schema.get("maximum")
        if (type(low) not in {int, float} or type(high) not in {int, float} or
                not math.isfinite(low) or not math.isfinite(high) or
                not -(2**53-1) <= low <= high <= 2**53-1):
            raise ValueError("action_schema")
    if "enum" in schema:
        values = schema["enum"]
        if not isinstance(values, list) or not 1 <= len(values) <= 64:
            raise ValueError("action_schema")
        for value in values:
            if value is not None and type(value) not in {str,int,float,bool}:
                raise ValueError("action_schema")
            validate({k: v for k, v in schema.items() if k != "enum"}, value)


def validate(schema, value):
    if value is None and schema.get("nullable") is True:
        return
    if "enum" in schema and not any((type(value) is type(x) or
            type(value) in {int,float} and type(x) in {int,float}) and value == x for x in schema["enum"]):
        raise ValueError("action_schema_value")
    kind = schema["type"]
    if kind == "object":
        if (not isinstance(value, dict) or set(value) - schema["properties"].keys() or
                set(schema.get("required", [])) - value.keys()):
            raise ValueError("action_schema_value")
        for key, item in value.items():
            validate(schema["properties"][key], item)
    elif kind == "array":
        if not isinstance(value, list) or not schema.get("minItems", 0) <= len(value) <= schema["maxItems"]:
            raise ValueError("action_schema_value")
        for item in value:
            validate(schema["items"], item)
    elif kind == "string":
        if not isinstance(value, str) or not schema.get("minLength", 0) <= len(value) <= schema["maxLength"]:
            raise ValueError("action_schema_value")
    elif kind == "boolean":
        if type(value) is not bool:
            raise ValueError("action_schema_value")
    elif kind in {"integer", "number"}:
        types = {int} if kind == "integer" else {int, float}
        if (type(value) not in types or not math.isfinite(value) or
                not schema["minimum"] <= value <= schema["maximum"]):
            raise ValueError("action_schema_value")
