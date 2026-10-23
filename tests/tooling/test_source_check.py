"""Public assistant instructions must not admit private or sensitive paths."""
import sys
import io
import unittest
from contextlib import redirect_stderr
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts/checks"))
from source_check import protected_path, review_source_size, role


class SourcePaths(unittest.TestCase):
    def test_large_coherent_source_is_a_review_diagnostic(self):
        output = io.StringIO()
        with redirect_stderr(output):
            review_source_size("internal/example/source.go", 301)
        self.assertIn("Source cohesion review:", output.getvalue())
        self.assertIn("301 lines", output.getvalue())
        with self.assertRaises(ValueError):
            role("internal/example/source.go", {})

    def test_public_entry_point_and_existing_protections(self):
        self.assertFalse(protected_path("AGENTS.md"))
        self.assertFalse(protected_path(".env.example"))
        self.assertFalse(protected_path("docs/agents/README.md"))
        for name in ["docs/AGENTS.md", "nested/AGENTS.md", ".agents/context.md",
                     ".env", ".env.local", "nested/.env.local", "private.key",
                     "nested/certificate.pem", "node_modules/file.js"]:
            with self.subTest(name=name):
                self.assertTrue(protected_path(name))
