"""Bounded metadata parsing worker. Never import or execute package code."""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from json_values import decode, encode
from packages import load

if __name__ == "__main__":
    try:
        request = decode(sys.stdin.buffer.read(65537), 65536)
        result = load(request["package"], request.get("content_sha256"), request.get("official") is True)
    except (ValueError, TypeError, KeyError, OSError, RecursionError, SyntaxError):
        result = {"error": "package_rejected"}
    sys.stdout.buffer.write(encode(result))
