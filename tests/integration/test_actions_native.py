"""Explicit disposable-host root proof for the generic delivered-code boundary."""
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path
import select
import shutil
import signal
import sys
import tempfile
import time
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "sdk/python/tests"))
sys.path.insert(0, str(ROOT / "sdk/python/runtime"))
from action_fixtures import action_package
from packages.admission import load


class ActionNative(unittest.TestCase):
    def setUp(self):
        if os.geteuid() != 0:
            self.fail("run explicitly as root on the designated disposable host")
        self.root = Path(tempfile.mkdtemp(prefix="tw-action-proof-", dir="/var/lib"))
        self.addCleanup(lambda: shutil.rmtree(self.root))
        self.children = set()
        self.jobs={kind:str(uuid.uuid4()) for kind in ('unprivileged','host_admin')}
        self.addCleanup(self.reap)
        self.package = action_package(self.root / "package")
        self.policy = self.root / "policy.json"
        self.policy.write_text(json.dumps({"schema_version": 1, "origins": [
            {"github_id": "12345", "owner_id": "67890", "classes": ["unprivileged", "host_admin"]}]}))

    def request(self, execution="unprivileged", phase="prepare"):
        path = self.package / "skill.json"
        manifest = json.loads(path.read_text())
        manifest["actions"][0]["execution_class"] = execution
        path.write_text(json.dumps(manifest))
        expected = 0 if execution == "host_admin" else 65534
        source = f"import os,time\nassert os.geteuid()=={expected}\n"
        source += '''def prepare(request, context):
    return {"plan_json": '{"complete":true}', "has_changes": False, "effect": "none", "result": None}
def fetch(request, context):
    fd=os.open('retained',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600,dir_fd=context.state_fd)
    os.write(fd,b'private staged state')
    os.close(fd)
    return {"downloaded": True, "plan_sha256": context.plan_sha256}
def apply(request, context):
    context.check_start()
    try:
        os.write(4,b'S')
    except OSError:
        pass
    time.sleep(0.3)
    return {"effect": "none", "reason": None, "local_verified": True, "process_running": False,
            "facts": {"complete": True}, "receipt_json": '{}'}
def verify(request, context):
    return {"verified": request["receipt"] == {}}
def inspect(request, context):
    context.commit({"complete": True})
'''
        if execution == "unprivileged":
            source = source.replace("assert os.geteuid()==65534", "assert os.geteuid()==65534\ntry:\n    os.fstat(3)\nexcept OSError:\n    pass\nelse:\n    raise RuntimeError('unprivileged code received the root guard')\nassert 'NoNewPrivs:\\t1' in open('/proc/self/status').read()")
        (self.package / "actions/entry.py").write_text(source)
        metadata = load(self.package)
        fixture = ROOT / "sdk/python/tests/fixtures/skill-actions-v1.json"
        request = copy.deepcopy(json.loads(fixture.read_text())["invocation"])
        request['job_id']=self.jobs[execution]
        request["binding"].update(content_sha256=metadata["content_sha256"],
            skill_id=metadata["manifest"]["id"], action_id=manifest["actions"][0]["id"],
            descriptor_sha256=metadata["actions"][0]["descriptor_sha256"])
        request["input_json"] = '{"complete":true}'
        request["binding"]["input_sha256"] = hashlib.sha256(request["input_json"].encode()).hexdigest()
        request["phase"] = phase
        if phase in {"fetch", "apply", "verify"}:
            request["plan_json"] = '{"complete":true}'
            request["plan_sha256"] = hashlib.sha256(request["plan_json"].encode()).hexdigest()
        if phase == "apply":
            request["start"] = {"job_id": request["job_id"], "attempt_id": request["binding"]["installation_id"],
                "boot_id": Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
                "not_after_ms": time.clock_gettime_ns(time.CLOCK_BOOTTIME)//1000000+50000}
        if phase == "verify":
            request["receipt_json"] = '{}'
        return request

    def spawn(self, request):
        guard = os.open(self.root / "guard", os.O_CREAT | os.O_RDWR, 0o600)
        fcntl.flock(guard, fcntl.LOCK_EX)
        stream = os.open(self.root / "result", os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
        reader, writer = os.pipe()
        notify_read, notify_write = os.pipe()
        child = os.fork()
        if child == 0:
            # Duplicate high before assigning the protocol descriptors.
            copied = [fcntl.fcntl(fd, fcntl.F_DUPFD, 100) for fd in (reader, stream, guard, notify_write)]
            for target, fd in zip((0, 1, 3, 4), copied):
                os.dup2(fd, target)
            os.closerange(5, 1024)
            os.execve('/usr/bin/python3', ['/usr/bin/python3', '-I', '-B',
                str(ROOT / 'sdk/python/runtime/actions/runner.py'), str(self.package), str(self.policy),str(self.root / ('action-state-'+request['job_id']))],
                {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C.UTF-8', 'TZ': 'UTC'})
        os.close(reader); os.close(stream); os.close(notify_write); os.close(guard)
        self.children.add(child)
        os.write(writer, json.dumps(request, separators=(',', ':')).encode()+b'\n')
        return child, writer, notify_read

    def replace_entry(self, request, source):
        (self.package / "actions/entry.py").write_text(source)
        metadata = load(self.package)
        request["binding"]["content_sha256"] = metadata["content_sha256"]
        request["binding"]["descriptor_sha256"] = metadata["actions"][0]["descriptor_sha256"]

    def assert_guard_held(self):
        fd = os.open(self.root / "guard", os.O_RDWR)
        try:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            os.close(fd)

    def test_returned_apply_retains_guard_until_detached_descendant_finishes(self):
        for execution in ("unprivileged", "host_admin"):
            with self.subTest(execution=execution):
                request = self.request(execution, "apply")
                source = (self.package / "actions/entry.py").read_text().replace(
                    "time.sleep(0.3)",
                    "import subprocess\n    subprocess.Popen(['/usr/bin/sleep','0.8'], start_new_session=True)")
                self.replace_entry(request, source)
                child, writer, notify = self.spawn(request)
                self.assertTrue(select.select([notify], [], [], 10)[0])
                self.assertEqual(os.read(notify, 1), b'S')
                deadline = time.monotonic() + 5
                while not (self.root / 'result').stat().st_size and time.monotonic() < deadline:
                    time.sleep(.01)
                self.assertGreater((self.root / 'result').stat().st_size, 0)
                self.assert_guard_held()
                self.assertEqual(os.waitpid(child, os.WNOHANG), (0, 0))
                os.kill(child, signal.SIGTERM)
                code, reply = self.finish(child, writer, notify)
                self.assertEqual(code, 0)
                self.assertTrue(reply['result']['local_verified'])

    def test_inspection_rejects_background_descendant_before_callback(self):
        request = self.request("host_admin", "inspect")
        source = (self.package / "actions/entry.py").read_text().replace(
            '    context.commit({"complete": True})',
            "    import subprocess\n    subprocess.Popen(['/usr/bin/sleep','30'], start_new_session=True)\n    context.commit({'complete':True})")
        self.replace_entry(request, source)
        child, writer, notify = self.spawn(request)
        self.assertTrue(select.select([notify], [], [], 10)[0])
        self.assertEqual(os.read(notify, 1), b'')
        code, _ = self.finish(child, writer, notify)
        self.assertEqual(code, 70)

    def test_safe_cancellation_reaps_detached_descendant(self):
        request = self.request("host_admin", "prepare")
        source = (self.package / "actions/entry.py").read_text().replace(
            'def prepare(request, context):',
            "def prepare(request, context):\n    import subprocess\n    subprocess.Popen(['/usr/bin/sleep','30'], start_new_session=True)\n    os.write(4,b'S')\n    time.sleep(30)")
        self.replace_entry(request, source)
        child, writer, notify = self.spawn(request)
        self.assertTrue(select.select([notify], [], [], 10)[0])
        self.assertEqual(os.read(notify, 1), b'S')
        self.assert_guard_held()
        os.kill(child, signal.SIGTERM)
        code, _ = self.finish(child, writer, notify)
        self.assertEqual(code, 70)
        fd = os.open(self.root / 'guard', os.O_RDWR)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            os.close(fd)

    def finish(self, child, writer, notify):
        os.close(writer); os.close(notify)
        _, status = os.waitpid(child, 0)
        self.children.discard(child)
        raw = (self.root / "result").read_bytes()
        return os.waitstatus_to_exitcode(status), json.loads(raw) if raw else None

    def test_both_classes_have_exact_scope_and_durable_regular_receipts(self):
        for execution in ('unprivileged', 'host_admin'):
            for phase in ('prepare', 'fetch', 'apply', 'verify'):
                with self.subTest(execution=execution, phase=phase):
                    request = self.request(execution, phase)
                    code, reply = self.finish(*self.spawn(request))
                    self.assertEqual(code, 0)
                    self.assertEqual(reply['binding'], request['binding'])
                    self.assertEqual(reply['phase'], phase)

    def test_policy_and_modified_artifact_fail_before_code(self):
        request = self.request()
        self.policy.write_text('{"schema_version":1,"origins":[]}')
        self.assertEqual(self.finish(*self.spawn(request)), (70, None))
        self.policy.write_text(json.dumps({"schema_version":1,"origins":[
            {"github_id":"12345","owner_id":"67890","classes":["unprivileged"]}]}))
        path = self.package/'actions/entry.py';path.write_text(path.read_text()+'\n# changed\n')
        self.assertEqual(self.finish(*self.spawn(request)), (70, None))

    def test_expired_start_never_enters_package_and_guard_survives_parent_copy(self):
        request = self.request(phase='apply')
        child, writer, notify = self.spawn(request)
        self.assertTrue(select.select([notify],[],[],10)[0])
        self.assertEqual(os.read(notify,1),b'S')
        # Original parent copy was closed. The root guardian retains the lock.
        lock = os.open(self.root/'guard', os.O_RDWR)
        try:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            os.kill(child, signal.SIGTERM)  # Applying guardian must let work finish.
            code, reply = self.finish(child, writer, notify)
            self.assertEqual(code, 0)
            self.assertTrue(reply['result']['local_verified'])
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            os.close(lock)
        request = self.request('host_admin','apply')
        request['start']['not_after_ms'] = 1
        self.assertEqual(self.finish(*self.spawn(request)), (70,None))

    def reap(self):
        for child in self.children:
            try:
                value, _ = os.waitpid(child,os.WNOHANG)
                if not value:
                    os.kill(child,signal.SIGKILL)
                    os.waitpid(child,0)
            except ChildProcessError:
                pass

    def test_inspection_holds_guard_until_local_commit_acknowledgement(self):
        request = self.request(phase='inspect')
        child, writer, notify = self.spawn(request)
        self.assertTrue(select.select([notify],[],[],10)[0])
        self.assertEqual(os.read(notify,1),b'\n')
        lock = os.open(self.root/'guard',os.O_RDWR)
        try:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            os.write(writer,b'\n')
            code, reply = self.finish(child,writer,notify)
            self.assertEqual(code,0)
            self.assertEqual(reply['result'],{'facts':{'complete':True}})
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        finally:
            os.close(lock)


if __name__ == '__main__':
    unittest.main()
