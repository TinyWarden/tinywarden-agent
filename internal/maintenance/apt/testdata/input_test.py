"""Native input/mirror proof; optional APT download, no scripts or installation."""
import ast
import contextlib
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
from types import SimpleNamespace
from urllib.parse import urlsplit, unquote

ROOT = Path(sys.argv.pop()).resolve()
scope = dict(deque=deque, hashlib=hashlib, json=json, os=os, Path=Path, stat=stat,
             MaintenanceError=RuntimeError)
tree = ast.parse((ROOT / '00_support.py').read_text())
exec(compile(ast.Module(body=[n for n in tree.body if isinstance(n, ast.FunctionDef)
     and n.name in ('open_input','file_digest','tree_digest','digest','canonical')], type_ignores=[]), '<trusted-input>', 'exec'), scope)
scope.update(POLICY={'archive_methods': ('http', 'https')}, urlsplit=urlsplit, unquote=unquote, contextlib=contextlib)
tree = ast.parse((ROOT / '15_archives.py').read_text())
exec(compile(ast.Module(body=[n for n in tree.body if isinstance(n, ast.FunctionDef)
     and n.name in ('protected_mirror_cache', 'mirror_sources', 'archive_methods', 'archive_items', 'mirror_aliases')], type_ignores=[]), '<mirrors>', 'exec'), scope)

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

    def mirrors(self, text):
        self.leaf.write_text(text)
        cache=SimpleNamespace(_list=SimpleNamespace(list=[SimpleNamespace(uri='mirror+file:'+str(self.leaf)+'/')]))
        return scope['mirror_sources'](cache)

    def test_protected_http_mirror_lists_and_exact_source_match(self):
        mirrors=self.mirrors('# Mirrors\nhttps://example.invalid/debian\tpriority:1 arch:amd64\nhttp://fallback.invalid/debian\n')
        scope['archive_methods']([next(iter(mirrors))+'/pool/p.deb'],mirrors)
        for uri in ['mirror+file:'+str(self.leaf)+'-other/p.deb', 'file:'+str(self.leaf), 'mirror+https://example.invalid/list/p.deb']:
            with self.assertRaisesRegex(RuntimeError,'maintenance_archive_method'):
                scope['archive_methods']([uri],mirrors)

    def test_unsafe_mirror_alternatives_and_untrusted_parent_are_closed(self):
        for text in ['', '# no mirrors\n', 'file:/local\n', 'cdrom:disc\n',
                     'https://example.invalid\nfile:/fallback\n', 'mirror+https://example.invalid/list\n',
                     'https://example.invalid invalid\n', 'https:///missing-host\n', 'x'* (1024*1024+1)]:
            with self.subTest(text=text[:40]), self.assertRaisesRegex(RuntimeError,'maintenance_archive_method'):
                self.mirrors(text)
        self.parent.chmod(0o777)
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            self.mirrors('https://example.invalid\n')

    def test_mirror_original_is_pinned_and_auxiliary_is_only_an_alias(self):
        self.mirrors('https://example.invalid\n')
        scratch=self.parent/'auxfiles';scratch.mkdir();os.chown(scratch,65534,65534)
        alias=scratch/'mirror';alias.symlink_to(self.leaf)
        aliases={alias:self.leaf}
        before=scope['tree_digest']([self.parent],[scratch],aliases)
        self.leaf.write_text('https://changed.invalid\n')
        self.assertNotEqual(scope['tree_digest']([self.parent],[scratch],aliases),before)
        alias.unlink();alias.write_text('https://poison.invalid\n')
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['tree_digest']([self.parent],[scratch],aliases)
        alias.unlink();alias.symlink_to(self.leaf);os.chown(alias,65534,65534,follow_symlinks=False)
        with self.assertRaisesRegex(RuntimeError,'maintenance_input_invalid'):
            scope['tree_digest']([self.parent],[scratch],aliases)

    def test_auxiliary_replacement_is_denied_and_ownership_restores_on_failure(self):
        import pwd,subprocess
        uid=pwd.getpwnam('_apt').pw_uid
        scratch=self.parent/'auxfiles';scratch.mkdir();os.chown(scratch,uid,0)
        alias=scratch/'mirror';alias.symlink_to(self.leaf)
        scope['apt_pkg']=SimpleNamespace(config=SimpleNamespace(find_dir=lambda _:str(self.parent)))
        with self.assertRaisesRegex(RuntimeError,'fixture failure'):
            with scope['protected_mirror_cache']():
                self.assertEqual(scratch.stat().st_uid,0)
                result=subprocess.run([sys.executable,'-I','-c','import os,sys; os.unlink(sys.argv[1])',str(alias)],
                                      user=uid,group=65534,capture_output=True)
                self.assertNotEqual(result.returncode,0)
                self.assertTrue(alias.is_symlink())
                raise RuntimeError('fixture failure')
        self.assertEqual(scratch.stat().st_uid,uid)

    def test_restrictive_auxiliary_modes_never_disclose_readable_contents(self):
        import pwd,subprocess
        uid=pwd.getpwnam('_apt').pw_uid
        scratch=self.parent/'auxfiles';scratch.mkdir()
        sentinel=scratch/'private';sentinel.write_text('benign fixture');sentinel.chmod(0o644)
        scope['apt_pkg']=SimpleNamespace(config=SimpleNamespace(find_dir=lambda _:str(self.parent)))
        def denied(observer_gid):
            result=subprocess.run([sys.executable,'-I','-c','import sys; open(sys.argv[1]).read()',str(sentinel)],
                                  user=65533,group=observer_gid,extra_groups=(),capture_output=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn(b'PermissionError',result.stderr)
        cases=((gid,mode,failed) for gid in (0,65534) for mode in (0o700,0o750) for failed in (False,True))
        for gid,mode,failed in cases:
            with self.subTest(gid=gid,mode=oct(mode),failure=failed):
                os.chown(scratch,uid,gid)
                scratch.chmod(mode)
                observer_gid=65534 if gid==0 else 0
                denied(observer_gid)
                with (self.assertRaisesRegex(RuntimeError,'fixture failure') if failed else contextlib.nullcontext()):
                    with scope['protected_mirror_cache']():
                        self.assertEqual((scratch.stat().st_uid,scratch.stat().st_gid),(0,gid))
                        self.assertEqual(stat.S_IMODE(scratch.stat().st_mode),mode)
                        denied(observer_gid)
                        if failed:
                            raise RuntimeError('fixture failure')
                self.assertEqual((scratch.stat().st_uid,scratch.stat().st_gid),(uid,gid))
                self.assertEqual(stat.S_IMODE(scratch.stat().st_mode),mode)
                denied(observer_gid)

@unittest.skipUnless(os.geteuid()==0 and os.environ.get('TW_MA1_MIRROR_DOWNLOAD')=='1',
                     'explicit disposable-host mirror download proof required')
class NativeMirrorTests(unittest.TestCase):
    def test_real_mirror_plan_download_and_same_native_inputs_without_install(self):
        native={'__name__':'maintenance_native_test'}
        for path in sorted(ROOT.glob('*.py')):
            exec(compile(path.read_text(),str(path),'exec'),native)
        native['configure']()
        apt_pkg=native['apt_pkg']; before=native['file_digest']('/var/lib/dpkg/status')
        # Private download cache; the host's source configuration and packages stay intact.
        with tempfile.TemporaryDirectory(prefix='tw-ma1-mirror-',dir='/var/lib') as raw:
            base=Path(raw);base.chmod(0o755);archives=base/'archives';archives.mkdir()
            partial=archives/'partial';partial.mkdir()
            import pwd
            os.chown(partial,pwd.getpwnam('_apt').pw_uid,0)
            apt_pkg.config.set('Dir::Cache::archives',str(archives))
            with native['package_lock'](),native['protected_mirror_cache']():
                cache,plan=native['resolve']()
                self.assertTrue(plan['packages'],'fixture needs an actual pending userspace update')
                mirrors=native['mirror_sources'](cache);self.assertTrue(mirrors)
                _,fetcher,progress=native['acquisition'](cache,plan)
                self.assertEqual(fetcher.run(),apt_pkg.Acquire.RESULT_CONTINUE)
                self.assertFalse(progress.failed)
                self.assertEqual((Path(apt_pkg.config.find_dir('Dir::State::lists'))/'auxfiles').stat().st_uid,0)
                native['verify_inputs'](fetcher,plan,native['installed_metadata'](cache),mirrors)
                self.assertGreater(len(fetcher.items),len(plan['packages']),'native mirror auxiliary input was not exercised')
                _,ready,_=native['acquisition'](cache,plan)
                self.assertFalse(ready.fetch_needed,'native install inputs must already be complete')
                native['verify_inputs'](ready,plan,native['installed_metadata'](cache),mirrors)
                self.assertEqual(native['digest'](native['resolve']()[1]),native['digest'](plan),'download must not invalidate the reviewed plan')
        self.assertEqual(native['file_digest']('/var/lib/dpkg/status'),before,'download changed installed packages')

if __name__=='__main__':unittest.main()
