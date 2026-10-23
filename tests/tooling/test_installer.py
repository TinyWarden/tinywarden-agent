"""Exercise the public installer before any download or host mutation."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[2] / "deploy/install.sh"


class InstallerPrerequisites(unittest.TestCase):
    def run_installer(self, missing=(), os_id="debian", version="13", arch="x86_64",
                      certificates=True, uid="0"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tools = root / "bin"
            tools.mkdir()
            release = root / "os-release"
            release.write_text(f'ID={os_id}\nVERSION_ID="{version}"\n')
            certs = root / "ca-certificates.crt"
            certs.write_text("fixture certificate bundle" if certificates else "")
            # Redirect only host metadata; run the real script and command checks.
            script = root / "install.sh"
            script.write_text(INSTALLER.read_text().replace("/etc/os-release", str(release))
                              .replace("/etc/ssl/certs/ca-certificates.crt", str(certs)))
            for tool in ("openssl", "python3.13", "tar", "sha256sum", "mktemp", "cat", "chmod", "rm"):
                if tool not in missing:
                    target = shutil.which(tool)
                    self.assertIsNotNone(target, f"Test prerequisite unavailable: {tool}")
                    (tools / tool).symlink_to(target)
            for name, body in {
                "id": f"printf '%s\\n' '{uid}'",
                "uname": f"printf '%s\\n' '{arch}'",
                # No network. Reaching this marker proves that preflight passed.
                "curl": "echo DOWNLOAD_REACHED >&2; exit 73",
            }.items():
                if name not in missing:
                    path = tools / name
                    path.write_text("#!/bin/bash\n" + body + "\n")
                    path.chmod(0o700)
            env = {"PATH": str(tools), "TMPDIR": str(root), "LC_ALL": "C"}
            return subprocess.run(["/bin/bash", str(script)], env=env,
                                  capture_output=True, text=True, timeout=10)

    def test_only_python_missing_suggests_default_debian_package(self):
        result = self.run_installer(missing={"python3.13"})
        self.assertEqual(result.returncode, 1)
        self.assertIn("Missing prerequisites: python3.13\n", result.stderr)
        self.assertIn("Install missing packages: apt install python3\n", result.stderr)
        self.assertNotIn("DOWNLOAD_REACHED", result.stderr)

    def test_multiple_missing_commands_deduplicate_package_names(self):
        result = self.run_installer(missing={"curl", "sha256sum", "mktemp"}, certificates=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("Install missing packages: apt install curl coreutils ca-certificates\n", result.stderr)
        self.assertNotIn("python3", result.stderr)
        self.assertNotIn("DOWNLOAD_REACHED", result.stderr)

    def test_unsupported_hosts_get_no_package_or_download_advice(self):
        for values in ({"version": "12"}, {"os_id": "ubuntu"}, {"arch": "aarch64"}):
            with self.subTest(values=values):
                result = self.run_installer(missing={"python3.13"}, **values)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stderr, "Supported platform is Debian 13 x64 (amd64).\n")

    def test_ready_host_reaches_download_without_package_advice(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 73)
        self.assertEqual(result.stderr, "DOWNLOAD_REACHED\n")

    def test_root_required_before_preflight(self):
        result = self.run_installer(uid="1000")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "Run this installer as root.\n")
