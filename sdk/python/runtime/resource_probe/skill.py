def validate_settings(settings):
    return []

def collect(settings, host):
    return {"observation": {}, "resources": [{"collection": "items", "complete": True, "reason": None, "items": []}]}

def reduce(context):
    return {}

def evaluate(context):
    return [{"from": context["now"], "subjects": [], "facts": []}]
