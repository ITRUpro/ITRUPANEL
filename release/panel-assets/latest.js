// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Актуальный номер версии и ссылка на архив берутся из /panel/latest.json: после выпуска новой версии
// страницу переписывать не нужно. В самой странице остаётся номер на момент её сборки (для поисковиков и на случай,
// если файл не загрузился). Подпись выпуска здесь не проверяется: она нужна установщику и панели, а страница
// только показывает номер и строит ссылку из строго проверенного номера версии.
(function () {
  const re = /^\d{1,6}\.\d{1,6}\.\d{1,6}$/;
  fetch('/panel/latest.json', { cache: 'no-cache', credentials: 'omit' })
    .then((r) => (r.ok ? r.json() : Promise.reject(new Error('latest.json ' + r.status))))
    .then((env) => {
      const payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(env.payload), (c) => c.charCodeAt(0))));
      const v = String(payload.version || '');
      if (!re.test(v)) return;
      document.querySelectorAll('.version, .terminal-version').forEach((el) => { el.textContent = v; });
      document.querySelectorAll('.sidebar-caption').forEach((el) => {
        const t = el.firstChild;
        if (t && t.nodeType === 3) t.nodeValue = t.nodeValue.replace(/\d+\.\d+\.\d+/, v);
      });
      document.querySelectorAll('a[href*="itrupanel-full-"]').forEach((a) => {
        const href = a.getAttribute('href') || '';
        a.setAttribute('href', href.replace(/itrupanel-full-\d+\.\d+\.\d+\.zip/, 'itrupanel-full-' + v + '.zip'));
        const last = [...a.childNodes].reverse().find((n) => n.nodeType === 3 && /\d+\.\d+\.\d+/.test(n.nodeValue));
        if (last) last.nodeValue = last.nodeValue.replace(/\d+\.\d+\.\d+/, v);
      });
    })
    .catch(() => {});
  // FreeBSD обновляется отдельным каналом: ссылка на его архив берётся из latest-freebsd.json
  fetch('/panel/latest-freebsd.json', { cache: 'no-cache', credentials: 'omit' })
    .then((r) => (r.ok ? r.json() : Promise.reject(new Error('latest-freebsd.json ' + r.status))))
    .then((env) => {
      const payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(env.payload), (c) => c.charCodeAt(0))));
      const v = String(payload.version || '');
      if (!re.test(v)) return;
      document.querySelectorAll('a[href*="itrupanel-freebsd-"]').forEach((a) => {
        a.setAttribute('href', (a.getAttribute('href') || '').replace(/itrupanel-freebsd-\d+\.\d+\.\d+\.zip/, 'itrupanel-freebsd-' + v + '.zip'));
      });
    })
    .catch(() => {});
})();
