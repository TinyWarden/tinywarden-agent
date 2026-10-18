import sys
import unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parents[1]/'runtime'))
from systemd_services import PROPERTIES, read_units, unit_name
from grants import inspect_grant, permits


def properties(name, load='loaded'):
    row={key:'' for key in PROPERTIES}
    row.update(Id=name,Names=name,LoadState=load,ActiveState='inactive',SubState='dead')
    return '\n'.join(key+'='+value for key,value in row.items()).encode()+b'\n'


class SystemdServicesTest(unittest.TestCase):
    def test_names_and_read_only_grants(self):
        for name in ['example.service','worker@one.service',r'worker@a\x2db.service']:
            self.assertTrue(unit_name(name))
        for name in ['--all','/etc/a.service','*.service','a.service\nb.service','a.timer']:
            self.assertFalse(unit_name(name))
        grant={'operation':'systemd.services.read'}
        inspect_grant(grant)
        self.assertTrue(permits(grant,'systemd.services.read',{'units':['example.service']}))
        self.assertFalse(permits(grant,'systemd.services.read',{'units':['--all']}))
        self.assertFalse(permits(grant,'systemd.services.read',{'units':['a.service','a.service']}))
        self.assertFalse(permits(grant,'systemd.services.list',{}))
        with self.assertRaises(ValueError):
            inspect_grant({'operation':'systemd.services.read','action':'restart'})

    def test_templates_do_not_abort_other_queries(self):
        with patch('systemd_services.query',return_value=(0,properties('a.service'),b'')) as query:
            rows=read_units(['a.service','worker@.service'],None,100)
        self.assertEqual(len(rows),2)
        self.assertEqual(next(r for r in rows if r['requested']=='worker@.service')['reason'],'template')
        self.assertNotIn('worker@.service',query.call_args.args[0])
        self.assertTrue(next(r for r in rows if r['requested']=='a.service')['available'])

    def test_explicit_not_found_and_read_failure_differ(self):
        with patch('systemd_services.query',return_value=(1,properties('gone.service','not-found'),b'')):
            row=read_units(['gone.service'],None,100)[0]
        self.assertTrue(row['available']);self.assertEqual(row['properties']['LoadState'],'not-found')
        with patch('systemd_services.query',return_value=(1,b'',b'')):
            row=read_units(['gone.service'],None,100)[0]
        self.assertFalse(row['available']);self.assertEqual(row['reason'],'unreadable')

    def test_aliases_share_canonical_identity(self):
        raw=properties('canonical.service').replace(b'Names=canonical.service',b'Names=canonical.service alias.service')
        with patch('systemd_services.query',return_value=(0,raw,b'')):
            rows=read_units(['alias.service','canonical.service'],None,100)
        self.assertEqual([r['properties']['Id'] for r in rows],['canonical.service','canonical.service'])


if __name__=='__main__': unittest.main()
