"""Run under a delegated native unit, never silently skip unavailable isolation."""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from fixtures import package

ROOT = Path(__file__).resolve().parents[2]
SUPERVISOR = ROOT / "sdk/python/runtime/supervisor.py"


class Isolation(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.path = package(Path(self.temporary.name) / "probe")

    def run_skill(self, function="validate_settings", arguments=None, grants=None, ceiling=None):
        request = {"action": "run", "package": str(self.path), "function": function,
                   "arguments": {} if arguments is None else arguments,
                   "grants": grants or [], "ceiling": ceiling or []}
        result = subprocess.run(["/usr/bin/python3.13", "-I", "-S", "-B", str(SUPERVISOR)],
                                input=json.dumps(request), capture_output=True, text=True, timeout=12,
                                env={}, cwd="/")
        self.assertEqual(result.returncode, 0, result.stderr[-200:])
        return json.loads(result.stdout)

    def replace_validation(self, body):
        path = self.path / "skill.py"
        text = path.read_text().replace("    return []", body, 1)
        path.write_text(text)

    def test_normal_execution(self):
        self.assertEqual(self.run_skill()["result"], [])

    def test_distro_library_aliases_share_only_private_mounts(self):
        self.replace_validation("""    import os
    assert os.readlink('/lib') == 'usr/lib'
    assert os.stat('/lib').st_ino == os.stat('/usr/lib').st_ino
    assert not os.path.exists('/usr/bin/apt-get')
    assert not os.path.exists('/etc/apt')
    return []""")
        self.assertEqual(self.run_skill()["result"], [])

    def test_host_credentials_and_network_are_absent(self):
        self.replace_validation("""    import os, socket
    assert os.environ.get('DATABASE_URL') is None
    assert os.environ.get('TW_TEST_SECRET') is None
    for path in ('/home', '/root', '/run', '/var/lib/tinywarden-agent', '/sys/fs/cgroup'):
        assert not os.path.exists(path)
    try:
        socket.socket()
    except OSError:
        pass
    else:
        raise AssertionError('socket available')
    assert 'NoNewPrivs:\\t1' in open('/proc/self/status').read()
    return []""")
        self.assertEqual(self.run_skill()["result"], [])

    def test_cpu_exhaustion_and_cleanup(self):
        self.replace_validation("    while True: pass")
        self.assertEqual(self.run_skill()["error"], "resource_exhausted")
        self.replace_validation("    return []")
        # A second independent invocation proves a slot is reusable after cleanup.
        package_path = self.path / "skill.py"
        package_path.write_text(package_path.read_text().replace("    while True: pass", "    return []"))
        self.assertEqual(self.run_skill()["result"], [])

    def test_namespace_syscalls_are_denied(self):
        self.replace_validation("""    import ctypes
    libc = ctypes.CDLL(None, use_errno=True)
    assert libc.unshare(0x10000000) == -1
    assert libc.ptrace(0, 0, 0, 0) == -1
    return []""")
        self.assertEqual(self.run_skill()["result"], [])

    def test_denied_broker_cannot_be_hidden_by_skill(self):
        path = self.path / "skill.py"
        path.write_text(path.read_text().replace("def collect(settings, host):\n    return {}", """def collect(settings, host):
    try:
        host.request('files.read', {'path': '/proc/meminfo', 'max_bytes': 65536})
    except Exception:
        pass
    return {}"""))
        self.assertEqual(self.run_skill("collect")["error"], "capability_denied")

    def test_memory_exhaustion_is_not_a_clean_result(self):
        self.replace_validation("    x = bytearray(512 * 1024 * 1024)\n    return []")
        self.assertEqual(self.run_skill()["error"], "resource_exhausted")

    def test_output_is_bounded(self):
        self.replace_validation("    import os\n    os.write(1, b'x' * 100000)\n    return []")
        self.assertEqual(self.run_skill()["error"], "output_exceeded")

    def test_process_budget_and_descendant_cleanup(self):
        self.replace_validation("""    import os, time
    while True:
        try:
            pid = os.fork()
        except OSError:
            return []
        if pid == 0:
            time.sleep(60)
            os._exit(0)""")
        self.assertEqual(self.run_skill()["error"], "resource_exhausted")

    def test_granted_host_read_and_local_ceiling(self):
        grant = {'operation': 'files.read', 'paths': ['/proc/meminfo'], 'roots': [], 'max_bytes': 65536}
        manifest = self.path / 'skill.json'
        value = json.loads(manifest.read_text()); value['capabilities'] = [grant]
        manifest.write_text(json.dumps(value))
        path = self.path / 'skill.py'
        path.write_text(path.read_text().replace('def collect(settings, host):\n    return {}', """def collect(settings, host):
    result = host.request('files.read', {'path': '/proc/meminfo', 'max_bytes': 65536})
    assert 'MemTotal:' in result['text'] and not result['truncated']
    return {}"""))
        self.assertEqual(self.run_skill('collect', grants=[grant], ceiling=[grant])['result'], {})
        self.assertEqual(self.run_skill('collect', grants=[grant])['error'], 'capability_denied')

    def test_readonly_package_and_host_process_isolation(self):
        self.replace_validation("""    import os
    try:
        open('/skill/skill.py', 'w').write('changed')
    except OSError:
        pass
    else:
        raise AssertionError('package writable')
    assert len([x for x in os.listdir('/proc') if x.isdigit()]) <= 3
    return []""")
        self.assertEqual(self.run_skill()['result'], [])


if __name__ == "__main__":
    unittest.main()
