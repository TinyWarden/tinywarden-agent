"""Regression proof that changes to the actual production boundary are rejected."""
import sys
import contextlib
import io
import shutil
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts/checks"))
from neutrality import ROOT, check, check_content, check_inventory


class Neutrality(unittest.TestCase):
    def test_new_dependency_or_runtime_payload_requires_ownership_review(self):
        for new in ("internal/checks/example", "skills/example/skill.py", "actions/example_native.py"):
            with self.subTest(new=new), self.assertRaises(ValueError):
                check_inventory(["generic", new], ["generic"], "Production")
        with self.assertRaises(ValueError):
            check_inventory([], ["generic"], "Production")

    def test_existing_file_cannot_hide_skill_recipe_identity_or_data(self):
        for content in ('{"alias":"reboot-required"}', 'unit = "fstrim.timer"',
                        'import apt_pkg', 'var subject = "tinywarden/disk-local"'):
            with self.subTest(content=content), self.assertRaises(ValueError):
                check_content("existing_platform_file", content)
        check_content("generic", 'operation = "files.read"; service = request["unit"]')

    def test_real_graph_rejects_new_inputs_inside_an_existing_package(self):
        with tempfile.TemporaryDirectory(prefix="tw-boundary-") as temporary:
            root = Path(temporary)
            for name in ("cmd", "internal", "scripts/checks", "sdk/python/runtime"):
                shutil.copytree(ROOT / name, root / name, ignore=shutil.ignore_patterns("__pycache__"))
            for name in ("go.mod", "go.sum"):
                if (ROOT / name).exists():
                    shutil.copy2(ROOT / name, root / name)
            with contextlib.redirect_stdout(io.StringIO()):
                check(root)
            for suffix, content in (("go", "package agent\nfunc databaseReplicationHealthy(lag int) bool { return lag <= 30 }\n"),
                                    ("s", "// Unexpected native domain code.\n")):
                path = root / "internal/agent" / ("database_replication." + suffix)
                path.write_text(content)
                try:
                    with self.subTest(suffix=suffix), self.assertRaisesRegex(ValueError, "Production input file boundary changed"):
                        check(root)
                finally:
                    path.unlink()
            payload = root / "internal/cli/unreviewed.txt"
            payload.write_text("Domain data outside the known-marker vocabulary.")
            source = root / "internal/cli/cli.go"
            source.write_text(source.read_text().replace("//go:embed en.json", "//go:embed en.json unreviewed.txt"))
            with self.assertRaisesRegex(ValueError, "internal/cli/unreviewed.txt"):
                check(root)


if __name__ == "__main__":
    unittest.main()
