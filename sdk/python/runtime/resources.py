"""SDK 2 bounded discovery and exact-selection subject contract."""
import re
from json_values import encode
from outcomes import message, facts, label

LIMITS = {"inventory": 262144, "observation": 131072, "state": 65536,
          "context": 524288, "timeline": 524288, "selected": 64, "discovered": 512}
COLLECTION = re.compile(r"[a-z][a-z0-9_]{0,63}\Z")


def resource_key(value):
    return isinstance(value, str) and not any(ord(c) < 32 or 127 <= ord(c) <= 159 or
        0xD800 <= ord(c) <= 0xDFFF for c in value) and 1 <= len(value.encode("utf-8")) <= 255


def text(value):
    return isinstance(value, str) and not any(ord(c) < 32 or 127 <= ord(c) <= 159 or
        0xD800 <= ord(c) <= 0xDFFF for c in value) and len(value.encode("utf-8")) <= 256


def inspect_resources(manifest, schemas, catalog):
    collections = manifest.get("resource_collections")
    if not isinstance(collections, dict) or not 1 <= len(collections) <= 4:
        raise ValueError("resource_collections")
    fields = set()
    for key, collection in collections.items():
        if not COLLECTION.fullmatch(key) or not isinstance(collection, dict) or set(collection) != {
                "selection_field", "label_key", "help_key", "max_discovered", "max_selected"}:
            raise ValueError("resource_collection")
        for name, maximum in (("max_discovered", 512), ("max_selected", 64)):
            if type(collection[name]) is not int or not 1 <= collection[name] <= maximum:
                raise ValueError("resource_limit")
        label(collection["label_key"], catalog)
        label(collection["help_key"], catalog)
        field = collection["selection_field"]
        if field in fields or field not in manifest["fields"]:
            raise ValueError("resource_field")
        fields.add(field)
        metadata = manifest["fields"][field]
        schema = schemas["settings"]["properties"][field]
        if metadata.get("scope") != "host" or metadata.get("editor") != {"kind": "resources", "collection": key}:
            raise ValueError("resource_field")
        if schema != {"type": "array", "items": {"type": "string", "minLength": 1, "maxLength": 255},
                      "minItems": 0, "maxItems": collection["max_selected"]} or manifest["defaults"][field] != []:
            raise ValueError("resource_field_schema")
    for key, metadata in manifest["fields"].items():
        if key not in fields and ("scope" in metadata or "editor" in metadata or
                                  schemas["settings"]["properties"][key]["type"] in {"object", "array"}):
            raise ValueError("resource_field")


def selection(metadata, settings):
    selected = set()
    for collection, spec in metadata["manifest"].get("resource_collections", {}).items():
        values = settings.get(spec["selection_field"])
        if not isinstance(values, list) or len(values) > spec["max_selected"] or any(not resource_key(v) for v in values):
            raise ValueError("resource_selection")
        if len(values) != len(set(values)):
            raise ValueError("resource_selection_duplicate")
        selected.update((collection, key) for key in values)
    if len(selected) > 64:
        raise ValueError("resource_selection_limit")
    return selected


def inventory(values, metadata):
    encode(values, LIMITS["inventory"])
    declared = metadata["manifest"]["resource_collections"]
    if not isinstance(values, list) or len(values) != len(declared):
        raise ValueError("resource_inventory")
    seen, total = set(), 0
    for value in values:
        if not isinstance(value, dict) or set(value) != {"collection", "complete", "reason", "items"}:
            raise ValueError("resource_inventory")
        collection = value["collection"]
        if collection not in declared or collection in seen or type(value["complete"]) is not bool:
            raise ValueError("resource_inventory")
        seen.add(collection)
        if value["reason"] is not None:
            message(value["reason"], metadata["catalog"])
        if value["complete"] != (value["reason"] is None):
            raise ValueError("resource_completeness")
        items = value["items"]
        if not isinstance(items, list) or len(items) > declared[collection]["max_discovered"]:
            raise ValueError("resource_inventory_limit")
        total += len(items)
        keys = set()
        for item in items:
            if not isinstance(item, dict) or set(item) != {"key", "label", "description", "selectable", "selection_reason", "state"}:
                raise ValueError("resource_item")
            key = item["key"]
            if not resource_key(key) or key in keys or not text(item["label"]) or not text(item["description"]) or type(item["selectable"]) is not bool:
                raise ValueError("resource_item")
            keys.add(key)
            if item["selection_reason"] is not None:
                message(item["selection_reason"], metadata["catalog"])
            if not item["selectable"] and item["selection_reason"] is None:
                raise ValueError("resource_eligibility")
            message(item["state"], metadata["catalog"])
    if total > 512:
        raise ValueError("resource_inventory_limit")


def collected(result, metadata, settings):
    if not isinstance(result, dict) or set(result) != {"observation", "resources"}:
        raise ValueError("resource_envelope")
    selection(metadata, settings)
    encode(result["observation"], LIMITS["observation"])
    inventory(result["resources"], metadata)


def timeline(entries, context, metadata):
    encode(entries, LIMITS["timeline"])
    expected = selection(metadata, context["settings"])
    now, expires = context["now"], context["evidence_expires_at"]
    if type(now) is not int or type(expires) is not int or now >= expires or not isinstance(entries, list) or not 1 <= len(entries) <= 8:
        raise ValueError("resource_timeline")
    previous = now - 1
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict) or set(entry) != {"from", "subjects", "facts"}:
            raise ValueError("resource_assessment")
        at = entry["from"]
        if type(at) is not int or not previous < at < expires or index == 0 and at != now:
            raise ValueError("resource_timeline_clock")
        previous = at
        facts(entry["facts"], metadata["catalog"])
        if not isinstance(entry["subjects"], list) or len(entry["subjects"]) != len(expected):
            raise ValueError("resource_subjects")
        actual = set()
        for subject in entry["subjects"]:
            if not isinstance(subject, dict) or set(subject) != {"collection", "key", "status", "reason", "facts"}:
                raise ValueError("resource_subject")
            identity = (subject["collection"], subject["key"])
            if identity not in expected or identity in actual or subject["status"] not in {"healthy", "warning", "critical", "unknown"}:
                raise ValueError("resource_subject")
            actual.add(identity)
            message(subject["reason"], metadata["catalog"])
            facts(subject["facts"], metadata["catalog"])
