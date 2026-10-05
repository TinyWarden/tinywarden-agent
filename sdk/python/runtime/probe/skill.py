def validate_settings(settings):
    return []

def collect(settings, host):
    return {}

def reduce(context):
    return {}

def evaluate(context):
    return [{"from": context["now"], "status": "healthy", "reason": {"key": "ok", "params": {}}, "facts": []}]
