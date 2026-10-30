"""Capture process streams as bounded diagnostic text, without running commands."""
import codecs
import os
import re
import selectors
import socket
import sys
import time
import traceback
import unicodedata

_control = None


def sync_output():
    """Best-effort flush before command/exit markers; never stall native work."""
    if _control is None:
        return True
    try:
        sys.stdout.flush()
        sys.stderr.flush()
        token = os.urandom(16)
        _control.send(token)
        deadline = time.monotonic() + .1
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            _control.settimeout(remaining)
            if _control.recv(16) == token:
                return True
    except Exception:
        return False


def clean_output(text):
    # Strip terminal controls; the viewer is plain text, not a terminal emulator.
    text = re.sub(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)", "", text)
    text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
    text = "".join(c if c in "\n\r\t" or unicodedata.category(c) not in {"Cc", "Cf", "Cs"}
                   else "" for c in text)
    text = re.sub(r"(?i)(https?://)[^\s/@]+(?::[^\s/@]*)?@", r"\1[redacted]@", text)
    text = re.sub(r"(?i)(authorization\s*:\s*(?:bearer|basic)\s+)\S+", r"\1[redacted]", text)
    text = re.sub(r"(?i)((?:password|passwd|token|secret|api[_-]?key)\s*(?:[:=]|\s)\s*)(?:\"[^\"]*\"|'[^']*'|[^\s,;]+)",
                  r"\1[redacted]", text)
    return re.sub(r"-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----",
                  "[redacted private key]", text)


class OutputCapture:
    """Guardian drains pipes after forking: no background thread in authored code."""
    def __init__(self, logger):
        self.logger = logger
        self.pipes, self.selector = {}, None
        self.reads, self.writes = {}, set()
        self.control_parent = self.control_child = None
        try:
            for stream in ("stdout", "stderr"):
                self.pipes[stream] = os.pipe2(os.O_CLOEXEC)
                read, write = self.pipes[stream]
                self.reads[read] = stream
                self.writes.add(write)
                os.set_blocking(read, False)
            self.selector = selectors.DefaultSelector()
            self.pending = {stream: "" for stream in self.pipes}
            self.decoders = {stream: codecs.getincrementaldecoder("utf-8")("replace") for stream in self.pipes}
            self.private_key = {stream: False for stream in self.pipes}
            self.control_parent, self.control_child = socket.socketpair(socket.AF_UNIX, socket.SOCK_DGRAM)
            self.control_parent.setblocking(False)
            self.control_child.settimeout(.1)
            # Allocate/configure every optional watch before authored work starts.
            self.selector.register(self.control_parent, selectors.EVENT_READ, "control")
            for read, stream in self.reads.items():
                self.selector.register(read, selectors.EVENT_READ, stream)
        except OSError:
            self.release()
            raise

    def child(self):
        global _control
        for stream, target in (("stdout", 1), ("stderr", 2)):
            os.dup2(self.pipes[stream][1], target)
        for read, write in self.pipes.values():
            os.close(read)
            os.close(write)
        self.selector.close()
        self.control_parent.close()
        _control = self.control_child

    def parent(self):
        self.control_child.close()
        while self.writes:
            write = self.writes.pop()
            os.close(write)

    def close_read(self, fd):
        self.reads.pop(fd, None)
        try:
            self.selector.unregister(fd)
        except (OSError, ValueError, KeyError):
            pass
        os.close(fd)

    def discard(self):
        """Independent bounded drain when the optional selector/decoder fails."""
        for fd in list(self.reads):
            try:
                if not os.read(fd, 65536):
                    self.close_read(fd)
            except OSError:
                # Nonblocking pipes may be empty; supervision must keep running.
                pass

    def release(self):
        """Close owned descriptors only before fork or after all children exit."""
        for fd in [*self.reads, *self.writes]:
            try:
                os.close(fd)
            except OSError:
                pass
        self.reads.clear()
        self.writes.clear()
        for resource in (self.control_parent, self.control_child, self.selector):
            if resource is not None:
                try:
                    resource.close()
                except OSError:
                    pass

    def line(self, stream, text):
        if self.private_key[stream]:
            if re.search(r"-----END [A-Z ]*PRIVATE KEY-----", text):
                self.private_key[stream] = False
            return
        if re.search(r"-----BEGIN [A-Z ]*PRIVATE KEY-----", text):
            self.private_key[stream] = not bool(re.search(r"-----END [A-Z ]*PRIVATE KEY-----", text))
            text = "[redacted private key]\n"
        try:
            self.logger.output(text, stream)
        except Exception:
            # A diagnostic sink must never interrupt or back up a native action.
            pass

    def error(self, error):
        try:
            self.logger.output("".join(traceback.TracebackException.from_exception(error, capture_locals=False).format()), "runtime")
        except Exception:
            pass

    def feed(self, stream, text, final=False):
        self.pending[stream] += text
        while self.pending[stream]:
            end = self.pending[stream].find("\n")
            if end < 0 and len(self.pending[stream]) < 8192 and not final:
                break
            count = end + 1 if 0 <= end < 8192 else min(8192, len(self.pending[stream]))
            self.line(stream, self.pending[stream][:count])
            self.pending[stream] = self.pending[stream][count:]

    def drain(self):
        # Bound each turn so a noisy child cannot starve supervision/cancellation.
        acknowledgements = []
        for key, _ in self.selector.select(0):
            if key.data == "control":
                try:
                    token = self.control_parent.recv(16)
                    if len(token) == 16:
                        acknowledgements.append(token)
                except BlockingIOError:
                    pass
                continue
            try:
                chunk = os.read(key.fd, 65536)
            except BlockingIOError:
                continue
            self.feed(key.data, self.decoders[key.data].decode(chunk, final=not chunk), final=not chunk)
            if not chunk:
                self.close_read(key.fd)
        if acknowledgements:
            # Drain prior native writes before recording a later exit marker.
            for key in list(self.selector.get_map().values()):
                if key.data == "control":
                    continue
                for _ in range(16):
                    try:
                        chunk = os.read(key.fd, 65536)
                    except BlockingIOError:
                        break
                    self.feed(key.data, self.decoders[key.data].decode(chunk, final=not chunk), final=not chunk)
                    if not chunk:
                        self.close_read(key.fd)
                        break
                self.feed(key.data, "", final=True)
            for token in acknowledgements:
                try:
                    self.control_parent.send(token)
                except OSError:
                    pass

    def close(self):
        # Called only after the supervisor has reaped all descendants.
        try:
            self.parent()  # Also closes writes left by an interrupted parent setup.
            while self.selector.get_map() and self.selector.select(0):
                self.drain()
            for stream in self.reads.values():
                self.feed(stream, self.decoders[stream].decode(b"", final=True), final=True)
        finally:
            self.release()
