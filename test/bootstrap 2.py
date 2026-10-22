# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
"""Exercise download validation and install/update selection in an isolated fake server."""
import hashlib, os, pathlib, shutil, subprocess, tempfile, unittest, zipfile, sys, time
SOURCE = pathlib.Path('release/bootstrap.sh.in').read_text()
class Bootstrap(unittest.TestCase):
 def scenario(self, installed=False, corrupt=False, download_error=False, low_space=False, panel_shell=False, incomplete=False, legacy_service=False, missing=None):
  with tempfile.TemporaryDirectory(prefix='itru-bootstrap-test-') as directory:
   root=pathlib.Path(directory);bindir=root/'bin';bindir.mkdir();panel=root/'panel';panel.mkdir()
   (root/'os-release').write_text('ID=debian\nPRETTY_NAME=Test\n')
   fixture=root/'fixture.zip'
   with zipfile.ZipFile(fixture,'w') as z:
    for mode in ('install','update'): z.writestr(f'itrupanel/{mode}.sh',f'#!/bin/bash\necho RAN_{mode}\nprintf \"TEMP=%s\\n\" \"$TMPDIR\"\n')
   digest=hashlib.sha256(fixture.read_bytes()).hexdigest()
   if corrupt: digest='0'*64
   if installed:
    (panel/'etc').mkdir();(panel/'etc/state.json').write_text('{}');(panel/'itrupanel').write_text('#!/bin/bash\n');(panel/'itrupanel').chmod(0o755)
   if installed and not incomplete and not legacy_service: (panel/'etc/.admin-set').write_text('done')
   if legacy_service: (root/'itrupanel.service').write_text('[Service]')
   if missing:
    for name in missing: (panel/name).unlink()
   wrappers={'df':"printf 'Filesystem 1024-blocks Used Available Capacity Mounted\\nfixture 204800 1 %s 1%% /var/tmp\\n' " + ('0' if low_space else '204799'), 'id':'echo 0','uname':'if [ "$1" = -s ]; then echo Linux; else echo x86_64; fi','flock':'exit 0','curl':'exit 22' if download_error else f'while [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then cp "{fixture}" "$2"; exit; fi; shift; done', 'sha256sum':f'exec "{shutil.which("python3")}" "{root}/sha.py" "$@"'}
   (root/'sha.py').write_text('import sys,hashlib,pathlib\nwanted,path=sys.stdin.read().strip().split(None,1)\nsys.exit(0 if hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()==wanted else 1)\n')
   for name,body in wrappers.items():
    p=bindir/name;p.write_text('#!/bin/bash\n'+body+'\n');p.chmod(0o755)
   source=SOURCE.replace('/etc/systemd/system/itrupanel.service',str(root/'itrupanel.service')).replace('RELEASE_SHA256_PLACEHOLDER',digest).replace('/run/systemd/system',str(root)).replace('/run/lock/itrupanel-installer.lock',str(root/'lock')).replace('/etc/os-release',str(root/'os-release')).replace('/usr/local/itrupanel',str(panel))
   source=source.replace('/var/tmp/itrupanel-download.', str(root/'work'/'itrupanel-download.'))
   (root/'work').mkdir()
   source=source.replace('export PATH=',f'export PATH="{bindir}:"')
   if panel_shell:
    (root/'cgroup').write_text('0::/system.slice/itrupanel.service\n')
    (root/'staging').mkdir()
    source=source.replace('/proc/self/cgroup',str(root/'cgroup')).replace('/var/tmp/itrupanel-update.',str(root/'staging'/'itrupanel-update.'))
    runner=bindir/'systemd-run'
    runner.write_text(f"""#!{sys.executable}
import pathlib,subprocess,sys
root=pathlib.Path({str(root)!r})
(root/'cgroup').write_text('0::/system.slice/itrupanel-update-test.service\\n')
subprocess.Popen(sys.argv[sys.argv.index('/bin/bash'):],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,close_fds=True)
""")
    runner.chmod(0o755)
   script=root/'install.sh';script.write_text(source)
   result = subprocess.run(['bash',str(script)],capture_output=True,text=True,timeout=15)
   if panel_shell:
    self.assertEqual(result.returncode,0,result.stderr)
    # The parent is gone. The worker must still finish using its private copy.
    script.unlink()
    deadline=time.monotonic()+10
    while list((root/'staging').iterdir()) and time.monotonic()<deadline:time.sleep(.05)
    self.assertEqual(list((root/'staging').iterdir()),[])
    logs='\n'.join(p.read_text() for p in (panel/'var').glob('*.log'))
    result=subprocess.CompletedProcess(result.args,result.returncode,result.stdout+logs,result.stderr)
   self.assertEqual(list((root/'work').iterdir()), [], 'workspace not cleaned')
   return result
 def test_panel_shell_worker_outlives_downloader(self):
  r=self.scenario(installed=True,panel_shell=True);self.assertIn('RAN_update',r.stdout);self.assertIn('Обновление передано systemd',r.stdout)
 def test_install(self):
  r=self.scenario();self.assertEqual(r.returncode,0,r.stderr);self.assertIn('RAN_install',r.stdout);self.assertIn('/work/itrupanel-download.',r.stdout)
 def test_update(self):
  r=self.scenario(installed=True);self.assertEqual(r.returncode,0,r.stderr);self.assertIn('RAN_update',r.stdout);self.assertNotIn('RAN_install',r.stdout)
 def test_incomplete_setup_resumes_installation(self):
  r=self.scenario(installed=True,incomplete=True);self.assertEqual(r.returncode,0,r.stderr);self.assertIn('RAN_install',r.stdout);self.assertNotIn('RAN_update',r.stdout)
 def test_legacy_service_is_updated_without_resetting_admin(self):
  r=self.scenario(installed=True,legacy_service=True);self.assertEqual(r.returncode,0,r.stderr);self.assertIn('RAN_update',r.stdout);self.assertNotIn('RAN_install',r.stdout)
 def test_partial_initial_setup_resumes(self):
  for missing in [('etc/state.json',), ('itrupanel',)]:
   with self.subTest(missing=missing):
    r=self.scenario(installed=True,incomplete=True,missing=missing)
    self.assertEqual(r.returncode,0,r.stderr);self.assertIn('RAN_install',r.stdout)
 def test_initialized_server_missing_files_is_not_reinstalled(self):
  for legacy in (False,True):
   for missing in [('etc/state.json',), ('itrupanel',), ('etc/state.json','itrupanel')]:
    with self.subTest(legacy=legacy,missing=missing):
     r=self.scenario(installed=True,legacy_service=legacy,missing=missing)
     self.assertNotEqual(r.returncode,0);self.assertNotIn('RAN_',r.stdout)
 def test_checksum_failure_never_executes(self):
  r=self.scenario(corrupt=True);self.assertNotEqual(r.returncode,0);self.assertNotIn('RAN_',r.stdout);self.assertIn('контрольная сумма',r.stderr)
 def test_download_failure_never_executes(self):
  r=self.scenario(download_error=True);self.assertNotEqual(r.returncode,0);self.assertNotIn('RAN_',r.stdout)
 def test_low_space_stops_before_install(self):
  r=self.scenario(low_space=True);self.assertNotEqual(r.returncode,0);self.assertNotIn('RAN_',r.stdout);self.assertIn('100 МБ',r.stderr)
if __name__=='__main__':unittest.main()
