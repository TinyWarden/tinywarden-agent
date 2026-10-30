"""Generic root guardian; action code runs outside the Go host-agent process."""
import os
from pathlib import Path
import stat
import sys
import signal
import ctypes
import traceback

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from actions.envelope import invocation
from actions.guest import dispatch
from actions.trust import admit
from contracts.json_values import decode
import skill_actions  # Public failure type is available after dropping cache access.
from actions.guardian import supervise
from actions.state import open_state
from actions.logging import ActionLogger, failure_code
from actions.output import OutputCapture


def main():
    if len(sys.argv) != 4:
        raise ValueError("action_invocation_invalid")
    if os.geteuid() != 0:
        raise ValueError("action_root_supervisor_required")
    guard = os.fstat(3)
    if not stat.S_ISREG(guard.st_mode) or guard.st_uid != 0 or guard.st_mode & 0o022 or guard.st_nlink != 1:
        raise ValueError("action_guard_required")
    # A regular root-owned result descriptor persists an interrupted invocation.
    output = os.dup(1)
    info = os.fstat(output)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1:
        raise ValueError("action_receipt_required")
    os.dup2(2, 1)
    # One framed request leaves stdin open for the lock-held inspect callback.
    raw = sys.stdin.buffer.readline(17 * 1024 * 1024 + 1)
    if not raw.endswith(b"\n"):
        raise ValueError("action_invocation_invalid")
    request = decode(raw, 17 * 1024 * 1024)
    invocation(request)
    action, metadata, files, arguments = admit(sys.argv[1], sys.argv[2], request)
    state=open_state(sys.argv[3],request["job_id"],action["descriptor"]["execution_class"])
    logger = ActionLogger(state, request["phase"], metadata["catalog"])
    try:
        capture = OutputCapture(logger)
    except OSError:
        capture = None  # Diagnostic resource exhaustion must not prevent execution.
    if request["phase"]=="inspect" and not stat.S_ISFIFO(os.fstat(4).st_mode):
        raise ValueError("action_inspection_channel_required")
    def child():
        if action["descriptor"]["execution_class"] == "unprivileged":
            os.close(3)
            if ctypes.CDLL(None, use_errno=True).prctl(38, 1, 0, 0, 0) != 0:
                raise ValueError("action_privilege_drop_failed")
            os.setgroups([])
            os.setgid(65534)
            os.setuid(65534)
        try:
            dispatch(action, metadata, files, arguments, request, output,state,sys.argv[3])
        except BaseException as error:
            failure = ActionLogger(state, request["phase"], metadata["catalog"])
            failure.output("".join(traceback.TracebackException.from_exception(error, capture_locals=False).format()), "runtime")
            failure.diagnostic(failure_code(error), "error")
            raise
    try:
        code=supervise(child,request["phase"],capture)
    except Exception as error:
        logger.output("".join(traceback.TracebackException.from_exception(error, capture_locals=False).format()), "runtime")
        raise
    os.close(output)
    os.close(state)
    raise SystemExit(code)


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except BaseException:
        # A missing/partial receipt stays uncertain. Never report raw host output.
        raise SystemExit(70)
