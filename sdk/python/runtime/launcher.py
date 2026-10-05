"""Trusted guest launcher; this file alone imports package code inside isolation."""
import importlib.util
import os
import sys

sys.path.insert(0, "/runtime")
from json_values import decode, encode


class Host:
    def __init__(self):
        self.reader = os.fdopen(int(os.environ["TW_BROKER_READ"]), "rb", buffering=0)
        self.writer = os.fdopen(int(os.environ["TW_BROKER_WRITE"]), "wb", buffering=0)
        self.sequence = 0

    def request(self, operation, arguments=None):
        self.sequence += 1
        payload = encode({"sequence": self.sequence, "operation": operation, "arguments": arguments or {}}, 65536)
        self.writer.write(payload + b"\n")
        data = self.reader.readline(1024 * 1024 + 1)
        response = decode(data)
        if response.get("sequence") != self.sequence or "error" in response:
            raise RuntimeError("observation_unavailable")
        return response["value"]


def run():
    request = decode(sys.stdin.buffer.read(1024 * 1024 + 1))
    function = request["function"]
    if function not in {"validate_settings", "collect", "reduce", "evaluate"}:
        raise ValueError("entrypoint")
    spec = importlib.util.spec_from_file_location("skill", "/skill/skill.py")
    module = importlib.util.module_from_spec(spec)
    sys.path.insert(0, "/skill")
    # stdout from imported code never becomes the trusted result channel.
    output = os.dup(sys.stdout.fileno())
    os.dup2(sys.stderr.fileno(), sys.stdout.fileno())
    spec.loader.exec_module(module)
    args = request["arguments"]
    result = module.collect(args, Host()) if function == "collect" else getattr(module, function)(args)
    data = encode({"result": result})
    with os.fdopen(output, "wb") as target:
        target.write(data)


if __name__ == "__main__":
    try:
        run()
    except BaseException:
        # Never report exception strings, raw output or package-controlled traceback text.
        sys.exit(70)
