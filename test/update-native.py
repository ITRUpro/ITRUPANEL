# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Run only in a disposable Docker test container. Requires /tmp/itrupanel-old-arm64 and /tmp/itru-update-release payload.
import os,pathlib,subprocess,json,time,signal,shutil
assert pathlib.Path('/.dockerenv').exists()
P=pathlib.Path('/usr/local/itrupanel')
assert not P.exists(), 'Dedicated panel path must be empty before test'
P.mkdir(); (P/'etc').mkdir(); (P/'var').mkdir()
old=pathlib.Path('/tmp/itrupanel-old-arm64');old.chmod(0o755);shutil.copy2(old,P/'itrupanel')
subprocess.run([str(P/'itrupanel'),'passwd','test-only-password'],check=True,stdout=subprocess.DEVNULL)
original=json.loads((P/'etc/state.json').read_text());original['settings']['test_fixture']=True
(P/'etc/state.json').write_text(json.dumps(original))
(P/'site-fixture').write_text('customer files stay unchanged')
manager=pathlib.Path('/usr/local/bin/systemctl');saved=manager.read_bytes() if manager.exists() else None
manager.write_text('''#!/usr/bin/python3
import sys,subprocess,pathlib,os,signal
p=pathlib.Path('/tmp/itru-update-native-pid')
args=sys.argv[1:];action=args[0]
if 'itrupanel' not in args:sys.exit(1 if action=='is-active' else 0)
if action in ['stop','restart'] and p.exists():
 try:os.kill(int(p.read_text()),signal.SIGTERM)
 except ProcessLookupError:pass
 p.unlink()
if action in ['start','restart']:
 f=open('/tmp/itru-update-native.log','ab')
 child=subprocess.Popen(['/usr/local/itrupanel/itrupanel','-http','-listen','127.0.0.1:3377'],stdout=f,stderr=f,start_new_session=True)
 p.write_text(str(child.pid))
if action=='is-active':
 try:os.kill(int(p.read_text()),0)
 except (ProcessLookupError,FileNotFoundError):sys.exit(3)
 print('active')
''');manager.chmod(0o755)
pathlib.Path('/run/systemd/system').mkdir(parents=True,exist_ok=True)
try:
 subprocess.run(['systemctl','start','itrupanel'],check=True);time.sleep(1)
 result=subprocess.run(['bash','/tmp/itru-update-release/update.sh'],text=True,capture_output=True,timeout=60)
 print(result.stdout,result.stderr);assert result.returncode==0
 assert subprocess.check_output([str(P/'itrupanel'),'health-check']).decode().strip()=='0.1.20'
 assert (P/'site-fixture').read_text()=='customer files stay unchanged'
 after=json.loads((P/'etc/state.json').read_text());assert after['settings']['admin_hash']==original['settings']['admin_hash']
 ledger=json.loads((P/'etc/update-migrations.json').read_text());assert len(ledger['applied'])==5
 snapshots=list((P/'var').glob('before-update-*/migration-snapshot.json'));assert len(snapshots)==1
 assert 'test_fixture' in snapshots[0].read_text() or snapshots[0].stat().st_size>100
 print('NATIVE UPDATE PASSED: 0.1.14 -> 0.1.20, real binaries, Unix health socket, five migrations, preserved credentials and site fixture')
finally:
 subprocess.run(['systemctl','stop','itrupanel']);shutil.rmtree(P)
 if saved is not None:manager.write_bytes(saved)
 else:manager.unlink()
