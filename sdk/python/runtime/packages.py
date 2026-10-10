"""Directory admission and executable identity; ZIP transport is a separate layer."""
import ast
import hashlib
import re
import stat
import struct
from pathlib import Path
from json_values import decode, encode
from schema import inspect, validate
from grants import inspect_grant
from package_files import tree
from display import inspect_display
from notifications import inspect_notifications

MANIFEST_KEYS = {"format", "id", "version", "runtime", "sdk", "state_version", "license",
                 "publisher", "name_key", "description_key", "category", "compatibility",
                 "defaults", "schedule", "capabilities", "limits", "fields", "alias"}
REQUIRED = {"skill.json", "skill.py", "settings.schema.json", "observation.schema.json",
            "state.schema.json", "messages/en.json", "README.md", "LICENSE"}
ALIASES = {"disk-local", "package-updates", "reboot-required", "fstrim-status"}
OPERATIONS = {"files.read", "files.stat", "files.list", "filesystems.snapshot",
              "systemd.properties", "command.capture"}


def path_name(name):
    parts = name.split("/")
    if len(name.encode()) > 200 or len(parts) > 8 or any(x in {"", ".", ".."} for x in parts):
        raise ValueError("package_path")
    if not re.fullmatch(r"[A-Za-z0-9_.\-/]+", name):
        raise ValueError("package_path")
    if name != "LICENSE" and Path(name).suffix not in {".py", ".json", ".md", ".txt"}:
        raise ValueError("package_payload")


def content(root):
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError("package_path")
    files, size = tree(root, path_name)
    if not REQUIRED <= files.keys():
        raise ValueError("package_missing")
    digest = hashlib.sha256(b"tw-skill-content-v1\0")
    for name in sorted(files):
        path = name.encode()
        data = files[name]
        digest.update(struct.pack(">I", len(path)) + path + struct.pack(">Q", len(data)))
        digest.update(hashlib.sha256(data).digest())
    return files, digest.hexdigest(), size


def load(root, expected=None, official=False):
    files, digest, size = content(root)
    if expected is not None and expected != digest:
        raise ValueError("package_digest")
    manifest = decode(files["skill.json"], 65536)
    if not isinstance(manifest, dict) or set(manifest) - MANIFEST_KEYS or MANIFEST_KEYS - {"alias"} - manifest.keys():
        raise ValueError("package_manifest")
    if type(manifest["format"]) is not int or type(manifest["sdk"]) is not int or manifest["format"] != 1 or manifest["sdk"] != 1 or manifest["runtime"] != "python-3.13-v1":
        raise ValueError("package_runtime")
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}/[a-z][a-z0-9-]{0,63}", manifest["id"]):
        raise ValueError("package_id")
    if manifest["id"].startswith("tinywarden/") and not official:
        raise ValueError("package_reserved")
    if manifest.get("alias") is not None and (not official or manifest["alias"] not in ALIASES):
        raise ValueError("package_alias")
    if not isinstance(manifest["version"], str) or len(manifest["version"]) > 64 or not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", manifest["version"]):
        raise ValueError("package_version")
    if type(manifest["state_version"]) is not int or not 1 <= manifest["state_version"] <= 65535:
        raise ValueError("package_state")
    for key in ("license", "publisher", "category", "name_key", "description_key"):
        if not isinstance(manifest[key], str) or not 1 <= len(manifest[key]) <= 200:
            raise ValueError("package_metadata")
    compatibility = manifest["compatibility"]
    if not isinstance(compatibility, dict) or set(compatibility) != {"os", "architectures"} or compatibility["os"] != ["debian:13"] or not isinstance(compatibility["architectures"], list) or not compatibility["architectures"] or len(set(compatibility["architectures"])) != len(compatibility["architectures"]) or not set(compatibility["architectures"]) <= {"amd64", "arm64"}:
        raise ValueError("package_compatibility")
    if not isinstance(manifest["capabilities"], list) or len(manifest["capabilities"]) > 32:
        raise ValueError("package_capabilities")
    for grant in manifest["capabilities"]:
        if not isinstance(grant, dict) or grant.get("operation") not in OPERATIONS:
            raise ValueError("package_capabilities")
        inspect_grant(grant)
    if manifest["schedule"] != {"minimum_seconds": 60, "maximum_seconds": 86400}:
        raise ValueError("package_schedule")
    limits = manifest["limits"]
    if not isinstance(limits, dict) or set(limits) != {"wall_seconds"} or type(limits["wall_seconds"]) is not int or not 1 <= limits["wall_seconds"] <= 60:
        raise ValueError("package_limits")
    schemas = {}
    for kind in ("settings", "observation", "state"):
        schemas[kind] = decode(files[kind + ".schema.json"], 65536)
        inspect(schemas[kind], settings=kind == "settings")
    validate(schemas["settings"], manifest["defaults"])
    interval = schemas["settings"].get("properties", {}).get("interval_seconds")
    if interval is not None and (interval.get("type") != "integer" or
            interval.get("minimum", 0) < 60 or interval.get("maximum", 86401) > 86400):
        raise ValueError("package_schedule")
    fields = manifest["fields"]
    if not isinstance(fields, dict) or set(fields) != set(schemas["settings"].get("properties", {})):
        raise ValueError("package_fields")
    catalog = decode(files["messages/en.json"], 65536)
    if not isinstance(catalog, dict) or not 1 <= len(catalog) <= 256:
        raise ValueError("package_catalog")
    for key, entry in catalog.items():
        if not re.fullmatch(r"[a-zA-Z][a-zA-Z0-9_.-]{0,127}", key) or not isinstance(entry, dict):
            raise ValueError("package_catalog")
        if set(entry) != {"text", "parameters"} or not isinstance(entry["text"], str) or len(entry["text"]) > 2048:
            raise ValueError("package_catalog")
        if any(c in entry["text"] for c in ("<", ">", "\x00")) or not isinstance(entry["parameters"], dict):
            raise ValueError("package_catalog")
        if set(re.findall(r"\{([a-zA-Z_][a-zA-Z0-9_]*)\}", entry["text"])) != set(entry["parameters"]):
            raise ValueError("package_catalog")
        if len(entry["parameters"]) > 16 or any(x not in {"string", "number", "boolean"} for x in entry["parameters"].values()):
            raise ValueError("package_catalog")
    refs = [manifest["name_key"], manifest["description_key"]]
    for field in fields.values():
        if not isinstance(field, dict) or set(field) != {"label_key", "help_key", "unit", "order"}:
            raise ValueError("package_fields")
        if not isinstance(field["unit"], str) or len(field["unit"]) > 32 or type(field["order"]) is not int:
            raise ValueError("package_fields")
        refs += [field["label_key"], field["help_key"]]
    if any(key not in catalog or catalog[key]["parameters"] for key in refs):
        raise ValueError("package_catalog")
    # Names reach shared history even when a package is disabled.
    if len(catalog[manifest["name_key"]]["text"].encode("utf-16-le")) > 4096 or len(
            encode(catalog[manifest["name_key"]]["text"])) > 1024:
        raise ValueError("package_catalog")
    parsed = ast.parse(files["skill.py"], filename="skill.py")
    functions = {node.name for node in parsed.body if isinstance(node, ast.FunctionDef)}
    if not {"validate_settings", "collect", "reduce", "evaluate"} <= functions:
        raise ValueError("package_entrypoints")
    encode(manifest)
    metadata = {"manifest": manifest, "schemas": schemas, "catalog": catalog,
                "content_sha256": digest, "size": size}
    if "display.json" in files:
        metadata["display"] = inspect_display(files["display.json"], catalog, schemas["settings"])
    if "notifications.json" in files:
        metadata["notifications"] = inspect_notifications(files["notifications.json"], catalog, schemas["settings"])
    return metadata
