// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Веб-серверы и PHP: версии, проверка обновлений, обновление и выбор источника пакетов.
const WS_SOURCE = { system: 'Пакеты системы', stable: 'nginx.org · stable', mainline: 'nginx.org · mainline', ondrej: 'Ondřej Surý', litespeed: 'LiteSpeed', remi: 'Remi' };

async function wsUpdate(r, after) {
  if (!(await confirmBox('Обновить ' + r.title + '?', 'Будет установлена версия ' + r.new + ' из текущего источника. Настройки сохраняются в копию и возвращаются, если обновление не удастся. Сайты могут кратко не отвечать во время перезапуска.', { ok: 'Обновить', danger: false }))) return;
  try { const j = await api('components.update', { id: r.id }); watchJob(j.job, { onComplete: () => after && after() }); } catch (e) { fail(e); }
}

async function wsChannels(r, after) {
  let d;
  try { d = await api('components.channels', { id: r.id }); } catch (e) { fail(e); return; }
  const m = modal(`<h3>${h(r.title)}: источник пакетов</h3><p class="hint">Выберите, откуда брать версии. Панель сохранит настройки, подключит источник, установит версию, проверит конфигурацию и перезапустит службу; при ошибке всё вернётся.</p><div data-ch></div><div class="btns"><button class="btn" data-close>Закрыть</button></div>`, { wide: true });
  const box = $('[data-ch]', m.el);
  box.innerHTML = d.channels.map((c, i) => `<div class="card" style="display:flex;gap:12px;align-items:center;justify-content:space-between;margin:8px 0;padding:12px 14px"><div><b>${h(c.title)}</b> ${c.current ? '<span class="tag ok">сейчас</span>' : ''}<br><small class="muted">${c.version ? 'Последняя версия: ' + h(c.version) : (c.note ? h(c.note) : 'Версию определить не удалось')}</small></div>${c.current ? '' : `<button class="btn primary" data-i="${i}">Перейти</button>`}</div>`).join('');
  $$('[data-i]', box).forEach((b) => b.onclick = async () => {
    const c = d.channels[b.dataset.i];
    if (!(await confirmBox('Перейти на «' + c.title + '»?', 'Версия ' + r.title + ' изменится' + (c.version ? ' на ' + c.version : '') + '. Настройки сохраняются, при ошибке источник и версия возвращаются.', { ok: 'Перейти', danger: false }))) return;
    try { const j = await api('components.switch', { id: r.id, channel: c.id }); m.close && m.close(); watchJob(j.job, { onComplete: () => after && after() }); } catch (e) { fail(e); }
  });
}

page('webservers', { title: 'Веб-серверы', async render(view) {
  head(view, 'Веб-серверы', { sub: 'Версии nginx, Apache, OpenLiteSpeed и PHP, проверка и установка обновлений', actions: [{ t: 'Проверить обновления', i: 'refresh', primary: true, fn: async () => { try { const j = await api('components.check'); watchJob(j.job, { onComplete: () => load(true) }); } catch (e) { fail(e); } } }] });
  const p = panel(view);
  const load = async (force) => {
    const d = await api('components.list', { force: !!force });
    table(p, {
      rows: d.rows,
      columns: [
        { t: 'Компонент', r: (r) => `<b style="font-size:16px">${h(r.title)}</b>` },
        { t: 'Версия', r: (r) => r.version ? h(r.version) : '<span class="muted">—</span>', cls: 'nowrap' },
        { t: 'Источник', r: (r) => `<span class="tag">${h(WS_SOURCE[r.source] || r.source)}</span>` },
        { t: 'Состояние', r: (r) => r.job === 'running' ? '<span class="tag acc"><span class="spin" style="width:12px;height:12px;border-width:2px"></span> обновляется</span>' : r.active ? '<span class="tag ok">работает</span>' : '<span class="tag warn">остановлен</span>' },
        { t: 'Обновление', r: (r) => r.new ? `<span class="tag acc">доступна ${h(r.new)}</span>` : '<span class="muted">актуальная версия</span>' },
      ],
      actions: (r) => r.job === 'running' ? [] : [
        r.new && { i: 'download', t: 'Обновить', label: 'Обновить', fn: () => wsUpdate(r, () => load(true)) },
        r.switch && { i: 'edit', t: 'Источник пакетов и версия', label: 'Источник', fn: () => wsChannels(r, () => load(true)) },
      ],
    });
    return d.rows.some((r) => r.job === 'running') || d.checking;
  };
  await load();
  every(5000, async () => { if ($$('.spin', p).length) load(true); }, view);
} });
