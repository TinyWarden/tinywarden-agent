"""Optional, bounded job diagnostics. These records never grant execution authority."""
import fcntl
import json
import os
import stat

from contracts.json_values import encode
from contracts.outcomes import message

NAME = "operation-log.jsonl"
MAX_ENTRIES = 128
MAX_ENTRY = 1024
MAX_BYTES = MAX_ENTRIES * MAX_ENTRY
DIAGNOSTICS = {
    "phase_started", "phase_completed", "log_truncated", "unexpected_exception",
    "action_schema_value", "action_result_invalid", "action_result_size",
    "action_start_invalid", "action_start_expired", "action_cancelled",
    "action_package_file", "action_inspection_invalid", "message", "parameter",
    "message_size",
}


class ActionLogger:
    def __init__(self, state_fd, phase, catalog):
        self.state_fd, self.phase, self.catalog = state_fd, phase, catalog

    def log(self, value, level="info"):
        try:
            message(value, self.catalog)
            return self._append({"message": value}, level)
        except (OSError, ValueError, TypeError, KeyError, OverflowError):
            return False

    def diagnostic(self, code, level="info"):
        if code not in DIAGNOSTICS:
            code = "unexpected_exception"
        return self._append({"code": code}, level)

    def _append(self, value, level):
        fd = None
        try:
            if level not in {"info", "warning", "error"}:
                return False
            fd = os.open(NAME, os.O_RDWR | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW |
                         os.O_NONBLOCK | os.O_CLOEXEC, 0o600, dir_fd=self.state_fd)
            info, owner = os.fstat(fd), os.fstat(self.state_fd).st_uid
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != owner or
                    info.st_nlink != 1 or info.st_mode & 0o777 != 0o600 or
                    info.st_size > MAX_BYTES):
                return False
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            raw = os.read(fd, MAX_BYTES + 1)
            lines = raw.splitlines()
            # Preserve only complete records after an interrupted diagnostic write.
            if raw and not raw.endswith(b"\n"):
                lines = lines[:-1]
                os.ftruncate(fd, sum(len(line) + 1 for line in lines))
            if len(lines) >= MAX_ENTRIES:
                return False
            sequence = len(lines) + 1
            if sequence == MAX_ENTRIES:
                value, level = {"code": "log_truncated"}, "warning"
            record = {"sequence": sequence, "phase": self.phase, "level": level, **value}
            data = encode(record, MAX_ENTRY - 1) + b"\n"
            # One bounded write; diagnostics are not transaction receipts.
            return os.write(fd, data) == len(data) and sequence < MAX_ENTRIES
        except (OSError, ValueError, TypeError, KeyError, OverflowError):
            return False
        finally:
            if fd is not None:
                os.close(fd)


def failure_code(error):
    """Only SDK-owned error codes may cross this boundary; no exception text."""
    if (type(error) is ValueError and len(error.args) == 1 and
            isinstance(error.args[0], str) and error.args[0] in DIAGNOSTICS -
            {"phase_started", "phase_completed", "log_truncated"}):
        return error.args[0]
    return "unexpected_exception"
