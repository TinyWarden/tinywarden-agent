"""Reader watchdog proof with a suspended-clock jump; no APT/package mutations."""
import ast
import contextlib
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
import time
from types import SimpleNamespace
import unittest

ROOT = Path(sys.argv.pop()).resolve()
scope = dict(contextlib=contextlib, os=os, signal=signal, threading=threading,
             MaintenanceError=RuntimeError)
for name, functions in [('00_support.py', {'readonly_budget'}), ('10_plan.py', {'selections'})]:
    tree = ast.parse((ROOT / name).read_text())
    code = ast.Module(body=[n for n in tree.body if isinstance(n, ast.FunctionDef)
                           and n.name in functions], type_ignores=[])
    exec(compile(code, '<reader-policy>', 'exec'), scope)


class ReaderTests(unittest.TestCase):
    def test_suspend_jump_terminates_only_fixed_reader(self):
        calls = iter([0, 31, 31])
        scope['budget_clock'] = lambda: next(calls, 31)
        child = subprocess.Popen(['/usr/bin/sleep', '60'], start_new_session=True)
        began = time.monotonic()
        with self.assertRaisesRegex(RuntimeError, 'maintenance_reader_timeout'):
            with scope['readonly_budget'](child):
                child.wait()
        self.assertLess(time.monotonic() - began, 2)
        self.assertEqual(child.returncode, -signal.SIGKILL)

    def test_elapsed_reader_result_is_not_accepted_after_suspend(self):
        calls = iter([0, 31])
        scope['budget_clock'] = lambda: next(calls, 31)
        child = subprocess.Popen(['/usr/bin/true'], start_new_session=True)
        with self.assertRaisesRegex(RuntimeError, 'maintenance_reader_timeout'):
            with scope['readonly_budget'](child):
                child.wait()

    def test_multiarch_identity_has_one_explicit_architecture(self):
        scope['digest'] = lambda value: value
        package = SimpleNamespace(name='example:i386', shortname='example',
                                  installed=SimpleNamespace(architecture='i386'),
                                  _pkg=SimpleNamespace(selected_state=1), is_auto_installed=False)
        self.assertEqual(scope['selections']([package]), [('example', 'i386', 1, False)])


if __name__ == '__main__':
    unittest.main()
