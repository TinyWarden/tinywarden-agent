"""Pure policy cases. Native APT/dpkg acceptance uses an authorized disposable host."""
import hashlib
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest

ROOT = Path(sys.argv.pop()).resolve()
class Error(Exception):
    def __init__(self, reason, subjects=()):
        super().__init__(reason)
        self.subjects = subjects
scope = {'POLICY': {}, 'MaintenanceError': Error}
exec(compile((ROOT / '05_boot.py').read_text(), '<boot-policy>', 'exec'), scope)
exec(compile((ROOT / '08_start.py').read_text(), '<start-gate>', 'exec'), scope)
exec(compile((ROOT / '09_hooks.py').read_text(), '<hook-policy>', 'exec'), scope)
exec(compile((ROOT / '10_plan.py').read_text(), '<plan-policy>', 'exec'), scope)
import ast
support = ast.parse((ROOT / '00_support.py').read_text())
exec(compile(ast.Module(body=[n for n in support.body if isinstance(n,ast.FunctionDef) and n.name in ('source_security','tree_digest','digest','canonical')], type_ignores=[]), '<source-policy>', 'exec'), scope)
scope.update(contextlib=SimpleNamespace(), stat=__import__('stat'))
exec(compile((ROOT / '15_archives.py').read_text().replace('@contextlib.contextmanager', ''),
             '<archive-policy>', 'exec'), scope)

def record(name, paths=(), triggers=(), section='utils'):
    return {'name': name, 'paths': paths, 'triggers': triggers, 'section': section}

class PolicyTests(unittest.TestCase):
    def setUp(self):
        self.ordinary = ('editor', 'amd64')
        self.handler = ('initramfs-tools', 'all')
        self.installed = {self.ordinary: record('editor'), self.handler:
            record('initramfs-tools', triggers=[('interest', 'update-initramfs')])}

    def blocked(self, changes, installed=None):
        with self.assertRaisesRegex(Error, 'maintenance_boot_review_required'):
            scope['boot_effects'](changes, installed or self.installed)

    def test_userspace_does_not_charge_or_classify_unrelated_boot(self):
        scope['boot_effects']({self.ordinary: self.installed[self.ordinary]}, self.installed)

    def test_candidate_initramfs_hook_blocks_whole_transaction(self):
        self.blocked({self.ordinary: record('editor', ['/usr/share/initramfs-tools/hooks/editor'])})

    def test_direct_boot_and_kernel_tooling(self):
        for name, paths, section in [('editor', ['/boot/vmlinuz'], 'utils'),
                ('grub-common', [], 'utils'), ('linux-foo', [], 'kernel')]:
            with self.subTest(name=name):
                self.blocked({self.ordinary: record(name, paths, section=section)})

    def test_explicit_trigger_reaches_unmodified_installed_handler(self):
        self.blocked({self.ordinary: record('editor', triggers=[('activate', 'update-initramfs')])})

    def test_old_trigger_also_applies_when_candidate_removes_it(self):
        self.installed[self.ordinary]['triggers'] = [('activate', 'update-initramfs')]
        self.blocked({self.ordinary: record('editor')})

    def test_file_trigger_and_declared_chain_reach_boot_handler(self):
        middle = ('middle', 'all')
        self.installed[middle] = record('middle', triggers=[('interest', '/usr/share/editor'),
                                                         ('activate', 'update-initramfs')])
        self.blocked({self.ordinary: record('editor', ['/usr/share/editor/data'])})

    def test_trigger_cycle_terminates(self):
        self.installed[self.ordinary]['triggers'] = [('activate', 'normal'), ('interest', 'normal')]
        scope['boot_effects']({self.ordinary: self.installed[self.ordinary]}, self.installed)

    def test_trigger_parser_rejects_malformed_or_unknown_metadata(self):
        self.assertEqual(scope['triggers']('activate-noawait update-initramfs # generated'),
                         [('activate', 'update-initramfs')])
        for text in ['activate', 'unknown name', 'interest name extra']:
            with self.assertRaisesRegex(Error, 'maintenance_metadata_invalid'):
                scope['triggers'](text)

    def test_archive_transport_alternatives_are_closed(self):
        scope['archive_methods'](['http://repo.invalid/p.deb', 'https://repo.invalid/p.deb'])
        for values in [[], ['file:/repo/p.deb'], ['cdrom:disc'],
                       ['https://repo.invalid/p.deb', 'mirror+file:/mirror/p.deb']]:
            with self.assertRaisesRegex(Error, 'maintenance_archive_method'):
                scope['archive_methods'](values)

    def test_unset_optional_paths_never_hash_the_working_directory(self):
        import json
        scope.update(json=json,hashlib=hashlib)
        def forbidden(path):
            raise AssertionError('unset APT path traversed the working directory')
        saved=scope['Path'];scope['Path']=forbidden
        try:self.assertEqual(scope['tree_digest'](['']),scope['digest']([]))
        finally:scope['Path']=saved

    def test_source_trust_and_expiry_bypass_spellings_are_rejected(self):
        for name, values in [('Trusted', ['yes','TRUE','1','on']),
                            ('Check-Valid-Until', ['no','FALSE','0','off']),
                            ('Allow-Insecure', ['true']), ('Allow-Weak', ['1'])]:
            for value in values:
                with self.assertRaisesRegex(Error, 'maintenance_repository_policy'):
                    scope['source_security'](name,value)
        scope['source_security']('Signed-By','/usr/share/keyrings/debian-archive-keyring.gpg')
        scope['source_security']('Trusted','no')
        for key in ('Valid-Until-Min', 'Valid-Until-Max', 'Date-Max-Future', 'InRelease-Path'):
            with self.assertRaisesRegex(Error, 'maintenance_repository_policy'):
                scope['source_security'](key, '60')
            scope['source_security'](key, '60', upgrade=False)

    def test_cached_release_expiry_and_future_dates_are_rechecked(self):
        from email.utils import formatdate, parsedate_to_datetime
        now = 1792843200
        scope['apt_pkg'] = SimpleNamespace(str_to_time=lambda value: int(parsedate_to_datetime(value).timestamp()))
        valid = {'Date': formatdate(now - 60, usegmt=True), 'Valid-Until': formatdate(now + 60, usegmt=True)}
        scope['valid_release_dates'](valid, now)
        scope['valid_release_dates']({'Date': valid['Date']}, now)
        for section in [dict(valid, **{'Valid-Until': formatdate(now, usegmt=True)}),
                        dict(valid, Date=formatdate(now + 11, usegmt=True)),
                        {'Valid-Until': valid['Valid-Until']}, dict(valid, Date='invalid')]:
            with self.assertRaisesRegex(Error, 'maintenance_repository_expired'):
                scope['valid_release_dates'](section, now)
        scope['apt_pkg'].str_to_time = lambda value: None if value == 'invalid' else int(parsedate_to_datetime(value).timestamp())
        with self.assertRaisesRegex(Error, 'maintenance_repository_expired'):
            scope['valid_release_dates'](dict(valid, **{'Valid-Until': 'invalid'}), now)

    def test_only_documented_noninteractive_hooks_are_supported(self):
        import types
        scope['Path'] = lambda name: types.SimpleNamespace(exists=lambda: False)
        for key, values, accepted in [('DPkg::Pre-Install-Pkgs', ['/usr/sbin/dpkg-preconfigure --apt || true'], True),
                ('DPkg::Pre-Install-Pkgs', ['/usr/bin/custom-prompt'], False),
                ('APT::Update::Post-Invoke', ['/usr/bin/custom-fetch'], False)]:
            scope['apt_pkg'] = types.SimpleNamespace(config=types.SimpleNamespace(keys=lambda: [key], value_list=lambda name: values))
            if accepted:
                scope['supported_hooks']()
            else:
                with self.assertRaisesRegex(Error, 'maintenance_hook_policy'):
                    scope['supported_hooks']()
        scope['Path'] = Path

    def test_start_gate_rejects_expiry_reboot_and_excess_lifetime(self):
        import re, time
        scope.update(re=re, time=time, Path=Path)
        start = {'job_id': '11111111-1111-4111-8111-111111111111',
                 'attempt_id': '22222222-2222-4222-8222-222222222222',
                 'boot_id': Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
                 'not_after_ms': time.clock_gettime_ns(time.CLOCK_BOOTTIME)//1000000+30000}
        scope['start_allowed'](start)
        for changed in [dict(start, not_after_ms=0),
                        dict(start, boot_id='33333333-3333-4333-8333-333333333333'),
                        dict(start, not_after_ms=start['not_after_ms']+60000)]:
            with self.assertRaisesRegex(Error, 'maintenance_start_expired'):
                scope['start_allowed'](changed)
        with self.assertRaisesRegex(Error, 'maintenance_start_invalid'):
            scope['start_allowed'](dict(start, command='apt-get'))

    def test_missing_metadata_and_payload_traversal_are_blocked(self):
        with self.assertRaisesRegex(Error, 'maintenance_metadata_invalid'):
            scope['boot_effects']({('missing', 'all'): None}, {})
        for name in ['../boot/x', './usr/../boot/x', 'a\x00b']:
            with self.assertRaisesRegex(Error, 'maintenance_metadata_invalid'):
                scope['canonical_path'](name)

if __name__ == '__main__':
    unittest.main()
