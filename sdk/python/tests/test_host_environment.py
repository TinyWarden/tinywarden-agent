"""Host helpers have stable time/locale without inheriting operator credentials."""
import json
import os
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1] / "runtime"))
from host.capture import capture


class Budget:
    def enter(self):
        pass

    def exhausted(self):
        return False


class HostEnvironmentTest(unittest.TestCase):
    def test_helpers_ignore_host_timezone_locale_and_secrets(self):
        script = """import json,os,time
print(json.dumps({'zone':time.strftime('%Z',time.localtime(1792359896)),
                  'clock':time.strftime('%Y-%m-%d %H:%M:%S',time.localtime(1792359896)),
                  'environment':dict(os.environ)}))"""
        for zone in ("Europe/Bucharest", "America/New_York", "Pacific/Auckland"):
            with self.subTest(zone=zone), patch.dict(os.environ, {
                    "TZ": zone, "LANG": "fr_FR.UTF-8", "TW_TEST_SECRET": "private"}):
                code, out, err = capture([sys.executable, "-I", "-S", "-c", script],
                                         b"", Budget(), 5)
            self.assertEqual(code, 0, err)
            value = json.loads(out)
            self.assertEqual(value["zone"], "UTC")
            self.assertEqual(value["clock"], "2026-10-18 21:44:56")
            self.assertEqual(value["environment"], {
                "TZ": "UTC", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"})


if __name__ == "__main__":
    unittest.main()
