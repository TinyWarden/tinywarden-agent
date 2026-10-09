"""Widget and history references for the static display verifier."""
from display import require, closed, key, unit

METER = {"min", "max", "thresholds", "status"}
BASE = {"id", "type", "title_key"}
HISTORY = {"metric", "default_window"}


def inspect_metrics(value, verifier):
    metrics = value.get("metrics", {})
    require(isinstance(metrics, dict) and len(metrics) <= 8)
    maximum = 0
    for name, metric in metrics.items():
        key(name)
        closed(metric, {"title_key", "value"}, {"series_key", "series_label", "max_series"})
        verifier.label(metric["title_key"])
        binding = metric["value"]
        require(isinstance(binding, dict))
        table = binding.get("fact") if "column" in binding else None
        verifier.binding(binding, table, True)
        if table is None:
            require(not set(metric) & {"series_key", "series_label", "max_series"})
            maximum += 1
        else:
            require({"series_key", "series_label"} <= metric.keys())
            for field in ("series_key", "series_label"):
                source = verifier.column(table, metric[field])
                require(source["kind"] == "text" and not source.get("encoding"))
            count = metric.get("max_series", 16)
            require(type(count) is int and 1 <= count <= 32)
            maximum += count
    require(maximum <= 128)
    return metrics


def snapshot(value, verifier, maximum):
    verifier.table(value["source"])
    label = verifier.column(value["source"], value["label"])
    require(label["kind"] == "text" and not label.get("encoding"))
    return verifier.binding({"fact": value["source"], "column": value["value"]}, value["source"], True)


def inspect_widget(value, verifier, metrics, format=1):
    require(isinstance(value, dict))
    kind = value.get("type")
    optional = {"help_key"}
    required = BASE.copy()
    if kind == "facts":
        required.add("facts")
    elif kind == "table":
        required |= {"source", "columns"}
    elif kind in ("meter", "gauge"):
        required |= {"value", "min", "max"}
        optional |= METER - {"min", "max"}
    elif kind in ("line_chart", "sparkline"):
        required.add("metric")
        optional.add("default_window")
    elif kind == "bar_chart":
        required.add("mode")
        if value.get("mode") == "history":
            required.add("metric")
            optional.add("default_window")
        else:
            require(value.get("mode") == "snapshot")
            required |= {"source", "label", "value"}
    elif kind == "donut":
        if "parts" in value:
            required.add("parts")
        else:
            required |= {"source", "label", "value"}
    else:
        require(False)
    if format == 2:
        optional.discard("default_window")
    closed(value, required, optional)
    verifier.label(value["title_key"])
    if "help_key" in value:
        verifier.label(value["help_key"])
    if kind == "facts":
        names = value["facts"]
        require(isinstance(names, list) and 1 <= len(names) <= 16 and all(isinstance(n, str) for n in names))
        require(len(set(names)) == len(names))
        for name in names:
            require(verifier.source(name)["kind"] != "table")
    elif kind == "table":
        table = value["source"]
        verifier.table(table)
        columns = value["columns"]
        require(isinstance(columns, list) and 1 <= len(columns) <= 12)
        seen = set()
        for column in columns:
            closed(column, {"key"}, {"label_key", "meter"})
            name = column["key"]
            require(isinstance(name, str) and name not in seen)
            seen.add(name)
            source = verifier.column(table, name)
            if "label_key" in column:
                verifier.label(column["label_key"])
            if "meter" in column:
                closed(column["meter"], {"min", "max"}, METER - {"min", "max"})
                verifier.binding({"fact": table, "column": name}, table, True)
                verifier.meter(column["meter"], table, source)
    elif kind in ("meter", "gauge"):
        verifier.meter(value, None, verifier.binding(value["value"], None, True))
    elif kind in ("line_chart", "sparkline") or kind == "bar_chart" and value["mode"] == "history":
        require(isinstance(value["metric"], str) and value["metric"] in metrics)
        if "default_window" in value:
            require(value["default_window"] in ("1h", "24h", "7d", "30d", "90d"))
    elif kind == "bar_chart":
        snapshot(value, verifier, 32)
    elif kind == "donut":
        if "parts" in value:
            parts = value["parts"]
            require(isinstance(parts, list) and 1 <= len(parts) <= 8)
            units = set()
            for part in parts:
                closed(part, {"label_key", "value"})
                verifier.label(part["label_key"])
                units.add(unit(verifier.binding(part["value"], None, True)))
            require(len(units) == 1)
        else:
            snapshot(value, verifier, 8)


def inspect_widgets(value, verifier):
    metrics = inspect_metrics(value, verifier)
    sections = value["sections"]
    require(isinstance(sections, list) and 1 <= len(sections) <= 8)
    ids, count, current = set(), 0, False
    format = value["format"]
    for section in sections:
        if format == 2:
            closed(section, {"id", "role", "title_key", "widgets"})
            require(section["role"] in ("current", "graph", "details"))
            current |= section["role"] == "current"
        else:
            closed(section, {"id", "title_key", "widgets"}, {"collapsed", "disclosure"})
        key(section["id"])
        require(section["id"] not in ids)
        ids.add(section["id"])
        verifier.label(section["title_key"])
        if "collapsed" in section:
            require(type(section["collapsed"]) is bool)
        if "disclosure" in section:
            require(section["disclosure"] in ("open", "closed") and "collapsed" not in section)
        widgets = section["widgets"]
        require(isinstance(widgets, list) and 1 <= len(widgets) <= 24)
        for widget in widgets:
            require(isinstance(widget, dict))
            key(widget.get("id"))
            require(widget["id"] not in ids)
            ids.add(widget["id"])
            inspect_widget(widget, verifier, metrics, format)
            if format == 2:
                temporal = widget["type"] in ("line_chart", "sparkline") or (
                    widget["type"] == "bar_chart" and widget["mode"] == "history")
                require(temporal == (section["role"] == "graph"))
            count += 1
    require(count <= 24 and (format == 1 or current))
