#!/usr/bin/env python3
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Подписывает набор правил антивируса ключом выпусков панели.
#   ITRUPANEL_SIGNING_KEY=/путь/ed25519.pem python3 release/sign-antivirus-rules.py
# Читает antivirus/rules-src.json, пишет подписанный antivirus/rules.json (его скачивают панели).
# Закрытый ключ хранится вне репозитория и в нём не публикуется.
import json, os, re, subprocess, sys, tempfile
from pathlib import Path

root = Path(__file__).resolve().parents[1]
src = json.loads((root / 'antivirus/rules-src.json').read_text(encoding='utf-8'))
key = Path(os.environ.get('ITRUPANEL_SIGNING_KEY', ''))
if not key.is_file():
    sys.exit('Укажите ITRUPANEL_SIGNING_KEY: путь к закрытому ключу подписи выпусков')
public_key = re.search(r'const releasePublicKey = "([a-f0-9]+)"', (root / 'src/panel_update.go').read_text()).group(1)
payload = json.dumps(src, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
with tempfile.NamedTemporaryFile(delete=False) as t:
    t.write(payload)
try:
    signed = subprocess.check_output(['go', 'run', str(root / 'release/sign/main.go'), str(key), t.name, public_key])
finally:
    os.unlink(t.name)
(root / 'antivirus/rules.json').write_bytes(signed)
print('antivirus/rules.json: версия %s, правил: %d' % (src['version'], len(src['rules'])))
