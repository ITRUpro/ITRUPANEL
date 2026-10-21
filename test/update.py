# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
"""Run update lifecycle using a fake systemd manager and private filesystem."""
import pathlib,tempfile,subprocess,os,sys,time,unittest,json
SOURCE=pathlib.Path('update.sh').read_text()
BOOTSTRAP=pathlib.Path('release/bootstrap.sh.in').read_text()
class Update(unittest.TestCase):
 def scenario(self,panel_shell=False,dispatch_fail=False,copy_fail=False,start_fail=False,health_fail=False,migration_fail=False):
  with tempfile.TemporaryDirectory(prefix='itru-update-test-') as temp:
   root=pathlib.Path(temp);panel=root/'panel';src=root/'source';bins=root/'commands';stage=root/'staging'
   for p in [panel/'etc',panel/'var',src/'bin',bins,stage]:p.mkdir(parents=True,exist_ok=True)
   (panel/'etc/state.json').write_text('{"keep":"settings"}')
   old='#!/bin/bash\necho OLD\n'
   new=f'''#!/bin/bash
case "$1" in
migrate-update)
 cp '{panel}/etc/state.json' "$2/migration-state"
 echo '{{"changed":"migration"}}' >'{panel}/etc/state.json'
 {'exit 1' if migration_fail else 'exit 0'};;
rollback-update)
 if [ -f "$2/migration-state" ]; then cp "$2/migration-state" '{panel}/etc/state.json'; fi
 exit 0;;
health-check) {'exit 1' if health_fail else 'exit 0'};;
*) echo 0.1.10;;
esac
''' 
   (panel/'itrupanel').write_text(old);(panel/'itrupanel').chmod(0o755)
   (src/'bin/itrupanel-linux-amd64').write_text(new);(src/'bin/itrupanel-linux-amd64').chmod(0o755)
   cgroup=root/'cgroup';cgroup.write_text('0::/system.slice/itrupanel.service\n' if panel_shell else '0::/user.slice/session.scope\n')
   calls=root/'calls';state=root/'state';state.write_text('running')
   commands={'id':'echo 0','uname':'echo x86_64','flock':'exit 0','sleep':'exit 0',
    'systemctl':f'''echo "$*" >>'{calls}'
case "$1" in
stop) echo stopped >'{state}';;
start|restart) {'if [ "$1" = start ]; then exit 1; fi;' if start_fail else ''} echo running >'{state}';;
is-active) test "$(cat '{state}')" = running;;
esac'''}
   if copy_fail:commands['cp']='exit 1'
   for name,body in commands.items():
    p=bins/name;p.write_text('#!/bin/bash\n'+body+'\n');p.chmod(0o755)
   runner=bins/'systemd-run';runner.write_text(f'''#!{sys.executable}
import pathlib,subprocess,sys,os
root=pathlib.Path({str(root)!r})
(root/'dispatch.json').write_text(__import__('json').dumps(sys.argv[1:]))
if {dispatch_fail!r}:sys.exit(1)
(root/'cgroup').write_text('0::/system.slice/itrupanel-update-test.service\\n')
argv=sys.argv[sys.argv.index('/bin/bash'):]
p=subprocess.Popen(argv,start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,close_fds=True)
(root/'worker-pid').write_text(str(p.pid))
''');runner.chmod(0o755)
   source=SOURCE.replace('/usr/local/itrupanel',str(panel)).replace('/run/systemd/system',str(root)).replace('/proc/self/cgroup',str(cgroup)).replace('/var/tmp/itrupanel-update.',str(stage/'itrupanel-update.')).replace('/run/lock/itrupanel-update.lock',str(root/'lock')).replace('export PATH=',f'export PATH="{bins}:"')
   script=src/'update.sh';script.write_text(source)
   result=subprocess.run(['bash',str(script)],capture_output=True,text=True,timeout=10)
   if (root/'worker-pid').exists():
    # Remove the caller's payload: the worker must use its independent copy.
    (src/'bin/itrupanel-linux-amd64').unlink()
    deadline=time.monotonic()+10
    while list(stage.iterdir()) and time.monotonic()<deadline:time.sleep(.05)
    self.assertEqual(list(stage.iterdir()),[],'worker did not clean its workspace')
   return {'code':result.returncode,'out':result.stdout+result.stderr,'binary':(panel/'itrupanel').read_text(),'state':state.read_text().strip(),'calls':calls.read_text() if calls.exists() else '',
    'logs':'\n'.join(p.read_text() for p in (panel/'var').glob('*.log')),'dispatch':(root/'dispatch.json').exists(),'stages':len(list(stage.iterdir())),
    'backups':[(p/'itrupanel').read_text() for p in (panel/'var').glob('before-update-*')],'settings':(panel/'etc/state.json').read_text()}
 def test_ssh_update_remains_synchronous(self):
  r=self.scenario();self.assertEqual(r['code'],0,r);self.assertFalse(r['dispatch']);self.assertIn('0.1.10',r['binary']);self.assertEqual(r['state'],'running');self.assertIn('OLD',r['backups'][0]);self.assertIn('changed',r['settings'])
 def test_panel_shell_handoff_survives_caller_and_payload_removal(self):
  r=self.scenario(panel_shell=True);self.assertEqual(r['code'],0,r);self.assertTrue(r['dispatch']);self.assertIn('0.1.10',r['binary']);self.assertEqual(r['state'],'running');self.assertIn('обновлена до 0.1.10',r['logs']);self.assertEqual(r['stages'],0)
 def test_dispatch_failure_never_stops_panel(self):
  r=self.scenario(panel_shell=True,dispatch_fail=True);self.assertNotEqual(r['code'],0);self.assertEqual(r['calls'],'');self.assertIn('OLD',r['binary']);self.assertEqual(r['stages'],0)
 def test_staging_failure_never_dispatches_or_stops_panel(self):
  r=self.scenario(panel_shell=True,copy_fail=True);self.assertNotEqual(r['code'],0);self.assertFalse(r['dispatch']);self.assertEqual(r['calls'],'');self.assertEqual(r['stages'],0)
 def test_restart_failure_restores_previous_binary_in_worker(self):
  r=self.scenario(panel_shell=True,start_fail=True);self.assertIn('OLD',r['binary']);self.assertEqual(r['state'],'running');self.assertIn('Восстанавливаю предыдущую',r['logs'])
 def test_unhealthy_new_version_rolls_back_binary_and_settings(self):
  r=self.scenario(health_fail=True);self.assertNotEqual(r['code'],0,r);self.assertIn('OLD',r['binary']);self.assertEqual(r['state'],'running');self.assertIn('keep',r['settings']);self.assertNotIn('changed',r['settings'])
 def test_failed_migration_rolls_back_before_new_binary_install(self):
  r=self.scenario(migration_fail=True);self.assertNotEqual(r['code'],0,r);self.assertIn('OLD',r['binary']);self.assertEqual(r['state'],'running');self.assertIn('keep',r['settings'])
 def test_bootstrap_handoff_before_lock_and_download(self):
  main=BOOTSTRAP[BOOTSTRAP.index('main() {'):]
  self.assertLess(main.index('detach_from_panel'),main.index('exec 9>'))
  self.assertLess(main.index('detach_from_panel'),main.index('WORK_DIR='))
if __name__=='__main__':unittest.main()
