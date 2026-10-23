// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Определение языка подсветки по имени и пути файла (отдельный модуль web/vendor/editor-langs.mjs, проверяется тестом test/editor-languages.mjs).
const EXT = {
  php: 'PHP', phtml: 'PHP', inc: 'PHP', php3: 'PHP', php4: 'PHP', php5: 'PHP', php7: 'PHP', php8: 'PHP',
  html: 'HTML', htm: 'HTML', xhtml: 'HTML', tpl: 'HTML', twig: 'HTML', vue: 'HTML',
  css: 'CSS', scss: 'CSS', less: 'CSS',
  js: 'JavaScript', mjs: 'JavaScript', cjs: 'JavaScript', jsx: 'JavaScript', ts: 'TypeScript', tsx: 'TypeScript',
  json: 'JSON', map: 'JSON', webmanifest: 'JSON',
  md: 'Markdown', markdown: 'Markdown', sql: 'SQL', yml: 'YAML', yaml: 'YAML',
  xml: 'XML', svg: 'XML', xsl: 'XML', rss: 'XML',
  sh: 'Shell', bash: 'Shell', zsh: 'Shell', ksh: 'Shell', env: 'INI', ini: 'INI', cnf: 'INI', properties: 'INI', cfg: 'INI', htaccess: 'INI', user: 'INI',
  conf: 'Nginx', toml: 'TOML', py: 'Python', go: 'Go', diff: 'Diff', patch: 'Diff',
};

// languageFor определяет язык по имени и пути файла; пустая строка — обычный текст.
// Расширения копий и заготовок: config.php.new, index.html.bak — подсветка по настоящему расширению перед ними.
const COPY_SUFFIX = new Set(['new', 'bak', 'old', 'orig', 'save', 'dist', 'sample', 'example', 'tmp', 'disabled', 'backup', 'copy', 'swp', 'rej', 'default', 'template']);

export function languageFor(filename) {
  const path = String(filename || '').replace(/\\/g, '/');
  let name = path.split('/').pop().toLowerCase();
  for (let i = 0; i < 3; i++) {
    const d = name.lastIndexOf('.');
    if (d > 0 && COPY_SUFFIX.has(name.slice(d + 1))) name = name.slice(0, d);
    else break;
  }
  name = name.replace(/~$/, '');
  if (name === 'dockerfile' || name.startsWith('dockerfile.')) return 'Dockerfile';
  if (name === '.htaccess' || name === '.user.ini' || name === 'php.ini' || name === '.env' || name.startsWith('.env.')) return 'INI';
  if (name === 'crontab' || name === '.bashrc' || name === '.profile' || name === '.bash_profile') return 'Shell';
  if (name === 'composer.lock' || name === 'package.json' || name === 'package-lock.json') return 'JSON';
  if (name.endsWith('.conf') && /\/(nginx|sites-available|sites-enabled|conf\.d)\//.test(path.toLowerCase())) return 'Nginx';
  const dot = name.lastIndexOf('.');
  const ext = dot >= 0 ? name.slice(dot + 1) : '';
  if (ext === 'conf' && /(apache|httpd|php|my\.|mysql|mariadb)/.test(path.toLowerCase())) return 'INI';
  return EXT[ext] || '';
}

