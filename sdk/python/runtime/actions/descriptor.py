"""Static action admission. Reading this module never imports package code."""
import ast
import hashlib
import json
import re
from pathlib import PurePosixPath
from actions.schema import inspect
from contracts.json_values import decode, encode

FIELDS = {"id", "version", "name_key", "description_key", "execution_class",
          "requires_approval", "requires_monitoring", "prepare_effect", "entrypoint",
          "prerequisites", "schemas", "review", "result_facts", "recovery_facts"}
PHASES = {"prepare", "fetch", "apply", "verify", "inspect"}
KINDS = {"text", "number", "boolean", "bytes", "sha256"}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True,
                      allow_nan=False).encode()


def exact(value, keys):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise ValueError("action_descriptor")


def label(value, catalog):
    if not isinstance(value, str) or value not in catalog or catalog[value]["parameters"]:
        raise ValueError("action_catalog")


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r"[a-z][a-z0-9_.]{1,63}", value):
        raise ValueError("action_identifier")


def relative(value, suffix, files):
    if (not isinstance(value, str) or value not in files or
            str(PurePosixPath(value)) != value or PurePosixPath(value).is_absolute() or
            ".." in PurePosixPath(value).parts or not value.endswith(suffix)):
        raise ValueError("action_path")


def prerequisites(items):
    if not isinstance(items, list) or len(items) > 16:
        raise ValueError("action_prerequisites")
    seen = set()
    for item in items:
        if not isinstance(item, dict) or item.get("kind") not in {"executable", "python_module"}:
            raise ValueError("action_prerequisites")
        kind = item["kind"]
        exact(item, {"kind", "id", "package", "path" if kind == "executable" else "module"})
        identifier(item["id"])
        if item["id"] in seen:
            raise ValueError("action_prerequisites")
        seen.add(item["id"])
        if (not isinstance(item["package"], str) or
                not re.fullmatch(r"[a-z0-9][a-z0-9+.-]{0,127}", item["package"])):
            raise ValueError("action_prerequisites")
        if kind == "executable":
            if not isinstance(item["path"], str) or not re.fullmatch(r"/usr/(?:s?bin)/[A-Za-z0-9_.+-]{1,128}", item["path"]):
                raise ValueError("action_prerequisites")
        elif not isinstance(item["module"], str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,127}", item["module"]):
            raise ValueError("action_prerequisites")


def scalar(schema, source, kind):
    value = schema.get("properties", {}).get(source)
    types = {"text": {"string"}, "sha256": {"string"}, "number": {"integer", "number"},
             "bytes": {"integer"}, "boolean": {"boolean"}}
    if not isinstance(source, str) or not value or kind not in KINDS or value["type"] not in types[kind]:
        raise ValueError("action_binding")


def facts(items, schema, catalog):
    if not isinstance(items, list) or len(items) > 8:
        raise ValueError("action_binding")
    seen = set()
    for item in items:
        exact(item, {"key", "label_key", "kind"})
        scalar(schema, item["key"], item["kind"])
        label(item["label_key"], catalog)
        if item["key"] in seen:
            raise ValueError("action_binding")
        seen.add(item["key"])


def review(value, schema, catalog):
    exact(value, {"facts", "tables"})
    facts(value["facts"], schema, catalog)
    tables = value["tables"]
    if not isinstance(tables, list) or len(tables) > 8:
        raise ValueError("action_binding")
    seen = set()
    for table in tables:
        exact(table, {"id", "label_key", "source", "columns"})
        identifier(table["id"])
        if table["id"] in seen or table["id"] == "coverage":
            raise ValueError("action_binding")
        seen.add(table["id"])
        label(table["label_key"], catalog)
        source = table["source"]
        if not isinstance(source, str):
            raise ValueError("action_binding")
        array = schema.get("properties", {}).get(source)
        if not array or array["type"] != "array" or array["items"]["type"] != "object":
            raise ValueError("action_binding")
        if (source not in schema.get("required", []) or array.get("nullable") or
                array["items"].get("nullable")):
            raise ValueError("action_review_table_schema")
        if not isinstance(table["columns"], list) or not 1 <= len(table["columns"]) <= 8:
            raise ValueError("action_binding")
        facts(table["columns"], array["items"], catalog)


def admit(items, files, catalog):
    if not isinstance(items, list) or len(items) > 16:
        raise ValueError("action_descriptor")
    admitted, seen = [], set()
    for item in items:
        exact(item, FIELDS | ({"verification"} if isinstance(item, dict) and "verification" in item else set()))
        if "verification" in item and (not isinstance(item["verification"], str) or item["verification"] not in {"local", "host_health"}):
            raise ValueError("action_verification")
        identifier(item["id"])
        if item["id"] in seen or type(item["version"]) is not int or not 1 <= item["version"] <= 65535:
            raise ValueError("action_identifier")
        seen.add(item["id"])
        label(item["name_key"], catalog)
        label(item["description_key"], catalog)
        if item["execution_class"] not in {"unprivileged", "host_admin"} or item["prepare_effect"] not in {"none", "changed"}:
            raise ValueError("action_descriptor")
        if any(type(item[key]) is not bool for key in ("requires_approval", "requires_monitoring")):
            raise ValueError("action_descriptor")
        relative(item["entrypoint"], ".py", files)
        if not item["entrypoint"].startswith("actions/"):
            raise ValueError("action_path")
        parsed = ast.parse(files[item["entrypoint"]], filename=item["entrypoint"])
        if not PHASES <= {n.name for n in parsed.body if isinstance(n, ast.FunctionDef)}:
            raise ValueError("action_entrypoints")
        prerequisites(item["prerequisites"])
        exact(item["schemas"], {"input", "plan", "result", "recovery"})
        schemas = {}
        for name, path in item["schemas"].items():
            relative(path, ".json", files)
            schema = decode(files[path], 65536)
            inspect(schema)
            def labels(node):
                for key in ("label_key","help_key"):
                    if key in node:
                        label(node[key],catalog)
                for child in node.get("properties",{}).values():
                    labels(child)
                if "items" in node:
                    labels(node["items"])
            labels(schema)
            if schema["type"] != "object" or schema.get("nullable"):
                raise ValueError("action_schema")
            schemas[name] = schema
        review(item["review"], schemas["plan"], catalog)
        facts(item["result_facts"], schemas["result"], catalog)
        facts(item["recovery_facts"], schemas["recovery"], catalog)
        binding = {"descriptor": item, "schemas": schemas}
        admitted.append({**binding, "descriptor_sha256": hashlib.sha256(canonical(binding)).hexdigest()})
    # Expanded schemas are duplicated per action in the admitted metadata reply.
    # Bound the actual expansion, not just each source file or manifest length.
    encode(admitted, 384 * 1024)
    return admitted
