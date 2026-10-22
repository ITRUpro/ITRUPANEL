// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Антивирус для сайтов: встроенная проверка и ClamAV, карантин и «Вылечить».
const avWeekdays = ['Воскресенье', 'Понедельник', 'Вторник', 'Среда', 'Четверг', 'Пятница', 'Суббота'];
const avActions = { restore: ['Восстановить из копии', 'ok'], clean: ['Вырезать вставку', 'acc'], quarantine: ['В карантин', 'warn'], manual: ['Проверить вручную', ''] };

function avScheduleText(d) {
  if (d.schedule === 'off') return 'Плановая проверка выключена';
  const at = String(d.hour).padStart(2, '0') + ':00';
  const day = window.PanelLanguage?.language === 'en' ? avWeekdays[d.weekday] : avWeekdays[d.weekday].toLowerCase();
  return d.schedule === 'daily' ? 'Каждый день в ' + at : 'Раз в неделю: ' + day + ', ' + at;
}

function avSettingsForm(d) {
  openForm({
    title: 'Настройки антивируса', ok: 'Сохранить',
    fields: [
      { name: 'schedule', label: 'Плановая проверка всех сайтов', type: 'seg', value: d.schedule, options: [['daily', 'Каждый день'], ['weekly', 'Раз в неделю'], ['off', 'Выключена']] },
      { name: 'weekday', label: 'День недели', type: 'select', value: String(d.weekday), options: avWeekdays.map((n, i) => [String(i), n]), show: (v) => v.schedule === 'weekly' },
      { name: 'hour', label: 'Время', type: 'select', value: String(d.hour), options: [...Array(24).keys()].map((x) => [String(x), String(x).padStart(2, '0') + ':00']), show: (v) => v.schedule !== 'off' },
      { name: 'clamav', label: 'Проверять и ClamAV, если он установлен', type: 'checkbox', value: d.clamav.enabled, hint: 'ClamAV ищет известные вирусы по базам. На время проверки ему нужно около 1,5 ГБ памяти; на серверах меньше 2 ГБ работает только встроенная проверка.' },
      { name: 'mirror', label: 'Зеркала баз ClamAV', value: d.clamav.mirror || '', placeholder: 'https://clamav-mirror.ru https://mirror.truenetwork.ru/clamav', hint: 'Необязательно, можно несколько через пробел. Официальный сервер баз отклоняет загрузку из России и некоторых других стран — тогда панель сама подключит проверенные зеркала: clamav-mirror.ru, mirror.truenetwork.ru и packages.microsoft.com. Базы подписаны ClamAV, подпись проверяется при загрузке.' },
    ],
    submit: async (v) => { await api('av.settings', { schedule: v.schedule, weekday: Number(v.weekday), hour: Number(v.hour), clamav: !!v.clamav, mirror: v.mirror }); toast('Сохранено', 'ok'); route(); },
  });
}

// site: домен, '*' — все сайты, массив — выбранные сайты.
async function avStartScan(site) {
  try { const r = await api('av.scan', Array.isArray(site) ? { sites: site } : { site }); watchJob(r.job); } catch (e) { fail(e); }
}

async function avCure(site, paths) {
  try {
    const r = await api('av.cure.plan', { site, paths: paths || [] });
    watchJob(r.job, { onComplete: (j) => { if (j.status === 'done' && j.result?.plan) avCurePlan(j.result); } });
  } catch (e) { fail(e); }
}

function avCurePlan(plan) {
  const items = plan.items || [];
  const fixable = items.filter((it) => it.action !== 'manual');
  const m = modal(`<div class="backup-settings-head"><h3>Лечение сайта · ${h(plan.site)}</h3><button class="btn" data-close>Закрыть</button></div>
    <p class="hint">Ничего ещё не изменено. Отметьте файлы и нажмите «Вылечить». Перед заменой текущая версия каждого файла сохраняется в карантин — её можно вернуть. Остальные файлы сайта не меняются.${plan.backups ? '' : ' <b>Резервных копий с файлами этого сайта нет:</b> восстановить чистые версии нельзя.'}</p>
    <div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all ${fixable.length ? 'checked' : 'disabled'} aria-label="Выбрать все"></th><th>Файл</th><th>Действие</th><th>Подробности</th></tr></thead><tbody>
    ${items.map((it, i) => `<tr><td class="chk">${it.action === 'manual' ? '' : `<input type="checkbox" data-i="${i}" checked aria-label="${h(it.path)}">`}</td><td><span class="mono">${h(it.path)}</span><br><small class="muted">${h(it.threat)}</small></td><td><span class="tag ${avActions[it.action][1]}">${h(avActions[it.action][0])}</span></td><td>${h(it.detail)}</td></tr>`).join('')}
    </tbody></table></div><div class="btns"><button class="btn" data-close>Отмена</button><button class="btn primary" data-apply ${fixable.length ? '' : 'disabled'}>${icon('shield')}Вылечить</button></div>`, { wide: true });
  const boxes = $$('[data-i]', m.el);
  $('[data-all]', m.el).onchange = (e) => boxes.forEach((b) => { b.checked = e.target.checked; });
  $('[data-apply]', m.el).onclick = async () => {
    const paths = boxes.filter((b) => b.checked).map((b) => items[b.dataset.i].path);
    if (!paths.length) { toast('Отметьте файлы', 'err'); return; }
    m.close();
    try { const r = await api('av.cure.apply', { plan: plan.plan, paths }); watchJob(r.job); } catch (e) { fail(e); }
  };
}

async function avFindings(site, unicode) {
  let d;
  try { d = await api('av.findings', { site }); } catch (e) { fail(e); return; }
  const scan = d.scan;
  const m = modal(`<div class="backup-settings-head"><h3>Результаты проверки · ${h(unicode || site)}</h3><button class="btn" data-close>Закрыть</button></div><div data-body></div>`, { wide: true });
  const body = $('[data-body]', m.el);
  if (!scan) { body.innerHTML = '<p class="muted">Сайт ещё не проверялся.</p>'; return; }
  const draw = () => {
    body.innerHTML = `<p class="muted">Проверено ${fmtDate(scan.finished)} · файлов: ${scan.files} · ${scan.engines.includes('clamav') ? 'встроенная проверка и ClamAV' : 'встроенная проверка'}${scan.more ? ` · показаны первые 500, ещё ${scan.more}` : ''}</p>${scan.error ? `<p class="err">${h(scan.error)}</p>` : ''}
      ${scan.findings.length ? `<div class="toolbar" style="margin:6px 0 12px"><button class="btn primary" data-cure>${icon('shield')}Вылечить сайт</button><span class="muted">План лечения покажется перед изменениями</span></div>` : ''}<div data-table></div>`;
    table($('[data-table]', body), {
      rows: scan.findings,
      empty: { icon: 'shield', title: 'Угроз не найдено' },
      columns: [
        { t: 'Файл', r: (f) => `<span class="mono">${h(f.path)}</span>${f.line ? `<small class="muted"> · строка ${f.line}</small>` : ''}${f.snippet ? `<pre class="av-snippet">${h(f.snippet)}</pre>` : ''}` },
        { t: 'Угроза', r: (f) => `<span class="tag ${f.severity === 'high' ? 'err' : 'warn'}">${f.severity === 'high' ? 'опасно' : 'подозрительно'}</span><br>${h(f.threat)}` },
        { t: 'Изменён', r: (f) => f.modified ? fmtDate(new Date(f.modified * 1000)) : '', cls: 'nowrap hide-sm' },
      ],
      actions: (f) => [
        { i: 'shield', t: 'Вылечить этот файл', fn: () => { m.close(); avCure(site, [f.path]); } },
        { i: 'folder', t: 'Открыть папку файла', fn: () => { m.close(); go('files', { path: d.root + '/' + f.path.split('/').slice(0, -1).join('/') }); } },
        { i: 'lock', t: 'В карантин', fn: async () => { if (!(await confirmBox('Перенести в карантин?', h(f.path) + ' будет убран с сайта. Вернуть можно в разделе «Карантин».', { ok: 'В карантин' }))) return; try { await api('av.quarantine', { site, path: f.path, threat: f.threat }); scan.findings = scan.findings.filter((x) => x.path !== f.path); toast('Файл в карантине', 'ok'); draw(); } catch (e) { fail(e); } } },
        { i: 'check', t: 'Не опасно — пропускать', fn: async () => { try { await api('av.ignore', { site, path: f.path, sha256: f.sha256 }); scan.findings = scan.findings.filter((x) => x.path !== f.path); toast('Файл больше не будет показываться, пока не изменится', 'ok'); draw(); } catch (e) { fail(e); } } },
      ],
    });
    const cure = $('[data-cure]', body);
    if (cure) cure.onclick = () => { m.close(); avCure(site); };
  };
  draw();
}

page('antivirus', {
  title: 'Антивирус',
  async render(view) {
    const d = await api('av.status');
    head(view, 'Антивирус', { sub: 'Проверка сайтов на вирусы, веб-шеллы и вредоносные вставки', actions: [{ t: 'Проверить все сайты', i: 'search', primary: true, fn: () => avStartScan('*') }, { t: 'Настройки', i: 'edit', fn: () => avSettingsForm(d) }] });
    let high = 0, medium = 0, scanned = 0;
    d.sites.forEach((s) => { if (s.scan) { scanned++; high += s.scan.high; medium += s.scan.medium + (s.scan.more || 0); } });
    const hero = document.createElement('section'); hero.className = 'sec-hero';
    hero.innerHTML = `<div class="sec-emblem">${icon('bug')}</div><div><div class="sec-eyebrow">ITRUPANEL · ${d.clamav.usable && d.clamav.enabled ? 'ВСТРОЕННАЯ ПРОВЕРКА + CLAMAV' : 'ВСТРОЕННАЯ ПРОВЕРКА'}</div><h2>${high ? 'Найдены опасные файлы.' : 'Сайты под присмотром.'}<br>${h(avScheduleText(d))}.</h2><p>Ищутся веб-шеллы, выполнение закодированного кода, вставки в PHP, JS и .htaccess, PHP-код в картинках.<br>«Вылечить» возвращает чистые версии файлов из резервных копий или вырезает вставку.</p></div>`;
    view.append(hero);
    const cards = document.createElement('div'); cards.className = 'sec-metrics'; cards.style.marginTop = '16px';
    cards.innerHTML = [['Проверено сайтов', scanned + ' из ' + d.sites.length, 'globe', d.last_run && new Date(d.last_run).getFullYear() > 2000 ? 'Последняя полная: ' + fmtDate(d.last_run) : 'Полной проверки ещё не было'], ['Опасные', high, 'bug', 'Веб-шеллы и выполнение кода'], ['Подозрительные', medium, 'search', 'Требуют внимания'], ['В карантине', d.quarantine.length, 'lock', 'Файлы можно вернуть']].map(([t, v, i, s]) => `<div class="sec-metric"><div class="sec-metric-top">${h(t)}${icon(i)}</div><strong>${h(v)}</strong><span>${h(s)}</span></div>`).join('');
    view.append(cards);

    const sitesPanel = panel(view, 'Сайты');
    const picked = new Set();
    const bar = document.createElement('div'); bar.className = 'toolbar'; bar.style.margin = '0 0 12px';
    bar.innerHTML = `<label class="check"><input type="checkbox" data-all-sites aria-label="Выбрать все сайты"><span>Выбрать все</span></label><button class="btn" data-scan-picked disabled>${icon('search')}Проверить выбранные</button><span class="muted" data-picked></span>`;
    const sites = document.createElement('div');
    sitesPanel.replaceChildren(bar, sites);
    const syncPicked = () => {
      $('[data-scan-picked]', bar).disabled = !picked.size;
      $('[data-picked]', bar).textContent = picked.size ? 'Выбрано: ' + picked.size : '';
      $('[data-all-sites]', bar).checked = picked.size > 0 && picked.size === d.sites.length;
    };
    $('[data-all-sites]', bar).onchange = (e) => { picked.clear(); if (e.target.checked) d.sites.forEach((s) => picked.add(s.domain)); $$('[data-site-pick]', sites).forEach((c) => { c.checked = e.target.checked; }); syncPicked(); };
    $('[data-scan-picked]', bar).onclick = () => avStartScan([...picked]);
    table(sites, {
      rows: d.sites,
      empty: { icon: 'globe', title: 'Сайтов пока нет' },
      columns: [
        { t: '', r: (s) => `<input type="checkbox" data-site-pick="${h(s.domain)}" aria-label="${h(s.unicode)}">`, cls: 'chk' },
        { t: 'Сайт', r: (s) => `<b>${h(s.unicode)}</b>` },
        { t: 'Последняя проверка', r: (s) => s.scan ? fmtDate(s.scan.finished) : '<span class="muted">не проверялся</span>', cls: 'nowrap' },
        { t: 'Файлов', r: (s) => s.scan ? h(s.scan.files) : '', cls: 'hide-sm' },
        { t: 'Результат', r: (s) => !s.scan ? '' : s.scan.high + s.scan.medium + (s.scan.more || 0) === 0 ? '<span class="tag ok">чисто</span>' : (s.scan.high ? `<span class="tag err">опасных: ${s.scan.high}</span> ` : '') + (s.scan.medium + (s.scan.more || 0) ? `<span class="tag warn">подозрительных: ${s.scan.medium + (s.scan.more || 0)}</span>` : '') },
      ],
      onRow: (s) => avFindings(s.domain, s.unicode),
      actions: (s) => [
        { i: 'search', t: 'Проверить', fn: () => avStartScan(s.domain) },
        s.scan && { i: 'file', t: 'Результаты', fn: () => avFindings(s.domain, s.unicode) },
        s.scan && s.scan.high + s.scan.medium > 0 && { i: 'shield', t: 'Вылечить', label: 'Вылечить', fn: () => avCure(s.domain) },
      ],
    });
    $$('[data-site-pick]', sites).forEach((c) => (c.onchange = () => { if (c.checked) picked.add(c.dataset.sitePick); else picked.delete(c.dataset.sitePick); syncPicked(); }));

    const rp = panel(view, 'Правила антивируса');
    const ru = d.rules;
    rp.innerHTML = `<p>${ru.version ? `Подписанный набор правил: версия ${h(ru.version)} от ${h(ru.released)}, правил: ${h(ru.pack)}.` : 'Дополнительный набор правил ещё не загружен, работают встроенные правила.'} Встроенных правил: ${h(ru.builtin)}.</p>
      <p class="muted">${ru.checked ? `Последняя проверка обновлений: ${fmtDate(ru.checked)}.` : 'Обновления ещё не проверялись.'} Источник: ${h(ru.source)} (резерв — itrupanel.com). Набор подписан ключом выпусков ITRUPANEL; панель принимает только подписанные и более новые наборы.</p>${ru.error ? `<p class="err">Последнее обновление не удалось: ${h(ru.error)}</p>` : ''}
      <div class="toolbar"><button class="btn" data-rules-update>${icon('refresh')}Обновить правила</button><label class="check"><input type="checkbox" data-rules-auto ${ru.auto ? 'checked' : ''}><span>Обновлять правила автоматически раз в сутки</span></label></div>
      <p class="hint">Правила обновляются отдельно от панели, без её перезапуска. Обновление баз, правил и проверки идут по очереди, а не одновременно, чтобы не нагружать слабый сервер.</p>`;
    $('[data-rules-update]', rp).onclick = async () => { try { const r = await api('av.rules.update'); watchJob(r.job); } catch (e) { fail(e); } };
    $('[data-rules-auto]', rp).onchange = async (e) => { try { await api('av.rules.settings', { auto: e.target.checked }); toast('Сохранено', 'ok'); } catch (er) { fail(er); e.target.checked = !e.target.checked; } };

    const clam = panel(view, 'ClamAV');
    const c = d.clamav;
    clam.innerHTML = `<p>${c.installed ? (c.db_date ? `Установлен. Базы сигнатур от ${fmtDate(c.db_date)}.` : 'Установлен, базы ещё не загружены.') : 'Не установлен. Встроенная проверка работает и без него.'}${c.installed && !c.enabled ? ' Сейчас выключен в настройках.' : ''}${c.installed && c.db_date && !c.usable ? ` <span class="muted">${h(c.reason)}.</span>` : ''}</p>
      ${c.installed ? `<p class="muted">Базы обновляются автоматически раз в сутки${c.db_checked ? `: последняя проверка ${fmtDate(c.db_checked)}, следующая — после ${fmtDate(c.db_next)}` : ''}.</p>` : ''}${c.installed && c.db_error ? `<p class="err">Последнее обновление не удалось: ${h(c.db_error)}</p>` : ''}
      <div class="toolbar">${c.installed ? `<button class="btn" data-update>${icon('refresh')}Обновить базы</button>` : `<button class="btn primary" data-install>${icon('download')}Установить ClamAV</button>`}</div>
      <p class="hint">ClamAV ищет известные вирусы по базам (около 120 МБ, обновляются автоматически). Если официальный сервер баз недоступен из страны сервера, панель сама переключится на зеркала.${c.mirror ? ' Сейчас используются: ' + h(c.mirror) + '.' : ''}</p>`;
    const install = $('[data-install]', clam), update = $('[data-update]', clam);
    if (install) install.onclick = async () => { if (!(await confirmBox('Установить ClamAV?', 'Будут установлены пакеты clamav и загружены базы сигнатур (около 120 МБ). Для проверки ClamAV нужно от 2 ГБ памяти.', { ok: 'Установить', danger: false }))) return; try { const r = await api('av.install'); watchJob(r.job); } catch (e) { fail(e); } };
    if (update) update.onclick = async () => { try { const r = await api('av.update'); watchJob(r.job); } catch (e) { fail(e); } };

    const q = panel(view, 'Карантин');
    table(q, {
      rows: d.quarantine,
      empty: { icon: 'lock', title: 'Карантин пуст' },
      columns: [
        { t: 'Файл', r: (x) => `<span class="mono">${h(x.path)}</span><br><small class="muted">${h(x.site)}</small>` },
        { t: 'Причина', r: (x) => (x.reason === 'cure' ? '<span class="tag">копия до лечения</span><br>' : '') + h(x.threat || '') },
        { t: 'Размер', r: (x) => fmtBytes(x.size), cls: 'nowrap hide-sm' },
        { t: 'Дата', r: (x) => fmtDate(x.time), cls: 'nowrap' },
      ],
      actions: (x) => [
        { i: 'refresh', t: 'Вернуть на сайт', fn: async () => { if (!(await confirmBox('Вернуть файл?', h(x.path) + ' вернётся на прежнее место' + (x.reason === 'cure' ? ' и заменит вылеченную версию' : '') + '.', { ok: 'Вернуть', danger: false }))) return; try { await api('av.restore', { id: x.id }); toast('Файл возвращён', 'ok'); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: 'Удалить навсегда', danger: true, fn: async () => { if (!(await confirmBox('Удалить файл навсегда?', h(x.path) + ' будет удалён из карантина без возможности вернуть.'))) return; try { await api('av.delete', { id: x.id }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (d.running) every(5000, () => { api('av.status').then((s) => { if (!s.running && view.isConnected) route(); }); }, view);
  },
});
