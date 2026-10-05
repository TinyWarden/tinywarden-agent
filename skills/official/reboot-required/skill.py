"""The marker is one local signal, not an all-services reboot assurance."""
def validate_settings(settings):
    return []


def collect(settings, host):
    result = host.request("files.stat", {"path": "/run/reboot-required"})
    return {"marker_observed": result["exists"], "assurance": "limited"}


def reduce(context):
    return {}


def evaluate(context):
    observed = context["observation"]["marker_observed"]
    return [{"from": context["now"], "status": "warning" if observed else "healthy",
             "reason": {"key": "reboot_marker_present" if observed else "reboot_marker_absent", "params": {}},
             "facts": [{"key": "marker", "label_key": "marker_label", "kind": "boolean", "value": observed},
                       {"key": "assurance", "label_key": "assurance_label", "kind": "text", "value": context["observation"]["assurance"]}]}]
