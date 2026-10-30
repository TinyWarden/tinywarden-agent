"""Retain the root guard until every action descendant has been reaped."""
import ctypes
import os
from pathlib import Path
import select
import signal
import threading
import time


def descendants(root):
    found, pending = {}, [root]
    while pending and len(found) < 4096:
        parent = pending.pop()
        try:
            children = Path(f"/proc/{parent}/task/{parent}/children").read_text().split()
        except (FileNotFoundError, ProcessLookupError):
            continue
        for value in children:
            pid = int(value)
            if pid in found:
                continue
            try:
                born = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
            except (FileNotFoundError, ProcessLookupError):
                continue
            found[pid] = born
            pending.append(pid)
    return found


def send(snapshot, sig):
    for pid, born in snapshot.items():
        try:
            fd = os.pidfd_open(pid)
            try:
                actual = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
                if actual == born:
                    signal.pidfd_send_signal(fd, sig)
            finally:
                os.close(fd)
        except ProcessLookupError:
            pass
        except FileNotFoundError:
            pass


def child_exit(callback):
    code = 0
    try:
        callback()
    except BaseException:
        code = 70
    # Even daemon threads may hold native work. Safe-phase cancellation can end
    # the process after its cleanup grace; applying work has no forced deadline.
    try:
        while True:
            threads = [t for t in threading.enumerate() if t is not threading.current_thread()]
            if not threads:
                break
            for thread in threads:
                thread.join(.05)
    except BaseException:
        code = 70
    raise SystemExit(code)


def supervise(callback, phase, capture=None):
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0) != 0:
        raise ValueError("action_supervision_unavailable")
    # Require stable process handles before any authored code is entered.
    handle = os.pidfd_open(os.getpid())
    os.close(handle)
    inspection = phase == "inspect"
    ready_read, ready_write = os.pipe() if inspection else (None, None)
    ack_read, ack_write = os.pipe() if inspection else (None, None)
    signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGTERM})
    worker = os.fork()
    if worker == 0:
        if capture is not None:
            capture.child()
        if inspection:
            os.dup2(ready_write, 4)
            os.dup2(ack_read, 0)
            for fd in (ready_read, ready_write, ack_read, ack_write):
                if fd not in {0, 4}:
                    os.close(fd)
        child_exit(callback)
    if inspection:
        os.close(ready_write)
        os.close(ack_read)
    cancelling, forwarded, acknowledged, notified = None, False, False, False
    worker_code, failed, sent = None, False, False
    capture_failed = False

    def capture_error(error):
        if capture is not None:
            try:
                capture.error(error)
            except Exception:
                pass

    def drain_output():
        nonlocal capture_failed
        if capture is None:
            return
        if not capture_failed:
            try:
                capture.drain()
                return
            except Exception as error:
                capture_failed = True
                capture_error(error)
        try:
            capture.discard()
        except Exception:
            pass

    def close_output():
        if capture is not None:
            try:
                capture.close()
            except Exception as error:
                capture_error(error)
            finally:
                capture.release()

    def cancel(signum, frame):
        nonlocal cancelling
        if phase != "apply" and cancelling is None:
            cancelling = time.monotonic()

    signal.signal(signal.SIGTERM, cancel)
    signal.pthread_sigmask(signal.SIG_UNBLOCK, {signal.SIGTERM})
    try:
        if capture is not None:
            try:
                capture.parent()
            except Exception as error:
                capture_failed = True
                capture_error(error)
        while True:
            drain_output()
            try:
                while True:
                    pid, status = os.waitpid(-1, os.WNOHANG)
                    if not pid:
                        break
                    if pid == worker:
                        worker_code = os.waitstatus_to_exitcode(status)
            except ChildProcessError:
                pass
            children = descendants(os.getpid())
            if inspection and not notified and select.select([ready_read], [], [], 0)[0]:
                value = os.read(ready_read, 1)
                if value == b"\n":
                    notified = True
                elif worker_code is None:
                    failed, cancelling = True, time.monotonic()
            if inspection and notified and not forwarded and cancelling is None:
                if any(pid != worker for pid in children) or worker_code is not None:
                    failed, cancelling = True, time.monotonic()
                else:
                    os.write(4, b"\n")
                    forwarded = True
            if inspection and forwarded and not acknowledged and select.select([0], [], [], 0)[0]:
                value = os.read(0, 1)
                if value == b"\n":
                    os.write(ack_write, value)
                    acknowledged = True
                else:
                    failed, cancelling = True, time.monotonic()
            if worker_code is not None and worker_code != 0 and phase != "apply" and cancelling is None:
                cancelling = time.monotonic()
            if cancelling is not None:
                failed = True
                if not sent:
                    send(children, signal.SIGTERM)
                    sent = True
                if time.monotonic() - cancelling >= 3:
                    send(children, signal.SIGKILL)  # Safe phases only; never Apply.
            if worker_code is not None and not children:
                close_output()
                return 70 if failed else worker_code
            time.sleep(.01)
    except Exception as error:
        # If process inspection itself fails, retain the guard and drain output
        # until the kernel confirms that every child/adopted descendant is gone.
        # Never kill an in-flight Apply just because diagnostics/supervision failed.
        capture_error(error)
        while True:
            drain_output()
            try:
                os.waitpid(-1, os.WNOHANG)
            except ChildProcessError:
                close_output()
                return 70
            time.sleep(.01)
    finally:
        if inspection:
            for fd in (ready_read, ack_write):
                os.close(fd)
