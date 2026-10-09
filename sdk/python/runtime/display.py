"""Static, closed skill display metadata; never imports package code."""
import re
from json_values import decode

KINDS = {"text", "number", "boolean", "duration", "time", "percent"}
UNITS = {"number", "count", "percent", "bytes", "seconds", "milliseconds",
         "celsius", "bytes_per_second", "per_second"}


def require(condition):
    if not condition:
        raise ValueError("package_display")


def closed(value, required, optional=()):
    require(isinstance(value, dict) and set(required) <= value.keys()
            and not set(value) - set(required) - set(optional))


def key(value):
    require(isinstance(value, str) and re.fullmatch(r"[a-z][a-z0-9_]{0,63}", value))


def depth(value, level=0):
    require(level <= 12)
    if isinstance(value, dict):
        for item in value.values():
            depth(item, level + 1)
    elif isinstance(value, list):
        for item in value:
            depth(item, level + 1)


def unit(source):
    return source.get("unit", {"percent": "percent", "duration": "seconds"}.get(source["kind"], "number"))


def numeric(source):
    return source["kind"] in {"number", "duration", "percent"} or bool(source.get("encoding"))


class Verifier:
    def __init__(self, sources, catalog, settings):
        self.sources, self.catalog, self.settings = sources, catalog, settings

    def label(self, value):
        require(isinstance(value, str) and value in self.catalog and not self.catalog[value]["parameters"])

    def scalar(self, source):
        closed(source, {"kind"}, {"unit", "encoding", "missing", "precision", "enum", "semantic", "muted_values"})
        require(isinstance(source["kind"], str) and source["kind"] in KINDS)
        kind = source["kind"]
        if "encoding" in source:
            require(kind == "text" and source["encoding"] in ("decimal", "uint64"))
        if "missing" in source:
            require(bool(source.get("encoding")) and source["missing"] == "")
        if "unit" in source:
            require(numeric(source) and isinstance(source["unit"], str) and source["unit"] in UNITS)
        require(kind != "percent" or unit(source) == "percent")
        require(kind != "duration" or unit(source) == "seconds")
        if "precision" in source:
            require(numeric(source) and type(source["precision"]) is int and 0 <= source["precision"] <= 3)
        if "enum" in source:
            require(kind == "text" and not source.get("encoding") and "semantic" not in source)
            values = source["enum"]
            require(isinstance(values, dict) and 1 <= len(values) <= 32)
            for raw, label in values.items():
                require(isinstance(raw, str) and len(raw) <= 1024)
                self.label(label)
        if "muted_values" in source:
            values = source["muted_values"]
            require(kind == "text" and "enum" in source and not source.get("encoding"))
            require(isinstance(values, list) and 1 <= len(values) <= 32)
            require(all(isinstance(v, str) and v in source["enum"] for v in values))
            require(len(set(values)) == len(values))
        if "semantic" in source:
            require(kind == "text" and not source.get("encoding") and source["semantic"] == "status")

    def source(self, name):
        require(isinstance(name, str) and name in self.sources)
        return self.sources[name]

    def table(self, name):
        source = self.source(name)
        require(source["kind"] == "table")
        return source

    def column(self, table, name):
        source = self.table(table)
        require(isinstance(name, str) and name in source["columns"])
        return source["columns"][name]

    def binding(self, value, table=None, number=False):
        closed(value, {"fact"}, {"column"})
        source = self.source(value["fact"])
        if "column" in value:
            require(value["fact"] == table)
            source = self.column(table, value["column"])
        else:
            require(source["kind"] != "table")
        require(not number or numeric(source))
        return source

    def reference(self, value, table, expected_unit):
        require(isinstance(value, dict))
        if set(value) == {"value"}:
            require(type(value["value"]) in (int, float))
        elif set(value) == {"setting"}:
            name = value["setting"]
            require(isinstance(name, str) and self.settings.get(name, {}).get("type") in ("number", "integer"))
        else:
            require(unit(self.binding(value, table, True)) == expected_unit)

    def meter(self, value, table, source):
        for name in ("min", "max"):
            self.reference(value[name], table, unit(source))
        minimum, maximum = value["min"], value["max"]
        if set(minimum) == set(maximum) == {"value"}:
            require(minimum["value"] < maximum["value"])
        thresholds = value.get("thresholds", [])
        require(isinstance(thresholds, list) and len(thresholds) <= 2)
        severities = set()
        for threshold in thresholds:
            closed(threshold, {"at", "severity"})
            severity = threshold["severity"]
            require(severity in ("warning", "critical") and severity not in severities)
            severities.add(severity)
            self.reference(threshold["at"], table, unit(source))
        if "status" in value and value["status"] != "assessment":
            require(self.binding(value["status"], table).get("semantic") == "status")


def inspect_display(data, catalog, settings):
    from display_widgets import inspect_widgets
    value = decode(data, 65536)
    depth(value)
    closed(value, {"format", "sources", "sections"}, {"metrics"})
    require(type(value["format"]) is int and value["format"] in (1, 2))
    sources = value["sources"]
    require(isinstance(sources, dict) and 1 <= len(sources) <= 64)
    verifier = Verifier(sources, catalog, settings.get("properties", {}))
    for name, source in sources.items():
        key(name)
        require(isinstance(source, dict))
        if source.get("kind") == "table":
            closed(source, {"kind", "columns"})
            require(isinstance(source["columns"], dict) and 1 <= len(source["columns"]) <= 12)
            for column, scalar in source["columns"].items():
                key(column)
                verifier.scalar(scalar)
        else:
            verifier.scalar(source)
    inspect_widgets(value, verifier)
    return value
