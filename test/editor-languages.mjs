// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import { languageFor } from '../src/web/vendor/editor-langs.mjs';

test('язык подсветки определяется по имени и пути файла', () => {
  const cases = {
    'index.php': 'PHP', '/var/www/a/b/template.phtml': 'PHP', 'page.HTML': 'HTML', 'style.css': 'CSS', 'app.js': 'JavaScript', 'main.ts': 'TypeScript',
    'data.json': 'JSON', 'README.md': 'Markdown', 'dump.sql': 'SQL', 'docker-compose.yml': 'YAML', 'feed.xml': 'XML',
    'deploy.sh': 'Shell', '.env': 'INI', '.env.production': 'INI', '.htaccess': 'INI', '.user.ini': 'INI', 'php.ini': 'INI', 'my.cnf': 'INI',
    'Dockerfile': 'Dockerfile', 'Dockerfile.dev': 'Dockerfile', 'crontab': 'Shell', 'composer.lock': 'JSON',
    '/etc/nginx/conf.d/site.conf': 'Nginx', '/etc/nginx/sites-available/default': '', 'site.conf': 'Nginx',
    '/etc/httpd/conf/httpd.conf': 'INI', 'notes.txt': '', 'archive': '', '': '',
  };
  for (const [name, want] of Object.entries(cases)) assert.equal(languageFor(name), want, name);
});
