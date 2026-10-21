# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import os,pathlib,subprocess,tempfile,json
root=pathlib.Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as tmp:
 t=pathlib.Path(tmp); b=t/'bin';b.mkdir();home=t/'panel'
 (b/'flock').write_text('#!/bin/bash\nexit 0\n')
 (b/'curl').write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALLS"\n[ "${OFFLINE:-0}" = 0 ] || exit 28\necho \'{"ok":true,"data":{"recorded":true}}\'\n')
 (b/'sleep').write_text('#!/bin/bash\nexit 0\n')
 for p in b.iterdir():p.chmod(0o755)
 env={**os.environ,'PATH':str(b)+':'+os.environ['PATH'],'HOME_DIR':str(home),'CALLS':str(t/'calls')}
 def run(code,**extra):subprocess.run(['bash','-c','source "$1"; '+code,'test',str(root/'installation-stats.sh')],env={**env,**extra},check=True)
 def binary():
  p=home/'itrupanel';p.write_text('#!/bin/bash\n[ "$1" != health-check ] || exit "${FAIL_HEALTH:-0}"\necho 0.1.24\n');p.chmod(0o755)
 run('itru_stats_prepare');ident=(home/'etc/installation-stat-id').read_text();assert len(ident.strip())==64
 binary();run('itru_stats_report',FAIL_HEALTH='1');assert not (t/'calls').exists()
 run('itru_stats_report',ITRUPANEL_INSTALL_STATS='0');assert not (t/'calls').exists()
 run('itru_stats_report',OFFLINE='1');assert not (home/'etc/installation-stat-sent').exists()
 run('itru_stats_report');assert (home/'etc/installation-stat-sent').exists()
 count=len((t/'calls').read_text().splitlines());run('itru_stats_prepare; itru_stats_report');assert len((t/'calls').read_text().splitlines())==count
 assert ident==(home/'etc/installation-stat-id').read_text()
 env['HOME_DIR']=str(t/'existing');home=t/'existing';home.mkdir();binary();run('itru_stats_prepare; itru_stats_report');assert not (home/'etc/installation-stat-id').exists()
 env['HOME_DIR']=str(t/'disabled');home=t/'disabled';run('itru_stats_prepare',ITRUPANEL_INSTALL_STATS='0');binary();run('itru_stats_prepare; itru_stats_report');assert not (home/'etc/installation-stat-id').exists()
 print('PASS: fresh ID, success-only report, unhealthy/offline/opt-out, stable retry ID, dedupe, existing installs excluded')
