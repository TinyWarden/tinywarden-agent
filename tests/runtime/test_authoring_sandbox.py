"""Native author-kit acceptance; requires delegation, never bypasses the sandbox."""
import json
import shutil
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from test_authoring import Workspace, ROOT, TOOL, call


class AuthorIsolation(Workspace):
    def test_captured_cases_with_standalone_sdk(self):
        kit = self.directory / "kit"
        shutil.copytree(ROOT / "sdk/python", kit / "sdk/python", ignore=shutil.ignore_patterns("__pycache__"))
        tool = kit / "sdk/python/skill_tool.py"
        code, result = call(tool, "test", self.skill, cwd=self.directory)
        self.assertEqual(code, 0, result)
        self.assertTrue(result["passed"])
        self.assertEqual(len(result["cases"]), 5)
        # The package chooses expected health, not the tool or core source.
        cases = json.loads((self.skill / "tests.json").read_text())
        cases["cases"] = cases["cases"][:1]
        cases["cases"][0]["expect"]["statuses"] = ["critical"]
        path = self.directory / "wrong.json"
        path.write_text(json.dumps(cases))
        code, result = call(tool, "test", self.skill, "--cases", path, cwd=self.directory)
        self.assertEqual(code, 1, result)
        self.assertEqual(result["cases"][0]["error"], "fixture_status_mismatch")

    def test_real_collection_requires_explicit_approval(self):
        code, result = call(TOOL, "collect", self.skill, cwd=self.directory)
        self.assertEqual(code, 2, result)
        observation = self.directory / "observed.json"
        code, result = call(TOOL, "collect", self.skill, "--approve-declared-grants", "--output", observation, cwd=self.directory)
        self.assertEqual(code, 0, result)
        self.assertEqual(json.loads(observation.read_text()), result["observation"])
        self.assertGreater(int(result["observation"]["total_bytes"]), 0)
        self.assertTrue(0 <= result["observation"]["used_percent"] <= 100)

    def test_package_execution_cannot_write_author_files(self):
        marker = self.directory / "host-marker"
        path = self.skill / "skill.py"
        code = path.read_text().replace("    return []", "    open(" + repr(str(marker)) + ", 'w').write('escaped')\n    return []", 1)
        path.write_text(code)
        status, result = call(TOOL, "test", self.skill, cwd=self.directory)
        self.assertEqual(status, 1, result)
        self.assertEqual(result["cases"][0]["error"], "runtime:execution_failed")
        self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
