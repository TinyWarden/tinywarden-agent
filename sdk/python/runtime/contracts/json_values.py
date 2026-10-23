"""Bounded JSON is shared by admission, execution and the host broker."""
import json
import math

MAX_JSON = 1024 * 1024
SAFE_INTEGER = (1 << 53) - 1


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("duplicate_key")
        result[key] = value
    return result


def check(value, depth=0):
    if depth > 16:
        raise ValueError("json_depth")
    if isinstance(value, float) and not math.isfinite(value):
        raise ValueError("non_finite")
    if type(value) is int and abs(value) > SAFE_INTEGER:
        raise ValueError("unsafe_integer")
    if isinstance(value, str) and any(0xD800 <= ord(char) <= 0xDFFF for char in value):
        raise ValueError("json_string")
    if isinstance(value, dict):
        for key, item in value.items():
            if not isinstance(key, str):
                raise ValueError("json_key")
            check(key, depth + 1)
            check(item, depth + 1)
    elif isinstance(value, list):
        for item in value:
            check(item, depth + 1)


def decode(data, limit=MAX_JSON):
    if len(data) > limit:
        raise ValueError("json_size")
    value = json.loads(data, object_pairs_hook=pairs,
                       parse_constant=lambda _: (_ for _ in ()).throw(ValueError("non_finite")))
    check(value)
    return value


def encode(value, limit=MAX_JSON):
    check(value)
    data = json.dumps(value, ensure_ascii=True, allow_nan=False, separators=(",", ":")).encode()
    if len(data) > limit:
        raise ValueError("json_size")
    return data
