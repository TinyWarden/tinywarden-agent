"""Official behavior compatibility and schedule-state fixtures; no host mutation."""
import datetime
import importlib.util
import json
import sys
import unittest
from pathlib import Path
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'sdk/python/runtime'))
from packages import load
from schema import validate

def module(alias):
    directory = ROOT / 'skills/official' / alias
    sys.path.insert(0, str(directory))
    spec = importlib.util.spec_from_file_location(alias.replace('-', '_'), directory / 'skill.py')
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    sys.path.pop(0)
    return value

def clean(value):
    if isinstance(value, dict): return {k:clean(v) for k,v in value.items() if v is not None}
    if isinstance(value, list): return [clean(v) for v in value]
    return value

class OfficialTests(unittest.TestCase):
    def test_apt_golden_counts_and_uncertain_output(self):
        skill = module('package-updates')
        for path in (ROOT/'internal/skills/builtin/testdata/package-updates').glob('*.json'):
            f=json.loads(path.read_text()); step=f['steps'][0]
            class Host:
                def request(self,*args):
                    return {'exit_code':step['exit_code'], 'stdout':step['stdout'], 'stderr_bytes':len(step['stderr'].encode()) + int(step['stdout_truncated'] or step['stderr_truncated'] or not step['cleanup_complete'])}
            with self.subTest(path=path.name):
                actual=skill.collect({'package_mode':f['mode']},Host())
                if f['expected']['problem']=='none':
                    self.assertEqual(actual, {'problem':'none',**f['expected']['packages']})
                    result=skill.evaluate({'observation':actual,'now':1})[0]
                    self.assertEqual(result['status'],'warning' if sum(actual[k] for k in ('upgraded','installed','removed','held_back')) else 'healthy')
                else:self.assertNotEqual(actual['problem'],'none')
    def test_trim_golden_normalization(self):
        skill=module('fstrim-status');parse=sys.modules['collector'].parse
        for path in (ROOT/'internal/skills/builtin/testdata/fstrim-status').glob('*.json'):
            f=json.loads(path.read_text());properties=[]
            for step in f['steps']: properties.append(dict(line.split('=',1) for line in step['stdout'].splitlines() if '=' in line))
            start=datetime.datetime.fromisoformat(f['started_at']).timestamp();end=datetime.datetime.fromisoformat(f['finished_at']).timestamp()
            with self.subTest(path=path.name):
                result=parse(*properties,start,end,f['boot_before'],f['boot_after'])
                if f['expected']['problem']=='none':self.assertEqual(result,{'problem':'none',**clean(f['expected']['fstrim'])})
                else:self.assertNotEqual(result['problem'],'none')
    def test_trim_schedule_survives_reboot_and_preserves_original_retention(self):
        skill=module('fstrim-status');f=json.loads((ROOT/'internal/skills/builtin/testdata/fstrim-status/completed-service-and-schedule.json').read_text())
        obs={'problem':'none',**clean(f['expected']['fstrim'])};now=obs['observed_at']*1000
        context={'observation':obs,'previous_state':None,'received_at':now,'now':now,'evidence_expires_at':now+3*3600*1000,'state':None}
        state=skill.reduce(context);context['state']=state
        self.assertEqual(skill.evaluate(context)[0]['status'],'healthy')
        reboot=json.loads(json.dumps(obs));reboot['observed_at']+=100;reboot['service']={'load_state':'loaded','active_state':'inactive','result':'success','exit_kind':0,'exit_status':0,'condition':{}}
        context.update(observation=reboot,previous_state=state,received_at=now+100000,now=now+100000)
        next_state=skill.reduce(context);self.assertEqual(next_state['last_execution']['recorded_at'],now)
        self.assertEqual(next_state['expected_at'],state['expected_at'])
        context.update(state=next_state,now=(next_state['expected_at']+86400)*1000,evidence_expires_at=(next_state['expected_at']+86401)*1000)
        self.assertEqual(skill.evaluate(context)[0]['reason']['key'],'fstrim_result_overdue')
        context.update(previous_state=next_state,received_at=now+91*86400000)
        self.assertNotIn('last_execution',skill.reduce(context))
    def test_disk_threshold_partial_coverage_and_read_only_mount(self):
        skill=module('disk-local');settings={'warning_percent':85,'critical_percent':95,'interval_seconds':300}
        mount={'mount_id':1,'mount_path':'/','mount_root':'/','filesystem_type':'ext4','kind':'local','writable':True,'shared_capacity':False,'reason':'none','total_bytes':'100','free_bytes':'5','available_bytes':'5'}
        context={'now':1,'settings':settings,'observation':{'coverage':'incomplete','reason':'none','excluded_kernel':0,'excluded_remote':0,'mounts':[mount]}}
        self.assertEqual(skill.evaluate(context)[0]['status'],'critical')
        mount['available_bytes']='15';self.assertEqual(skill.evaluate(context)[0]['status'],'warning')
        mount['available_bytes']='16';self.assertEqual(skill.evaluate(context)[0]['status'],'unknown')
        context['observation']['coverage']='complete';self.assertEqual(skill.evaluate(context)[0]['status'],'healthy')
        mount['writable']=False;self.assertEqual(skill.evaluate(context)[0]['status'],'unknown')
        self.assertTrue(skill.validate_settings({**settings,'critical_percent':85}))
    def test_metadata_all_packages_and_fifth(self):
        for directory in [*(ROOT/'skills/official').iterdir(),ROOT/'skills/examples/memory-pressure']:
            with self.subTest(package=directory.name):
                m=load(directory,official=directory.parent.name=='official');validate(m['schemas']['settings'],m['manifest']['defaults'])

if __name__=='__main__':unittest.main()
