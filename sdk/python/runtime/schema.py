"""Closed, non-executable schema subset. No references or implicit coercion."""
KEYWORDS = {"type", "properties", "required", "additionalProperties", "items", "enum",
            "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems"}
TYPES = {"object", "array", "string", "integer", "number", "boolean"}


def inspect(schema, depth=0, settings=False):
    if not isinstance(schema, dict) or set(schema) - KEYWORDS or depth > 8:
        raise ValueError("unsupported_schema")
    kind = schema.get("type")
    if kind not in TYPES:
        raise ValueError("schema_type")
    permitted = {"type", "enum"} | {"object": {"properties", "required", "additionalProperties"},
        "array": {"items", "minItems", "maxItems"}, "string": {"minLength", "maxLength"},
        "integer": {"minimum", "maximum"}, "number": {"minimum", "maximum"}, "boolean": set()}[kind]
    if set(schema) - permitted or settings and kind != "object":
        raise ValueError("unsupported_schema")
    if kind == "object":
        props = schema.get("properties")
        required = schema.get("required", [])
        if not isinstance(props, dict) or len(props) > 64 or schema.get("additionalProperties") is not False:
            raise ValueError("schema_object")
        if not isinstance(required, list) or any(not isinstance(x, str) for x in required) or len(required) != len(set(required)) or set(required) - props.keys() or settings and set(required) != set(props):
            raise ValueError("schema_required")
        for key, child in props.items():
            if not key.isascii() or not key.replace("_", "").isalnum() or len(key) > 64:
                raise ValueError("schema_field")
            if not isinstance(child, dict) or settings and child.get("type") in {"array", "object"}:
                raise ValueError("settings_scalar")
            inspect(child, depth + 1)
    elif kind == "array":
        if type(schema.get("minItems", 0)) is not int or type(schema.get("maxItems")) is not int or not 0 <= schema.get("minItems", 0) <= schema["maxItems"] <= 128:
            raise ValueError("schema_array")
        inspect(schema.get("items"), depth + 1)
    elif kind == "string":
        if type(schema.get("minLength", 0)) is not int or type(schema.get("maxLength")) is not int or not 0 <= schema.get("minLength", 0) <= schema["maxLength"] <= 65536:
            raise ValueError("schema_string")
    elif kind in {"number", "integer"}:
        if type(schema.get("minimum")) not in {int, float} or type(schema.get("maximum")) not in {int, float}:
            raise ValueError("schema_numeric")
        if schema["minimum"] > schema["maximum"]:
            raise ValueError("schema_numeric")
    if "enum" in schema:
        if not isinstance(schema["enum"], list) or not 1 <= len(schema["enum"]) <= 64:
            raise ValueError("schema_enum")
        for value in schema["enum"]:
            validate({k: v for k, v in schema.items() if k != "enum"}, value)


def validate(schema, value, path=""):
    kind = schema["type"]
    if "enum" in schema and value not in schema["enum"]:
        raise ValueError("schema_value:" + path)
    if kind == "object":
        if not isinstance(value, dict) or set(value) - schema["properties"].keys():
            raise ValueError("schema_value:" + path)
        if set(schema.get("required", [])) - value.keys():
            raise ValueError("schema_value:" + path)
        for key, item in value.items():
            validate(schema["properties"][key], item, path + "/" + key)
    elif kind == "array":
        if not isinstance(value, list) or not schema.get("minItems", 0) <= len(value) <= schema["maxItems"]:
            raise ValueError("schema_value:" + path)
        for item in value:
            validate(schema["items"], item, path + "/*")
    elif kind == "string":
        if not isinstance(value, str) or not schema.get("minLength", 0) <= len(value) <= schema["maxLength"]:
            raise ValueError("schema_value:" + path)
    elif kind == "boolean":
        if type(value) is not bool:
            raise ValueError("schema_value:" + path)
    elif kind in {"integer", "number"}:
        permitted = (int,) if kind == "integer" else (int, float)
        if type(value) not in permitted or not schema["minimum"] <= value <= schema["maximum"]:
            raise ValueError("schema_value:" + path)
