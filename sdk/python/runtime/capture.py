"""Nonblocking IO, aggregate budget checks and bounded broker responses."""
import os
import selectors
import subprocess
import time
from json_values import decode, encode


def capture(argv, payload, group, timeout, pass_fds=(), broker=None, channels=None,
            output_limit=1024 * 1024, error_limit=16384):
    process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               env={}, cwd="/", close_fds=True, pass_fds=pass_fds,
                               start_new_session=True, preexec_fn=group.enter)
    selector = selectors.DefaultSelector()
    output, errors = bytearray(), bytearray()
    pending_input = bytearray(payload)
    pending_reply = bytearray()
    requests = bytearray()
    try:
        for file, label in ((process.stdout, "stdout"), (process.stderr, "stderr")):
            os.set_blocking(file.fileno(), False)
            selector.register(file, selectors.EVENT_READ, label)
        os.set_blocking(process.stdin.fileno(), False)
        if pending_input:
            selector.register(process.stdin, selectors.EVENT_WRITE, "stdin")
        else:
            process.stdin.close()
        if channels:
            request_fd, reply_fd = channels
            os.set_blocking(request_fd, False)
            os.set_blocking(reply_fd, False)
            selector.register(request_fd, selectors.EVENT_READ, "broker_in")
        deadline = time.monotonic() + timeout
        while selector.get_map():
            if time.monotonic() >= deadline:
                raise RuntimeError("deadline_exceeded")
            if group.exhausted():
                raise RuntimeError("resource_exhausted")
            for key, _ in selector.select(.025):
                label = key.data
                fd = key.fd
                if label in {"stdin", "broker_out"}:
                    pending = pending_input if label == "stdin" else pending_reply
                    try:
                        count = os.write(fd, pending[:65536])
                        del pending[:count]
                    except BrokenPipeError:
                        pending.clear()
                    if not pending:
                        selector.unregister(key.fileobj)
                        if label == "stdin":
                            process.stdin.close()
                    continue
                data = os.read(fd, 65536)
                if not data:
                    selector.unregister(key.fileobj)
                    continue
                if label in {"stdout", "stderr"}:
                    target = output if label == "stdout" else errors
                    target.extend(data)
                    if len(target) > (output_limit if label == "stdout" else error_limit):
                        raise RuntimeError("output_exceeded")
                else:
                    requests.extend(data)
                    if len(requests) > 65536:
                        raise RuntimeError("broker_limit")
                    while b"\n" in requests:
                        line, _, remainder = requests.partition(b"\n")
                        requests[:] = remainder
                        value = broker(decode(line, 65536), max(0, deadline - time.monotonic()))
                        pending_reply.extend(encode(value) + b"\n")
                        if len(pending_reply) > 1024 * 1024:
                            raise RuntimeError("broker_limit")
                        if reply_fd not in selector.get_map():
                            selector.register(reply_fd, selectors.EVENT_WRITE, "broker_out")
            if process.poll() is not None:
                # Wait only for stdout/stderr drain. A guest must not leave a broker
                # descriptor or descendant holding the slot open after its exit.
                for key in list(selector.get_map().values()):
                    if key.data not in {"stdout", "stderr"}:
                        selector.unregister(key.fileobj)
        code = process.wait(timeout=max(.01, deadline - time.monotonic()))
        if group.exhausted():
            raise RuntimeError("resource_exhausted")
        return code, bytes(output), bytes(errors)
    finally:
        selector.close()
        # The owning invocation kills the entire cgroup, including nested commands.
        if process.poll() is None:
            process.kill()
        process.wait(timeout=2)
        for file in (process.stdin, process.stdout, process.stderr):
            file.close()
