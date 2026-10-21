// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
const securityModes = [['off', 'Выключена'], ['monitor', 'Наблюдение'], ['enforce', 'Блокировка']];
function securityMode(mode) { return securityModes.find(x => x[0] === mode)?.[1] || mode; }
function securityForm(site) {
  const p = site.policy;
  openForm({ title: 'Защита · ' + site.domain, wide: true, fields: [
    { name: 'mode', label: 'Режим защиты', type: 'seg', value: p.mode, options: securityModes },
    { type: 'html', html: v => '<div class="sec-form-note">' + (v.mode === 'monitor' ? 'Запросы проходят без блокировки. Превышения лимитов и совпадения с чёрным списком попадают в журнал.' : v.mode === 'enforce' ? 'Nginx отклоняет превышения лимитов с кодом 429, адреса из чёрного списка — с кодом 403.' : 'Правила сохранены, но не применяются к запросам.') + '</div>' },
    { type: 'section', label: 'Лимиты на один IP' },
    { name: 'rate', label: 'Запросов в секунду', type: 'number', value: p.rate, required: true, hint: 'От 1 до 10000. Учитываются запросы, дошедшие до обработки Nginx, включая статику.' },
    { name: 'burst', label: 'Запас для короткого всплеска', type: 'number', value: p.burst, hint: 'От 0 до 10000 дополнительных запросов без задержки. Помогает загрузить страницу с несколькими ресурсами.' },
    { name: 'connections', label: 'Одновременных обрабатываемых запросов', type: 'number', value: p.connections, required: true, hint: 'От 1 до 10000. В HTTP/2 и HTTP/3 параллельные запросы учитываются отдельно.' },
    { type: 'section', label: 'Списки адресов' },
    { name: 'allow', label: 'Белый список', type: 'textarea', rows: 4, value: (p.allow || []).join('\n'), placeholder: '203.0.113.10\n2001:db8::/64', hint: 'IP или CIDR, по одному на строку. Белый список отменяет лимиты и имеет приоритет над чёрным.' },
    { name: 'deny', label: 'Чёрный список', type: 'textarea', rows: 4, value: (p.deny || []).join('\n'), placeholder: '198.51.100.0/24', hint: 'До 256 адресов или подсетей в каждом списке. Поддерживаются IPv4 и IPv6.' },
    { type: 'html', html: '<p class="hint">Проверки Let’s Encrypt исключены из правил. Если сайт работает через CDN, сначала настройте реальный IP посетителя только от доверенных прокси. Иначе лимиты будут общими для адреса прокси.</p>' },
  ], submit: async v => {
    await api('security.save', { domain: site.domain, policy: { mode: v.mode, rate: Number(v.rate), burst: Number(v.burst), connections: Number(v.connections), allow: v.allow.split(/[\s,;]+/).filter(Boolean), deny: v.deny.split(/[\s,;]+/).filter(Boolean), revision: p.revision } });
    toast(App.me.demo ? 'Настройки сохранены в демо-режиме' : 'Настройки проверены и применены', 'ok'); await route();
  } });
}
function securityReason(e) {
  if (e.listed === '1') return 'Чёрный список';
  if ((e.conn || '').includes('REJECTED')) return 'Лимит соединений';
  return 'Лимит запросов';
}
page('security', { title: 'Защита сайтов', async render(view) {
  const d = await api('security.list'), sites = d.sites || [], events = d.events || [];
  let tab = 'overview', selected = '', search = '';
  const blocked = events.filter(e => e.mode === 'enforce' && (e.listed === '1' || e.req === 'REJECTED' || e.conn === 'REJECTED')).length;
  const monitored = events.filter(e => e.mode === 'monitor').length;
  const active = sites.filter(s => s.policy.mode !== 'off' && !s.suspended).length;
  head(view, 'Защита сайтов', { sub: 'Контроль HTTP-запросов на уровне Nginx', actions: [{ t: 'Обновить', i: 'refresh', fn: route }] });
  const intro = document.createElement('section'); intro.className = 'sec-hero';
  intro.innerHTML = `<div class="sec-emblem">${icon('shield')}</div><div><div class="sec-eyebrow">NGINX · HTTP</div><h2>Меньше лишних запросов.<br>Больше ресурсов для посетителей.</h2><p>Управляйте лимитами и доступом к каждому сайту.<br>Начните с наблюдения, затем включите блокировку.</p></div><div class="sec-hero-side"><span class="sec-status-dot"></span><b>${active} / ${sites.length}</b><span>сайтов с включёнными правилами</span></div>`;
  view.append(intro);
  const info = document.createElement('div'); info.className = 'sec-context';
  info.innerHTML = d.demo ? '<span class="tag warn">Демо-режим</span> Настройки сохраняются локально. Системные команды не выполняются; события в журнале — примеры.' : 'Защита применяется к сайтам. Административная панель и защита интернет-канала настраиваются отдельно.';
  view.append(info);
  const metrics = document.createElement('div'); metrics.className = 'sec-metrics';
  metrics.innerHTML = [[active, 'Защищаемые сайты', 'Из ' + sites.length + ' сайтов', 'shield'], [blocked, 'Заблокировано', 'В выборке журнала', 'lock'], [monitored, 'Наблюдение', 'Без блокировки посетителей', 'eye'], [new Set(events.map(e => e.ip)).size, 'IP в событиях', 'В выборке журнала', 'globe']].map(([n,t,sub,i]) => `<div class="sec-metric"><div class="sec-metric-top">${h(t)}${icon(i)}</div><strong>${n}</strong><span>${h(sub)}</span></div>`).join('');
  view.append(metrics);
  const nav = document.createElement('div'); nav.className = 'sec-tabs';
  nav.innerHTML = [['overview','Обзор'],['sites','Настройки сайтов'],['events','Журнал событий']].map(([id,label]) => `<button class="${id === tab ? 'on' : ''}" data-sec-tab="${id}">${label}</button>`).join('');view.append(nav);
  const content = document.createElement('div');view.append(content);
  const renderSites = () => {
    const box = panel(content, 'Правила для сайтов'); box.replaceChildren();
    const list = document.createElement('div');box.append(list);
    table(list, { rows: sites, empty: { icon: 'globe', title: 'Сначала добавьте сайт', text: '<p>После создания сайта здесь появятся настройки его защиты.</p>' }, columns: [
      { t: 'Сайт', r: s => '<b>' + h(s.domain) + '</b>' + (s.suspended ? '<div class="hint">Сайт приостановлен</div>' : '') },
      { t: 'Режим', r: s => `<span class="tag ${s.policy.mode === 'enforce' ? 'ok' : s.policy.mode === 'monitor' ? 'warn' : ''}">${h(securityMode(s.policy.mode))}</span>` },
      { t: 'Запросы / сек', r: s => s.policy.rate + '<span class="muted"> + ' + s.policy.burst + '</span>' },
      { t: 'Соединения', r: s => s.policy.connections },
      { t: 'Списки IP', r: s => (s.policy.allow || []).length + ' / ' + (s.policy.deny || []).length },
    ], onRow: securityForm, actions: s => [{ t: 'Настроить защиту', label: 'Настроить', i: 'edit', fn: () => securityForm(s) }] });
  };
  const renderEvents = () => {
    const box = panel(content, 'Последние срабатывания'); box.replaceChildren();
    const filters = document.createElement('div');filters.className = 'sec-filters';
    filters.innerHTML = `<select aria-label="Сайт"><option value="">Все сайты</option>${sites.map(s => `<option value="${h(s.domain)}" ${s.domain === selected ? 'selected' : ''}>${h(s.domain)}</option>`).join('')}</select><input type="search" aria-label="Поиск по IP или пути" placeholder="Поиск по IP или пути" value="${h(search)}">`;
    box.append(filters);
    const list = document.createElement('div');box.append(list);
    const draw = () => table(list, { rows: events.filter(e => (!selected || e.domain === selected) && (!search || (e.ip + ' ' + e.uri).toLowerCase().includes(search.toLowerCase()))), empty: { icon: 'shield', title: 'Срабатываний пока нет', text: '<p>События появятся при превышении лимита или совпадении с чёрным списком.</p>' }, columns: [
      { t: 'Время', r: e => h(fmtDate(e.time)) }, { t: 'Сайт / запрос', r: e => `<b>${h(e.domain)}</b><div class="sec-uri" title="${h(e.uri)}">${h(e.method)} ${h(e.uri)}</div>` },
      { t: 'IP-адрес', r: e => `<span class="mono">${h(e.ip)}</span>` }, { t: 'Причина', r: e => h(securityReason(e)) },
      { t: 'Действие', r: e => `<span class="tag ${e.mode === 'monitor' ? 'warn' : 'err'}">${e.mode === 'monitor' ? 'Наблюдение' : 'Блокировка'}</span><div class="hint">HTTP ${e.status}</div>` },
    ] });
    $('select', filters).onchange = e => { selected = e.target.value;draw(); };
    $('input', filters).oninput = e => { search = e.target.value;draw(); };draw();
    const hint = document.createElement('p');hint.className = 'hint';hint.textContent = 'До 300 последних событий: не более 200 на сайт из последних 256 КБ текущего журнала. Записи могут появляться с задержкой до 5 секунд. Это выборка, а не полная статистика за сутки.';box.append(hint);
  };
  const draw = () => {
    content.replaceChildren();
    if (tab === 'overview') {
      const box = panel(content, 'Срабатывания за последние 60 минут');
      const now = Date.now(), buckets = Array.from({length: 12}, () => ({block:0, monitor:0}));
      events.forEach(e => {const i = Math.floor((new Date(e.time).getTime() - (now - 3600000)) / 300000);if (i >= 0 && i < 12) buckets[i][e.mode === 'monitor' ? 'monitor' : 'block']++;});
      const max = Math.max(1, ...buckets.map(b => b.block + b.monitor));
      box.innerHTML = `<div class="sec-legend"><span><i></i>Блокировка</span><span><i></i>Наблюдение</span><span class="muted">По выборке журнала · интервал 5 минут</span></div><div class="sec-chart">${buckets.map((b,i) => `<div class="sec-bar" title="${b.block + b.monitor}"><div class="sec-bar-fill" style="height:${(b.block+b.monitor)/max*100}%"><div style="flex:${b.monitor};background:var(--warn)"></div><div style="flex:${b.block};background:var(--accent)"></div></div><span>${new Date(now - (12-i)*300000).toLocaleTimeString(window.PanelLanguage?.locale,{hour:'2-digit',minute:'2-digit'})}</span></div>`).join('')}</div>`;
      renderSites();
      const note = panel(content);note.innerHTML = '<b>Что защищает этот модуль</b><p class="muted">Лимиты помогают снизить нагрузку от частых HTTP-запросов. Здесь пока нет анализа SQL-инъекций, XSS и геоблокировок. При переполнении канала нужна фильтрация у хостера.</p>';
    } else if (tab === 'sites') renderSites(); else renderEvents();
    if (d.warnings?.length) {const p = panel(content);p.textContent = d.warnings.join('\n');}
  };
  $$('button',nav).forEach(b => b.onclick = () => {tab = b.dataset.secTab;$$('button',nav).forEach(x => x.classList.toggle('on',x === b));draw();});draw();
} });
