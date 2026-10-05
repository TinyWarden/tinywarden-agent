"""Exercise the public author CLI from an independent directory, without imports."""
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TOOL = ROOT / "sdk/python/skill_tool.py"


def call(tool, *args, cwd):
    result = subprocess.run(["/usr/bin/python3.13", "-I", "-S", "-B", str(tool), *map(str, args)],
                            cwd=cwd, env={"LANG": "C.UTF-8", "PATH": "/usr/bin:/bin"},
                            capture_output=True, text=True, timeout=90)
    return result.returncode, json.loads(result.stdout) if result.stdout.startswith("{") else result.stderr


class Workspace(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.skill = self.directory / "memory"
        code, result = call(TOOL, "init", self.skill, "--id", "acme/memory", "--publisher", "Acme", cwd=self.directory)
        self.assertEqual(code, 0, result)


class Authoring(Workspace):
    def test_independent_kit_and_deterministic_zip(self):
        kit = self.directory / "kit"
        shutil.copytree(ROOT / "sdk/python", kit / "sdk/python", ignore=shutil.ignore_patterns("__pycache__"))
        shutil.copytree(ROOT / "skills/examples/memory-pressure", kit / "skills/examples/memory-pressure")
        tool = kit / "sdk/python/skill_tool.py"
        independent = self.directory / "other-author"
        code, created = call(tool, "init", independent, "--id", "independent/memory", "--publisher", "Independent", cwd=self.directory)
        self.assertEqual(code, 0, created)
        self.assertFalse((kit / "internal").exists())
        self.assertFalse((kit / "app").exists())
        archives = [self.directory / "one.zip", self.directory / "two.zip"]
        for archive in archives:
            code, packed = call(tool, "pack", independent, "--output", archive, cwd=self.directory)
            self.assertEqual(code, 0, packed)
            self.assertEqual(packed["content_sha256"], created["content_sha256"])
            code, admitted = call(tool, "validate", archive, cwd=self.directory)
            self.assertEqual(code, 0, admitted)
            self.assertEqual(admitted["content_sha256"], created["content_sha256"])
        self.assertEqual(archives[0].read_bytes(), archives[1].read_bytes())
        # An existing output is preserved byte-for-byte, even if writable.
        code, result = call(tool, "pack", independent, "--output", archives[0], cwd=self.directory)
        self.assertNotEqual(code, 0, result)
        self.assertEqual(archives[0].read_bytes(), archives[1].read_bytes())

    def test_static_validation_and_pack_never_execute_module(self):
        marker = self.directory / "import-would-write-this"
        path = self.skill / "skill.py"
        path.write_text(path.read_text() + "\nopen(" + repr(str(marker)) + ", 'w').write('imported')\n")
        for args in [("validate", self.skill), ("pack", self.skill, "--output", self.directory / "safe.zip")]:
            code, result = call(TOOL, *args, cwd=self.directory)
            self.assertEqual(code, 0, result)
            self.assertFalse(marker.exists())

    def test_existing_directory_reserved_id_and_source_output_are_refused(self):
        manifest = (self.skill / "skill.json").read_bytes()
        for identity, target in [("acme/other", self.skill), ("tinywarden/community", self.directory / "reserved")]:
            code, result = call(TOOL, "init", target, "--id", identity, "--publisher", "Acme", cwd=self.directory)
            self.assertNotEqual(code, 0, result)
        self.assertEqual((self.skill / "skill.json").read_bytes(), manifest)
        alias = self.directory / "alias"
        alias.symlink_to(self.skill, target_is_directory=True)
        for output in (self.skill / "output.zip", alias / "output.zip"):
            code, result = call(TOOL, "pack", self.skill, "--output", output, cwd=self.directory)
            self.assertNotEqual(code, 0, result)
            self.assertFalse((self.skill / "output.zip").exists())

    def test_bad_schema_and_links_are_rejected(self):
        schema = self.skill / "settings.schema.json"
        original = schema.read_bytes()
        value = json.loads(original)
        value["properties"]["warning_percent"]["pattern"] = ".*"
        schema.write_text(json.dumps(value))
        code, result = call(TOOL, "validate", self.skill, cwd=self.directory)
        self.assertNotEqual(code, 0, result)
        schema.write_bytes(original)
        os.symlink(self.directory / "outside", self.skill / "linked.txt")
        code, result = call(TOOL, "pack", self.skill, "--output", self.directory / "linked.zip", cwd=self.directory)
        self.assertNotEqual(code, 0, result)
        self.assertFalse((self.directory / "linked.zip").exists())

    def test_invalid_test_expectations_and_missing_runtime_fail_explicitly(self):
        cases = self.directory / "bad-cases.json"
        cases.write_text(json.dumps({"format": 1, "cases": [{"name": "No assertion", "expect": {"errors": []}}]}))
        code, result = call(TOOL, "test", self.skill, "--cases", cases, cwd=self.directory)
        self.assertNotEqual(code, 0, result)
        self.assertEqual(result["error"], "fixture_expect")
        code, result = call(TOOL, "test", self.skill, "--runtime", self.directory / "absent", cwd=self.directory)
        self.assertNotEqual(code, 0, result)
        self.assertEqual(result["error"], "FileNotFoundError")


if __name__ == "__main__":
    unittest.main()
