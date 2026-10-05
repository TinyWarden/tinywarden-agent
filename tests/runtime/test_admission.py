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

if __name__=='__main__':unittest.main()
