"""Immutable test bytes; official source lives in the separate skills repository.

Fixture origin: TinyWarden/tinywarden at 9eb1ab9438ea79634b7e58a801043f05a3ef6c49,
tests/skills/packages/fixtures/official-packages.json. These are pinned test inputs,
not maintained or distributed official package source.
"""
import json
import tempfile
from pathlib import Path


def official_root(test):
    workspace = tempfile.TemporaryDirectory(prefix='tw-sdk-official-')
    test.addCleanup(workspace.cleanup)
    root = Path(workspace.name)
    pinned = json.loads((Path(__file__).parent/'fixtures/official-packages.json').read_text())
    for key, package in pinned['packages'].items():
        for name, content in package['files'].items():
            path = root/key/name
            path.parent.mkdir(parents=True,exist_ok=True)
            path.write_text(content)
    return root
