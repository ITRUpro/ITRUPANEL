// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// ITRUPANEL — ядро интерфейса: API, маршрутизация, меню, вкладки, общие компоненты.

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const h = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const ls = {
  get(k, d) { try { const v = localStorage.getItem('itrupanel.' + k); return v == null ? d : JSON.parse(v); } catch (e) { return d; } },
  set(k, v) { try { localStorage.setItem('itrupanel.' + k, JSON.stringify(v)); } catch (e) {} },
};

const App = { me: null, pages: {}, current: null, timers: [] };

// ---------- API ----------
async function api(name, data, options = {}) {
  const ctl = options.timeout ? new AbortController() : null;
  const timer = ctl ? setTimeout(() => ctl.abort(), options.timeout) : null;
  let r;
  try {
    r = await fetch('/api/' + name, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Itrupanel': '1' }, body: JSON.stringify(data || {}), credentials: 'same-origin', ...(ctl ? { signal: ctl.signal } : {}) });
  } catch (e) {
    throw new Error(e.name === 'AbortError' ? 'Сервер не ответил вовремя' : 'Нет связи с сервером');
  } finally { if (timer) clearTimeout(timer); }
  if (r.status === 401) { showLogin(); throw new Error('Требуется вход'); }
  const j = await r.json().catch(() => ({ error: 'Ошибка ответа сервера (' + r.status + ')' }));
  if (j.code === 'terms_required' && App.me && !App.me.terms_required) { App.me.terms_required = true; showTerms(); }
  if (j.error) throw new Error(j.error);
  return j.data;
}

// ---------- форматирование ----------
function fmtBytes(n, maxDigits) {
  n = Number(n) || 0;
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toLocaleString(window.PanelLanguage?.locale || 'ru-RU', {useGrouping:false, maximumFractionDigits:maxDigits ?? (i ? (n < 10 ? 1 : 0) : 2)}) + ' ' + u[i];
}
function fmtDate(s, withTime = true) {
  if (!s) return '—';
  const d = new Date(s);
  if (isNaN(d) || d.getFullYear() < 2000) return '—';
  if (window.PanelLanguage?.language === 'en') return new Intl.DateTimeFormat('en-GB', {dateStyle:'short', ...(withTime ? {timeStyle:'short'} : {})}).format(d);
  const p = (x) => String(x).padStart(2, '0');
  const date = `${p(d.getDate())}.${p(d.getMonth() + 1)}.${d.getFullYear()}`;
  return withTime ? `${date} ${p(d.getHours())}:${p(d.getMinutes())}` : date;
}
function fmtAgo(s) {
  const sec = (Date.now() - new Date(s)) / 1000;
  if (sec < 60) return 'только что';
  if (sec < 3600) return Math.floor(sec / 60) + ' мин назад';
  if (sec < 86400) return Math.floor(sec / 3600) + ' ч назад';
  return fmtDate(s);
}
function fmtDuration(sec) {
  const d = Math.floor(sec / 86400), hh = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return (d ? d + ' д ' : '') + hh + ' ч ' + m + ' мин';
}
function plural(n, a, b, c) {
  if (window.PanelLanguage?.language === 'en') return n + ' ' + (n === 1 ? a : b);
  const m10 = n % 10, m100 = n % 100;
  return n + ' ' + (m10 === 1 && m100 !== 11 ? a : m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20) ? b : c);
}

// ---------- уведомления, окна ----------
function toast(msg, kind) {
  let box = $('.toasts');
  if (!box) { box = document.createElement('div'); box.className = 'toasts'; document.body.append(box); }
  const t = document.createElement('div');
  t.className = 'toast ' + (kind || '');
  t.textContent = msg;
  box.append(t);
  setTimeout(() => t.remove(), kind === 'err' ? 9000 : 4000);
}
const fail = (e) => toast(e.message || String(e), 'err');

function modal(html, { wide } = {}) {
  const w = document.createElement('div');
  w.className = 'modal-wrap';
  w.innerHTML = `<div class="modal ${wide ? 'wide' : ''}">${html}</div>`;
  const close = () => { w.remove(); document.removeEventListener('keydown', esc); };
  const esc = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', esc);
  w.addEventListener('mousedown', (e) => { if (e.target === w) close(); });
  document.body.append(w);
  $$('[data-close]', w).forEach((b) => (b.onclick = close));
  return { el: w, close };
}

function confirmBox(title, text, { ok = 'Удалить', danger = true, extra = '' } = {}) {
  return new Promise((res) => {
    const m = modal(`<h3>${h(title)}</h3><p class="muted">${text}</p>${extra}<div class="btns"><button class="btn" data-close>Отмена</button><button class="btn ${danger ? 'primary' : 'primary'}" style="${danger ? 'background:var(--err);border-color:var(--err)' : ''}" data-ok>${h(ok)}</button></div>`);
    $('[data-ok]', m.el).onclick = () => {
      const vals = {};
      $$('input', m.el).forEach((i) => (vals[i.name] = i.type === 'checkbox' ? i.checked : i.value));
      m.close();
      res(vals);
    };
    $$('[data-close]', m.el).forEach((b) => b.addEventListener('click', () => res(null)));
  });
}

function showSecrets(title, items, note) {
  const rows = items.filter((x) => x[1]).map(([k, v]) => `<div class="field"><label>${h(k)}</label><div class="secret"><span>${h(v)}</span><button class="btn small icon-only" data-copy="${h(v)}" title="Скопировать">${icon('copy')}</button></div></div>`).join('');
  const m = modal(`<h3>${h(title)}</h3>${note ? `<p class="muted">${note}</p>` : ''}${rows}<div class="btns"><button class="btn primary" data-close>Готово</button></div>`);
  $$('[data-copy]', m.el).forEach((b) => (b.onclick = () => { navigator.clipboard?.writeText(b.dataset.copy); toast('Скопировано'); }));
}

function showOutput(title, text) {
  modal(`<h3>${h(title)}</h3><pre class="out">${h(text || '(пусто)')}</pre><div class="btns"><button class="btn primary" data-close>Закрыть</button></div>`, { wide: true });
}

// ---------- форма в выезжающей панели ----------
// fields: {name, label, type, value, options:[[v,t]], hint, show:(vals)=>bool, gen, placeholder, required}
function openForm({ title, fields, submit, ok = 'Сохранить', wide = false, onChange }) {
  const ov = document.createElement('div');
  ov.className = 'overlay';
  ov.innerHTML = `<div class="drawer ${wide ? 'wide' : ''}"><div class="drawer-head"><h2>${h(title)}</h2><button class="round-btn" data-x title="Закрыть">${icon('close')}</button></div><form class="drawer-body" autocomplete="off"></form><div class="drawer-foot"><button class="btn primary" data-ok>${h(ok)}</button><button class="btn" data-x>Отмена</button></div></div>`;
  const form = $('form', ov);
  const vals = {};
  fields.forEach((f) => { if (f.name) vals[f.name] = f.value ?? (f.type === 'checkbox' ? false : ''); });

  const render = () => {
    form.innerHTML = fields.map((f, i) => {
      if (f.show && !f.show(vals)) return '';
      const id = 'f' + i;
      const v = vals[f.name];
      const hint = f.hint ? `<div class="hint">${f.hint}</div>` : '';
      switch (f.type) {
        case 'section': return `<div class="section-title">${h(f.label)}</div>`;
        case 'html': return `<div class="field">${typeof f.html === 'function' ? f.html(vals) : f.html}</div>`;
        case 'checkbox': return `<div class="field"><label class="check"><input type="checkbox" name="${f.name}" ${v ? 'checked' : ''}><span>${h(f.label)}${f.hint ? `<small>${f.hint}</small>` : ''}</span></label></div>`;
        case 'select': return `<div class="field"><label for="${id}">${h(f.label)}</label><select id="${id}" name="${f.name}">${(typeof f.options === 'function' ? f.options(vals) : f.options).map(([ov, ot]) => `<option value="${h(ov)}" ${String(ov) === String(v) ? 'selected' : ''}>${h(ot)}</option>`).join('')}</select>${hint}</div>`;
        case 'seg': return `<div class="field"><label>${h(f.label)}</label><div class="seg" data-seg="${f.name}">${f.options.map(([ov, ot]) => `<button type="button" data-v="${h(ov)}" class="${String(ov) === String(v) ? 'on' : ''}">${h(ot)}</button>`).join('')}</div>${hint}</div>`;
        case 'textarea': return `<div class="field"><label for="${id}">${h(f.label)}</label><textarea id="${id}" name="${f.name}" rows="${f.rows || 6}" placeholder="${h(f.placeholder || '')}" spellcheck="false">${h(v)}</textarea>${hint}</div>`;
        default: {
          const t = f.type === 'password' ? 'text' : (f.type || 'text');
          const gen = f.gen ? `<button type="button" class="btn" data-gen="${f.name}" title="Сгенерировать">${icon('key')}</button>` : '';
          return `<div class="field"><label for="${id}">${h(f.label)}${f.required ? ' *' : ''}</label><div class="row"><input id="${id}" type="${t}" name="${f.name}" value="${h(v)}" placeholder="${h(f.placeholder || '')}" ${f.readonly ? 'readonly' : ''} spellcheck="false">${gen}</div>${hint}</div>`;
        }
      }
    }).join('');
    $$('[data-seg] button', form).forEach((b) => (b.onclick = () => { vals[b.parentNode.dataset.seg] = b.dataset.v; changed(); }));
    $$('[data-gen]', form).forEach((b) => (b.onclick = () => { vals[b.dataset.gen] = genPass(); render(); }));
  };
  const changed = () => { onChange && onChange(vals); render(); };
  form.addEventListener('input', (e) => {
    const t = e.target;
    if (!t.name) return;
    vals[t.name] = t.type === 'checkbox' ? t.checked : t.value;
    const f = fields.find((x) => x.name === t.name);
    if (t.type === 'checkbox' || t.tagName === 'SELECT' || (f && f.rerender)) { onChange && onChange(vals, t.name); const pos = t.selectionStart; render(); const n = form.querySelector(`[name="${t.name}"]`); if (n && n.type === 'text') { n.focus(); try { n.setSelectionRange(pos, pos); } catch (e) {} } }
  });
  form.addEventListener('submit', (e) => { e.preventDefault(); $('[data-ok]', ov).click(); });
  render();

  const close = () => { ov.remove(); document.removeEventListener('keydown', esc); };
  const esc = (e) => { if (e.key === 'Escape' && !$('.modal-wrap')) close(); };
  document.addEventListener('keydown', esc);
  $$('[data-x]', ov).forEach((b) => (b.onclick = close));
  ov.addEventListener('mousedown', (e) => { if (e.target === ov) close(); });
  const okBtn = $('[data-ok]', ov);
  okBtn.onclick = async () => {
    for (const f of fields) {
      if (f.required && (!f.show || f.show(vals)) && !String(vals[f.name] ?? '').trim()) { toast('Заполните поле «' + f.label + '»', 'err'); return; }
    }
    okBtn.disabled = true;
    const label = okBtn.innerHTML;
    okBtn.innerHTML = '<span class="spin" style="width:18px;height:18px;border-color:rgba(255,255,255,.4);border-top-color:#fff"></span> ' + label;
    try {
      const r = await submit({ ...vals });
      if (r !== false) close();
    } catch (e) { fail(e); } finally { okBtn.disabled = false; okBtn.innerHTML = label; }
  };
  document.body.append(ov);
  const first = $('input[type=text]', form);
  if (first && !first.value) first.focus();
  return { close, vals, render, el: ov };
}

function genPass(n = 16) {
  const a = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const b = new Uint32Array(n);
  crypto.getRandomValues(b);
  return [...b].map((x) => a[x % a.length]).join('');
}

// ---------- таблица ----------
// opts: {columns:[{t, r:(row)=>html, cls}], rows, actions:(row)=>[{i,t,fn,danger}], empty:{icon,title,text}, onRow}
function table(el, opts) {
  if (!opts.rows || !opts.rows.length) {
    const e = opts.empty || {};
    el.innerHTML = `<div class="empty"><div class="circle">${icon(e.icon || 'file')}</div><b>${h(e.title || 'Пока пусто')}</b>${e.text || ''}</div>`;
    return;
  }
  const cols = opts.columns;
  el.innerHTML = `<div class="table-wrap"><table class="grid"><thead><tr>${cols.map((c) => `<th class="${c.cls || ''}">${h(c.t)}</th>`).join('')}${opts.actions ? '<th></th>' : ''}</tr></thead><tbody>${opts.rows.map((r, i) => `<tr data-i="${i}">${cols.map((c) => `<td class="${c.cls || ''}">${c.r(r)}</td>`).join('')}${opts.actions ? `<td class="actions">${opts.actions(r).filter(Boolean).map((a, j) => `<button class="btn small ${a.label ? '' : 'icon-only'} ${a.danger ? 'danger' : ''}" data-a="${j}" title="${h(a.t)}">${icon(a.i)}${a.label ? h(a.label) : ''}</button>`).join('')}</td>` : ''}</tr>`).join('')}</tbody></table></div>`;
  $$('tbody tr', el).forEach((tr) => {
    const row = opts.rows[tr.dataset.i];
    if (opts.actions) {
      const acts = opts.actions(row).filter(Boolean);
      $$('[data-a]', tr).forEach((b) => (b.onclick = (e) => { e.stopPropagation(); acts[b.dataset.a].fn(row); }));
    }
    if (opts.onRow) { tr.style.cursor = 'pointer'; tr.onclick = (e) => { if (!e.target.closest('a,button,input')) opts.onRow(row); }; }
  });
}

// ---------- меню и вкладки ----------
const NAV = [
  ['dashboard', 'Дашборд', 'dashboard'],
  ['sites', 'Сайты', 'globe'],
  ['db', 'Базы данных', 'database'],
  ['dbservers', 'Серверы БД', 'server'],
  ['docker', 'Docker', 'docker'],
  ['mail', 'Почта', 'mail'],
  ['ssl', 'SSL-сертификаты', 'lock'],
  ['dns', 'Управление DNS', 'dns'],
  '-',
  ['cron', 'Планировщик CRON', 'cron'],
  ['backups', 'Резервные копии', 'backup'],
  ['files', 'Менеджер файлов', 'folder'],
  ['ips', 'IP-адреса', 'ip'],
  '-',
  ['ftp', 'FTP-пользователи', 'ftp'],
  ['import', 'Импорт данных', 'importuser'],
  '-',
  ['oplog', 'Журнал операций', 'oplog'],
  ['visits', 'Журнал посещений', 'visits'],
  ['wwwlogs', 'Журналы сайтов', 'wwwlog'],
  ['traffic', 'Использование трафика', 'traffic'],
  ['resources', 'Потребление ресурсов', 'resources'],
  '-',
  ['notices', 'Уведомления', 'bell'],
  ['disk', 'Использование диска', 'disk'],
  ['php', 'PHP', 'php'],
  ['webservers', 'Веб-серверы', 'server'],
  ['jobs', 'Задачи', 'clock'],
  ['firewall', 'Брандмауэр', 'shield'],
  ['security', 'Защита сайтов', 'shield'],
  ['authguard', 'Защита входа', 'auth'],
  ['antivirus', 'Антивирус', 'bug'],
  ['info', 'Информация о сервере', 'info'],
  ['shell', 'Shell-клиент', 'terminal'],
  ['feedback', 'Обратная связь', 'mail'],
];
const NAV_GROUPS = [
  {key:'hosting',title:'Хостинг',pages:['sites','ssl','db','mail','dns','files','ftp','backups','import']},
  {key:'server',title:'Сервер',pages:['info','php','webservers','dbservers','docker','cron','ips','shell']},
  {key:'security',title:'Безопасность',pages:['security','antivirus','authguard','firewall']},
  {key:'diagnostics',title:'Диагностика',pages:['oplog','visits','wwwlogs','traffic','resources','disk','jobs']}
];
function renderNavigation(query = '', revealActive = false) {
  const nav = $('.nav');
  if (!nav) return;
  const expanded = ls.get('nav-groups', {hosting:true});
  const link = n => `<a href="#/${n[0]}" data-k="${n[0]}" class="${n[3]==='soon'?'is-soon ':''}${App.current===n[0]?'active':''}">${icon(n[2])}<span>${h(n[1])}</span>${n[0]==='notices'?'<span class="badge" hidden></span>':''}</a>`;
  nav.innerHTML = link(NAV[0]) + NAV_GROUPS.map(group => {
    const rows = group.pages.map(key=>NAV.find(n=>n[0]===key)).filter(n=>n && allowedPage(n[0]) && (!query || n[1].toLowerCase().includes(query)));
    if (!rows.length) return '';
    const open = query || expanded[group.key] || (revealActive && rows.some(n=>n[0]===App.current));
    return `<section class="nav-group"><button class="nav-group-title" data-nav-group="${group.key}" aria-expanded="${!!open}" aria-controls="nav-${group.key}">${h(group.title)}<span aria-hidden="true">${open?'−':'+'}</span></button><div class="nav-group-items" id="nav-${group.key}" ${open?'':'hidden'}>${rows.map(link).join('')}</div></section>`;
  }).join('') + (allowedPage('feedback') ? `<div class="nav-feedback">${link(NAV.find(n=>n[0]==='feedback'))}</div>` : '');
  $$('[data-nav-group]',nav).forEach(button=>button.onclick=()=>{
    const open = button.getAttribute('aria-expanded') !== 'true';
    expanded[button.dataset.navGroup]=open; ls.set('nav-groups',expanded);
    button.setAttribute('aria-expanded',String(open));
    $('span',button).textContent=open?'−':'+';
    document.getElementById(button.getAttribute('aria-controls')).hidden=!open;
  });
}
// На FreeBSD нет Docker, а «Серверы БД» работают в контейнерах Docker.
const BSD_HIDDEN = ['docker', 'dbservers'];
const allowedPage = key => !NAV.some(n=>n[0]===key && n[3]==='soon') && !(App.me && App.me.family==='freebsd' && BSD_HIDDEN.includes(key));
const navTitle = (k) => (NAV.find((n) => n[0] === k) || [])[1] || (App.pages[k] && App.pages[k].title) || k;
const navIcon = (k) => (NAV.find((n) => n[0] === k) || [])[2] || 'file';

function page(key, def) { App.pages[key] = def; }

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, '');
  const [path, qs] = raw.split('?');
  const params = Object.fromEntries(new URLSearchParams(qs || ''));
  return { key: path || 'dashboard', params };
}
function go(key, params) {
  const qs = params ? '?' + new URLSearchParams(params) : '';
  const target = '#/' + key + qs;
  if (location.hash === target) route(); else location.hash = target;
}

function panelCopyright() {
  return '<a href="https://itrupanel.com/" target="_blank" rel="noopener noreferrer">© 2026 Oleg Sergeevich Dmitriev · ITRUPANEL</a>';
}

function renderShell() {
  const app = $('#app');
  const tabs = ls.get('tabs', []).filter(allowedPage);
  app.innerHTML = `
    ${App.me && App.me.demo ? '<div class="demo-flag">Демо-режим: команды на сервере не выполняются</div>' : ''}
    <div class="layout">
      <aside class="sidebar">
        <a class="logo" href="#/dashboard"><span class="logo-mark"><img src="/logo.png?v=0.1.35" width="29" height="29" alt="" aria-hidden="true"></span><span class="logo-text"><b>ITRU</b>PANEL</span></a>
        <div class="side-tools">
          <button class="round-btn" data-search title="Поиск по меню">${icon('search')}</button>
        </div>
        <div class="side-search"><input placeholder="Найти раздел…"></div>
        <nav class="nav" aria-label="Разделы панели"></nav>
        <div class="side-foot">
          <button data-theme-btn title="Тема">${icon('moon')}<span>Тема</span></button>
          <button data-passwd title="Сменить логин и пароль">${icon('key')}<span>Пароль</span></button>
          <button data-logout title="Выйти">${icon('logout')}<span>Выйти</span></button>
          <div style="flex-basis:100%">ITRUPANEL ${h(panelVersion(App.me?.version))} · ${h(App.me?.os || '')}</div>
          <div class="app-copyright">${panelCopyright()}</div>
        </div>
      </aside>
      <main class="main">
        <div class="mobile-bar"><button class="round-btn" data-menu>${icon('menu')}</button><a class="logo" href="#/dashboard"><span class="logo-mark"><img src="/logo.png?v=0.1.35" width="29" height="29" alt="" aria-hidden="true"></span><span class="logo-text"><b>ITRU</b>PANEL</span></a></div>
        <div class="panel-topbar"><span class="panel-context">Панель управления</span>${languageControl()}<a class="panel-bell" href="#/notices" data-k="notices" title="Уведомления" aria-label="Уведомления">${icon('bell')}<span class="badge" hidden></span></a><button class="panel-update-button" data-panel-updates>Обновления</button></div>
        <div class="tab-strip"><div class="tabs" aria-label="Открытые разделы"></div></div>
        <div id="view"></div>
        <footer class="app-copyright mobile-copyright">${panelCopyright()}</footer>
      </main>
    </div>`;
  renderNavigation('',true);
  const side = $('.sidebar');
  $('[data-menu]').onclick = () => side.classList.toggle('open');
  document.addEventListener('click', (e) => { if (side.classList.contains('open') && !e.target.closest('.sidebar,[data-menu]')) side.classList.remove('open'); });
  $('[data-search]').onclick = () => { const s = $('.side-search'); s.classList.toggle('open'); $('input', s).focus(); };
  $('.side-search input').oninput = (e) => {
    const q = e.target.value.toLowerCase().trim();
    renderNavigation(q,true);
  };
  $('.side-search input').onkeydown = (e) => { if (e.key === 'Enter') { const a = $$('.nav a').find((x) => !x.classList.contains('hidden')); if (a) location.hash = a.getAttribute('href'); } };
  $('[data-logout]').onclick = async () => { await fetch('/api/logout', { method: 'POST' }); location.hash = ''; location.reload(); };
  $('[data-passwd]').onclick = () => openForm({
    title: 'Логин и пароль панели', ok: 'Сменить',
    fields: [{ name: 'login', label: 'Логин', value: App.me?.user || '', hint: 'Латинские буквы, цифры, «_», «.» и «-», 3–32 символа' }, { name: 'old', label: 'Текущий пароль', type: 'password', required: true }, { name: 'new', label: 'Новый пароль', type: 'password', gen: true, hint: 'Не короче 8 символов; оставьте пустым, чтобы не менять пароль' }],
    submit: async (v) => { await api('settings.password', v); if (App.me && v.login) App.me.user = v.login; toast('Данные для входа изменены', 'ok'); },
  });
  $('[data-theme-btn]').onclick = () => {
    const cur = document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    const next = cur === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    ls.set('theme', next);
  };
  renderTabs(tabs);
}

function renderTabs() {
  const tabs = ls.get('tabs', []).filter(allowedPage);
  const cur = App.current;
  const el = $('.tabs');
  if (!el) return;
  el.innerHTML = `<a class="tab home ${cur === 'dashboard' ? 'active' : ''}" href="#/dashboard" title="Дашборд">${icon('dashboard')}</a>` +
    tabs.map((k) => `<a class="tab ${k === cur ? 'active' : ''}" href="#/${k}"><span>${h(navTitle(k))}</span><span class="x" data-close-tab="${k}">${icon('close')}</span></a>`).join('');
  $$('[data-close-tab]', el).forEach((x) => (x.onclick = (e) => {
    e.preventDefault(); e.stopPropagation();
    const k = x.dataset.closeTab;
    const list = ls.get('tabs', []).filter((t) => t !== k);
    ls.set('tabs', list);
    if (k === App.current) go(list[list.length - 1] || 'dashboard'); else renderTabs();
  }));
}

function clearTimers() { App.timers.forEach(clearInterval); App.timers = []; if (App.cleanup) { App.cleanup(); App.cleanup = null; } }
function every(ms, fn, view) {
  if (!view.isConnected) return;
  App.timers.push(setInterval(() => { if (view.isConnected) fn(); }, ms));
}

async function route() {
  if (!App.me || App.me.terms_required) return;
  const { key, params } = parseHash();
  // одноразовые параметры (?new=1 и т. п.) убираем из адреса, чтобы форма не открывалась повторно
  const once = ['new', 'le', 'self', 'import', 'domain'];
  if (once.some((k) => k in params)) {
    const rest = Object.fromEntries(Object.entries(params).filter(([k]) => !once.includes(k)));
    const qs = new URLSearchParams(rest).toString();
    history.replaceState(null, '', '#/' + key + (qs ? '?' + qs : ''));
  }
  const k = allowedPage(key) && App.pages[key] ? key : 'dashboard';
  const def = App.pages[k];
  clearTimers();
  $$('.overlay,.modal-wrap').forEach((x) => x.remove());
  App.current = k;
  if (k !== 'dashboard') {
    const tabs = ls.get('tabs', []).filter(allowedPage);
    if (!tabs.includes(k)) { tabs.push(k); if (tabs.length > 8) tabs.shift(); ls.set('tabs', tabs); }
  }
  renderTabs();
  renderNavigation($('.side-search input')?.value.toLowerCase().trim() || '',true);
  $('.sidebar')?.classList.remove('open');
  // Each navigation owns its DOM node. Late responses can only update the
  // detached previous page, never append content to the current page.
  const previousView = $('#view');
  const view = document.createElement('div');
  view.id = 'view';
  previousView.replaceWith(view);
  window.scrollTo(0, 0);
  try { await def.render(view, params); } catch (e) { if (view.isConnected) fail(e); }
  if (view.isConnected) refreshBadge();
}

// Шапка страницы: заголовок, подзаголовок и кнопки действий.
function head(view, title, { sub, actions } = {}) {
  const el = document.createElement('div');
  el.className = 'head-card';
  el.innerHTML = `<h1>${h(title)}</h1><div class="head-actions"></div>${sub ? `<div class="sub">${sub}</div>` : ''}`;
  (actions || []).filter(Boolean).forEach((a) => {
    const b = document.createElement(a.href ? 'a' : 'button');
    b.className = 'btn ' + (a.primary ? 'primary' : '');
    b.innerHTML = (a.i ? icon(a.i) : '') + h(a.t);
    if (a.href) { b.href = a.href; b.target = '_blank'; } else b.onclick = a.fn;
    $('.head-actions', el).append(b);
  });
  view.append(el);
  return el;
}
function panel(view, title, cls = '') {
  const el = document.createElement('div');
  el.className = 'panel ' + cls;
  el.innerHTML = (title ? `<h3>${h(title)}</h3>` : '') + '<div class="pbody"><div class="loading"><span class="spin"></span></div></div>';
  view.append(el);
  return $('.pbody', el);
}

async function refreshBadge() {
  try {
    const me = await api('me');
    $$('.nav .badge, .panel-bell .badge').forEach((b) => { b.hidden = !me.unread; b.textContent = me.unread > 99 ? '99+' : me.unread; });
    const bell = $('.panel-bell');
    if (bell) bell.setAttribute('aria-label', me.unread ? 'Уведомления: непрочитанных ' + me.unread : 'Уведомления');
  } catch (e) {}
}

// ---------- вход ----------
function showLogin() {
  if (typeof stopPanelUpdates === "function") stopPanelUpdates();
  App.me = null;
  clearTimers();
  $('#app').innerHTML = `<div class="login"><form class="login-card">
    <div class="logo"><span class="logo-mark"><img src="/logo.png?v=0.1.35" width="29" height="29" alt="" aria-hidden="true"></span><span class="logo-text"><b>ITRU</b>PANEL</span></div>
    <div class="login-language">${languageControl()}</div>
    <div class="field"><label>Логин</label><input type="text" name="user" value="" autocomplete="username" autocapitalize="none" spellcheck="false"></div>
    <div class="field"><label>Пароль</label><input type="password" name="password" autocomplete="current-password" autofocus></div>
    <div class="field" data-code-field style="display:none"><label>Код подтверждения</label><input type="text" name="code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="none" spellcheck="false" maxlength="24" placeholder="123456 или резервный код"></div>
    <p class="err"></p>
    <button class="btn primary">Войти</button>
    <div class="app-copyright login-copyright">${panelCopyright()}</div></form></div>`;
  const f = $('.login-card');
  $('[name=password]', f).focus();
  f.onsubmit = async (e) => {
    e.preventDefault();
    const btn = $('button', f);
    btn.disabled = true;
    const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ user: f.user.value, password: f.password.value, code: f.code.value }) }).then((r) => r.json()).catch(() => ({ error: 'Нет связи с сервером' }));
    btn.disabled = false;
    if (r.error) { $('.err', f).textContent = r.error; return; }
    if (r.need_code) { $('[data-code-field]', f).style.display = ''; $('.err', f).textContent = ''; f.code.focus(); return; }
    resetUpdateOffer();
    start();
  };
}

// Mandatory first-login step. No background shell or dismissible overlay.
async function showTerms() {
  if (typeof stopPanelUpdates === 'function') stopPanelUpdates();
  clearTimers();
  $$('.overlay,.modal-wrap').forEach((el) => el.remove());
  $('#app').innerHTML = `<div class="login terms-screen"><section class="terms-card" role="dialog" aria-modal="true" aria-labelledby="terms-title">
    <div class="terms-heading"><div class="logo-text"><b>ITRU</b>PANEL</div>${languageControl()}</div>
    <h1 id="terms-title">УСЛОВИЯ ИСПОЛЬЗОВАНИЯ</h1>
    <p class="muted">Перед началом работы прочитайте условия и подтвердите согласие.</p>
    <pre class="terms-document" tabindex="0" aria-label="Текст условий использования">Загрузка условий…</pre>
    <form><label class="check terms-agreement"><input type="checkbox" name="agree" required disabled><span>Я прочитал и принимаю условия использования</span></label>
    <p class="err" role="alert"></p><div class="btns"><button type="submit" class="btn primary" data-accept disabled>Принять и открыть панель</button><button type="button" class="btn" data-retry hidden>Повторить</button><button type="button" class="btn" data-exit>Выйти</button></div></form>
    <div class="app-copyright">${panelCopyright()}</div></section></div>`;
  const card = $('.terms-card'), form = $('form', card), check = $('[name=agree]', card), button = $('[data-accept]', card), error = $('.err', card), retry = $('[data-retry]', card);
  let doc = null, saving = false;
  const load = async () => {
    retry.hidden = true; error.textContent = ''; doc = null; check.checked = false; check.disabled = true; button.disabled = true;
    try {
      const result = await api('terms.get', {}, {timeout:15000});
      if (!card.isConnected) return;
      doc = result; $('.terms-document', card).textContent = doc.text; check.disabled = false;
    } catch (e) { if (card.isConnected) { error.textContent = e.message; retry.hidden = false; } }
  };
  check.onchange = () => { button.disabled = !doc || !check.checked || saving; };
  retry.onclick = load;
  $('[data-exit]', card).onclick = async () => {
    try { await api('logout'); showLogin(); } catch (e) { if (card.isConnected) error.textContent = e.message; }
  };
  form.onsubmit = async (e) => {
    e.preventDefault();
    if (!doc || !check.checked || saving) return;
    saving = true; button.disabled = true; check.disabled = true; error.textContent = '';
    try {
      await api('terms.accept', {agree:true, revision:doc.revision, sha256:doc.sha256}, {timeout:15000});
      if (card.isConnected) await start();
    } catch (e) { if (card.isConnected) error.textContent = e.message; }
    finally { saving = false; if (card.isConnected) { check.disabled = false; button.disabled = !check.checked; } }
  };
  await load();
}

async function start() {
  const th = ls.get('theme', null);
  if (th) document.documentElement.dataset.theme = th;
  try { App.me = await api('me'); } catch (e) { if (!App.me) { showLogin(); } return; }
  if (App.me.terms_required) { await showTerms(); return; }
  renderShell();
  await route();
  if (App.me && !App.me.terms_required && typeof startPanelUpdates === "function") startPanelUpdates();
}

window.addEventListener('hashchange', route);
if (!window.ITRUPANEL_BOOTSTRAP) document.addEventListener('DOMContentLoaded', start);

// Короткая версия СУБД: «11.8.6-MariaDB-5ubuntu0.1 from Ubuntu» → «11.8.6»
const dbShortVersion=(v)=>String(v||'').trim().split(/[-\s]/)[0];

// Всплывающее меню (правая кнопка мыши, кнопка «⋯»). items: {t, i, fn, danger} или '-'.
function contextMenu(items, x, y) {
  $$('.ctx-menu').forEach((e) => e.remove());
  const menu = document.createElement('div');
  menu.className = 'ctx-menu';
  menu.setAttribute('role', 'menu');
  items.forEach((it, k) => {
    if (it === '-') { if (k && items[k - 1] !== '-' && k < items.length - 1) menu.insertAdjacentHTML('beforeend', '<hr>'); return; }
    const b = document.createElement('button');
    b.type = 'button'; b.setAttribute('role', 'menuitem'); if (it.danger) b.className = 'danger';
    b.innerHTML = icon(it.i || 'file') + '<span>' + h(it.t) + '</span>';
    b.onclick = () => { close(); it.fn(); };
    menu.append(b);
  });
  document.body.append(menu);
  const w = menu.offsetWidth, hgt = menu.offsetHeight;
  menu.style.left = Math.max(8, Math.min(x, innerWidth - w - 8)) + 'px';
  menu.style.top = Math.max(8, y + hgt > innerHeight - 8 ? y - hgt : y) + 'px';
  const close = () => { menu.remove(); document.removeEventListener('mousedown', outside, true); document.removeEventListener('keydown', esc); window.removeEventListener('scroll', close, true); };
  const outside = (e) => { if (!menu.contains(e.target)) close(); };
  const esc = (e) => { if (e.key === 'Escape') close(); };
  setTimeout(() => { document.addEventListener('mousedown', outside, true); document.addEventListener('keydown', esc); window.addEventListener('scroll', close, true); });
  menu.querySelector('button')?.focus();
}

async function copyText(text, done = 'Скопировано') {
  try { await navigator.clipboard.writeText(text); }
  catch { const t = document.createElement('textarea'); t.value = text; document.body.append(t); t.select(); document.execCommand('copy'); t.remove(); }
  toast(done, 'ok');
}

