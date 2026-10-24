"""Native input ancestor/alias proof; no APT imports, scripts or package mutations."""
import ast
from collections import deque
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import sys
import tempfile
import unittest

ROOT = Path(sys.argv.pop()).resolve()
scope = dict(deque=deque, hashlib=hashlib, json=json, os=os, Path=Path, stat=stat,
             MaintenanceError=RuntimeError)
tree = ast.parse((ROOT / '00_support.py').read_text())
exec(compile(ast.Module(body=[n for n in tree.body if isinstance(n, ast.FunctionDef)
     and n.name in ('open_input','file_digest','tree_digest','digest','canonical')], type_ignores=[]), '<trusted-input>', 'exec'), scope)

@unittest.skipUnless(os.geteuid() == 0, 'explicit native root proof required')
class InputTests(unittest.TestCase):
    def setUp(self):
        self.base=Path(tempfile.mkdtemp(prefix='tw-ma1-input-',dir='/var/lib'))
        self.base.chmod(0o755)
        self.parent=self.base/'safe';self.parent.mkdir()
        self.leaf=self.parent/'input';self.leaf.write_text('trusted');self.leaf.chmod(0o644)
    def tearDown(self):
        shutil.rmtree(self.base)
    def test_root_alias_and_relative_target_remain_supported(self):
        alias=self.base/'alias';alias.symlink_to('safe')
        self.assertEqual(scope['file_digest'](alias/'input'), hashlib.sha256(b'trusted').hexdigest())
        absolute=self.base/'absolute';absolute.symlink_to(self.leaf)
        self.assertEqual(scope['file_digest'](absolute),scope['file_digest'](self.leaf))
    def test_root_leaf_inside_writable_parent_is_refused(self):
        self.parent.chmod(0o777)
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['file_digest'](self.leaf)
    def test_alias_target_traversing_writable_directory_is_refused(self):
        unsafe=self.base/'unsafe';unsafe.mkdir();unsafe.chmod(0o777)
        (unsafe/'hop').symlink_to(self.parent)
        alias=self.base/'alias';alias.symlink_to('unsafe/hop/input')
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['file_digest'](alias)
    def test_untrusted_link_and_link_loop_are_refused(self):
        alias=self.base/'alias';alias.symlink_to(self.leaf);os.chown(alias,65534,65534,follow_symlinks=False)
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['file_digest'](alias)
        alias.unlink();alias.symlink_to('alias')
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['file_digest'](alias)
    def test_empty_or_absent_input_has_no_mutable_parent(self):
        empty=self.parent/'empty';empty.mkdir();empty.chmod(0o777)
        for path in (empty,empty/'missing'):
            with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
                scope['tree_digest']([path])
    def test_only_empty_declared_auxiliary_scratch_is_ignored(self):
        scratch=self.parent/'auxfiles';scratch.mkdir();os.chown(scratch,65534,65534)
        scope['tree_digest']([self.parent],[scratch])
        (scratch/'input').write_text('untrusted')
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['tree_digest']([self.parent],[scratch])
    def test_special_leaf_cannot_block_the_reader(self):
        fifo=self.parent/'fifo';os.mkfifo(fifo)
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['file_digest'](fifo)

if __name__=='__main__':unittest.main()
