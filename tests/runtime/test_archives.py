"""Transport attacks and container/content identity; never import uploaded code."""
import hashlib
import io
import os
import stat
import struct
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'sdk/python/runtime'))
from archives import extract, records, publish_archive, MAX_ARCHIVE
from packages import load
from fixtures import package

class Archives(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name);self.source=package(self.root/'source')
    def zip(self,entries=None,descriptor=False):
        class Streaming(io.BytesIO):
            def seekable(self):return False
            def seek(self,*args):raise io.UnsupportedOperation()
        out=Streaming() if descriptor else io.BytesIO()
        with zipfile.ZipFile(out,'w',compression=zipfile.ZIP_DEFLATED) as z:
            for name,data in (entries or [(p.relative_to(self.source).as_posix(),p.read_bytes()) for p in self.source.rglob('*') if p.is_file()]):
                z.writestr(name,data)
        return out.getvalue()
    def write(self,data):
        p=self.root/'input.zip';p.write_bytes(data);return p
    def stage(self):
        p=self.root/'stage';p.mkdir();return p
    def test_valid_zip_and_streaming_descriptor_have_same_content(self):
        expected=load(self.source)['content_sha256']
        for streaming in (False,True):
            data=self.zip(descriptor=streaming);stage=self.root/str(streaming);stage.mkdir()
            extract(self.write(data),stage,hashlib.sha256(data).hexdigest())
            self.assertEqual(load(stage)['content_sha256'],expected)
    def test_reserved_identity_and_code_are_not_trusted(self):
        (self.source/'skill.py').write_text("raise RuntimeError('must not run')\n"+(self.source/'skill.py').read_text())
        store=self.root/'store'
        result=publish_archive({'archive':str(self.write(self.zip())),'store':str(store)})
        self.assertEqual(result['content_sha256'],load(self.source)['content_sha256'])
        archive=store/'.archives'/(result['content_sha256']+'.zip')
        self.assertEqual(hashlib.sha256(archive.read_bytes()).hexdigest(),result['archive']['sha256'])
        self.assertEqual(publish_archive({'archive':str(self.write(self.zip(descriptor=True))),'store':str(store)}),result)
    def test_paths_duplicates_and_unsupported_payloads(self):
        for names in (['../skill.py'],['/skill.py'],['C:/skill.py'],['a\\skill.py'],['skill.py','Skill.py'],['A/x.py','a/y.py'],['hook.sh'],['a//b.py'],['a/./b.py']):
            with self.subTest(names=names),self.assertRaises(ValueError):records(self.zip([(n,b'x') for n in names]))
    def test_standard_deflate_compression_options_are_supported(self):
        data=bytearray(self.zip())
        for flag in (2,4,6):
            changed=bytearray(data)
            for signature,offset in ((b'PK\x03\x04',6),(b'PK\x01\x02',8)):
                pos=0
                while (pos:=changed.find(signature,pos))>=0:
                    struct.pack_into('<H',changed,pos+offset,flag);pos+=4
            records(changed)
    def test_symlinks_devices_and_encryption(self):
        for kind in (stat.S_IFLNK,stat.S_IFCHR):
            entry=zipfile.ZipInfo('skill.py');entry.create_system=3;entry.external_attr=(kind|0o600)<<16
            out=io.BytesIO()
            with zipfile.ZipFile(out,'w') as z:z.writestr(entry,b'/etc/passwd')
            with self.assertRaises(ValueError):records(out.getvalue())
        data=bytearray(self.zip());local=data.find(b'PK\x03\x04');central=data.find(b'PK\x01\x02')
        for pos in (local+6,central+8):struct.pack_into('<H',data,pos,1)
        with self.assertRaises(ValueError):records(data)
    def test_local_central_mismatch_overlap_trailing_and_crc(self):
        original=self.zip()
        variants=[original+b'payload',b'MZ'+original]
        changed=bytearray(original);changed[30]=ord('X');variants.append(changed)
        changed=bytearray(original);central=changed.find(b'PK\x01\x02');struct.pack_into('<I',changed,central+42,1);variants.append(changed)
        for data in variants:
            with self.subTest(),self.assertRaises(ValueError):records(data)
        changed=bytearray(original);changed[20]^=1
        with self.assertRaises((ValueError,zipfile.BadZipFile)):extract(self.write(changed),self.stage())
    def test_actual_unpacked_limit_and_wrong_digest(self):
        data=self.zip([(f'{i}.txt',b'x'*(8*1024*1024)) for i in range(3)])
        self.assertLess(len(data),MAX_ARCHIVE)
        with self.assertRaises(ValueError):extract(self.write(data),self.stage())
        with self.assertRaises(ValueError):extract(self.write(self.zip()),self.root, '0'*64)
    def test_archive_symlink_and_hardlink_are_rejected(self):
        source=self.write(self.zip());p=self.root/'linked.zip';p.symlink_to(source)
        with self.assertRaises(OSError):extract(p,self.stage())
        p.unlink();os.link(source,p)
        with self.assertRaises(ValueError):extract(p,self.root)

if __name__=='__main__':unittest.main()
