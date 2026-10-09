"""Canonical capacity and stable-series proof through the actual SDK sandbox."""
import json
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from packages import load


class DiskDisplay(unittest.TestCase):
    def test_capacity_thresholds_missing_coverage_and_stable_series(self):
        directory = ROOT / "skills/official/disk-local"
        metadata = load(directory, official=True)
        mount = {"mount_id": 1, "mount_path": "/", "mount_root": "/", "filesystem_type": "ext4",
                 "kind": "local", "writable": True, "shared_capacity": False, "reason": "none",
                 "total_bytes": "100", "free_bytes": "20", "available_bytes": "15"}

        def evaluate(value, coverage="complete", reason="none"):
            context = {"identity": {"installation_id": "11111111-1111-4111-8111-111111111111",
                                    "assignment_id": "22222222-2222-4222-8222-222222222222",
                                    "content_sha256": metadata["content_sha256"], "generation": 1},
                       "settings": metadata["manifest"]["defaults"],
                       "observation": {"coverage": coverage, "reason": reason, "excluded_kernel": 0,
                                       "excluded_remote": 0, "mounts": [value]},
                       "previous_state": None, "state": {}, "captured_at": 1800000000000,
                       "received_at": 1800000000000, "now": 1800000000000,
                       "evidence_expires_at": 1800000600000}
            request = {"action": "run", "package": str(directory), "official": True,
                       "content_sha256": metadata["content_sha256"], "function": "evaluate", "arguments": context}
            result = subprocess.run(["/usr/bin/python3.13", "-I", "-S", "-B",
                                     str(ROOT / "sdk/python/runtime/supervisor.py")],
                                    input=json.dumps(request), capture_output=True, text=True, timeout=12,
                                    cwd="/", env={"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"})
            self.assertEqual(result.returncode, 0)
            response = json.loads(result.stdout)
            self.assertNotIn("error", response, response)
            assessment = response["result"][0]
            rows = next(f for f in assessment["facts"] if f["key"] == "filesystems")["rows"]
            row = rows[0] if rows else None
            return assessment, row

        result, row = evaluate(mount)
        self.assertEqual((result["status"], row["used"]), ("healthy", "84.2"))
        diagnostic = {fact["key"]: fact for fact in result["facts"]}
        self.assertEqual(diagnostic["collection_notice"]["value"], "")
        self.assertEqual(diagnostic["collection_issues"]["rows"], [])
        self.assertEqual(evaluate({**mount, "mount_id": 99})[1]["series"], row["series"])
        self.assertNotEqual(evaluate({**mount, "mount_root": "/other"})[1]["series"], row["series"])
        for free, status in (("15", "warning"), ("5", "critical")):
            self.assertEqual(evaluate({**mount, "free_bytes": free, "available_bytes": free})[0]["status"], status)
        incomplete, _ = evaluate(mount, "incomplete", "topology_changed")
        self.assertEqual(incomplete["status"], "unknown")
        self.assertEqual(next(f for f in incomplete["facts"] if f["key"] == "collection_notice")["value"], "topology_changed")
        result, readonly = evaluate({**mount, "writable": False})
        self.assertEqual(result["status"], "unknown")
        self.assertIsNone(readonly)
        missing = {k: v for k, v in mount.items() if k != "free_bytes"}
        result, row = evaluate(missing)
        self.assertEqual(row["used"], "")
        self.assertEqual(next(f for f in result["facts"] if f["key"] == "collection_issues")["rows"],
                         [{"path": "/", "problem": "capacity_unavailable"}])
        self.assertEqual(evaluate({**mount, "free_bytes": "14"})[1]["used"], "")
        inaccessible = {**missing, "mount_path": "/data", "reason": "mount_inaccessible"}
        result, _ = evaluate(inaccessible, "incomplete", "mount_inaccessible")
        self.assertEqual(next(f for f in result["facts"] if f["key"] == "collection_issues")["rows"],
                         [{"path": "/data", "problem": "mount_inaccessible"}])
        sources = metadata["display"]["sources"]
        key = sources["collection_issues"]["columns"]["problem"]["enum"]["mount_inaccessible"]
        self.assertEqual(metadata["catalog"][key]["text"], "Could not access this filesystem or read its space usage.")
        self.assertNotIn("excluded_kernel", sources)
        self.assertNotIn("excluded_remote", sources)


if __name__ == "__main__":
    unittest.main()
