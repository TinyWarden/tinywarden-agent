#!/usr/bin/env python3
"""Zero-dependency SDK author CLI. Never import authored skills in this process."""
import argparse
import json
import subprocess
import sys
import zipfile
from pathlib import Path

SDK = Path(__file__).resolve().parent
sys.path.insert(0, str(SDK / "runtime"))
sys.path.insert(0, str(SDK))
from authoring.cases import run_cases
from authoring.execution import ASSETS, invoke, runtime_assets
from authoring.packages import create_zip, exclusive_write, initialize, snapshot, summary
from json_values import decode, encode

COPY = json.loads((SDK / "authoring/en.json").read_text(encoding="utf-8"))


def parser():
    result = argparse.ArgumentParser(description=COPY["description"])
    commands = result.add_subparsers(dest="command", required=True)
    starter = commands.add_parser("init", help=COPY["init"])
    starter.add_argument("directory", help=COPY["new_directory"])
    starter.add_argument("--id", required=True, help=COPY["id"])
    starter.add_argument("--publisher", required=True, help=COPY["publisher"])
    for name in ("validate", "pack", "test", "collect"):
        command = commands.add_parser(name, help=COPY[name])
        command.add_argument("source", help=COPY["source"])
        if name == "pack":
            command.add_argument("--output", required=True, help=COPY["new_output"])
        if name in {"test", "collect"}:
            command.add_argument("--runtime", default=str(ASSETS), help=COPY["runtime"])
        if name == "test":
            command.add_argument("--cases", help=COPY["cases"])
        if name == "collect":
            command.add_argument("--approve-declared-grants", action="store_true", required=True, help=COPY["approval"])
            command.add_argument("--settings", help=COPY["settings"])
            command.add_argument("--filesystem-helper", help=COPY["filesystem_helper"])
            command.add_argument("--output", help=COPY["observation_output"])
    return result


def execute(args):
    if args.command == "init":
        return summary(initialize(args.directory, args.id, args.publisher)), 0
    source = Path(args.source).absolute()
    if args.command == "pack":
        output = Path(args.output).resolve()
        if source.resolve() == output or source.is_dir() and source.resolve() in output.parents:
            raise ValueError("archive_output")
    if args.command == "collect" and args.output:
        output = Path(args.output).resolve()
        if source.resolve() == output or source.is_dir() and source.resolve() in output.parents:
            raise ValueError("observation_output")
    with snapshot(source) as (directory, metadata):
        if args.command == "validate":
            return {**summary(metadata), "validation": "static", "code_executed": False}, 0
        if args.command == "pack":
            return create_zip(directory, metadata, args.output), 0
        runtime = runtime_assets(args.runtime)
        if args.command == "test":
            cases = Path(args.cases).absolute() if args.cases else directory / "tests.json"
            results = run_cases(runtime, directory, metadata, cases)
            return results, 0 if results["passed"] else 1
        settings = metadata["manifest"]["defaults"]
        if args.settings:
            with open(args.settings, "rb") as opened:
                settings = decode(opened.read(1024 * 1024 + 1))
        errors = invoke(runtime, directory, metadata, "validate_settings", settings)
        if errors:
            return {"error": "settings_invalid", "fields": errors}, 1
        observation = invoke(runtime, directory, metadata, "collect", settings, collect=True,
                             filesystem_helper=args.filesystem_helper)
        if args.output:
            exclusive_write(Path(args.output).absolute(), encode(observation) + b"\n")
        return {"observation": observation, "content_sha256": metadata["content_sha256"]}, 0


def main():
    args = parser().parse_args()
    try:
        result, code = execute(args)
    except subprocess.TimeoutExpired:
        result, code = {"error": "runtime:deadline_exceeded"}, 1
    except (ValueError, TypeError, KeyError, OSError, RecursionError, SyntaxError, UnicodeError, zipfile.BadZipFile) as error:
        # Static errors are from our loader. No module traceback or raw host output.
        result = {"error": str(error)[:200] if isinstance(error, ValueError) else type(error).__name__}
        code = 1
    sys.stdout.buffer.write(encode(result) + b"\n")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
