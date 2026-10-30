"""Real child streams and vanished proc inodes never alter execution authority."""
import json
import errno
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import signal
import time
import unittest
from unittest.mock import patch

RUNTIME = Path(__file__).resolve().parents[1] / "runtime"
sys.path.insert(0, str(RUNTIME))
from actions.guardian import descendants
from actions.output import OutputCapture, sync_output


class ActionOutput(unittest.TestCase):
    def test_watch_quota_failure_happens_before_work_and_releases_resources(self):
        from actions import output
        before = set(os.listdir('/proc/self/fd'))
        with patch.object(output.selectors.EpollSelector, 'register', side_effect=OSError(errno.ENOSPC, 'watch quota')):
            with self.assertRaises(OSError):
                OutputCapture(object())
        self.assertEqual(set(os.listdir('/proc/self/fd')), before)

    def test_capture_setup_drain_and_close_failures_keep_supervision_and_native_work(self):
        program = r'''
import os,sys,time
from pathlib import Path
from actions.guardian import supervise
from actions.logging import ActionLogger
from actions.output import OutputCapture
root=Path(sys.argv[1]);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
capture=OutputCapture(ActionLogger(fd,"apply",{}))
def fault(*args):
    raise OSError(28,"optional capture unavailable")
setattr(capture,sys.argv[2],fault)
def action():
    # Exceeds pipe capacity: fallback must drain, not merely wait for the worker.
    os.write(1,b"output\n"*20000)
    child=os.fork()
    if child==0:
        os.write(2,b"error\n"*20000)
        time.sleep(.1)
        (root/"receipt").write_text("complete")
        os._exit(0)
code=supervise(action,"apply",capture)
assert code==0, code
assert (root/"receipt").read_text()=="complete"
try:
    os.waitpid(-1,os.WNOHANG)
except ChildProcessError:
    pass
else:
    raise AssertionError("supervision returned with a live or unreaped child")
'''
        for method in ('parent', 'drain', 'close'):
            with self.subTest(method=method), tempfile.TemporaryDirectory() as directory:
                result = subprocess.run([sys.executable, '-c', program, directory, method],
                                        env={**os.environ, 'PYTHONPATH': str(RUNTIME)},
                                        capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stderr.decode())

    def test_capture_failure_preserves_apply_and_safe_phase_signal_policy(self):
        program = r'''
import os,signal,sys,time
from pathlib import Path
from actions.guardian import supervise
from actions.logging import ActionLogger
from actions.output import OutputCapture
root=Path(sys.argv[1]);phase=sys.argv[2];fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
capture=OutputCapture(ActionLogger(fd,phase,{}))
def fault():
    raise OSError(28,"capture failed")
capture.drain=fault
def stop(signum,frame):
    raise SystemExit(70)
def action():
    signal.signal(signal.SIGTERM,signal.SIG_IGN if phase=="apply" else stop)
    signal.pthread_sigmask(signal.SIG_UNBLOCK,{signal.SIGTERM})
    (root/"started").write_text("started")
    time.sleep(.5)
    (root/"receipt").write_text("complete")
raise SystemExit(supervise(action,phase,capture))
'''
        for phase in ('apply', 'prepare'):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                process = subprocess.Popen([sys.executable, '-c', program, directory, phase],
                                           env={**os.environ, 'PYTHONPATH': str(RUNTIME)},
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    deadline = time.monotonic() + 5
                    while not (Path(directory)/'started').exists() and time.monotonic() < deadline:
                        time.sleep(.01)
                    self.assertTrue((Path(directory)/'started').exists())
                    process.send_signal(signal.SIGTERM)
                    _, error = process.communicate(timeout=6)
                    self.assertEqual(process.returncode, 0 if phase == 'apply' else 70, error.decode())
                    self.assertEqual((Path(directory)/'receipt').exists(), phase == 'apply')
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.wait()

    def test_partial_capture_resource_failure_releases_allocated_fds(self):
        pair = os.pipe()
        with patch('actions.output.os.pipe2', side_effect=[pair, OSError(24, "too many open files")]):
            with self.assertRaises(OSError):
                OutputCapture(object())
        for fd in pair:
            with self.assertRaises(OSError):
                os.fstat(fd)

    def test_marker_flush_discards_stale_acknowledgements_and_fails_without_affecting_work(self):
        class Control:
            def send(self, token):
                self.token = token
                self.replies = [b"stale", token]
            def recv(self, count):
                return self.replies.pop(0)
            def settimeout(self, timeout):
                if not 0 < timeout <= .1:
                    raise AssertionError("flush budget changed")
        with patch('actions.output._control', Control()):
            self.assertTrue(sync_output())
        with patch('actions.output._control', Control()), patch.object(Control, 'recv', side_effect=TimeoutError()):
            self.assertFalse(sync_output())

    def test_real_reaped_proc_inode_esrch_is_treated_as_a_vanished_child(self):
        child = subprocess.Popen(["/bin/sleep", "30"])
        with open(f"/proc/{child.pid}/stat") as stale:
            child.terminate()
            child.wait()
            original = Path.read_text
            def raced(path, *args, **kwargs):
                if str(path) == "/proc/999999999/task/999999999/children":
                    return str(child.pid)
                if str(path) == f"/proc/{child.pid}/stat":
                    return stale.read()  # Actual Linux ESRCH, not a fabricated error.
                return original(path, *args, **kwargs)
            with patch.object(Path, "read_text", raced):
                self.assertEqual(descendants(999999999), {})

    def test_reaped_children_inode_esrch_is_handled_but_other_errors_are_not_hidden(self):
        with patch.object(Path, "read_text", side_effect=ProcessLookupError(3, "gone")):
            self.assertEqual(descendants(999999999), {})
        with patch.object(Path, "read_text", side_effect=PermissionError(13, "denied")):
            with self.assertRaises(PermissionError):
                descendants(999999999)

    def test_guardian_drains_native_output_and_waits_for_descendant_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            program = r'''
import os,subprocess,sys,time
from pathlib import Path
from actions.guardian import supervise
from actions.logging import ActionLogger
from actions.output import OutputCapture
root=Path(sys.argv[1]);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
logger=ActionLogger(fd,"apply",{});capture=OutputCapture(logger)
def action():
    # A native subprocess and Python share the captured fds. No worker thread.
    subprocess.run(["/bin/sh","-c","printf 'native output\\n'; printf 'native error\\n' >&2"],check=True)
    print("unicode: 測試",flush=True)
    child=os.fork()
    if child==0:
        time.sleep(.15)
        (root/"receipt").write_text("complete")
        os._exit(0)
    # Return before the descendant: guardian must retain ownership and drain it.
code=supervise(action,"apply",capture)
assert (root/"receipt").read_text()=="complete"
assert code==0
'''
            result = subprocess.run([sys.executable, "-c", program, directory],
                                    env={**os.environ, "PYTHONPATH": str(RUNTIME)}, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            records = [json.loads(line) for line in (Path(directory) / "operation-log.jsonl").read_text().splitlines()]
            outputs = [r["output"] for r in records]
            self.assertIn("native output", "".join(r["text"] for r in outputs if r["stream"] == "stdout"))
            self.assertIn("native error", "".join(r["text"] for r in outputs if r["stream"] == "stderr"))
            self.assertIn("unicode: 測試", "".join(r["text"] for r in outputs))

    def test_supervision_error_is_logged_and_does_not_abandon_a_running_child(self):
        with tempfile.TemporaryDirectory() as directory:
            program = r'''
import os,sys,time
from pathlib import Path
import actions.guardian as guardian
from actions.logging import ActionLogger
from actions.output import OutputCapture
root=Path(sys.argv[1]);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
capture=OutputCapture(ActionLogger(fd,"apply",{}))
def action():
    time.sleep(.1)
    print("completed after supervision fault",flush=True)
    (root/"receipt").write_text("complete")
def fault(root):
    raise PermissionError(13,"inspection failed")
guardian.descendants=fault
code=guardian.supervise(action,"apply",capture)
assert code==70
assert (root/"receipt").read_text()=="complete"
'''
            result = subprocess.run([sys.executable, "-c", program, directory],
                                    env={**os.environ, "PYTHONPATH": str(RUNTIME)}, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            records = [json.loads(line) for line in (Path(directory) / "operation-log.jsonl").read_text().splitlines()]
            text = "".join(r["output"]["text"] for r in records)
            self.assertIn("PermissionError", text)
            self.assertIn("completed after supervision fault", text)

    def test_noisy_native_output_is_drained_after_the_retention_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            program = r'''
import os,sys
from pathlib import Path
from actions.guardian import supervise
from actions.logging import ActionLogger
from actions.output import OutputCapture
root=Path(sys.argv[1]);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
capture=OutputCapture(ActionLogger(fd,"apply",{}))
def action():
    os.write(1,b"x"*(4*1024*1024))
    (root/"receipt").write_text("complete")
assert supervise(action,"apply",capture)==0
assert (root/"receipt").read_text()=="complete"
'''
            result = subprocess.run([sys.executable, "-c", program, directory],
                                    env={**os.environ, "PYTHONPATH": str(RUNTIME)}, capture_output=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            path = Path(directory) / 'operation-log.jsonl'
            records = [json.loads(line) for line in path.read_text().splitlines()]
            self.assertLessEqual(path.stat().st_size, 1024*1024)
            self.assertEqual(len(records), 1024)
            self.assertEqual(records[-1]['code'], 'log_truncated')
