"""Closed declarative notification Details; no package execution or HTML."""
import re
from json_values import decode

MARKS = {"bold", "italic", "underline"}
TYPES = {"string", "number", "boolean"}
PLACEHOLDER = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)\}")
FORBIDDEN = re.compile(r"[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u2028\u2029\u202a-\u202e\u2066-\u206f]|(?:\b[A-Za-z][A-Za-z0-9+.-]*:(?=\S)|www\.)", re.I)


def require(condition):
    if not condition:
        raise ValueError("package_notifications")


def closed(value, required, optional=()):
    require(isinstance(value, dict) and set(required) <= value.keys()
            and not set(value) - set(required) - set(optional))


def depth(value, level=0):
    require(level <= 8)
    if isinstance(value, dict):
        for item in value.values():
            depth(item, level + 1)
    elif isinstance(value, list):
        for item in value:
            depth(item, level + 1)


def marks(value):
    require(isinstance(value, list) and 1 <= len(value) <= 3)
    require(all(isinstance(v, str) and v in MARKS for v in value))
    require(len(set(value)) == len(value))


class Verifier:
    def __init__(self, catalog, settings):
        self.catalog, self.settings = catalog, settings

    def entry(self, key):
        require(isinstance(key, str) and key in self.catalog)
        entry = self.catalog[key]
        require(not FORBIDDEN.search(entry["text"]))
        return entry

    def source(self, value, reason):
        require(isinstance(value, dict))
        if set(value) == {"reason_param"}:
            key = value["reason_param"]
            require(isinstance(key, str) and key in reason["parameters"])
            return reason["parameters"][key]
        if set(value) == {"fact", "type"}:
            require(isinstance(value["fact"], str) and re.fullmatch(r"[a-z][a-z0-9_]{0,63}", value["fact"]))
            require(isinstance(value["type"], str) and value["type"] in TYPES)
            return value["type"]
        if set(value) == {"setting"}:
            key = value["setting"]
            properties = self.settings.get("properties", {})
            require(isinstance(key, str) and key in properties)
            kind = properties[key]["type"]
            kind = "number" if kind == "integer" else kind
            require(kind in TYPES)
            return kind
        closed(value, {"catalog_key"})
        require(not self.entry(value["catalog_key"])["parameters"])
        return "string"

    def binding(self, value, destination, reason):
        closed(value, {"source"}, {"marks", "precision", "plural"})
        kind = self.source(value["source"], reason)
        if "marks" in value:
            marks(value["marks"])
        require(not ("precision" in value and "plural" in value))
        if "precision" in value:
            require(kind == "number" and type(value["precision"]) is int and 0 <= value["precision"] <= 3)
        if "plural" in value:
            require(kind == "number" and destination == "string")
            closed(value["plural"], {"one_key", "other_key"})
            for key in value["plural"].values():
                require(self.entry(key)["parameters"] == {"count": "number"})
            kind = "string"
        require(kind == destination)

    def rule(self, value):
        closed(value, {"reason", "states", "message_key", "parameters"}, {"marks"})
        reason = self.entry(value["reason"])
        states = value["states"]
        require(isinstance(states, list) and 1 <= len(states) <= 3)
        require(all(isinstance(v, str) and v in {"healthy", "warning", "critical"} for v in states))
        require(len(set(states)) == len(states))
        if "marks" in value:
            marks(value["marks"])
        message = self.entry(value["message_key"])
        require(len(PLACEHOLDER.findall(message["text"])) <= 32)
        require(isinstance(value["parameters"], dict) and set(value["parameters"]) == set(message["parameters"]))
        for key, binding in value["parameters"].items():
            self.binding(binding, message["parameters"][key], reason)
        return [(value["reason"], state) for state in states]


def inspect_notifications(data, catalog, settings):
    value = decode(data, 16384)
    depth(value)
    closed(value, {"format", "rules"})
    require(type(value["format"]) is int and value["format"] == 1)
    require(isinstance(value["rules"], list) and 1 <= len(value["rules"]) <= 32)
    verifier, seen = Verifier(catalog, settings), set()
    for rule in value["rules"]:
        for pair in verifier.rule(rule):
            require(pair not in seen)
            seen.add(pair)
    return value
