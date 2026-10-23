// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

// Двойной щелчок в менеджере файлов открывает редактор у любого файла, кроме заведомо двоичных.
for (const dir of ['src/web/pages.js', 'src/web/en/pages.js']) {
  test('file manager opens text-like files in the editor, binaries download ' + dir, () => {
    const src = fs.readFileSync(dir, 'utf8');
    const re = new RegExp(src.match(/const BINARY_EXT = \/(.+)\/i;/)[1], 'i');
    for (const name of ['config.php.new', 'config.php', '.env', 'wp-config.php.bak', 'index.html', 'Makefile', 'notes.txt', 'site.conf.disabled', 'dump.sql', 'app.log.1', 'composer.lock', 'README', 'data.json5']) {
      assert.equal(re.test(name), false, name + ' должен открываться в редакторе');
    }
    for (const name of ['photo.jpg', 'logo.PNG', 'backup.tar.gz', 'archive.zip', 'video.mp4', 'doc.pdf', 'sheet.xlsx', 'font.woff2', 'tool.exe', 'cache.sqlite']) {
      assert.equal(re.test(name), true, name + ' должен скачиваться');
    }
    assert.match(src, /if \(BINARY_EXT\.test\(p\) && r\.size > 0\)/);
    assert.doesNotMatch(src, /if \(!TEXT_EXT\.test\(p\)/);
  });
}
