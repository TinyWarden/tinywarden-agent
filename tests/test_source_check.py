"""Public assistant instructions must not admit private or sensitive paths."""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))
from source_check import protected_path


class SourcePaths(unittest.TestCase):
    def test_public_entry_point_and_existing_protections(self):
        self.assertFalse(protected_path("AGENTS.md"))
        self.assertFalse(protected_path(".env.example"))
        self.assertFalse(protected_path("docs/agents/README.md"))
        for name in ["docs/AGENTS.md", "nested/AGENTS.md", ".agents/context.md",
                     ".env", ".env.local", "nested/.env.local", "private.key",
                     "nested/certificate.pem", "node_modules/file.js"]:
            with self.subTest(name=name):
                self.assertTrue(protected_path(name))
