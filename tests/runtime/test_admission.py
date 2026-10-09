"""Bounded admission must reject unsafe data without importing its code."""
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'sdk/python/runtime'))
from packages import load
from grants import inspect_grant, permits
from grants import safe_path
from fixtures import package

class Admission(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.root=package(Path(self.tmp.name)/'skill')
    def test_code_is_not_imported_and_digest_binds_all_files(self):
        p=self.root/'skill.py';p.write_text("raise RuntimeError('metadata imported code')\n"+p.read_text())
        admitted=load(self.root)
        (self.root/'README.md').write_text('different bytes')
        with self.assertRaises(ValueError):load(self.root,expected=admitted['content_sha256'])
    def test_links_and_case_collisions_are_rejected(self):
        for mode in ('symlink','hardlink','case'):
            with self.subTest(mode=mode):
                p=self.root/('Skill.py' if mode=='case' else 'extra.py')
                if mode=='symlink':p.symlink_to('/etc/passwd')
                elif mode=='hardlink':os.link(self.root/'skill.py',p)
                else:p.write_text('')
                with self.assertRaises((ValueError,OSError)):load(self.root)
                p.unlink()
    def test_unknown_runtime_and_schema_extensions_are_rejected(self):
        p=self.root/'skill.json';original=p.read_text();v=json.loads(original);v['sdk']=2;p.write_text(json.dumps(v))
        with self.assertRaises(ValueError):load(self.root)
        p.write_text(original);schema=self.root/'settings.schema.json';v=json.loads(schema.read_text());v['minimum']=0;schema.write_text(json.dumps(v))
        with self.assertRaises(ValueError):load(self.root)
    def test_commands_need_exact_grants_and_cannot_request_credentials(self):
        grant={'operation':'command.capture','executable':'/usr/bin/apt-get','argv':[['--simulate','upgrade']],
               'inputs':['/var/lib/dpkg/status'],'helpers':['/usr/bin/dpkg'],'empty_directories':['/etc/apt/apt.conf.d'],'timeout_seconds':30}
        inspect_grant(grant)
        args={k:grant[k] for k in ('executable','inputs','helpers','empty_directories')};args['argv']=['--simulate','upgrade']
        self.assertTrue(permits(grant,'command.capture',args))
        self.assertFalse(permits(grant,'command.capture',{**args,'helpers':['/usr/bin/sh']}))
        self.assertFalse(permits(grant,'command.capture',{**args,'argv':['update']}))
        for path in ('/etc','/etc/apt','/etc/apt/auth.conf','/var/lib/tinywarden-agent'):
            with self.subTest(path=path),self.assertRaises(ValueError):inspect_grant({**grant,'inputs':[path]})

    def test_library_helpers_stay_explicit_and_bounded(self):
        helper='/usr/lib/apt/methods/mirror+file'
        grant={'operation':'command.capture','executable':'/usr/bin/apt-get','argv':[['--simulate','upgrade']],
               'inputs':['/etc/apt/mirrors'],'helpers':[helper],'timeout_seconds':30}
        inspect_grant(grant)
        args={'executable':grant['executable'],'argv':['--simulate','upgrade'],'inputs':grant['inputs'],'helpers':[helper]}
        self.assertTrue(permits(grant,'command.capture',args))
        self.assertFalse(permits(grant,'command.capture',{**args,'helpers':['/usr/lib/apt/methods/http']}))
        self.assertFalse(permits({**grant,'helpers':[]},'command.capture',args))
        for path in ('/home/helper','/tmp/helper','/usr/lib/../bin/dpkg','/usr/lib//apt/methods/file',
                     '/usr/lib/apt/./methods/file','/usr/lib/apt/methods/sh','/usr/lib/apt/methods/env',
                     '/usr/libexec/helper'):
            with self.subTest(path=path),self.assertRaises(ValueError):inspect_grant({**grant,'helpers':[path]})
        with self.assertRaises(ValueError):inspect_grant({**grant,'executable':helper})

    def test_optional_reads_keep_exact_file_and_operation_permissions(self):
        path='/run/reboot-required.pkgs'
        grant={'operation':'files.read','paths':[path],'roots':[],'max_bytes':16384}
        inspect_grant(grant)
        args={'path':path,'max_bytes':16384,'optional':True}
        self.assertTrue(permits(grant,'files.read',args))
        self.assertFalse(permits(grant,'files.read',{**args,'max_bytes':16385}))
        self.assertFalse(permits({**grant,'paths':[]},'files.read',args))
        for value in ('true',1,None):self.assertFalse(permits(grant,'files.read',{**args,'optional':value}))
        for operation in ('files.stat','files.list'):
            self.assertFalse(permits({**grant,'operation':operation},operation,args))
        with self.assertRaises(ValueError):inspect_grant({**grant,'optional':True})
        for denied in ('/run','/run/other','/run/reboot-required.pkgs.old','/var/run/reboot-required.pkgs','/run/../run/reboot-required.pkgs'):
            self.assertFalse(safe_path(denied))
            with self.assertRaises(ValueError):inspect_grant({**grant,'paths':[denied]})
        self.assertFalse(safe_path(path,True))
        with self.assertRaises(ValueError):inspect_grant({**grant,'paths':[],'roots':[path]})

    def test_optional_file_errors_never_soften_links_special_files_or_bugs(self):
        import errno
        from unittest.mock import patch
        from files import observe
        with tempfile.TemporaryDirectory(dir='/var/tmp') as directory:
            path=Path(directory)/'data';args={'path':str(path),'max_bytes':16,'optional':True}
            self.assertEqual(observe('files.read',args),{'available':False,'reason':'not_found'})
            with self.assertRaises(RuntimeError):observe('files.read',{'path':str(path),'max_bytes':16})
            path.write_bytes(b'hello')
            self.assertEqual(observe('files.read',args),{'available':True,'text':'hello','truncated':False})
            self.assertEqual(observe('files.read',{**args,'optional':False}),{'text':'hello','truncated':False})
            path.write_bytes(b'\xff')
            self.assertEqual(observe('files.read',args),{'available':False,'reason':'invalid_encoding'})
            with self.assertRaises(UnicodeDecodeError):observe('files.read',{**args,'optional':False})
            path.unlink();path.symlink_to(Path(directory)/'target')
            with self.assertRaises(OSError):observe('files.read',args)
            path.unlink();os.mkfifo(path)
            with self.assertRaises(RuntimeError):observe('files.read',args)
        for code in (errno.EACCES,errno.EPERM,errno.EIO,errno.ESTALE):
            with patch('files.open_path',side_effect=OSError(code,'private error')):
                self.assertEqual(observe('files.read',args),{'available':False,'reason':'unreadable'})
        for error in (OSError(errno.ENOTDIR,'wrong directory'),RuntimeError('capability_denied'),OSError(errno.EBADF,'bug')):
            with patch('files.open_path',side_effect=error),self.assertRaises(type(error)):
                observe('files.read',args)

if __name__=='__main__':unittest.main()
