"""Synthetic typed notification contract; contains no released skill payload."""
def metadata():
    return {
        "schemas": {"settings": {"type": "object", "properties": {}, "required": [], "additionalProperties": False}},
        "catalog": {
            "name": {"text": "Example", "parameters": {}},
            "sample_value": {"text": "{path} reports {percent}.", "parameters": {"path": "string", "percent": "number"}},
            "notify.sample.warning": {"text": "{mount} reports {used}.", "parameters": {"mount": "string", "used": "number"}},
        },
        "notifications": {"format": 1, "rules": [{
            "reason": "sample_value", "states": ["warning"], "message_key": "notify.sample.warning",
            "parameters": {"mount": {"source": {"reason_param": "path"}, "marks": ["bold"]},
                           "used": {"source": {"reason_param": "percent"}, "precision": 1, "marks": ["bold"]}},
        }]},
    }
