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
                    expected = {'problem':'none',**f['expected']['packages']}
                    self.assertEqual({key:actual[key] for key in expected}, expected)
                    result=skill.evaluate({'observation':actual,'now':1})[0]
                    self.assertEqual(result['status'],'warning' if sum(actual[k] for k in ('upgraded','installed','removed','held_back')) else 'healthy')
                else:self.assertNotEqual(actual['problem'],'none')
    def test_package_list_matches_real_apt_actions_and_held_names(self):
        skill = module('package-updates')
        output = """The following packages have been kept back:
  held-package other-held:amd64
1 upgraded, 1 newly installed, 1 to remove and 2 not upgraded.
Inst nano [8.4-1] (8.4-1+deb13u1 Debian:13.7/stable [amd64])
Inst libnew:amd64 (1:2.0-1 Debian:13.7/stable [amd64])
Remv obsolete [1.0-1]
Conf nano (8.4-1+deb13u1 Debian:13.7/stable [amd64])
"""
        class Host:
            def request(self,*args): return {'exit_code':0,'stdout':output,'stderr_bytes':0}
        observation = skill.collect({'package_mode':'upgrade'},Host())
        self.assertTrue(observation['package_list_complete'])
        self.assertFalse(observation['package_list_truncated'])
        rows = observation['packages']
        self.assertEqual(rows[2], {'package':'nano','installed_version':'8.4-1','available_version':'8.4-1+deb13u1','action':'upgraded'})
        self.assertEqual(rows[3]['action'],'installed')
        self.assertEqual(rows[4], {'package':'obsolete','installed_version':'1.0-1','available_version':'','action':'removed'})
        self.assertEqual(rows[0]['available_version'],'')
        self.assertEqual(len(rows),5)
        metadata = load(ROOT/'skills/official/package-updates',official=True)
        validate(metadata['schemas']['observation'],observation)
        from outcomes import timeline
        context = {'observation':observation,'now':1000,'evidence_expires_at':2000}
        timeline(skill.evaluate(context),context,metadata['catalog'])

    def test_package_details_do_not_change_counts_or_expose_arbitrary_output(self):
        skill = module('package-updates')
        for output in ['Private repository token blah\n2 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\nInst nano [8.4-1] (8.4-2 Debian [amd64])\n',
                       '1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\nInst malicious/path [1] (2)\n']:
            class Host:
                def request(self,*args): return {'exit_code':0,'stdout':output,'stderr_bytes':0}
            obs = skill.collect({'package_mode':'upgrade'},Host())
            self.assertEqual(obs['problem'],'none')
            self.assertFalse(obs['package_list_complete'])
            self.assertNotIn('Private',json.dumps(obs))
            self.assertNotIn('malicious',json.dumps(obs))
            self.assertEqual(skill.evaluate({'observation':obs,'now':1})[0]['status'],'warning')

    def test_package_details_bound_rows_and_flag_mismatch_duplicates(self):
        skill = module('package-updates')
        rows = ['Inst pkg%d [1.0] (2.0 Debian [amd64])'%i for i in range(101)]
        result = skill.package_details(rows,{'upgraded':101,'installed':0,'removed':0,'held_back':0})
        self.assertEqual(len(result['packages']),100)
        self.assertTrue(result['package_list_truncated'])
        self.assertTrue(result['package_list_complete'])
        result = skill.package_details(rows[:1]*2,{'upgraded':1,'installed':0,'removed':0,'held_back':0})
        self.assertFalse(result['package_list_complete'])
        self.assertEqual(len(result['packages']),1)
        result = skill.package_details(rows[:1],{'upgraded':0,'installed':0,'removed':0,'held_back':0})
        self.assertFalse(result['package_list_complete'])
        self.assertEqual(result['packages'],[])

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

    def test_trim_readable_presentation_preserves_schedule_and_actual_finish_time(self):
        import copy
        skill = module('fstrim-status')
        fixture = json.loads((ROOT/'internal/skills/builtin/testdata/fstrim-status/completed-service-and-schedule.json').read_text())
        observation = {'problem':'none', **clean(fixture['expected']['fstrim'])}
        now = observation['observed_at'] * 1000
        context = {'observation':observation, 'previous_state':None, 'received_at':now,
                   'now':now, 'evidence_expires_at':now + 3*3600*1000}
        context['state'] = skill.reduce(context)
        facts = {f['key']:f for f in skill.evaluate(context)[0]['facts']}
        self.assertEqual(facts['trim_last_result']['value'], 'success')
        self.assertEqual(facts['trim_completed_at']['value'], observation['service']['finished_at']*1000)
        self.assertNotEqual(facts['trim_completed_at']['value'], now)
        self.assertEqual(facts['trim_automatic']['value'], 'enabled')
        self.assertEqual(facts['trim_next_at']['value'], observation['timer']['next_elapse']*1000)
        self.assertEqual(facts['trim_problem']['value'], '')
        self.assertEqual(facts['trim_due']['rows'], [])
        for unit_state, active_state, expected in [('disabled','inactive','disabled'), ('enabled','inactive','inactive')]:
            changed = copy.deepcopy(context)
            changed['observation']['timer'].update(unit_file_state=unit_state, active_state=active_state)
            result = skill.evaluate(changed)[0]
            values = {f['key']:f for f in result['facts']}
            self.assertEqual(result['status'], 'warning')
            self.assertEqual(values['trim_automatic']['value'], expected)
            self.assertNotIn('trim_next_at', values)
            self.assertTrue(values['trim_problem']['value'])
        running = copy.deepcopy(context)
        running['observation']['service']['active_state'] = 'activating'
        running['state'].pop('last_execution')
        values = {f['key']:f for f in skill.evaluate(running)[0]['facts']}
        self.assertEqual(values['trim_automatic']['value'], 'running')
        self.assertEqual(values['trim_last_result']['value'], 'unrecorded')
        self.assertNotIn('trim_completed_at', values)
        unknown = copy.deepcopy(context)
        unknown['observation'] = {'problem':'clock_uncertain'}
        result = skill.evaluate(unknown)[0]
        self.assertEqual(result['status'], 'unknown')
        self.assertEqual(next(f for f in result['facts'] if f['key']=='trim_automatic')['value'], 'unconfirmed')
        self.assertNotIn('trim_next_at', {f['key']:f for f in result['facts']})

    def test_trim_future_overdue_details_are_not_shown_in_healthy_reading(self):
        skill = module('fstrim-status')
        fixture = json.loads((ROOT/'internal/skills/builtin/testdata/fstrim-status/completed-service-and-schedule.json').read_text())
        obs = {'problem':'none', **clean(fixture['expected']['fstrim'])}
        now = obs['observed_at']*1000
        context = {'observation':obs, 'previous_state':None, 'received_at':now, 'now':now,
                   'evidence_expires_at':(obs['timer']['next_elapse'] + 86401)*1000}
        context['state'] = skill.reduce(context)
        result = skill.evaluate(context)
        from outcomes import timeline
        metadata = load(ROOT/'skills/official/fstrim-status', official=True)
        timeline(result, context, metadata['catalog'])
        self.assertEqual(result[0]['status'], 'healthy')
        self.assertEqual(next(f for f in result[0]['facts'] if f['key']=='trim_problem')['value'], '')
        overdue = result[-1]
        self.assertEqual(overdue['reason']['key'], 'fstrim_result_overdue')
        facts = {f['key']:f for f in overdue['facts']}
        self.assertEqual(facts['trim_due']['rows'], [{'scheduled_for':obs['timer']['next_elapse']*1000, 'result':'unconfirmed'}])
        self.assertNotIn('trim_next_at', facts)

    def test_disk_threshold_partial_coverage_and_read_only_mount(self):
        skill=module('disk-local');settings={'warning_percent':85,'critical_percent':95,'interval_seconds':300}
        mount={'mount_id':1,'mount_path':'/','mount_root':'/','filesystem_type':'ext4','kind':'local','writable':True,'shared_capacity':False,'reason':'none','total_bytes':'100','free_bytes':'5','available_bytes':'5'}
        context={'now':1,'settings':settings,'observation':{'coverage':'incomplete','reason':'none','excluded_kernel':0,'excluded_remote':0,'mounts':[mount]}}
        self.assertEqual(skill.evaluate(context)[0]['status'],'critical')
        mount['free_bytes']=mount['available_bytes']='15';self.assertEqual(skill.evaluate(context)[0]['status'],'warning')
        mount['free_bytes']=mount['available_bytes']='16';self.assertEqual(skill.evaluate(context)[0]['status'],'unknown')
        context['observation']['coverage']='complete';self.assertEqual(skill.evaluate(context)[0]['status'],'healthy')
        mount['writable']=False;self.assertEqual(skill.evaluate(context)[0]['status'],'unknown')
        self.assertTrue(skill.validate_settings({**settings,'critical_percent':85}))
    def test_metadata_all_packages_and_fifth(self):
        for directory in [*(ROOT/'skills/official').iterdir(),ROOT/'skills/examples/memory-pressure']:
            with self.subTest(package=directory.name):
                m=load(directory,official=directory.parent.name=='official');validate(m['schemas']['settings'],m['manifest']['defaults'])

    def test_reboot_packages_preserve_request_and_discard_orphan_lists(self):
        skill=module('reboot-required');metadata=load(ROOT/'skills/official/reboot-required',official=True)
        class Host:
            def __init__(self,markers,result):self.markers=iter(markers);self.result=result;self.reads=0
            def request(self,operation,args):
                if operation=='files.stat':return {'exists':next(self.markers)}
                self.reads+=1
                self_request={'path':'/run/reboot-required.pkgs','max_bytes':16384,'optional':True}
                if args!=self_request:raise AssertionError(args)
                return self.result
        def assess(markers,result):
            host=Host(markers,result);obs=skill.collect(metadata['manifest']['defaults'],host)
            validate(metadata['schemas']['observation'],obs)
            context={'observation':obs,'now':1000,'evidence_expires_at':2000}
            result=skill.evaluate(context)[0]
            from outcomes import timeline
            timeline([result],context,metadata['catalog'])
            return obs,result,{f['key']:f for f in result['facts']},host
        valid={'available':True,'text':'linux-image-amd64\n dbus \nlinux-image-amd64\n','truncated':False}
        obs,result,facts,host=assess([True,True],valid)
        self.assertEqual((result['status'],obs['packages']),('warning',['dbus','linux-image-amd64']))
        self.assertEqual(obs['package_list_status'],'reported')
        self.assertEqual(facts['package_notice']['value'],'')
        for markers in ([False],[True,False]):
            obs,result,facts,host=assess(markers,valid)
            self.assertEqual((result['status'],obs['packages']),('healthy',[]))
            self.assertEqual(facts['packages']['rows'],[])
            self.assertEqual(facts['package_notice']['value'],'')
            if markers==[False]:self.assertEqual(host.reads,0)
        for unavailable in [{'available':False,'reason':r} for r in ('not_found','unreadable','invalid_encoding')]+[{'available':True,'text':'','truncated':False},{'available':True,'text':'private text\n<script>\n','truncated':False}]:
            obs,result,facts,_=assess([True,True],unavailable)
            self.assertEqual((result['status'],obs['packages']),('warning',[]))
            self.assertEqual(facts['package_notice']['value'],'unavailable')
        for partial in [{'available':True,'text':'dbus\nprivate words\n','truncated':False},{'available':True,'text':'dbus\nlinux-image','truncated':True}]:
            obs,result,facts,_=assess([True,True],partial)
            self.assertEqual(obs['packages'],['dbus'])
            self.assertEqual(obs['package_list_status'],'partial')
            self.assertEqual(facts['package_notice']['value'],'partial')
            self.assertFalse(facts['packages']['truncated'])
        oversized={'available':True,'text':'\n'.join('pkg%03d'%i for i in range(101)),'truncated':False}
        obs,result,facts,_=assess([True,True],oversized)
        self.assertEqual(len(obs['packages']),100)
        self.assertTrue(facts['packages']['truncated'])
        self.assertEqual(facts['package_notice']['value'],'')
        for marker in (False,True):
            old={'marker_observed':marker,'assurance':'limited'}
            validate(metadata['schemas']['observation'],old)
            result=skill.evaluate({'observation':old,'now':1000})[0]
            self.assertEqual(result['status'],'warning' if marker else 'healthy')

    def test_disk_omits_readonly_storage_but_monitors_writable_tmpfs(self):
        skill=module('disk-local')
        writable={'mount_id':1,'mount_path':'/tmp','mount_root':'/','filesystem_type':'tmpfs','kind':'local','writable':True,'shared_capacity':False,'reason':'none','total_bytes':'100','free_bytes':'5','available_bytes':'5'}
        readonly={**writable,'mount_id':2,'mount_path':'/run/credentials/systemd-networkd.service','writable':False}
        snapshot={'coverage':'complete','reason':'none','excluded_kernel':0,'excluded_remote':0,'mounts':[readonly,writable]}
        class Host:
            def request(self,*args):return json.loads(json.dumps(snapshot))
        observation=skill.collect({},Host())
        self.assertEqual([m['mount_path'] for m in observation['mounts']],['/tmp'])
        validate(load(ROOT/'skills/official/disk-local',official=True)['schemas']['observation'],observation)
        context={'now':1,'settings':{'warning_percent':85,'critical_percent':95},'observation':snapshot}
        assessment=skill.evaluate(context)[0]
        self.assertEqual(assessment['status'],'critical')
        table=next(f for f in assessment['facts'] if f['key']=='filesystems')
        self.assertEqual([row['path'] for row in table['rows']],['/tmp'])
        self.assertEqual(table['rows'][0]['used'],'95.0')
        self.assertNotIn('/run/credentials',json.dumps(assessment))
        # Missing capacity on a relevant writable mount stays visible as Unknown.
        snapshot['mounts']=[{k:v for k,v in writable.items() if k!='free_bytes'}]
        assessment=skill.evaluate(context)[0]
        self.assertEqual(assessment['status'],'unknown')
        self.assertEqual(next(f for f in assessment['facts'] if f['key']=='filesystems')['rows'][0]['used'],'')

if __name__=='__main__':unittest.main()
