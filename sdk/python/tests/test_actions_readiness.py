"""Declarative prerequisite probes never execute a command or module."""
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runtime"))
from actions.readiness import check


class ActionReadiness(unittest.TestCase):
    def probe(self):
        value = json.loads((Path(__file__).parent / "fixtures/skill-actions-v1.json").read_text())["invocation"]["binding"]
        return {"grant_id": value["installation_id"], "installation_id": value["installation_id"],
            "content_sha256": value["content_sha256"], "descriptor_sha256": value["descriptor_sha256"],
            "id": value["action_id"], "version": 1, "revision": "1", "origin": value["origin"],
            "execution_class": "unprivileged", "prerequisites": []}

    def test_declared_module_is_found_without_import(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); marker = root / "imported"
            (root / "tw_readiness_only.py").write_text("open(" + repr(str(marker)) + ", 'w').write('executed')")
            probe = self.probe()
            probe["prerequisites"] = [{"kind": "python_module", "id": "example.module", "module": "tw_readiness_only", "package": "example"}]
            policy = {"origins": [{**probe["origin"], "classes": ["unprivileged"]}]}
            sys.path.insert(0, temporary)
            try:
                with patch("actions.readiness.read_policy", return_value=policy):
                    self.assertTrue(check([probe], "/policy")["actions"][0]["ready"])
                self.assertFalse(marker.exists())
            finally:
                sys.path.remove(temporary)

    def test_unknown_trust_and_missing_prerequisites_stay_ineligible(self):
        probe = self.probe()
        probe["prerequisites"] = [{"kind": "executable", "id": "example.binary", "path": "/usr/bin/tw_missing_fixture", "package": "example"}]
        with patch("actions.readiness.read_policy", return_value={"origins": []}):
            value = check([probe], "/policy")["actions"][0]
            self.assertFalse(value["ready"])
            self.assertEqual(value["reason"], "action_permission_required")
            self.assertEqual(value["missing"], ["example.binary"])
            with self.assertRaises(ValueError):
                check([probe, probe], "/policy")
        probe["prerequisites"][0]["path"] = "/tmp/run"
        with patch("actions.readiness.read_policy", return_value={"origins": []}), self.assertRaises(ValueError):
            check([probe], "/policy")


if __name__ == "__main__":
    unittest.main()
