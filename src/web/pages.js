// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// ITRUPANEL — страницы разделов.

const soon = () => toast('Раздел появится во втором этапе');

// ================= Дашборд =================
function dashboardGauge(title, value, detail, note, routeKey, tip = '') {
  const known = value != null && Number.isFinite(value);
  const percent = known ? Math.max(0, value) : 0;
  const tone = percent >= 90 ? 'err' : percent >= 75 ? 'warn' : '';
  return `<a class="server-metric" href="#/${routeKey}" title="${h(tip)}"><div class="metric-ring ${tone}" role="img" aria-label="${h(title)}: ${known ? Math.round(percent) + '%' : 'нет данных'}"><svg viewBox="0 0 100 100" aria-hidden="true"><circle class="ring-track" cx="50" cy="50" r="42"/><circle class="ring-value" cx="50" cy="50" r="42" pathLength="100" stroke-dasharray="${Math.min(percent, 100)} 100"/></svg><strong>${known ? Math.round(percent) + '<small>%</small>' : '—'}</strong></div><div class="metric-title">${h(title)}</div><div class="metric-detail">${detail}</div><div class="metric-note">${h(note)}</div></a>`;
}
function dashboardMarkup(d) {
  const pct = (used, total) => total > 0 ? 100 * used / total : null;
  const bytes = (used, total) => total > 0 ? `<b>${fmtBytes(used)}</b><span> / ${fmtBytes(total)}</span>` : 'Нет данных';
  const loads = (d.load || []).map((n) => Number(n).toFixed(2)).join(' / ');
  const summary = (title, ic, value, text, key) => `<a class="server-summary" href="#/${key}"><div>${h(title)}${icon(ic)}</div><strong>${h(value)}</strong><small>${h(text)}</small></a>`;
  return `<div class="server-metrics">${[
    dashboardGauge('Нагрузка', d.load?.length && d.cores > 0 ? d.load[0] / d.cores * 100 : null, h(loads || 'Нет данных'), 'За 1 / 5 / 15 минут', 'resources', 'Средняя нагрузка за минуту / число логических ядер × 100. Может превышать 100%.'),
    dashboardGauge('CPU', d.cpu, h(plural(d.cores, 'логическое ядро', 'логических ядра', 'логических ядер')) + (d.cpu_temp != null && Number.isFinite(d.cpu_temp) ? ` · <span class="metric-temp ${d.cpu_temp >= 90 ? 'err' : d.cpu_temp >= 80 ? 'warn' : ''}" title="Температура процессора">${Math.round(d.cpu_temp)} °C</span>` : ''), 'Использование процессора', 'resources'),
    dashboardGauge('RAM', pct(d.mem_used, d.mem_total), bytes(d.mem_used, d.mem_total), 'Использовано / всего', 'resources'),
    dashboardGauge('Диск /', pct(d.disk_used, d.disk_total), bytes(d.disk_used, d.disk_total), 'Использовано / всего', 'disk'),
  ].join('')}</div><section class="server-overview" aria-label="Обзор сервера"><div class="server-summaries">${[
    summary('Сайты', 'globe', d.sites, `Включено: ${d.enabled} · Отключено: ${d.suspended}`, 'sites'),
    summary('FTP', 'ftp', d.ftp, 'Учётные записи', 'ftp'),
    summary('Базы данных', 'database', d.dbs, 'Создано в панели', 'db'),
    summary('Предупреждения', 'bell', d.warnings, 'Непрочитанные предупреждения и ошибки', 'notices'),
  ].join('')}</div></section>`;
}

function updateDashboard(container, data) {
  const html = dashboardMarkup(data);
  if (!$('.server-metrics', container)) { container.innerHTML = html; return; }
  // Keep cards and links in place while live values change.
  const next = document.createElement('div');
  next.innerHTML = html;
  for (const selector of ['.metric-ring strong', '.metric-detail', '.server-summary strong', '.server-summary small']) {
    const values = $$(selector, next);
    $$(selector, container).forEach((node, i) => { if (node.innerHTML !== values[i].innerHTML) node.innerHTML = values[i].innerHTML; });
  }
  const rings = $$('.metric-ring', next), strokes = $$('.ring-value', next);
  $$('.metric-ring', container).forEach((node, i) => {
    node.className = rings[i].className;
    node.setAttribute('aria-label', rings[i].getAttribute('aria-label'));
  });
  $$('.ring-value', container).forEach((node, i) => node.setAttribute('stroke-dasharray', strokes[i].getAttribute('stroke-dasharray')));
}

function dashboardServiceState(state) {
  return ({running: ['Работает', 'ok'], stopped: ['Остановлен', 'err'], failed: ['Ошибка', 'err'], missing: ['Не установлен', 'off'], masked: ['Заблокирован', 'err'], changing: ['Переходное состояние', 'warn'], unknown: ['Нет данных', 'warn']})[state] || ['Нет данных', 'warn'];
}
function dashboardServiceTitle(s) {
  const titles = {'nginx':'Веб-сервер nginx','mariadb':'СУБД MariaDB','pure-ftpd':'FTP-сервер','cron':'Планировщик cron','crond':'Планировщик cron','named':'DNS-сервер BIND','rspamd':'Антиспам · Rspamd'};
  return titles[s.name] || s.title;
}
function dashboardNeedsAttention(service) { return !['running', 'missing'].includes(service.state); }
function dashboardMonitorMarkup(d, attention = false) {
  const services = d.services || [], count = services.filter(dashboardNeedsAttention).length;
  const rows = services.filter(s => !attention || dashboardNeedsAttention(s));
  const kv = (name, value) => `<div><dt>${h(name)}</dt><dd>${h(value || '—')}</dd></div>`;
  return `<div class="monitor-layout"><section class="monitor-card monitor-services"><div class="section-head"><div><h2>${icon('resources')}Мониторинг сервисов</h2><p>Работают: ${services.filter(s => s.state === 'running').length} · Требуют внимания: ${count}</p></div></div>
    <div class="service-toolbar"><div class="filters"><button data-monitor-filter="all" class="${!attention ? 'chosen' : ''}" aria-pressed="${!attention}">Все <span>${services.length}</span></button><button data-monitor-filter="attention" class="${attention ? 'chosen' : ''}" aria-pressed="${attention}">Требуют внимания <span>${count}</span></button></div></div>
    <div class="service-table"><table><thead><tr><th>Сервис</th><th>Состояние</th><th class="memory">Память</th></tr></thead><tbody>${rows.map(s => { const [label,tone] = dashboardServiceState(s.state); return `<tr><td><span class="service-name">${h(dashboardServiceTitle(s))}</span><small>${h(s.name)}</small></td><td><span class="state ${tone}"><i></i>${h(label)}</span></td><td class="memory">${s.memory == null ? '—' : fmtBytes(s.memory)}</td></tr>`; }).join('') || '<tr><td colspan="3" class="muted">Нет сервисов, требующих внимания</td></tr>'}</tbody></table></div>
    <div class="card-bottom"><span>Состояния systemd · каждые 10 секунд<br>${h(new Date(d.updated).toLocaleTimeString(window.PanelLanguage?.locale || 'ru-RU'))}</span><a href="#/info">Все сервисы →</a></div></section>
    <section class="monitor-card server-info"><div class="section-head"><h2>${icon('server')}Данные о сервере</h2></div><dl>${[
      kv('Имя сервера',d.hostname),kv('IP-адреса сервера',(d.ips || []).join(', ')),kv('Операционная система',d.os),kv('Ядро',`${d.kernel || '—'} · ${d.arch}`),kv('Процессор',`${d.cpu || d.arch} · ${d.cores}`),kv('Память',`${fmtBytes(d.mem_used)} / ${fmtBytes(d.mem_total)}`),kv('Диск /',`${fmtBytes(d.disk_used)} / ${fmtBytes(d.disk_total)}`),kv('Работает без перезагрузки',fmtDuration(d.uptime)),kv('Часовой пояс',`${d.timezone} (UTC${d.utc_offset >= 0 ? '+' : '−'}${Math.abs(d.utc_offset)/3600})`)
    ].join('')}</dl><div class="server-identity">${icon('server')}<div><b>ITRUPANEL ${h(panelVersion(d.version))}</b><span>${h(location.host)}</span></div></div><div class="card-bottom"><span>Данные текущего сервера</span><a href="#/info">Подробнее →</a></div></section></div>`;
}

// The server information card defines the height; show only complete service rows.
function fitDashboardServices(container) {
  const info = $('.server-info', container), card = $('.monitor-services', container);
  if (!info || !card) return;
  card.style.height = `${info.getBoundingClientRect().height}px`;
  const table = $('.service-table', card), rows = $$('tbody tr', table);
  rows.forEach(row => { row.hidden = false; });
  const bottom = table.getBoundingClientRect().bottom;
  const visible = rows.map(row => row.getBoundingClientRect().bottom <= bottom + 0.5);
  rows.forEach((row, i) => { row.hidden = !visible[i]; });
}

function dashboardIORate(previous, current, elapsed) {
  if (!previous || !current || !(elapsed > 0 && elapsed <= 10) || current.in < previous.in || current.out < previous.out) return null;
  return { incoming: (current.in - previous.in) / elapsed, outgoing: (current.out - previous.out) / elapsed };
}
function createDashboardIO(view) {
  const card = document.createElement('section');
  card.className = 'dashboard-io monitor-card';
  card.innerHTML = `<div class="section-head"><div class="filters"><button data-io-tab="network" class="chosen" aria-pressed="true">Трафик</button><button data-io-tab="disks" aria-pressed="false">Диск IO</button></div><div class="io-controls"><label><span data-io-label>Сеть</span>: <select data-io-device aria-label="Интерфейс или диск"></select></label><button class="btn icon-only" data-io-expand title="Развернуть график" aria-label="Развернуть график" aria-pressed="false">${icon('expand')}</button></div></div><div class="io-values"></div><div class="io-chart" role="img" aria-label="График скорости передачи данных"></div><div class="card-bottom"><span data-io-status role="status">Ожидание измерений…</span><span data-io-note></span></div>`;
  view.append(card);
  let mode = 'network', device = 'all', previous = null, latest = null, points = [], rate = null;
  const select = $('[data-io-device]', card);
  function counters(data) {
    const values = data?.[mode] || {};
    if (device !== 'all') return values[device] || null;
    const rows = Object.values(values);
    return rows.length ? rows.reduce((sum, item) => ({in:sum.in + item.in,out:sum.out + item.out}), {in:0,out:0}) : null;
  }
  function devices(data) {
    const names = Object.keys(data?.[mode] || {}).sort();
    const options = (mode === 'network' && names.length ? [['all','Все интерфейсы']] : []).concat(names.map(n => [n,n]));
    if (!options.some(([n]) => n === device)) { device = options[0]?.[0] || ''; previous = null; points = []; rate = null; }
    const html = options.map(([n,title]) => `<option value="${h(n)}">${h(title)}</option>`).join('');
    if (select.innerHTML !== html) select.innerHTML = html;
    select.value = device;
    select.disabled = !options.length;
  }
  function plot() {
    const el = $('.io-chart', card), W = Math.max(640, el.clientWidth || 900), H = card.classList.contains('io-expanded') ? Math.max(240, window.innerHeight - 270) : 240, left = 75, right = 35, top = 12, bottom = 32;
    const valid = points.filter(p => p.rate);
    if (!valid.length) { el.innerHTML = '<div class="io-empty">Ожидание двух измерений для расчёта скорости…</div>'; return; }
    const maximum = Math.max(1, ...valid.flatMap(p => [p.rate.incoming,p.rate.outgoing])) * 1.15;
    const end = points[points.length-1].time, start = end - 60000;
    const x = t => left + (t - start) / 60000 * (W-left-right), y = v => H-bottom - v/maximum*(H-top-bottom);
    let svg = '';
    for (let i=0;i<5;i++) { const v=maximum*i/4,yy=y(v); svg += `<line x1="${left}" y1="${yy}" x2="${W-right}" y2="${yy}" class="io-grid"/><text x="${left-8}" y="${yy+4}" text-anchor="end">${h(fmtBytes(v,2))}/с</text>`; }
    for (let i=0;i<5;i++) { const t=start+i*15000; svg += `<text x="${x(t)}" y="${H-8}" text-anchor="middle">${h(new Date(t).toLocaleTimeString(window.PanelLanguage?.locale || 'ru-RU'))}</text>`; }
    for (const [key,color] of [['outgoing','#23a26a'],['incoming','#f4a340']]) {
      let path='', connected=false, segment=[];
      const area=()=>{if(segment.length>1) svg+=`<path d="M${segment[0][0]},${H-bottom} ${segment.map(([xx,yy])=>`L${xx},${yy}`).join(' ')} L${segment[segment.length-1][0]},${H-bottom} Z" fill="${color}" opacity=".08"/>`;segment=[];};
      for (const point of points) {
        if (!point.rate) { area(); connected=false; continue; }
        segment.push([x(point.time).toFixed(1),y(point.rate[key]).toFixed(1)]);
        path += `${connected?'L':'M'}${x(point.time).toFixed(1)},${y(point.rate[key]).toFixed(1)} `; connected=true;
      }
      area();
      svg += `<path d="${path}" fill="none" stroke="${color}" stroke-width="2.5" stroke-linejoin="round"/>`;
      const last=points[points.length-1];if(last.rate)svg+=`<circle cx="${x(last.time)}" cy="${y(last.rate[key])}" r="3" fill="${color}"/>`;
    }
    el.innerHTML = `<svg viewBox="0 0 ${W} ${H}" aria-hidden="true">${svg}</svg>`;
  }
  function draw() {
    const c = counters(latest), network = mode === 'network';
    const values = [[network?'Исходящий':'Запись на диск',rate?.outgoing,true],[network?'Входящий':'Чтение',rate?.incoming,true],[network?'Всего отправлено':'Всего записано',c?.out,false],[network?'Всего получено':'Всего прочитано',c?.in,false]];
    $('.io-values',card).innerHTML = values.map(([label,value,speed],i)=>`<div><span>${i<2?`<i style="background:${i===0?'#23a26a':'#f4a340'}"></i>`:''}${h(label)}</span><strong>${value==null?'—':fmtBytes(value,speed ? 2 : undefined)+(speed?'/с':'')}</strong></div>`).join('');
    $('[data-io-label]',card).textContent = network ? 'Сеть' : 'Диск';
    $('[data-io-note]',card).textContent = network && device==='all' ? 'Сумма интерфейсов без lo; виртуальные сети могут учитывать трафик повторно.' : 'Объёмы с запуска устройства. График — последняя минута.';
    $('[data-io-status]',card).textContent = !c ? 'Нет данных устройства' : rate ? 'Обновление каждую секунду' : 'Ожидание измерений…';
    plot();
  }
  function reset() { previous=null;rate=null;points=[];devices(latest);draw(); }
  select.onchange = () => { device=select.value;reset(); };
  $$('[data-io-tab]',card).forEach(button => button.onclick=()=>{
    mode=button.dataset.ioTab;device=mode==='network'?'all':'';
    $$('[data-io-tab]',card).forEach(b=>{b.classList.toggle('chosen',b===button);b.setAttribute('aria-pressed',String(b===button));});reset();
  });
  $('[data-io-expand]',card).onclick = e => { const wide=card.classList.toggle('io-expanded');e.currentTarget.setAttribute('aria-pressed',String(wide));e.currentTarget.title=wide?'Свернуть график':'Развернуть график';e.currentTarget.setAttribute('aria-label',e.currentTarget.title); plot(); };
  card.addEventListener('keydown', e => { if(e.key==='Escape' && card.classList.contains('io-expanded')) $('[data-io-expand]',card).click(); });
  draw();
  return {
    update(data) {
      latest=data;devices(data);
      const current=counters(data),time=new Date(data.updated).getTime();
      const names=Object.keys(data[mode] || {}).sort().join('\n');
      rate=previous && previous.names===names ? dashboardIORate(previous.counters,current,(time-previous.time)/1000) : null;
      points.push({time,rate});points=points.filter(p=>p.time>=time-60000).slice(-90);
      previous={counters:current,time,names};draw();
    },
    unavailable() {rate=null;previous=null;draw();$('[data-io-status]',card).textContent='Нет связи с сервером. Ожидание новых данных…';}
  };
}

page('dashboard', {
  title: 'Дашборд',
  async render(view) {
    head(view, 'Главная');
    const server = document.createElement('div');
    server.className = 'server-dashboard';
    server.innerHTML = '<div class="server-content"><div class="panel muted">Загружаю показатели сервера…</div></div><p class="server-update" role="status"></p>';
    view.append(server);
    const monitor = document.createElement('div');
    monitor.className = 'dashboard-monitor';
    monitor.innerHTML = '<p class="monitor-status muted" role="status">Загружаю состояния сервисов…</p><div class="monitor-content"></div>';
    view.append(monitor);
    let monitoring = false, monitorData = null, attention = false, monitorObserver = null;
    App.cleanup = () => monitorObserver?.disconnect();
    const drawMonitor = () => {
      monitorObserver?.disconnect();
      $('.monitor-content', monitor).innerHTML = dashboardMonitorMarkup(monitorData, attention);
      fitDashboardServices(monitor);
      if (typeof ResizeObserver !== 'undefined') {
        monitorObserver = new ResizeObserver(() => fitDashboardServices(monitor));
        monitorObserver.observe($('.server-info', monitor));
      }
      $$('[data-monitor-filter]', monitor).forEach(button => button.onclick = () => {
        attention = button.dataset.monitorFilter === 'attention';
        drawMonitor();
        $(`[data-monitor-filter="${attention ? 'attention' : 'all'}"]`, monitor)?.focus();
      });
    };
    const refreshMonitor = async () => {
      if (monitoring || !view.isConnected) return;
      monitoring = true;
      try {
        const data = await api('stats.monitor');
        if (!view.isConnected) return;
        monitorData = data;
        drawMonitor();
        $('.monitor-status', monitor).textContent = '';
      } catch (e) {
        if (view.isConnected) $('.monitor-status', monitor).textContent = 'Не удалось обновить сервисы: ' + e.message + '. Показаны последние полученные данные.';
      } finally { monitoring = false; }
    };
    every(10000, refreshMonitor, view);
    void refreshMonitor();
    const liveIO = createDashboardIO(view);
    let busy = false;
    const refresh = async () => {
      if (busy || !view.isConnected) return;
      busy = true;
      try {
        const d = await api('stats.dashboard');
        if (!view.isConnected) return;
        updateDashboard($('.server-content', server), d);
        liveIO.update(d);
        $('.server-update', server).textContent = 'Обновлено: ' + new Date(d.updated).toLocaleTimeString(window.PanelLanguage?.locale || 'ru-RU') + ' · каждую секунду';
      } catch (e) {
        if (view.isConnected) { liveIO.unavailable(); $('.server-update', server).textContent = 'Не удалось обновить показатели: ' + e.message + '. Повторная попытка через секунду.'; }
      } finally { busy = false; }
    };
    every(1000, refresh, view);
    await refresh();
  },
});


// ================= Сайты =================
function phpOptions(meta) {
  return [...(meta.php_all || meta.php)].reverse().map((v) => [v, 'PHP ' + v + (meta.php.includes(v) ? '' : ' — будет установлена')]);
}
async function siteForm(edit) {
  const meta = await api('sites.form');
  const s = edit || {};
  const isNew = !edit;
  const users = [['', 'Новый пользователь (по имени домена)'], ...meta.users.map((u) => [u, u])];
  const php = phpOptions(meta);
  const certs = [['', 'Без SSL'], ...meta.certs.map((c) => [c.name, c.name + ' (' + ({ le: 'Let’s Encrypt', self: 'самоподписанный', custom: 'свой' }[c.type] || c.type) + ')'])];
  openForm({
    title: isNew ? 'Создать сайт' : 'Сайт ' + toU(s.domain),
    ok: isNew ? 'Создать' : 'Сохранить',
    fields: [
      { name: 'domain', label: 'Домен', value: s.domain ? toU(s.domain) : '', required: true, readonly: !isNew, placeholder: 'example.ru', hint: isNew ? 'Можно кириллицей: пример.рф' : '' },
      { name: 'add_www', label: 'Добавить www-алиас', type: 'checkbox', value: isNew, show: () => isNew },
      { name: 'aliases', label: 'Алиасы (через пробел)', value: (s.aliases || []).map(toU).join(' '), placeholder: 'www.example.ru shop.example.ru', hint: 'Оставьте поле пустым, чтобы удалить все алиасы.' },
      { name: 'user', label: 'Владелец (системный пользователь)', type: 'select', options: users, value: '', show: () => isNew },
      { name: 'mode', label: 'Режим работы', type: 'seg', value: s.mode === 'php' && ['apache', 'openlitespeed'].includes(s.backend) ? s.backend : (s.mode || 'php'), options: [['php', 'nginx + PHP-FPM'], ['apache', 'nginx + Apache'], ['openlitespeed', 'nginx + OpenLiteSpeed'], ['static', 'Только статика'], ['proxy', 'Прокси']],
        hint: 'Apache поддерживает .htaccess; OpenLiteSpeed — правила Rewrite в .htaccess. Во всех режимах PHP работает через PHP-FPM. OpenLiteSpeed устанавливается при первом выборе. После изменения .htaccess перезапустите OpenLiteSpeed в разделе «Информация о сервере».' },
      { name: 'php', label: 'Версия PHP', type: 'select', options: php, value: s.php || meta.default_php || '8.3', show: (v) => ['php', 'apache', 'openlitespeed'].includes(v.mode), hint: 'Если версия не установлена, панель установит её при сохранении (2–5 минут). <a href="#/php">Все версии PHP</a>' },
      { name: 'try_index', label: 'Все запросы на index.php (ЧПУ)', type: 'checkbox', value: s.try_index ?? true, show: (v) => v.mode === 'php', hint: 'Нужно для Laravel, X3 и большинства CMS. В режиме Apache это делает .htaccess.' },
      { name: 'proxy_to', label: 'Адрес приложения', value: s.proxy_to || '', placeholder: 'http://127.0.0.1:3000', show: (v) => v.mode === 'proxy' },
      { type: 'section', label: 'SSL' },
      { name: 'create_zone', label: 'Создать DNS-зону для домена', type: 'checkbox', value: false, show: () => isNew, hint: 'Для поддомена существующей зоны A-записи добавятся автоматически' },
      { name: 'auto_ssl', label: 'Автоматический SSL (Let’s Encrypt)', type: 'checkbox', value: isNew ? true : !!s.auto_ssl, hint: 'Сертификат выпускается сам, как только домен укажет на этот сервер, и продлевается автоматически. Новые алиасы добавляются в сертификат сами.' },
      { name: 'email', label: 'E-mail для Let’s Encrypt (необязательно)', value: meta.email || '', show: (v) => v.auto_ssl && !meta.email },
      { name: 'ssl', label: 'Сертификат', type: 'select', options: certs, value: s.ssl || '', show: () => !isNew },
      { name: 'redirect_https', label: 'Перенаправлять HTTP → HTTPS', type: 'checkbox', value: s.redirect_https ?? true },
      { type: 'section', label: 'Дополнительно' },
      { name: 'www_redirect', label: 'Перенаправление www', type: 'select', value: s.www_redirect ?? (isNew ? 'from_www' : ''), options: [['', 'Нет'], ['from_www', 'www.домен → домен'], ['to_www', 'домен → www.домен']] },
      { name: 'gzip', label: 'Сжатие gzip', type: 'checkbox', value: s.gzip ?? true },
      { name: 'static_expires', label: 'Кэширование статики в браузере', type: 'select', value: s.static_expires ?? (isNew ? '30d' : ''), options: [['', 'Не задавать'], ['1d', '1 день'], ['7d', '7 дней'], ['30d', '30 дней'], ['365d', '1 год']], show: (v) => v.mode !== 'proxy' },
      { name: 'max_body', label: 'Максимальный размер загрузки, МБ', type: 'number', value: s.max_body || 64 },
      { name: 'index', label: 'Индексные файлы', value: s.index || 'index.php index.html', show: (v) => v.mode !== 'proxy' },
      { name: 'root', label: 'Корневая папка', value: s.root || '', placeholder: 'по умолчанию /var/www/<пользователь>/data/www/<домен>', show: () => !isNew },
    ],
    submit: async (v) => {
      v.max_body = Number(v.max_body) || 64;
      if (['apache', 'openlitespeed'].includes(v.mode)) { v.backend = v.mode; v.mode = 'php'; } else v.backend = '';
      if (v.mode === 'php' && !meta.php.includes(v.php)) toast('Устанавливаю PHP ' + v.php + ' — это займёт несколько минут…');
      if (v.backend === 'apache' && !meta.apache) toast('Устанавливаю Apache…');
      if (v.backend === 'openlitespeed' && !meta.openlitespeed) toast('Устанавливаю OpenLiteSpeed — это займёт несколько минут…');
      v.edit = !isNew;
      if (!isNew) { v.domain = s.domain; v.add_www = false; }
      const r = await api('sites.start', v);
      watchJob(r.job);
    },
  });
}
const toU = (d) => d;

// Сайт по умолчанию — что открывается по IP-адресу сервера и по именам, не привязанным к сайтам.
async function defaultSiteForm() {
  const [rows, cur] = await Promise.all([api('sites.list'), api('sites.default')]);
  openForm({
    title: 'Сайт по умолчанию', ok: 'Сохранить',
    fields: [
      { name: 'site', label: 'При заходе по IP-адресу или чужому имени показывать', type: 'select', value: cur.site || '', options: [['', 'Страницу «Сайт не найден»'], ['-', 'Ничего (закрыть соединение)'], ...rows.map((r) => [r.site.domain, r.unicode])], hint: 'Например, http://IP-сервера/ или домен, который направлен на сервер, но не добавлен в панель. HTTPS по IP-адресу всегда отклоняется: сертификата на IP нет, а показывать чужой нельзя.' },
    ],
    submit: async (v) => { await api('sites.default.set', { site: v.site }); toast('Сохранено', 'ok'); route(); },
  });
}

page('sites', {
  title: 'Сайты',
  async render(view, params) {
    head(view, 'Сайты', { actions: [{ t: 'Создать сайт', i: 'plus', primary: true, fn: () => siteForm() }, { t: 'Сайт по умолчанию', i: 'globe', fn: () => defaultSiteForm() }, { t: 'Задачи', i: 'clock', fn: () => go('jobs') }] });
    const p = panel(view);
    const [rows, def] = await Promise.all([api('sites.list'), api('sites.default').catch(() => ({}))]);
    table(p, {
      rows,
      empty: { icon: 'globe', title: 'Сайтов пока нет', text: '<p><button class="btn primary" onclick="siteForm()">Создать первый сайт</button></p>' },
      columns: [
        { t: 'Домен', r: (r) => `<div class="site-name">${h(r.unicode)}${def.site === r.site.domain ? ' <span class="tag acc" title="Открывается по IP-адресу сервера и по именам без сайта">по умолчанию</span>' : ''}<small>${h((r.site.aliases || []).join(', '))}</small></div>` },
        { t: 'Владелец', r: (r) => h(r.site.user), cls: 'hide-sm' },
        { t: 'Режим', r: (r) => r.site.mode === 'php' ? `<span class="tag acc">PHP ${h(r.site.php)}</span> <span class="tag">${r.site.backend === 'openlitespeed' ? 'OpenLiteSpeed' : r.site.backend === 'apache' ? 'Apache' : 'nginx'}</span>` : r.site.mode === 'proxy' ? `<span class="tag">прокси</span>` : '<span class="tag">статика</span>' },
        { t: 'SSL', r: (r) => { if (!r.site.ssl) return r.site.auto_ssl ? '<span class="tag acc" title="Выпустится автоматически, когда домен укажет на сервер">авто: ждёт DNS</span>' : '<span class="tag">нет</span>'; const d = Math.floor((new Date(r.ssl_expires) - Date.now()) / 864e5); return `<span class="ssl-cell"><span class="tag ${d < 14 ? 'warn' : 'ok'}">${icon('lock')} ${d} дн.</span>${r.site.auto_ssl ? '<span class="tag">авто</span>' : ''}</span>`; } },
        { t: 'Состояние', r: (r) => r.site.suspended ? '<span class="tag err">выключен</span>' : '<span class="tag ok">работает</span>', cls: 'hide-sm' },
      ],
      onRow: (r) => siteForm(r.site),
      actions: (r) => [
        { i: 'external', t: 'Открыть сайт', fn: () => window.open((r.site.ssl ? 'https://' : 'http://') + r.site.domain, '_blank') },
        { i: 'folder', t: 'Файлы сайта', fn: () => go('files', { path: r.site.root }) },
        { i: 'code', t: 'Свои правила nginx', fn: () => nginxCustom(r.site.domain) },
        { i: 'wwwlog', t: 'Журналы', fn: () => go('wwwlogs', { site: r.site.domain }) },
        { i: r.site.suspended ? 'play' : 'pause', t: r.site.suspended ? 'Включить' : 'Выключить', fn: async () => { try { await api('sites.suspend', { domain: r.site.domain, suspend: !r.site.suspended }); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => {
          const ok = await confirmBox('Удалить сайт ' + r.unicode + '?', 'Конфигурация nginx и PHP будет удалена. Базы данных и сертификаты остаются.', { extra: '<label class="check"><input type="checkbox" name="files"><span>Удалить и файлы сайта</span></label>' });
          if (!ok) return;
          try { await api('sites.delete', { domain: r.site.domain, files: ok.files }); toast('Сайт удалён', 'ok'); route(); } catch (e) { fail(e); }
        } },
      ],
    });
    if (params.new && view.isConnected) siteForm();
  },
});
window.siteForm = siteForm;

async function nginxCustom(domain) {
  const d = await api('sites.get', { domain });
  openForm({
    title: 'nginx: ' + domain, wide: true,
    fields: [
      { type: 'html', html: '<p class="hint">Блок location / или location ^~ / заменяет стандартный блок панели. Остальные обработчики, включая PHP, сохраняются. При ошибке проверки nginx изменения не применяются.</p>' },
      { name: 'text', label: 'Свои правила (вставляются внутрь server {})', type: 'textarea', rows: 14, value: d.custom },
      { type: 'html', html: `<label>Итоговая конфигурация (только чтение)</label><pre class="out" style="max-height:340px">${h(d.vhost)}</pre>` },
    ],
    submit: async (v) => { await api('sites.custom.save', { domain, text: v.text }); toast('Сохранено, nginx перезагружен', 'ok'); },
  });
}

// ================= Базы данных =================
async function dbServerOptions() {
  const d = await api('dbservers.list');
  return d.rows.filter((r) => r.active && !r.status).map((r) => [r.name, r.main ? 'MariaDB ' + dbShortVersion(r.version) + ' (основной)' : r.title + ' · порт ' + r.port]);
}
async function dbForm() {
  const sites = await api('sites.list');
  const servers = await dbServerOptions();
  openForm({
    title: 'Создать базу данных', ok: 'Создать',
    fields: [
      { name: 'server', label: 'Сервер БД', type: 'select', options: servers, value: '', show: () => servers.length > 1, hint: '<a href="#/dbservers">Добавить MySQL или MariaDB другой версии</a>' },
      { name: 'name', label: 'Имя базы', required: true, placeholder: 'shop', hint: 'Латиница, цифры и _' },
      { name: 'user', label: 'Пользователь', placeholder: 'по умолчанию как имя базы' },
      { name: 'password', label: 'Пароль', type: 'password', value: genPass(16), gen: true },
      { name: 'site', label: 'Сайт (для резервных копий)', type: 'select', options: [['', '— не привязывать —'], ...sites.map((s) => [s.site.domain, s.unicode])] },
      { name: 'charset', label: 'Кодировка', type: 'select', value: 'utf8mb4', options: [['utf8mb4', 'utf8mb4 (рекомендуется)'], ['utf8', 'utf8'], ['cp1251', 'cp1251'], ['latin1', 'latin1']] },
    ],
    submit: async (v) => {
      const s = sites.find((x) => x.site.domain === v.site);
      v.owner = s ? s.site.user : '';
      const r = await api('db.create', v);
      const port = (servers.find((x) => x[0] === v.server) || [])[1] || '';
      const m = /порт (\d+)/.exec(port);
      showSecrets('База создана', [['Хост', m ? '127.0.0.1:' + m[1] : 'localhost'], ['База', v.name], ['Пользователь', v.user || v.name], ['Пароль', r.password]]);
      route();
    },
  });
}
// Импорт дампа в базу: файл с компьютера (загрузка с прогрессом) или файл на сервере.
function dbImportForm(name) {
  const m = modal(`<h3>Импорт дампа в ${h(name)}</h3>
    <div class="seg" data-src><button type="button" data-v="file" class="on">С компьютера</button><button type="button" data-v="path">Файл на сервере</button></div>
    <div class="field" data-file style="margin-top:16px"><label>Файл дампа</label><input type="file" accept=".sql,.gz,.zip"></div>
    <div class="field" data-path hidden style="margin-top:16px"><label>Путь к файлу</label><input type="text" placeholder="/root/backup.sql.gz" spellcheck="false"><div class="hint">${user ? 'Путь внутри вашей папки, например загруженный через менеджер файлов или FTP.' : 'Полный путь к файлу на сервере.'}</div></div>
    <p class="hint">Форматы: .sql, .sql.gz и .zip с файлом .sql внутри. Таблицы с такими же именами будут заменены данными из дампа.${user ? ' Дамп выполняется с правами только на эту базу.' : ''}</p>
    <progress max="100" value="0" hidden style="width:100%"></progress><p class="muted" data-status></p>
    <div class="btns"><button class="btn" data-close>Отмена</button><button class="btn primary" data-ok>Импортировать</button></div>`);
  let src = 'file';
  $$('[data-src] button', m.el).forEach((b) => (b.onclick = () => { src = b.dataset.v; $$('[data-src] button', m.el).forEach((x) => x.classList.toggle('on', x === b)); $('[data-file]', m.el).hidden = src !== 'file'; $('[data-path]', m.el).hidden = src !== 'path'; }));
  const ok = $('[data-ok]', m.el), bar = $('progress', m.el), status = $('[data-status]', m.el);
  ok.onclick = async () => {
    try {
      let r;
      if (src === 'file') {
        const f = $('[data-file] input', m.el).files[0];
        if (!f) { toast('Выберите файл дампа', 'err'); return; }
        ok.disabled = true; bar.hidden = false;
        r = await sendFileUpload([f], '/api/db/import-upload?name=' + encodeURIComponent(name), (p) => {
          if (p.stage === 'processing') { bar.value = 100; status.textContent = 'Файл передан, запускаю импорт…'; }
          else if (p.total) { bar.value = Math.floor(p.loaded / p.total * 100); status.textContent = fmtBytes(p.loaded) + ' / ' + fmtBytes(p.total); }
        });
      } else {
        const path = $('[data-path] input', m.el).value.trim();
        if (!path) { toast('Укажите путь к файлу', 'err'); return; }
        ok.disabled = true;
        r = await api('db.import', { name, path });
      }
      m.close();
      watchJob(r.job);
    } catch (e) { fail(e); ok.disabled = false; status.textContent = ''; bar.hidden = true; }
  };
}
// Доступ к базе по IP и сетевые подключения основного MariaDB.
async function databaseAccessForm(name){
 const d=await api('db.access',{name});
 openForm({title:'Доступ к базе: '+name,fields:[
  {name:'mode',label:'Удалённый доступ',type:'select',value:d.mode,options:[['local','Только локально'],['all','С любых IP'],['ips','Только указанные IP']]},
  {name:'ips',label:'IP-адреса (через пробел)',value:(d.ips||[]).join(' '),show:v=>v.mode==='ips'},
  {name:'password',label:'Пароль базы для всех разрешённых адресов',type:'password',required:true,gen:true,hint:'От 12 до 72 символов. Указанный пароль станет действовать и для локального подключения.'},
  {type:'html',html:'<p class="hint">Локальный доступ сохраняется всегда. Прослушивание сети включается в «Серверы БД» → «Сетевые подключения».</p>'}
 ],submit:async v=>{await api('db.access',{name,save:true,mode:v.mode,password:v.password,ips:v.mode==='ips'?v.ips.split(/[\s,]+/).filter(Boolean):[]});toast('Сохранено','ok');}});
}
async function databaseNetworkForm(){
 const d=await api('db.network');
 openForm({title:'Сетевые подключения MariaDB',fields:[
  {name:'enabled',label:'Принимать подключения по сети (IPv4)',type:'checkbox',value:d.bind_address!=='127.0.0.1'&&d.bind_address!=='localhost'&&d.bind_address!=='::1'},
  {type:'html',html:`<p>Порт: ${h(d.port)}. Разрешения задаются отдельно у каждой базы. Для внешнего подключения также разрешите этот порт в разделе «Файрвол» и в настройках хостинга.</p><p class="hint">Сохранение перезапустит MariaDB и кратковременно прервёт соединения с базами. Локальный доступ сохраняется.</p>`}
 ],submit:async v=>{await api('db.network',{save:true,enabled:v.enabled});toast('Сохранено','ok');}});
}
function dbExport(name) {
  toast('Готовлю дамп ' + name + '… Скачивание начнётся автоматически');
  location.href = '/api/db/export?name=' + encodeURIComponent(name);
}

page('db', {
  title: 'Базы данных',
  async render(view, params) {
    const d = await api('db.list');
    head(view, 'Базы данных', { actions: [
      { t: 'Создать базу', i: 'plus', primary: true, fn: dbForm },
      d.pma ? { t: 'phpMyAdmin', i: 'phpmyadmin', href: '/phpmyadmin/' } : { t: 'Установить phpMyAdmin', i: 'download', fn: async () => { toast('Устанавливаю phpMyAdmin…'); try { await api('pma.install'); toast('phpMyAdmin установлен', 'ok'); route(); } catch (e) { fail(e); } } },
    ], sub: d.server ? '' : '<span class="tag err">MariaDB не запущена</span>' });
    const p = panel(view);
    table(p, {
      rows: d.rows,
      empty: { icon: 'database', title: 'Баз данных нет' },
      columns: [
        { t: 'База', r: (r) => `<b>${h(r.db.name)}</b>` },
        { t: 'Пользователь', r: (r) => h(r.db.user) },
        { t: 'Сервер', r: (r) => `<span class="tag">${h(r.server)}</span>` + (r.port !== 3306 ? ` <small class="muted">:${r.port}</small>` : ''), cls: 'hide-sm' },
        { t: 'Сайт', r: (r) => h(r.db.site || '—'), cls: 'hide-sm' },
        { t: 'Размер', r: (r) => fmtBytes(r.size), cls: 'nowrap' },
        { t: 'Создана', r: (r) => fmtDate(r.db.created, false), cls: 'hide-sm' },
      ],
      actions: (r) => [
        { i: 'download', t: 'Скачать дамп', fn: () => dbExport(r.db.name) },
        { i: 'upload', t: 'Импорт дампа', fn: () => dbImportForm(r.db.name) },
        {i:'shield',t:'IP',fn:()=>databaseAccessForm(r.db.name)},
        { i: 'key', t: 'Сменить пароль', fn: () => openForm({ title: 'Пароль пользователя ' + r.db.user, ok: 'Сменить', fields: [{ name: 'password', label: 'Новый пароль', type: 'password', value: genPass(16), gen: true }], submit: async (v) => { const x = await api('db.password', { name: r.db.name, password: v.password }); showSecrets('Пароль изменён', [['Пользователь', r.db.user], ['Пароль', x.password]]); } }) },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => {
          const ok = await confirmBox('Удалить базу ' + r.db.name + '?', 'Все данные базы будут удалены без возможности восстановления (если нет резервной копии).', { extra: '<label class="check"><input type="checkbox" name="drop_user" checked><span>Удалить и пользователя ' + h(r.db.user) + '</span></label>' });
          if (!ok) return;
          try { await api('db.delete', { name: r.db.name, drop_user: ok.drop_user }); toast('База удалена', 'ok'); route(); } catch (e) { fail(e); }
        } },
      ],
    });
    if (params.new && view.isConnected) dbForm();
  },
});

// ================= DNS =================
async function zoneForm(d) {
  openForm({
    title: 'Создать DNS-зону', ok: 'Создать',
    fields: [
      { name: 'name', label: 'Домен', required: true, placeholder: 'example.ru' },
      { name: 'ip', label: 'IP-адрес для записей', value: d.ip || '', hint: 'Будут созданы записи @, www' + (d.settings.mail ? ', mail, MX и SPF' : '') + ' и CAA для Let’s Encrypt' },
      { name: 'ns', label: 'Серверы имён (NS)', value: (d.settings.nameservers || []).join(' '), placeholder: 'пусто — ns1 и ns2 этого домена', hint: 'Эти же NS нужно указать у регистратора домена' },
      { name: 'mail', label: 'Добавить записи для почты (mail, MX, SPF)', type: 'checkbox', value: !!d.settings.mail },
    ],
    submit: async (v) => { const z = await api('dns.zone.create', v); toast('Зона создана', 'ok'); go('dns', { zone: z.name }); },
  });
}
function recordForm(zone, rec, types, reload) {
  const r = rec || { type: 'A', name: '', ttl: 0, prio: 10, value: '' };
  const hints = { A: 'IPv4, например 203.0.113.10', AAAA: 'IPv6-адрес', CNAME: 'Имя хоста, например example.ru. (с точкой — полное имя)', MX: 'Почтовый сервер, например mail', TXT: 'Текст: SPF, DKIM, подтверждение домена', NS: 'Сервер имён, например ns1.example.ru.', SRV: 'вес порт хост, например: 5 5060 sip.example.ru.', CAA: '0 issue "letsencrypt.org"' };
  openForm({
    title: rec ? 'Запись ' + r.type : 'Новая запись',
    fields: [
      { name: 'type', label: 'Тип', type: 'seg', value: r.type, options: types.map((t) => [t, t]) },
      { name: 'name', label: 'Имя', value: r.name === '@' ? '' : r.name, placeholder: '@ — сам домен', hint: (v) => '' },
      { type: 'html', html: (v) => `<p class="muted" style="margin:-8px 0 0">Полное имя: <span class="mono">${h((v.name && v.name !== '@' ? v.name + '.' : '') + zone)}</span></p>` },
      { name: 'prio', label: 'Приоритет', type: 'number', value: r.prio || 10, show: (v) => v.type === 'MX' || v.type === 'SRV' },
      { name: 'value', label: 'Значение', value: r.value, required: true, rerender: false },
      { type: 'html', html: (v) => `<p class="muted" style="margin:-8px 0 0">${h(hints[v.type] || '')}</p>` },
      { name: 'ttl', label: 'TTL, секунд (0 — по умолчанию зоны)', type: 'number', value: r.ttl || 0 },
    ],
    submit: async (v) => { await api('dns.record.save', { zone, record: { id: r.id || '', name: v.name || '@', type: v.type, ttl: +v.ttl || 0, prio: +v.prio || 0, value: v.value } }); toast('Сохранено, зона обновлена', 'ok'); reload(); },
  });
}
page('dns', {
  title: 'Управление DNS',
  async render(view, params) {
    const d = await api('dns.list');
    if (!view.isConnected) return;
    if (params.zone) return zonePage(view, params.zone);
    head(view, 'Управление DNS', { sub: d.installed ? (d.active ? '' : '<span class="tag warn">DNS-сервер BIND остановлен</span>') : '<span class="tag warn">DNS-сервер BIND не установлен</span>', actions: [
      d.installed ? { t: 'Создать зону', i: 'plus', primary: true, fn: () => zoneForm(d) } : { t: 'Установить BIND', i: 'download', primary: true, fn: async () => { toast('Устанавливаю BIND…'); try { await api('dns.install'); toast('Установлен', 'ok'); route(); } catch (e) { fail(e); } } },
    ] });
    const p = panel(view);
    table(p, {
      rows: d.rows,
      empty: { icon: 'dns', title: 'Зон пока нет', text: '<p>Создайте зону, затем укажите у регистратора домена серверы имён (NS) этого сервера.</p>' },
      columns: [
        { t: 'Домен', r: (r) => `<div class="site-name">${h(r.unicode)}<small>${h(r.ns.join(', '))}</small></div>` },
        { t: 'Записей', r: (r) => r.records },
        { t: 'Делегирование', r: (r) => `<span class="tag" data-deleg="${h(r.name)}"><span class="spin" style="width:12px;height:12px;border-width:2px"></span></span>`, cls: 'hide-sm' },
        { t: 'Серийный номер', r: (r) => `<span class="mono">${r.serial}</span>`, cls: 'hide-sm' },
      ],
      onRow: (r) => go('dns', { zone: r.name }),
      actions: (r) => [
        { i: 'edit', t: 'Записи', fn: () => go('dns', { zone: r.name }) },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить зону ' + r.unicode + '?', 'Все записи зоны будут удалены, домен перестанет отвечать на этом DNS-сервере.'))) return; try { await api('dns.zone.delete', { name: r.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    $$('[data-deleg]', p).forEach(async (el) => {
      try { const x = await api('dns.delegation', { name: el.dataset.deleg }); el.className = 'tag ' + (x.ok ? 'ok' : 'warn'); el.textContent = x.ok ? 'на этот сервер' : (x.got.length ? 'на другие NS' : 'не делегирован'); el.title = 'Сейчас: ' + (x.got.join(', ') || '—') + '\nНужно: ' + x.want.join(', '); } catch (e) { el.textContent = '—'; }
    });
    const s = panel(view, 'Настройки');
    s.innerHTML = `<div class="cols2"><div class="field"><label>Серверы имён для новых зон (NS)</label><input type="text" data-ns value="${h((d.settings.nameservers || []).join(' '))}" placeholder="пусто — ns1 и ns2 каждого домена"><div class="hint">Например: ns1.mydomain.ru ns2.mydomain.ru. IP этого сервера: <span class="mono">${h(d.ip)}</span></div></div>
      <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" data-mail ${d.settings.mail ? 'checked' : ''}><span>Добавлять в новые зоны записи для почты (mail, MX, SPF)</span></label></div></div><button class="btn primary" data-s>Сохранить</button>`;
    $('[data-s]', s).onclick = async () => { try { await api('dns.settings', { nameservers: $('[data-ns]', s).value, mail: $('[data-mail]', s).checked }); toast('Сохранено', 'ok'); } catch (e) { fail(e); } };
    if (params.new && view.isConnected) zoneForm(d);
  },
});
async function zonePage(view, name) {
  const load = async () => {
    const d = await api('dns.zone.get', { name });
    view.innerHTML = '';
    head(view, d.zone.name, { sub: '<a href="#/dns">← Все зоны</a> · серийный номер <span class="mono">' + d.zone.serial + '</span>', actions: [
      { t: 'Добавить запись', i: 'plus', primary: true, fn: () => recordForm(name, null, d.types, load) },
      { t: 'Параметры зоны', i: 'edit', fn: () => openForm({ title: 'Параметры зоны', fields: [{ name: 'email', label: 'E-mail администратора (SOA)', value: d.zone.email }, { name: 'ttl', label: 'TTL по умолчанию, секунд', type: 'number', value: d.zone.ttl }], submit: async (v) => { await api('dns.zone.settings', { name, email: v.email, ttl: +v.ttl }); load(); } }) },
      { t: 'Файл зоны', i: 'code', fn: () => showOutput('Зона ' + name, d.text) },
    ] });
    const p = panel(view);
    const order = { NS: 0, A: 1, AAAA: 2, CNAME: 3, MX: 4, TXT: 5, SRV: 6, CAA: 7 };
    const rows = d.zone.records.slice().sort((a, b) => order[a.type] - order[b.type] || a.name.localeCompare(b.name));
    table(p, {
      rows,
      empty: { icon: 'dns', title: 'Записей нет' },
      columns: [
        { t: 'Имя', r: (r) => `<b class="mono">${h(r.name)}</b>` },
        { t: 'Тип', r: (r) => `<span class="tag acc">${h(r.type)}</span>` },
        { t: 'Значение', r: (r) => `<span class="mono" style="word-break:break-all">${r.type === 'MX' || r.type === 'SRV' ? '<span class="muted">' + r.prio + '</span> ' : ''}${h(r.value)}</span>` },
        { t: 'TTL', r: (r) => r.ttl || '<span class="muted">' + d.zone.ttl + '</span>', cls: 'hide-sm' },
      ],
      onRow: (r) => recordForm(name, r, d.types, load),
      actions: (r) => [
        { i: 'edit', t: 'Изменить', fn: () => recordForm(name, r, d.types, load) },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить запись?', h(r.name + ' ' + r.type + ' ' + r.value)))) return; try { await api('dns.record.delete', { zone: name, ids: [r.id] }); load(); } catch (e) { fail(e); } } },
      ],
    });
  };
  await load();
}

// ================= Серверы БД =================
page('dbservers', {
  title: 'Серверы БД',
  async render(view) {
    head(view, 'Серверы баз данных', { sub: 'Основной MariaDB и дополнительные MySQL/MariaDB нужных версий (каждый — в своём контейнере Docker на отдельном порту)', actions: [{ t: 'Добавить сервер', i: 'plus', primary: true, fn: () => add() }, {t:'Сетевые подключения',i:'globe',fn:databaseNetworkForm}] });
    const p = panel(view);
    let data;
    const add = () => openForm({
      title: 'Добавить сервер БД', ok: 'Установить',
      fields: [
        { name: 'type', label: 'СУБД', type: 'seg', value: 'mysql', options: [['mysql', 'MySQL'], ['mariadb', 'MariaDB']] },
        { name: 'version', label: 'Версия', type: 'select', value: '8.4', options: (v) => (data.versions[v.type] || []).slice().reverse().map((x) => [x, (v.type === 'mysql' ? 'MySQL ' : 'MariaDB ') + x]) },
        { type: 'html', html: (v) => `<p class="muted" style="margin:0">${data.docker ? '' : 'Docker будет установлен автоматически. '}Сервер получит свободный порт (3307, 3308…) и доступен только с этого сервера: <span class="mono">127.0.0.1:порт</span>.${v.type === 'mysql' && v.version === '5.7' ? '<br><b>MySQL 5.7</b> выпускается только для x86_64 и больше не поддерживается разработчиком.' : ''}</p>` },
      ],
      onChange: (v, n) => { if (n === 'type' || !data.versions[v.type].includes(v.version)) v.version = data.versions[v.type].slice(-2)[0]; },
      submit: async (v) => { await api('dbservers.create', v); toast('Устанавливаю сервер — это займёт 1–3 минуты'); load(); },
    });
    const load = async () => {
      data = await api('dbservers.list');
      table(p, {
        rows: data.rows,
        columns: [
          { t: 'Сервер', r: (r) => `<b>${h(r.title)}</b><br><small class="muted mono">${h(r.version)}</small>` },
          { t: 'Подключение', r: (r) => `<span class="mono">${h(r.host)}${r.main ? '' : ':' + r.port}</span>` },
          { t: 'Баз', r: (r) => r.dbs },
          { t: 'Состояние', r: (r) => r.status === 'installing' ? '<span class="tag acc"><span class="spin" style="width:12px;height:12px;border-width:2px"></span> устанавливается</span>' : r.status === 'error' ? `<span class="tag err" title="${h(r.error)}">ошибка</span>` : r.active ? '<span class="tag ok">работает</span>' : '<span class="tag warn">остановлен</span>' },
        ],
        onRow: (r) => r.error && showOutput(r.title, r.error),
        actions: (r) => r.main ? [{ i: 'refresh', t: 'Перезапустить', fn: async () => { try { await api('system.service', { name: 'mariadb', action: 'restart' }); toast('Перезапущен', 'ok'); load(); } catch (e) { fail(e); } } }] : r.status === 'installing' ? [] : [
          r.status === 'error' && { i: 'refresh', t: 'Повторить установку', fn: async () => { await api('dbservers.action', { name: r.name, action: 'reinstall' }); load(); } },
          !r.status && { i: 'refresh', t: 'Перезапустить', fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'restart' }); load(); } catch (e) { fail(e); } } },
          !r.status && (r.active ? { i: 'stop', t: 'Остановить', fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'stop' }); load(); } catch (e) { fail(e); } } } : { i: 'play', t: 'Запустить', fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'start' }); load(); } catch (e) { fail(e); } } }),
          { i: 'trash', t: 'Удалить', danger: true, fn: async () => {
            const ok = await confirmBox('Удалить ' + r.title + '?', 'Контейнер сервера будет удалён.', { extra: '<label class="check"><input type="checkbox" name="data"><span>Удалить и файлы данных</span></label>' });
            if (!ok) return;
            try { await api('dbservers.action', { name: r.name, action: 'delete', data: ok.data }); load(); } catch (e) { fail(e); }
          } },
        ],
      });
    };
    await load();
    every(4000, () => { if ($$('.spin', p).length) load(); }, view);
  },
});

// ================= SSL =================
async function leForm() {
  const meta = await api('sites.form');
  const sites = await api('sites.list');
  if (!sites.length) { toast('Сначала создайте сайт', 'err'); return; }
  openForm({
    title: 'Выпустить Let’s Encrypt', ok: 'Выпустить',
    fields: [
      { name: 'site', label: 'Сайт', type: 'select', options: sites.map((s) => [s.site.domain, s.unicode]), value: sites[0].site.domain },
      { name: 'www', label: 'Включить www-алиас', type: 'checkbox', value: true },
      { name: 'aliases', label: 'Включить остальные алиасы', type: 'checkbox', value: true },
      { name: 'email', label: 'E-mail (уведомления Let’s Encrypt)', value: meta.email || '' },
      { type: 'html', html: '<p class="muted" style="margin:0">Все имена должны указывать на IP этого сервера, порт 80 — открыт. Продление — автоматически. Выпуская сертификат, вы принимаете условия Let’s Encrypt.</p>' },
    ],
    submit: async (v) => { toast('Запрашиваю сертификат…'); const r = await api('ssl.le', v); toast(r?.skipped?.length ? 'Сертификат выпущен без имён ' + r.skipped.join(', ') + ' — они ведут на другой сервер. Исправьте DNS и выпустите снова.' : 'Сертификат выпущен и подключён', r?.skipped?.length ? 'warn' : 'ok'); route(); },
  });
}
async function selfForm() {
  const sites = await api('sites.list');
  openForm({
    title: 'Самоподписанный сертификат', ok: 'Создать',
    fields: [
      { name: 'domains', label: 'Домены или IP (через пробел)', required: true, placeholder: 'example.ru www.example.ru' },
      { name: 'name', label: 'Имя сертификата', placeholder: 'по первому домену' },
      { name: 'days', label: 'Срок, дней', type: 'number', value: 365 },
      { name: 'site', label: 'Подключить к сайту', type: 'select', options: [['', '— не подключать —'], ...sites.map((s) => [s.site.domain, s.unicode])] },
    ],
    submit: async (v) => { v.days = Number(v.days); await api('ssl.self', v); toast('Сертификат создан', 'ok'); route(); },
  });
}
async function importForm() {
  const sites = await api('sites.list');
  openForm({
    title: 'Добавить существующий сертификат', ok: 'Добавить', wide: true,
    fields: [
      { name: 'name', label: 'Имя сертификата', placeholder: 'по домену из сертификата' },
      { name: 'crt', label: 'Сертификат (PEM)', type: 'textarea', required: true, placeholder: '-----BEGIN CERTIFICATE-----' },
      { name: 'chain', label: 'Цепочка (CA bundle), если есть', type: 'textarea', rows: 4 },
      { name: 'key', label: 'Закрытый ключ', type: 'textarea', required: true, placeholder: '-----BEGIN PRIVATE KEY-----' },
      { name: 'site', label: 'Подключить к сайту', type: 'select', options: [['', '— не подключать —'], ...sites.map((s) => [s.site.domain, s.unicode])] },
    ],
    submit: async (v) => { await api('ssl.import', v); toast('Сертификат добавлен', 'ok'); route(); },
  });
}
page('ssl', {
  title: 'SSL-сертификаты',
  async render(view, params) {
    head(view, 'SSL-сертификаты', { actions: [
      { t: 'Let’s Encrypt', i: 'plus', primary: true, fn: leForm },
      { t: 'Самоподписанный', i: 'shield', fn: selfForm },
      { t: 'Добавить свой', i: 'shieldcert', fn: importForm },
    ] });
    const p = panel(view);
    const rows = await api('ssl.list');
    table(p, {
      rows,
      empty: { icon: 'lock', title: 'Сертификатов нет', text: '<p>Выпустите бесплатный сертификат Let’s Encrypt для сайта.</p>' },
      columns: [
        { t: 'Имя', r: (r) => `<div class="site-name">${h(r.cert.name)}<small>${h((r.cert.domains || []).join(', '))}</small></div>` },
        { t: 'Тип', r: (r) => ({ le: '<span class="tag acc">Let’s Encrypt</span>', self: '<span class="tag">самоподписанный</span>', custom: '<span class="tag">свой</span>' }[r.cert.type]) },
        { t: 'Действует до', r: (r) => `${fmtDate(r.cert.expires, false)} <span class="tag ${r.days < 0 ? 'err' : r.days < 14 ? 'warn' : 'ok'}">${r.days < 0 ? 'истёк' : r.days + ' дн.'}</span>`, cls: 'nowrap' },
        { t: 'Сайты', r: (r) => h((r.sites || []).join(', ') || '—'), cls: 'hide-sm' },
      ],
      actions: (r) => [
        r.cert.type === 'le' && { i: 'refresh', t: 'Продлить сейчас', fn: async () => { toast('Продлеваю…'); try { await api('ssl.renew', { name: r.cert.name }); toast('Продлён', 'ok'); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить сертификат ' + r.cert.name + '?', 'Сайты с этим сертификатом перейдут на HTTP.'))) return; try { await api('ssl.delete', { name: r.cert.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (params.le && view.isConnected) leForm();
    if (params.self && view.isConnected) selfForm();
    if (params.import && view.isConnected) importForm();
  },
});

// ================= CRON =================
const CRON_PRESETS = [['', 'Своё расписание'], ['* * * * *', 'Каждую минуту'], ['*/5 * * * *', 'Каждые 5 минут'], ['*/15 * * * *', 'Каждые 15 минут'], ['0 * * * *', 'Каждый час'], ['0 3 * * *', 'Каждый день в 03:00'], ['0 4 * * 1', 'По понедельникам в 04:00'], ['0 5 1 * *', '1-го числа в 05:00'], ['@reboot', 'При загрузке сервера']];
function cronHuman(s) { const p = CRON_PRESETS.find((x) => x[0] === s); return p && p[0] ? p[1] : ''; }
async function cronForm(job, users) {
  const j = job || { enabled: true, user: users[1] || 'root', schedule: '0 3 * * *' };
  openForm({
    title: job ? 'Задание CRON' : 'Новое задание CRON',
    fields: [
      { name: 'user', label: 'Выполнять от пользователя', type: 'select', options: users.map((u) => [u, u]), value: j.user },
      { name: 'preset', label: 'Быстрый выбор', type: 'select', options: CRON_PRESETS, value: cronHuman(j.schedule) ? j.schedule : '' },
      { name: 'schedule', label: 'Расписание (мин час день месяц день_недели)', value: j.schedule, required: true, hint: 'Пример: <code>*/10 * * * *</code> — каждые 10 минут' },
      { name: 'command', label: 'Команда', value: j.command || '', required: true, placeholder: '/usr/bin/php8.3 /var/www/user/data/www/site.ru/cron.php' },
      { name: 'comment', label: 'Комментарий', value: j.comment || '' },
      { name: 'enabled', label: 'Включено', type: 'checkbox', value: j.enabled },
    ],
    onChange: (v, name) => { if (name === 'preset' && v.preset) v.schedule = v.preset; },
    submit: async (v) => { v.id = j.id || ''; await api('cron.save', v); toast('Сохранено', 'ok'); route(); },
  });
}
page('cron', {
  title: 'Планировщик CRON',
  async render(view) {
    const d = await api('cron.list');
    head(view, 'Планировщик CRON', { actions: [{ t: 'Создать задание', i: 'plus', primary: true, fn: () => cronForm(null, d.users) }] });
    const p = panel(view);
    table(p, {
      rows: d.rows || [],
      empty: { icon: 'cron', title: 'Заданий нет' },
      columns: [
        { t: 'Расписание', r: (r) => `<span class="mono">${h(r.schedule)}</span><br><small class="muted">${h(cronHuman(r.schedule))}</small>`, cls: 'nowrap' },
        { t: 'Команда', r: (r) => `<span class="mono">${h(r.command)}</span>${r.comment ? `<br><small class="muted">${h(r.comment)}</small>` : ''}` },
        { t: 'Пользователь', r: (r) => h(r.user), cls: 'hide-sm' },
        { t: 'Состояние', r: (r) => r.enabled ? '<span class="tag ok">включено</span>' : '<span class="tag">выключено</span>' },
      ],
      onRow: (r) => cronForm(r, d.users),
      actions: (r) => [
        { i: 'play', t: 'Запустить сейчас', fn: async () => { toast('Выполняю…'); try { const o = await api('cron.run', { id: r.id }); showOutput('Результат: ' + r.command, (o.error ? o.error + '\n\n' : '') + (o.output || '')); } catch (e) { fail(e); } } },
        { i: r.enabled ? 'pause' : 'check', t: r.enabled ? 'Выключить' : 'Включить', fn: async () => { try { await api('cron.save', { ...r, enabled: !r.enabled }); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить задание?', h(r.command)))) return; try { await api('cron.delete', { id: r.id }); route(); } catch (e) { fail(e); } } },
      ],
    });
  },
});

// ================= Резервные копии =================
// Копии трёх видов: файлы сайтов, каждая база данных отдельно и настройки панели.
async function backupForm(tab = 'files') {
  if (tab === 'panel') {
    if (!(await confirmBox('Создать копию настроек панели?', 'Общие параметры, настройки DNS и резервного копирования, включая пароль FTP. Учётная запись администратора, адрес панели и сертификаты не включаются.', { ok: 'Создать', danger: false }))) return;
    try { await api('backups.create', { kind: 'panel' }); toast('Копия создаётся…'); route(); } catch (e) { fail(e); }
    return;
  }
  if (tab === 'db') {
    const names = Object.keys((await api('backups.list')).db_sites || {}).sort();
    if (!names.length) { toast('Баз данных пока нет', 'err'); return; }
    openForm({
      title: 'Копия базы данных', ok: 'Создать',
      fields: [
        { name: 'db', label: 'База данных', type: 'select', options: [['*', 'Все базы'], ...names.map((n) => [n, n])], value: '*', hint: 'Каждая база сохраняется отдельной копией: её можно скачать как .sql.gz или восстановить, не трогая файлы сайтов и другие базы.' },
      ],
      submit: async (v) => { await api('backups.create', { kind: 'database', db: v.db }); toast('Копия создаётся…'); route(); },
    });
    return;
  }
  const sites = await api('sites.list');
  if (!sites.length) { toast('Нет сайтов для копирования', 'err'); return; }
  openForm({
    title: 'Копия файлов сайта', ok: 'Создать',
    fields: [
      { name: 'site', label: 'Сайт', type: 'select', options: [['*', 'Все сайты'], ...sites.map((s) => [s.site.domain, s.unicode])], value: '*' },
      { name: 'mode', label: 'Режим архивации', type: 'seg', value: 'full', options: [['full','Полная'],['incremental','Инкрементная']], hint: 'Инкрементная копия хранит изменения файлов. Первая копия — полная; далее не более шести инкрементных подряд; для восстановления нужна вся цепочка.' },
      { name: 'site_settings', label: 'Настройки сайта', type: 'checkbox', value: false, hint: 'Параметры сайта и свои правила nginx. При восстановлении сохраняются текущая папка и владелец сайта; SSL-сертификаты должны уже быть на сервере.' },
      { type: 'html', html: '<p class="muted" style="margin:0">Копия создаётся в фоне. Базы данных копируются отдельно, на вкладке «Базы данных».</p>' },
    ],
    submit: async (v) => { await api('backups.create', { ...v, kind: 'files' }); toast('Копия создаётся…'); route(); },
  });
}
// Скачивание копии базы — сразу дамп .sql.gz, без архива с манифестом.
async function backupDownload(r) {
  if (r.kind !== 'database') { location.href = '/api/backups/download?id=' + encodeURIComponent(r.id); return; }
  try {
    const res = await api('backups.extract', { id: r.id, dbs: [r.db] });
    watchJob(res.job, { onComplete: (j) => { if (j.status === 'done' && j.result?.download) location.href = j.result.download; } });
  } catch (e) { fail(e); }
}
// Содержимое копии (как в ISPmanager): файлы сайта по папкам и базы данных; выбранное можно
// скачать или восстановить. Восстанавливаются только отмеченные пути, остальные файлы сайта не меняются.
async function backupContents(r) {
  const m = modal(`<div class="backup-settings-head"><h3>Содержимое копии · ${h(r.site)}</h3><button class="btn" data-close>Закрыть</button></div><p class="muted">${h(new Date(r.created).toLocaleString(window.PanelLanguage?.locale || 'ru-RU'))}</p><div data-contents><div class="loading"><span class="spin"></span></div></div>`, { wide: true });
  const body = $('[data-contents]', m.el);
  const selected = new Map();
  let current = '';
  const key = (kind, value) => kind + ':' + value;
  const act = async (what) => {
    const paths = [...selected.values()].filter(x => x.kind === 'path').map(x => x.value);
    const dbs = [...selected.values()].filter(x => x.kind === 'db').map(x => x.value);
    if (!paths.length && !dbs.length) { toast('Отметьте файлы, папки или базы', 'err'); return; }
    if (what === 'download') {
      try {
        const res = await api('backups.extract', { id: r.id, paths, dbs });
        watchJob(res.job, { onComplete: (j) => { if (j.status === 'done' && j.result?.download) location.href = j.result.download; } });
      } catch (e) { fail(e); }
      return;
    }
    const list = [...paths.map(p => p === '' ? 'весь сайт' : '/' + p), ...dbs.map(d => 'база ' + d)];
    if (!await confirmBox('Восстановить выбранное?', 'Будет заменено содержимым копии: ' + list.map(h).join(', ') + '. Остальные файлы и базы не изменятся.', { ok: 'Восстановить' })) return;
    try { await api('backups.restore', { id: r.id, options: { paths, dbs } }); toast('Восстановлено', 'ok'); selected.clear(); load(current); } catch (e) { fail(e); }
  };
  const load = async (dir) => {
    current = dir;
    let d;
    try { d = await api('backups.browse', { id: r.id, path: dir }); } catch (e) { body.innerHTML = `<p class="err">${h(e.message)}</p>`; return; }
    if (!m.el.isConnected) return;
    const crumbs = [`<a href="#" data-dir="">${h(r.site)}</a>`, ...dir.split('/').filter(Boolean).map((part, i, all) => `<a href="#" data-dir="${h(all.slice(0, i + 1).join('/'))}">${h(part)}</a>`)].join(' / ');
    const check = (kind, value, label) => `<input type="checkbox" data-pick="${h(key(kind, value))}" data-kind="${kind}" data-value="${h(value)}" ${selected.has(key(kind, value)) ? 'checked' : ''} aria-label="${h(label)}">`;
    let html = `<div class="toolbar" style="margin:8px 0 12px"><button class="btn primary" data-act="restore">${icon('refresh')}Восстановить</button><button class="btn" data-act="download">${icon('download')}Скачать</button><span class="muted" data-count></span></div>`;
    if (d.needs_scan) html += `<div class="panel" style="margin-bottom:12px"><p>Список содержимого этой копии ещё не прочитан (копия подключена с FTP или создана до этой версии).</p><button class="btn" data-scan>${icon('search')}Прочитать архив</button></div>`;
    if (d.has_db && !dir && d.dbs.length) {
      html += `<h4 style="margin:8px 0">Базы данных</h4><div class="table-wrap"><table class="grid"><tbody>${d.dbs.map(db => `<tr><td class="chk">${check('db', db, db)}</td><td><span class="backup-item">${icon('database')}<b>${h(db)}</b></span></td></tr>`).join('')}</tbody></table></div>`;
    }
    if (d.has_files) {
      html += `<h4 style="margin:14px 0 8px">Файлы сайта</h4><div class="muted mono" style="margin-bottom:6px">${crumbs}</div><div class="table-wrap"><table class="grid"><thead><tr><th class="chk">${check('path', dir, dir ? 'Вся папка' : 'Весь сайт')}</th><th>${dir ? 'Вся папка /' + h(dir) : 'Весь сайт'}</th><th>Размер</th><th>Изменён</th></tr></thead><tbody>`;
      if (dir) { const up = dir.split('/').slice(0, -1).join('/'); html += `<tr><td></td><td><span class="backup-item"><a href="#" data-dir="${h(up)}">${icon('folder')}..</a></span></td><td></td><td></td></tr>`; }
      html += d.entries.map(e => { const p = dir ? dir + '/' + e.name : e.name; return `<tr><td class="chk">${check('path', p, p)}</td><td><span class="backup-item">${e.dir ? `<a href="#" data-dir="${h(p)}">${icon('folder')}${h(e.name)}</a>` : `${icon('file')}${h(e.name)}`}</span></td><td>${e.dir ? '' : fmtBytes(e.size)}</td><td>${e.modified ? new Date(e.modified * 1000).toLocaleString(window.PanelLanguage?.locale || 'ru-RU') : ''}</td></tr>`; }).join('') || (d.needs_scan ? '' : '<tr><td></td><td class="muted" colspan="3">Папка пуста</td><td></td></tr>');
      html += '</tbody></table></div>';
    }
    body.innerHTML = html;
    const count = () => { $('[data-count]', body).textContent = selected.size ? 'Выбрано: ' + selected.size : ''; };
    count();
    $$('[data-dir]', body).forEach(a => a.onclick = (e) => { e.preventDefault(); load(a.dataset.dir); });
    $$('[data-pick]', body).forEach(c => c.onchange = () => { if (c.checked) selected.set(c.dataset.pick, { kind: c.dataset.kind, value: c.dataset.value }); else selected.delete(c.dataset.pick); count(); });
    $$('[data-act]', body).forEach(b => b.onclick = () => act(b.dataset.act));
    const scan = $('[data-scan]', body);
    if (scan) scan.onclick = async () => { try { const res = await api('backups.scan', { id: r.id }); watchJob(res.job, { onComplete: () => load(current) }); } catch (e) { fail(e); } };
  };
  load('');
}

async function backupRestoreForm(r) {
  if (r.kind === 'database' || r.kind === 'panel') {
    const db = r.kind === 'database';
    if (!(await confirmBox(db ? 'Восстановить базу ' + r.db + '?' : 'Восстановить настройки панели?', db ? 'Текущее содержимое базы будет заменено копией от ' + fmtDate(r.created) + '. Файлы сайтов и другие базы не изменятся.' : 'Настройки панели будут заменены копией от ' + fmtDate(r.created) + '. Сайты, базы и учётная запись администратора не изменятся.', { ok: 'Восстановить' }))) return;
    try { await api('backups.restore', { id: r.id, options: db ? { databases: true } : { panel_settings: true } }); toast('Восстановлено', 'ok'); route(); } catch (e) { fail(e); }
    return;
  }
  openForm({title:'Восстановить ' + r.site + '?',ok:'Восстановить',fields:[
    {name:'files',label:'Файлы сайта',type:'checkbox',value:r.kind==='full'||r.kind==='files',show:()=>r.kind==='full'||r.kind==='files'},
    {name:'databases',label:'Базы данных',type:'checkbox',value:r.kind==='full'||r.kind==='db',show:()=>r.kind==='full'||r.kind==='db'},
    {name:'site_settings',label:'Настройки сайта',type:'checkbox',value:false,show:()=>!!r.site_settings},
    {name:'panel_settings',label:'Настройки панели',type:'checkbox',value:false,show:()=>!!r.panel_settings},
    {type:'html',html:'<p class="muted">Выбранные данные будут заменены содержимым копии. Настройки панели применяются ко всему серверу.</p>' + (r.parent_id ? '<p class="muted">Для восстановления панель автоматически соберёт полную копию и все последующие изменения. Все части цепочки должны быть доступны.</p>' : '')},
  ],submit:async(options)=>{await api('backups.restore',{id:r.id,options});toast('Восстановлено','ok');route();}});
}
async function backupDiscoverFTP() {
  if (!(await confirmBox('Найти копии в хранилище?', 'Панель прочитает сохранённую папку FTP или облака и добавит найденные архивы ITRUPANEL в список. Файлы в хранилище не изменяются. Для восстановления создайте сайт с тем же доменом; для инкрементных копий нужна вся цепочка. Первая новая копия будет полной.', {ok:'Начать поиск',danger:false}))) return;
  try { const r = await api('backups.ftp.discover'); watchJob(r.job); } catch (e) { fail(e); }
}
async function backupSettingsForm() {
  const m = modal(`<div class="backup-settings-head"><h3>Настройки резервных копий</h3><button class="btn" data-close>Закрыть</button></div><div data-backup-settings><div class="loading"><span class="spin"></span></div></div>`, {wide:true});
  const body = $('[data-backup-settings]', m.el);
  try {
    const [d, f] = await Promise.all([api('backups.list'), api('backups.ftp.get')]);
    if (!m.el.isConnected) return;
    body.innerHTML = '';
    const set = panel(body, 'Автоматические копии');
    const where = panel(body, 'Где хранить копии');
      const s = d.settings;
      set.innerHTML = `<div style="display:flex;gap:18px;align-items:center;flex-wrap:wrap"><label class="check"><input type="checkbox" id="bd" ${s.daily ? 'checked' : ''}><span>Каждый день создавать копии</span></label>
        <span class="muted">в</span><select id="bh" class="search-input" style="min-width:90px">${[...Array(24).keys()].map((x) => `<option ${x === s.hour ? 'selected' : ''} value="${x}">${String(x).padStart(2, '0')}:00</option>`).join('')}</select>
        <span class="muted">хранить</span><input id="bk" type="number" class="search-input" style="min-width:0;width:90px" min="1" max="60" step="1" value="${s.keep}"><span class="muted">последних</span></div><div class="field" style="margin-top:18px"><label for="bm">Режим архивации</label><select id="bm"><option value="full" ${s.mode!=='incremental' ? 'selected' : ''}>Полная</option><option value="incremental" ${s.mode==='incremental' ? 'selected' : ''}>Инкрементная</option></select><p class="hint">Режим относится к файлам сайтов. Сохраняются последние точки восстановления и все нужные им части цепочки. Первая копия — полная; далее не более шести инкрементных подряд. Базы данных сохраняются целиком.</p></div><div style="display:flex;gap:18px;flex-wrap:wrap;margin-top:18px"><label class="check"><input type="checkbox" checked disabled><span>Файлы всех сайтов</span></label><label class="check"><input type="checkbox" id="bdb" ${s.databases !== false ? 'checked' : ''}><span>Все базы данных, каждая отдельно</span></label><label class="check"><input type="checkbox" id="bss" ${s.site_settings ? 'checked' : ''}><span>Настройки сайтов</span></label><label class="check"><input type="checkbox" id="bps" ${s.panel_settings ? 'checked' : ''}><span>Настройки панели</span></label></div><p class="muted" style="margin:12px 0 0">Папка копий: <span class="mono">${h(d.dir)}</span></p>`;
      f.s3 = f.s3 || {};
      const presets = [
        ['', 'Другой S3-совместимый сервис', '', '', true, ''],
        ['aws', 'Amazon S3', 'https://s3.eu-central-1.amazonaws.com', 'eu-central-1', false, 'Замените eu-central-1 на регион вашего бакета — и в адресе, и в поле «Регион».'],
        ['yandex', 'Yandex Object Storage', 'https://storage.yandexcloud.net', 'ru-central1', true, ''],
        ['r2', 'Cloudflare R2', 'https://ACCOUNT_ID.r2.cloudflarestorage.com', 'auto', true, 'Замените ACCOUNT_ID на идентификатор аккаунта Cloudflare.'],
        ['wasabi', 'Wasabi', 'https://s3.wasabisys.com', 'us-east-1', true, ''],
        ['do', 'DigitalOcean Spaces', 'https://nyc3.digitaloceanspaces.com', 'nyc3', true, 'Замените nyc3 на регион вашего Space — и в адресе, и в поле «Регион».'],
      ];
      f.webdav = f.webdav || {}; f.dropbox = f.dropbox || {}; f.gdrive = f.gdrive || {};
      const services = [
        ['s3', 's3', 'S3-совместимое облако (Amazon S3, Yandex Object Storage, Cloudflare R2, Wasabi…)'],
        ['yandex', 'webdav', 'Яндекс.Диск'],
        ['mailru', 'webdav', 'Облако Mail.ru'],
        ['dav', 'webdav', 'Другой WebDAV (Nextcloud, ownCloud…)'],
        ['dropbox', 'dropbox', 'Dropbox'],
        ['gdrive', 'gdrive', 'Google Drive'],
      ];
      const davUrl = { yandex: 'https://webdav.yandex.ru', mailru: 'https://webdav.cloud.mail.ru' };
      const kindOfTarget = (tg) => String(tg || '').replace(/^local_/, '');
      const modeOfTarget = (tg) => ['local', 'ftp', 'both'].includes(tg) ? tg : (String(tg).startsWith('local_') ? 'local_cloud' : 'cloud');
      const svcOfTarget = (tg) => {
        const k = kindOfTarget(tg);
        if (k === 'webdav') return f.webdav.provider === 'yandex' ? 'yandex' : f.webdav.provider === 'mailru' ? 'mailru' : 'dav';
        return ['dropbox', 'gdrive'].includes(k) ? k : 's3';
      };
      const kindOfSvc = () => (services.find((x) => x[0] === f.svc) || services[0])[1];
      const targetNow = () => f.mode === 'cloud' ? kindOfSvc() : f.mode === 'local_cloud' ? 'local_' + kindOfSvc() : f.mode;
      const draw = (t) => {
        const ftpOn = t === 'ftp' || t === 'both', cloudOn = t === 'cloud' || t === 'local_cloud', svcKind = kindOfSvc();
        where.innerHTML = `<div class="seg" data-t>${[['local', 'На этом сервере'], ['ftp', 'На FTP-сервере'], ['both', 'Здесь и на FTP'], ['cloud', 'В облаке'], ['local_cloud', 'Здесь и в облаке']].map(([v, n]) => `<button data-v="${v}" class="${v === t ? 'on' : ''}">${n}</button>`).join('')}</div>
          <div data-ftp style="margin-top:18px;${ftpOn ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>FTP-сервер</label><input type="text" name="host" value="${h(f.ftp.host)}" placeholder="backup.example.ru"></div>
            <div class="field"><label>Порт</label><input type="number" name="port" value="${f.ftp.port || 21}"></div>
            <div class="field"><label>Логин</label><input type="text" name="user" value="${h(f.ftp.user)}"></div>
            <div class="field"><label>Пароль</label><input type="password" name="pass" value="${h(f.ftp.pass)}"></div>
            <div class="field"><label>Папка на FTP</label><input type="text" name="dir" value="${h(f.ftp.dir || '/itrupanel')}"></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="tls" ${f.ftp.tls ? 'checked' : ''}><span>Шифрование (FTPS)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px">«На FTP-сервере» — после загрузки локальный файл удаляется. «Здесь и на FTP» — хранятся обе копии.</p></div>
          <div data-cloud style="margin-top:18px;${cloudOn ? '' : 'display:none'}">
            <div class="field"><label>Облачный сервис</label><select data-svc>${services.map(([v, , n]) => `<option value="${v}" ${v === f.svc ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
          <div data-s3 style="${svcKind === 's3' ? '' : 'display:none'}">
            <div class="field"><label>Сервис S3</label><select data-preset>${presets.map(([v, n]) => `<option value="${v}">${n}</option>`).join('')}</select><div class="hint" data-preset-hint></div></div>
            <div class="cols2">
            <div class="field"><label>Адрес хранилища</label><input type="text" name="endpoint" value="${h(f.s3.endpoint || '')}" placeholder="https://s3.example.com" spellcheck="false"></div>
            <div class="field"><label>Регион</label><input type="text" name="region" value="${h(f.s3.region || 'us-east-1')}" spellcheck="false"></div>
            <div class="field"><label>Бакет</label><input type="text" name="bucket" value="${h(f.s3.bucket || '')}" spellcheck="false"></div>
            <div class="field"><label>Папка в бакете</label><input type="text" name="prefix" value="${h(f.s3.prefix === undefined ? 'itrupanel' : f.s3.prefix)}" spellcheck="false"></div>
            <div class="field"><label>Ключ доступа</label><input type="text" name="access_key" value="${h(f.s3.access_key || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>Секретный ключ</label><input type="password" name="secret_key" value="${h(f.s3.secret_key || '')}" autocomplete="new-password"></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="path_style" ${f.s3.path_style !== false ? 'checked' : ''}><span>Адрес вида сервер/бакет (path-style)</span></label></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="insecure" ${f.s3.insecure ? 'checked' : ''}><span>Не проверять сертификат (самоподписанный)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px">Бакет нужно создать заранее в личном кабинете облака, а ключу выдать права на чтение, запись и удаление объектов. «В облаке» — после загрузки локальный файл удаляется. «Здесь и в облаке» — хранятся обе копии.</p></div>
          <div data-webdav style="${svcKind === 'webdav' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>Адрес WebDAV</label><input type="text" name="url" value="${h(f.webdav.url || davUrl[f.svc] || '')}" ${davUrl[f.svc] ? 'readonly' : ''} placeholder="https://dav.example.com" spellcheck="false"></div>
            <div class="field"><label>Логин</label><input type="text" name="user" value="${h(f.webdav.user || '')}" autocomplete="off" spellcheck="false" placeholder="${f.svc === 'mailru' ? 'полный адрес почты' : f.svc === 'yandex' ? 'логин Яндекса' : ''}"></div>
            <div class="field"><label>Пароль приложения</label><input type="password" name="pass" value="${h(f.webdav.pass || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Папка в хранилище</label><input type="text" name="dir" value="${h(f.webdav.dir === undefined ? 'itrupanel' : f.webdav.dir)}" spellcheck="false"></div>
            <div class="field" ${f.svc === 'dav' ? '' : 'style="display:none"'}><label>&nbsp;</label><label class="check"><input type="checkbox" name="insecure" ${f.webdav.insecure ? 'checked' : ''}><span>Не проверять сертификат (самоподписанный)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px" data-webdav-hint></p></div>
          <div data-dropbox style="${svcKind === 'dropbox' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>Ключ приложения (App key)</label><input type="text" name="app_key" value="${h(f.dropbox.app_key || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>Секрет приложения (необязательно)</label><input type="password" name="app_secret" value="${h(f.dropbox.app_secret || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Папка в Dropbox</label><input type="text" name="dir" value="${h(f.dropbox.dir === undefined ? 'itrupanel' : f.dropbox.dir)}" spellcheck="false"></div>
            <div class="field"><label>Доступ</label><div data-connected>${f.dropbox.refresh_token ? '<span class="tag acc">Подключено</span> ' + h(f.dropbox.account || '') : '<span class="muted">не подключено</span>'}</div></div></div>
            <div style="margin-bottom:10px"><button class="btn" type="button" data-connect="dropbox">${f.dropbox.refresh_token ? 'Подключить заново' : 'Подключить Dropbox'}</button></div><div data-connect-box></div>
            <p class="muted" style="margin:0 0 14px">Создайте приложение на dropbox.com/developers/apps: «Scoped access» → «App folder», во вкладке «Permissions» отметьте files.metadata.read, files.content.read и files.content.write. Скопируйте «App key» сюда, нажмите «Подключить Dropbox» и разрешите доступ. Панель видит только свою папку внутри Apps/&lt;имя приложения&gt;.</p></div>
          <div data-gdrive style="${svcKind === 'gdrive' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>Идентификатор клиента (Client ID)</label><input type="text" name="client_id" value="${h(f.gdrive.client_id || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>Секрет клиента (Client secret)</label><input type="password" name="client_secret" value="${h(f.gdrive.client_secret || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Папка в Google Drive</label><input type="text" name="dir" value="${h(f.gdrive.dir === undefined ? 'itrupanel' : f.gdrive.dir)}" spellcheck="false"></div>
            <div class="field"><label>Доступ</label><div data-connected>${f.gdrive.refresh_token ? '<span class="tag acc">Подключено</span> ' + h(f.gdrive.account || '') : '<span class="muted">не подключено</span>'}</div></div></div>
            <div style="margin-bottom:10px"><button class="btn" type="button" data-connect="gdrive">${f.gdrive.refresh_token ? 'Подключить заново' : 'Подключить Google Drive'}</button></div><div data-connect-box></div>
            <p class="muted" style="margin:0 0 14px">В console.cloud.google.com создайте проект и включите «Google Drive API». В «OAuth consent screen» выберите «External» и переведите приложение в «In production» (иначе доступ перестанет действовать через 7 дней). Затем «Credentials» → «Create credentials» → «OAuth client ID», тип «TVs and Limited Input devices». Скопируйте Client ID и Client secret сюда и нажмите «Подключить Google Drive». Панель видит только созданные ею файлы.</p></div>
          </div>
          <div style="display:flex;gap:10px;margin-top:4px"><button class="btn primary" data-save>Сохранить</button><button class="btn" data-test ${t === 'local' ? 'hidden' : ''}>Проверить подключение</button></div>`;
        $$('[data-t] button', where).forEach((b) => (b.onclick = () => { f.mode = b.dataset.v; keep(); draw(b.dataset.v); }));
        const keep = () => {
          $$('[data-ftp] input', where).forEach((i) => (f.ftp[i.name] = i.type === 'checkbox' ? i.checked : i.type === 'number' ? +i.value : i.value));
          $$('[data-s3] input', where).forEach((i) => (f.s3[i.name] = i.type === 'checkbox' ? i.checked : i.value));
          $$('[data-webdav] input', where).forEach((i) => (f.webdav[i.name] = i.type === 'checkbox' ? i.checked : i.value));
          $$('[data-dropbox] input', where).forEach((i) => { if (i.name !== 'refresh_token') f.dropbox[i.name] = i.value; });
          $$('[data-gdrive] input', where).forEach((i) => { if (i.name !== 'refresh_token') f.gdrive[i.name] = i.value; });
          f.webdav.provider = f.svc === 'yandex' ? 'yandex' : f.svc === 'mailru' ? 'mailru' : 'custom';
        };
        const svcSel = $('[data-svc]', where);
        svcSel.onchange = () => { keep(); f.svc = svcSel.value; if (davUrl[f.svc]) f.webdav.url = davUrl[f.svc]; else if (kindOfSvc() === 'webdav' && davUrl[f.webdav.provider] === f.webdav.url) f.webdav.url = ''; draw(f.mode); };
        const davHints = { yandex: 'Создайте пароль приложения: id.yandex.ru → «Безопасность» → «Пароли приложений» → «Файлы (WebDAV)». Логин — логин Яндекса. Основной пароль не подойдёт.', mailru: 'Создайте пароль для внешнего приложения: Облако Mail.ru → «Настройки» → «Пароли для внешних приложений» → «Доступ к файлам по WebDAV». Логин — полный адрес почты.', dav: 'Укажите адрес WebDAV вашего сервера (например, Nextcloud: https://cloud.example.com/remote.php/dav/files/ЛОГИН). Рекомендуем пароль приложения.' };
        const dh = $('[data-webdav-hint]', where); if (dh) dh.textContent = (davHints[f.svc] || '') + ' «В облаке» — после загрузки локальный файл удаляется, «Здесь и в облаке» — хранятся обе копии.';
        $$('[data-connect]', where).forEach((btn) => (btn.onclick = async () => {
          keep();
          const kind = btn.dataset.connect, box = $('[data-connect-box]', btn.closest('[data-dropbox],[data-gdrive]'));
          const cfg = f[kind];
          const clientId = kind === 'dropbox' ? cfg.app_key : cfg.client_id, clientSecret = kind === 'dropbox' ? cfg.app_secret : cfg.client_secret;
          btn.disabled = true;
          const finishOk = async (r) => {
            const nf = await api('backups.ftp.get');
            f.dropbox = nf.dropbox; f.gdrive = nf.gdrive;
            toast('Подключено' + (r.account ? ': ' + r.account : ''), 'ok');
            draw(f.mode);
          };
          try {
            const r = await api('backups.cloud.oauth.start', { kind, client_id: clientId, client_secret: clientSecret });
            if (kind === 'dropbox') {
              box.innerHTML = `<div class="callout"><p>1. <a href="${h(r.url)}" target="_blank" rel="noopener">Откройте страницу Dropbox</a> и разрешите доступ.</p><p>2. Вставьте показанный там код:</p><div style="display:flex;gap:10px"><input type="text" data-code placeholder="Код из Dropbox" autocomplete="off" spellcheck="false"><button class="btn primary" type="button" data-confirm>Подтвердить</button></div></div>`;
              $('[data-confirm]', box).onclick = async () => {
                try { await finishOk(await api('backups.cloud.oauth.finish', { kind, state: r.state, code: $('[data-code]', box).value })); } catch (e) { fail(e); }
              };
            } else {
              box.innerHTML = `<div class="callout"><p>1. Откройте <a href="${h(r.url)}" target="_blank" rel="noopener">${h(r.url)}</a> и войдите в нужный аккаунт Google.</p><p>2. Введите код: <b style="font-size:20px;letter-spacing:2px">${h(r.user_code)}</b></p><p class="muted" data-wait><span class="spin" style="width:14px;height:14px;border-width:2px"></span> Ждём подтверждения…</p></div>`;
              const deadline = Date.now() + 15 * 60 * 1000;
              const poll = async () => {
                if (!m.el.isConnected || !box.isConnected) return;
                try {
                  const x = await api('backups.cloud.oauth.finish', { kind, state: r.state });
                  if (x.pending) { if (Date.now() < deadline) setTimeout(poll, Math.max(3, r.interval || 5) * 1000); else $('[data-wait]', box).textContent = 'Время ожидания истекло. Нажмите «Подключить Google Drive» ещё раз.'; return; }
                  await finishOk(x);
                } catch (e) { if (box.isConnected) $('[data-wait]', box).textContent = e.message; }
              };
              setTimeout(poll, Math.max(3, r.interval || 5) * 1000);
            }
          } catch (e) { fail(e); }
          finally { btn.disabled = false; }
        }));
        const preset = $('[data-preset]', where);
        preset.onchange = () => {
          const x = presets.find((q) => q[0] === preset.value);
          if (!x || !x[0]) { $('[data-preset-hint]', where).textContent = ''; return; }
          $('[name=endpoint]', where).value = x[2]; $('[name=region]', where).value = x[3]; $('[name=path_style]', where).checked = x[4];
          $('[data-preset-hint]', where).textContent = x[5];
        };
        const send = async (test) => {
          keep();
          const schedule = {daily:$('#bd',set).checked,hour:+$('#bh',set).value,keep:+$('#bk',set).value,site_settings:$('#bss',set).checked,panel_settings:$('#bps',set).checked,databases:$('#bdb',set).checked,mode:$('#bm',set).value};
          if (!test && !$('#bk',set).reportValidity()) return;
          const controls = $$('button, input, select',body);
          controls.forEach(control => { control.disabled = true; });
          try {
            const result = await api('backups.ftp.save', {target:targetNow(),ftp:f.ftp,s3:f.s3,webdav:f.webdav,dropbox:f.dropbox,gdrive:f.gdrive,test,...(!test ? {schedule} : {})});
            toast(test ? result : 'Сохранено','ok');
            if (!test) m.close();
          } catch (e) { fail(e); }
          finally { controls.forEach(control => { control.disabled = false; }); }
        };
        $('[data-save]', where).onclick = () => send(false);
        $('[data-test]', where).onclick = () => send(true);
      };
      f.target = f.target || 'local';
      f.mode = modeOfTarget(f.target);
      f.svc = svcOfTarget(f.target);
      draw(f.mode);
  } catch (e) {
    if (m.el.isConnected) { body.textContent = e.message; fail(e); }
  }
}
const backupTabs = [['files', 'Файлы сайтов'], ['db', 'Базы данных'], ['panel', 'Настройки панели']];
// Копия лежит в облаке (S3, WebDAV, Dropbox, Google Drive), если идентификатор хранилища начинается с s3-, dav-, dbx- или gdr-; иначе это FTP.
const backupInCloud = (r) => /^(s3|dav|dbx|gdr)-/.test(r.remote_source || '');
const backupTabOf = (r) => r.kind === 'database' ? 'db' : r.kind === 'panel' ? 'panel' : 'files';
page('backups', {
  title: 'Резервные копии',
  async render(view, params) {
    let tab = backupTabs.some(([v]) => v === params.tab) ? params.tab : (ls.get('backups-tab', 'files') || 'files');
    if (!backupTabs.some(([v]) => v === tab)) tab = 'files';
    head(view, 'Резервные копии', { actions: [{ t: 'Создать копию', i: 'plus', primary: true, fn: () => backupForm(tab) }, { t: 'Настройки', fn: backupSettingsForm }, { t: 'Найти копии в хранилище', i: 'search', fn: backupDiscoverFTP }] });
    const bar = document.createElement('div'); bar.className = 'toolbar'; bar.style.marginBottom = '12px';
    view.append(bar);
    const crumb = document.createElement('div'); crumb.className = 'backup-crumb'; crumb.hidden = true;
    view.append(crumb);
    const hint = document.createElement('p'); hint.className = 'hint'; hint.textContent = 'После подключения старого хранилища (FTP или облака) нажмите «Найти копии в хранилище». Проверяются манифесты; содержимое архивов проверяется перед восстановлением. Импортированные копии не удаляются по расписанию.';
    view.append(hint);
    const p = panel(view, 'Копии');
    let data = null;
    let sel = null; // открытый сайт, база или 'panel'; null — общий список
    const keyOf = (r) => r.kind === 'database' ? r.db : r.kind === 'panel' ? 'panel' : r.site;
    const allOfTab = () => [...(data.running || []).map(runningOf), ...(data.rows || [])].filter((r) => backupTabOf(r) === tab);
    const drawBar = () => {
      const count = (v) => data ? new Set([...(data.running || []).map(runningOf), ...(data.rows || [])].filter((r) => backupTabOf(r) === v).map(keyOf)).size : 0;
      bar.innerHTML = `<div class="seg">${backupTabs.map(([v, t]) => `<button type="button" data-tab="${v}" class="${v === tab ? 'on' : ''}">${t}${data ? ` <span class="muted">${count(v)}</span>` : ''}</button>`).join('')}</div>`;
      $$('[data-tab]', bar).forEach((b) => (b.onclick = () => { tab = b.dataset.tab; sel = null; try { ls.set('backups-tab', tab); } catch {} drawBar(); draw(); }));
    };
    const runningOf = (key) => key.startsWith('db:') ? { running: true, kind: 'database', db: key.slice(3) } : key === 'panel:' ? { running: true, kind: 'panel' } : { running: true, site: key };
    const spin = '<span class="tag acc"><span class="spin" style="width:12px;height:12px;border-width:2px"></span> создаётся</span>';
    const auto = (r) => r.auto ? ' <span class="tag">авто</span>' : '';
    const common = [
      { t: 'Размер', r: (r) => r.running ? '' : (r.size ? fmtBytes(r.size) : '—'), cls: 'nowrap' },
      { t: 'Хранится', r: (r) => r.running ? '' : [r.local !== false ? '<span class="tag">сервер</span>' : '', r.remote ? '<span class="tag acc">' + (backupInCloud(r) ? 'Облако' : 'FTP') + '</span>' : ''].join(' '), cls: 'hide-sm' },
      { t: 'Дата', r: (r) => r.running ? '' : fmtDate(r.created), cls: 'nowrap' },
      { t: 'Восстановление', r: (r) => r.running ? '' : (r.restore_issue ? '<span class="muted">'+h(r.restore_issue)+'</span>' : (r.imported ? (backupInCloud(r) ? 'Подключена из облака' : 'Подключена с FTP') : '—')) },
    ];
    const columns = {
      files: [
        { t: 'Сайт', r: (r) => `<b>${h(r.site)}</b>` },
        { t: 'Содержимое', r: (r) => r.running ? spin : ({ full: 'файлы и базы', files: 'файлы', db: 'базы', settings: 'настройки' }[r.kind] || '') + (r.site_settings ? ' <span class="tag">Настройки сайта</span>' : '') + (r.panel_settings ? ' <span class="tag">Настройки панели</span>' : '') + auto(r) },
        { t: 'Режим', r: (r) => r.running ? '' : (r.mode==='incremental' ? '<span class="tag acc">Инкрементная</span><br><small class="muted">Основа: ' + h(r.parent_id) + '</small>' : '<span class="tag">Полная</span>') },
        ...common,
      ],
      db: [
        { t: 'База данных', r: (r) => `<span class="backup-item">${icon('database')}<b>${h(r.db)}</b></span>` + (r.running ? ' ' + spin : auto(r)) },
        { t: 'Сайт', r: (r) => h((data.db_sites || {})[r.db] || '—'), cls: 'hide-sm' },
        ...common,
      ],
      panel: [
        { t: 'Копия', r: (r) => r.running ? spin : 'Настройки панели' + auto(r) },
        ...common,
      ],
    };
    const groupCols = {
      files: [{ t: 'Сайт', r: (g) => `<span class="backup-item"><b>${h(g.key)}</b></span>` }],
      db: [{ t: 'База данных', r: (g) => `<span class="backup-item">${icon('database')}<b>${h(g.key)}</b></span>` }, { t: 'Сайт', r: (g) => h((data.db_sites || {})[g.key] || '—'), cls: 'hide-sm' }],
      panel: [{ t: 'Что копируется', r: () => '<b>Настройки панели</b>' }],
    };
    const summary = [
      { t: 'Копий', r: (g) => String(g.rows.length) + (g.running ? ' ' + spin : ''), cls: 'nowrap' },
      { t: 'Последняя', r: (g) => g.rows.length ? fmtDate(g.rows[0].created) : '', cls: 'nowrap' },
      { t: 'Общий размер', r: (g) => g.size ? fmtBytes(g.size) : '—', cls: 'nowrap hide-sm' },
      { t: 'Хранится', r: (g) => [g.local ? '<span class="tag">сервер</span>' : '', g.ftp ? '<span class="tag acc">FTP</span>' : '', g.cloud ? '<span class="tag acc">Облако</span>' : ''].join(' '), cls: 'hide-sm' },
    ];
    const groups = () => {
      const m = new Map();
      for (const r of allOfTab()) {
        const k = keyOf(r);
        if (!m.has(k)) m.set(k, { key: k, rows: [], size: 0, local: false, ftp: false, cloud: false, running: false });
        const g = m.get(k);
        if (r.running) { g.running = true; continue; }
        g.rows.push(r);
        g.size += r.size || 0;
        if (r.local !== false) g.local = true;
        if (r.remote) { if (backupInCloud(r)) g.cloud = true; else g.ftp = true; }
      }
      for (const g of m.values()) g.rows.sort((a, b) => Date.parse(b.created) - Date.parse(a.created));
      return [...m.values()].sort((a, b) => a.key.localeCompare(b.key));
    };
    const draw = () => {
      if (!data) return;
      const all = groups();
      if (sel !== null && !all.some((g) => g.key === sel)) sel = null;
      if (sel === null) {
        crumb.hidden = true;
        table(p, {
          rows: all,
          empty: { icon: 'backup', title: tab === 'db' ? 'Копий баз пока нет' : tab === 'panel' ? 'Копий настроек панели пока нет' : 'Копий пока нет' },
          columns: [...groupCols[tab], ...summary],
          onRow: (g) => { sel = g.key; draw(); },
          actions: (g) => [{ i: 'folder', t: 'Показать копии', fn: () => { sel = g.key; draw(); } }],
        });
        return;
      }
      const g = all.find((x) => x.key === sel);
      const what = tab === 'panel' ? '<b>Настройки панели</b>' : `<span class="muted">${tab === 'db' ? 'База' : 'Сайт'}</span> <b>${h(sel)}</b>`;
      crumb.hidden = false;
      crumb.innerHTML = `<button type="button" class="btn small" data-back>← <span>${tab === 'db' ? 'Все базы' : tab === 'panel' ? 'Назад' : 'Все сайты'}</span></button>${what}<span class="muted">копий:</span> <span>${g.rows.length}</span>`;
      $('[data-back]', crumb).onclick = () => { sel = null; draw(); };
      const running = (data.running || []).map(runningOf).filter((r) => backupTabOf(r) === tab && keyOf(r) === sel);
      table(p, {
        rows: [...running, ...g.rows],
        empty: { icon: 'backup', title: 'Копий нет' },
        columns: columns[tab].slice(tab === 'panel' ? 0 : 1),
        actions: (r) => r.running ? [] : [
          backupIsBrowsable(r) && { i: 'folder', t: 'Содержимое', fn: () => backupContents(r) },
          { i: 'download', t: 'Скачать', fn: () => backupDownload(r) },
          !r.restore_issue && { i: 'refresh', t: 'Восстановить', fn: () => backupRestoreForm(r) },
          { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить копию?', h(r.kind === 'database' ? 'база ' + r.db : r.kind === 'panel' ? 'настройки панели' : r.site) + ', ' + fmtDate(r.created)))) return; try { await api('backups.delete', { id: r.id }); load(); } catch (e) { fail(e); } } },
        ],
      });
    };
    const load = async () => {
      const d = await api('backups.list');
      if (!view.isConnected) return false;
      data = d;
      drawBar();
      draw();
      return (d.running || []).length > 0;
    };
    drawBar();
    await load();
    every(5000, () => { if (data && (data.running || []).length) load(); }, view);
    if (params.new && view.isConnected) backupForm(tab);
  },
});
const backupIsBrowsable = (r) => r.kind !== 'database' && r.kind !== 'panel';

// ================= Менеджер файлов =================
function pathCrumbs(path) {
  const parts = path.split('/').filter(Boolean);
  return '<a href="#" data-p="/" aria-label="Корень">/</a>' + parts.map((name, i) => `<a href="#" data-p="/${h(parts.slice(0, i + 1).join('/'))}">${h(name)}</a>`).join('<span class="s">/</span>');
}

function folderDestination({ title, path, action, select }) {
  let selected = null, request = 0, current = path;
  const f = openForm({ title, ok: action + ' сюда', fields: [{ type: 'html', html: `<div class="folder-picker"><label>Папка назначения</label><div class="folder-address"><input type="text" data-folder-path aria-label="Путь папки назначения" spellcheck="false"><button class="btn" type="button" data-folder-go>Перейти</button></div><div class="folder-navigation"><button class="btn small" type="button" data-folder-up>↑ Наверх</button><nav class="crumbs" aria-label="Папка назначения"></nav></div><div class="folder-list" aria-live="polite"></div><p class="hint">Откройте нужную папку и нажмите «${h(action)} сюда».</p></div>` }], submit: async () => {
    if (selected == null) throw new Error('Сначала выберите доступную папку');
    await select(selected);
  } });
  const ok = $('[data-ok]', f.el), input = $('[data-folder-path]', f.el), list = $('.folder-list', f.el);
  const load = async (target) => {
    const id = ++request;
    selected = null; ok.disabled = true;
    input.value = target;
    list.innerHTML = '<div class="loading"><span class="spin"></span></div>';
    try {
      const d = await api('files.list', { path: target });
      if (id !== request || !f.el.isConnected) return;
      current = selected = d.path;
      input.value = current;
      $('.crumbs', f.el).innerHTML = pathCrumbs(current);
      $$('.crumbs a', f.el).forEach((a) => a.onclick = (e) => { e.preventDefault(); load(a.dataset.p); });
      $('[data-folder-up]', f.el).disabled = current === '/';
      const folders = d.rows.filter((row) => row.dir);
      list.innerHTML = folders.length ? folders.map((row, i) => `<button type="button" class="folder-choice" data-folder="${i}">${icon('folder')}<span>${h(row.name)}</span><span class="muted">›</span></button>`).join('') : '<p class="muted">Вложенных папок нет. Можно выбрать текущую папку.</p>';
      $$('[data-folder]', list).forEach((b) => b.onclick = () => load((current === '/' ? '' : current) + '/' + folders[+b.dataset.folder].name));
      ok.disabled = false;
    } catch (e) {
      if (id !== request || !f.el.isConnected) return;
      list.innerHTML = `<p class="folder-error">${h(e.message)}</p><button type="button" class="btn small" data-retry>Повторить</button>`;
      $('[data-retry]', list).onclick = () => load(target);
    }
  };
  $('[data-folder-go]', f.el).onclick = () => load(input.value);
  input.oninput = () => { ++request; selected = null; ok.disabled = true; list.innerHTML = '<p class="muted">Нажмите «Перейти», чтобы открыть введённый путь.</p>'; };
  input.onkeydown = (e) => { if (e.key === 'Enter') { e.preventDefault(); load(input.value); } };
  $('[data-folder-up]', f.el).onclick = () => load(current.split('/').slice(0, -1).join('/') || '/');
  load(path);
  return f;
}

const TEXT_EXT = /\.(php|html?|css|js|mjs|json|txt|md|xml|ya?ml|ini|conf|htaccess|env|sh|py|rb|go|sql|csv|log|svg|twig|tpl|vue|ts|jsx|tsx|lock|gitignore|example)$/i;
// Корзина менеджера файлов: восстановление, удаление навсегда, очистка и переключатель.
async function recycleBin(onChange) {
  const m = modal(`<div class="backup-settings-head"><h3>Корзина</h3><button class="btn" data-close>Закрыть</button></div><div data-bin><div class="loading"><span class="spin"></span></div></div>`, { wide: true });
  const body = $('[data-bin]', m.el);
  const picked = new Set();
  const load = async () => {
    let d;
    try { d = await api('files.recycle.list'); } catch (e) { body.innerHTML = `<p class="err">${h(e.message)}</p>`; return; }
    if (!m.el.isConnected) return;
    picked.clear();
    body.innerHTML = `<div class="toolbar" style="margin-bottom:12px"><label class="check"><input type="checkbox" data-enabled ${d.enabled ? 'checked' : ''}><span>Перемещать удалённые файлы в корзину</span></label><span class="grow"></span><span class="muted">${d.items.length} · ${fmtBytes(d.total)}</span></div>
      <div class="toolbar" style="margin-bottom:12px"><button class="btn primary" data-restore disabled>${icon('refresh')}Восстановить</button><button class="btn danger" data-delete disabled>${icon('trash')}Удалить навсегда</button><span class="grow"></span><button class="btn danger" data-empty ${d.items.length ? '' : 'disabled'}>Очистить корзину</button></div>
      ${d.items.length ? `<div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all aria-label="Выбрать все"></th><th>Имя</th><th>Откуда удалён</th><th>Размер</th><th>Удалён</th></tr></thead><tbody>${d.items.map((it) => `<tr><td class="chk"><input type="checkbox" data-id="${h(it.id)}" aria-label="${h(it.name)}"></td><td><span class="fname ${it.dir ? 'dir' : ''}">${icon(it.dir ? 'folder' : 'file')}${h(it.name)}</span></td><td class="mono">${h(it.path)}</td><td class="nowrap">${fmtBytes(it.size)}</td><td class="nowrap">${fmtDate(it.deleted)}</td></tr>`).join('')}</tbody></table></div>` : `<div class="empty"><div class="circle">${icon('trash')}</div><b>Корзина пуста</b>${d.enabled ? 'Удалённые файлы появятся здесь' : 'Корзина выключена: файлы удаляются сразу'}</div>`}`;
    const boxes = $$('[data-id]', body);
    const sync = () => { $('[data-restore]', body).disabled = $('[data-delete]', body).disabled = !picked.size; };
    boxes.forEach((b) => (b.onchange = () => { b.checked ? picked.add(b.dataset.id) : picked.delete(b.dataset.id); sync(); }));
    const all = $('[data-all]', body);
    if (all) all.onchange = () => { boxes.forEach((b) => { b.checked = all.checked; b.checked ? picked.add(b.dataset.id) : picked.delete(b.dataset.id); }); sync(); };
    $('[data-enabled]', body).onchange = async (e) => { try { await api('files.recycle.settings', { enabled: e.target.checked }); toast(e.target.checked ? 'Корзина включена' : 'Корзина выключена: файлы будут удаляться сразу', 'ok'); } catch (err) { fail(err); e.target.checked = !e.target.checked; } };
    $('[data-restore]', body).onclick = async () => { try { const r = await api('files.recycle.restore', { ids: [...picked] }); toast(r.info, 'ok'); if (onChange) onChange(); load(); } catch (e) { fail(e); } };
    $('[data-delete]', body).onclick = async () => { if (!(await confirmBox('Удалить навсегда?', 'Выбранные объекты (' + picked.size + ') будут удалены без возможности восстановления.'))) return; try { await api('files.recycle.delete', { ids: [...picked] }); load(); } catch (e) { fail(e); } };
    $('[data-empty]', body).onclick = async () => { if (!(await confirmBox('Очистить корзину?', 'Все объекты в корзине (' + d.items.length + ', ' + fmtBytes(d.total) + ') будут удалены навсегда.'))) return; try { await api('files.recycle.empty'); toast('Корзина очищена', 'ok'); load(); } catch (e) { fail(e); } };
  };
  load();
}

page('files', {
  title: 'Менеджер файлов',
  async render(view, params) {
    let path = params.path || ls.get('files.path', '/var/www');
    let rows = [];
    const sel = new Set();
    head(view, 'Менеджер файлов');
    const box = document.createElement('div');
    box.className = 'panel dropzone file-manager';
    view.append(box);
    box.innerHTML = `<div class="crumbs"></div><div class="toolbar file-toolbar">
      <button class="btn" data-act="up" title="Наверх">${icon('chevron')}</button>
      <button class="btn" data-act="mkdir">${icon('folder')}Папка</button>
      <button class="btn" data-act="mkfile">${icon('file')}Файл</button>
      <button class="btn" data-act="upload">${icon('upload')}Загрузить</button>
      <span class="grow"></span>
      <button class="btn" data-act="download" data-need="1" title="Скачать" aria-label="Скачать">${icon('download')}</button>
      <button class="btn" data-act="rename" data-need="1">${icon('edit')}Переименовать</button>
      <button class="btn" data-act="copy" data-need="n">${icon('copy')}Копировать</button>
      <button class="btn" data-act="move" data-need="n">Переместить</button>
      <button class="btn" data-act="chmod" data-need="n">Права</button>
      <button class="btn" data-act="zip" data-need="n">${icon('archive')}Архив</button>
      <button class="btn" data-act="unzip" data-need="1">Распаковать</button>
      <button class="btn danger" data-act="del" data-need="n" title="Удалить" aria-label="Удалить">${icon('trash')}</button>
      <button class="btn" data-act="recycle" title="Корзина">${icon('trash')}Корзина</button>
    </div><input type="file" multiple hidden><div class="list"></div>`;
    const list = $('.list', box);
    const fileIn = $('input[type=file]', box);
    const full = (n) => (path === '/' ? '' : path) + '/' + n;
    const upd = () => $$('[data-need]', box).forEach((b) => (b.disabled = b.dataset.need === '1' ? sel.size !== 1 : sel.size === 0));

    const load = async (p) => {
      try {
        const d = await api('files.list', { path: p });
        path = d.path; rows = d.rows; sel.clear();
        ls.set('files.path', path);
      } catch (e) { fail(e); if (p !== '/var/www') return load('/var/www'); }
      $('.crumbs', box).innerHTML = pathCrumbs(path);
      $$('.crumbs a', box).forEach((a) => (a.onclick = (e) => { e.preventDefault(); load(a.dataset.p); }));
      draw();
    };
    const draw = () => {
      if (!rows.length) { list.innerHTML = `<div class="empty"><div class="circle">${icon('folder')}</div><b>Папка пуста</b>Перетащите файлы сюда, чтобы загрузить</div>`; upd(); return; }
      list.innerHTML = `<div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all></th><th>Имя</th><th>Размер</th><th class="hide-sm">Права</th><th class="hide-sm">Владелец</th><th class="hide-sm">Изменён</th><th class="actions fm-more"></th></tr></thead><tbody>${rows.map((r, i) => `<tr data-i="${i}" class="${sel.has(r.name) ? 'sel' : ''}"><td class="chk"><input type="checkbox" ${sel.has(r.name) ? 'checked' : ''}></td><td><span class="fname ${r.dir ? 'dir' : ''}">${icon(r.dir ? 'folder' : 'file')}${h(r.name)}${r.link ? ' <span class="tag">ссылка</span>' : ''}</span></td><td class="nowrap">${r.dir ? '' : fmtBytes(r.size)}</td><td class="mono hide-sm">${h(r.mode)}</td><td class="hide-sm">${h(r.owner)}</td><td class="nowrap hide-sm">${fmtDate(r.mtime)}</td><td class="actions fm-more"><button class="btn small icon-only" data-more title="Действия" aria-label="Действия с ${h(r.name)}">${icon('more')}</button></td></tr>`).join('')}</tbody></table></div>`;
      $$('tbody tr', list).forEach((tr) => {
        const r = rows[tr.dataset.i];
        $('input', tr).onchange = (e) => { e.target.checked ? sel.add(r.name) : sel.delete(r.name); tr.classList.toggle('sel', e.target.checked); upd(); };
        $('.fname', tr).onclick = () => (r.dir ? load(full(r.name)) : openFile(full(r.name), r));
        $('[data-more]', tr).onclick = (e) => { e.stopPropagation(); const b = e.currentTarget.getBoundingClientRect(); rowMenu(r, b.right, b.bottom); };
        tr.addEventListener('contextmenu', (e) => { e.preventDefault(); rowMenu(r, e.clientX, e.clientY); });
      });
      $('[data-all]', list).onchange = (e) => { rows.forEach((r) => (e.target.checked ? sel.add(r.name) : sel.delete(r.name))); draw(); };
      upd();
    };
    const openFile = async (p, r) => {
      if (!TEXT_EXT.test(p) && r.size > 0 && !/^[^.]+$/.test(r.name)) { location.href = '/api/files/download?path=' + encodeURIComponent(p); return; }
      try {
        const d = await api('files.read', { path: p });
        let ed = null;
        try { await loadScript('/vendor/codemirror.js'); if (window.ITRUEditor) ed = true; } catch { ed = null; }
        const useCM = !!ed;
        ed = null;
        const barHtml = useCM
          ? `<div class="ed-bar"><span class="muted mono ed-path">${h(p)}</span><span class="grow"></span><span class="tag warn" data-dirty hidden>не сохранено</span><select data-lang aria-label="Язык подсветки"></select><label class="check ed-wrap"><input type="checkbox" data-wrap><span>Перенос строк</span></label><button class="btn small" type="button" data-find>${icon('search')}Поиск</button></div><div class="cm-host"></div><div class="ed-status muted mono" data-status></div>`
          : `<div class="muted mono" style="margin-bottom:8px">${h(p)}</div><textarea class="editor" spellcheck="false">${h(d.text)}</textarea>`;
        openForm({ title: p.split('/').pop(), wide: true, ok: 'Сохранить', fields: [{ type: 'html', html: barHtml }], submit: async () => { await api('files.write', { path: p, text: ed ? ed.getText() : $('.editor').value }); if (ed) ed.markSaved(); toast('Сохранено', 'ok'); return false; } });
        if (useCM) {
          const root = $('.drawer');
          $('.drawer-body', root).classList.add('ed-body');
          const dark = document.documentElement.dataset.theme === 'dark' || (!document.documentElement.dataset.theme && matchMedia('(prefers-color-scheme: dark)').matches);
          const phrases = { 'Find': 'Найти', 'Replace': 'Заменить', 'next': 'дальше', 'previous': 'назад', 'all': 'все', 'match case': 'с учётом регистра', 'regexp': 'регулярное выражение', 'by word': 'слово целиком', 'replace': 'заменить', 'replace all': 'заменить все', 'close': 'закрыть', 'Go to line': 'Перейти к строке', 'go': 'перейти', 'current match': 'текущее совпадение', 'on line': 'в строке', 'replaced $ matches': 'заменено совпадений: $', 'replaced match on line $': 'заменено совпадение в строке $', 'Folded lines': 'Свёрнутые строки', 'Unfolded lines': 'Развёрнутые строки', 'folded code': 'свёрнутый код', 'unfold': 'развернуть', 'Fold line': 'Свернуть строку', 'Unfold line': 'Развернуть строку' };
          const status = $('[data-status]', root), dirtyTag = $('[data-dirty]', root), langSel = $('[data-lang]', root);
          ed = window.ITRUEditor.create($('.cm-host', root), {
            text: d.text, filename: p, dark, phrases,
            onChange: (dirty) => { dirtyTag.hidden = !dirty; },
            onSave: () => $('.drawer [data-ok]').click(),
            onStatus: (st) => { status.textContent = `Строка ${st.line}, столбец ${st.col} · строк ${st.lines}`; },
          });
          langSel.innerHTML = ['', ...window.ITRUEditor.languages].map((l) => `<option value="${h(l)}">${l ? h(l) : 'Обычный текст'}</option>`).join('');
          langSel.value = ed.language;
          langSel.onchange = () => ed.setLanguage(langSel.value);
          $('[data-wrap]', root).onchange = (e) => ed.setWrap(e.target.checked);
          $('[data-find]', root).onclick = () => { ed.find(); };
          root.addEventListener('keydown', (e) => {
            // Ctrl/Cmd+S работает и в панели поиска; если редактор уже обработал нажатие, второй раз не сохраняем
            if (!e.defaultPrevented && (e.metaKey || e.ctrlKey) && String(e.key).toLowerCase() === 's') { e.preventDefault(); $('[data-ok]', root).click(); }
          });
          ed.focus();
        } else {
          const tx = $('.editor');
          tx.addEventListener('keydown', (e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); $('.drawer [data-ok]').click(); }
            if (e.key === 'Tab') { e.preventDefault(); const st = tx.selectionStart; tx.setRangeText('    ', st, tx.selectionEnd, 'end'); }
          });
          tx.focus();
        }
      } catch (e) { if (/текстовый|3 МБ/.test(e.message)) location.href = '/api/files/download?path=' + encodeURIComponent(p); else fail(e); }
    };
    const upload = (files) => {
      const destination = path;
      return uploadFiles(files, destination, () => {
        if (view.isConnected && path === destination) return load(destination);
      });
    };
    fileIn.onchange = () => { const files = Array.from(fileIn.files); fileIn.value = ''; upload(files); };
    box.addEventListener('dragover', (e) => { e.preventDefault(); box.classList.add('drag'); });
    box.addEventListener('dragleave', (e) => { if (!box.contains(e.relatedTarget)) box.classList.remove('drag'); });
    box.addEventListener('drop', (e) => { e.preventDefault(); box.classList.remove('drag'); upload(e.dataTransfer.files); });

    const rowMenu = (r, x, y) => {
      if (!sel.has(r.name)) { sel.clear(); sel.add(r.name); draw(); }
      const many = sel.size > 1;
      const archive = /\.(zip|tar|tgz|tar\.gz|tar\.bz2|tar\.xz|gz)$/i.test(r.name);
      const items = [
        !many && { t: r.dir ? 'Открыть' : (TEXT_EXT.test(r.name) ? 'Редактировать' : 'Открыть'), i: r.dir ? 'folder' : 'edit', fn: () => (r.dir ? load(full(r.name)) : openFile(full(r.name), r)) },
        !many && !r.dir && { t: 'Скачать', i: 'download', fn: acts.download },
        '-',
        { t: 'Копировать', i: 'copy', fn: acts.copy },
        { t: 'Переместить (вырезать)', i: 'external', fn: acts.move },
        !many && { t: 'Переименовать', i: 'edit', fn: acts.rename },
        { t: 'Права и владелец', i: 'key', fn: acts.chmod },
        '-',
        { t: 'Сжать в архив', i: 'archive', fn: acts.zip },
        !many && archive && { t: 'Распаковать', i: 'archive', fn: acts.unzip },
        !many && { t: 'Скопировать путь', i: 'copy', fn: () => copyText(full(r.name)) },
        '-',
        { t: many ? 'Удалить выбранное (' + sel.size + ')' : 'Удалить', i: 'trash', danger: true, fn: acts.del },
      ].filter(Boolean);
      contextMenu(items, x, y);
    };
    const selPaths = () => [...sel].map(full);
    const one = () => [...sel][0];
    const acts = {
      up: () => load(path.split('/').slice(0, -1).join('/') || '/'),
      mkdir: () => openForm({ title: 'Новая папка', ok: 'Создать', fields: [{ name: 'name', label: 'Имя', required: true }], submit: async (v) => { await api('files.mkdir', { path, name: v.name }); load(path); } }),
      mkfile: () => openForm({ title: 'Новый файл', ok: 'Создать', fields: [{ name: 'name', label: 'Имя', required: true, placeholder: 'index.php' }], submit: async (v) => { await api('files.create', { path, name: v.name }); load(path); } }),
      upload: () => fileIn.click(),
      download: () => (location.href = '/api/files/download?path=' + encodeURIComponent(full(one()))),
      rename: () => openForm({ title: 'Переименовать', fields: [{ name: 'to', label: 'Новое имя', value: one(), required: true }], submit: async (v) => { await api('files.rename', { path: full(one()), to: v.to }); load(path); } }),
      copy: () => { const paths = selPaths(); folderDestination({ title: 'Копировать ' + plural(paths.length, 'объект', 'объекта', 'объектов'), path, action: 'Копировать', select: async (to) => { await api('files.copy', { paths, to }); toast('Скопировано', 'ok'); load(path); } }); },
      move: () => { const paths = selPaths(); folderDestination({ title: 'Переместить ' + plural(paths.length, 'объект', 'объекта', 'объектов'), path, action: 'Переместить', select: async (to) => { for (const p of paths) await api('files.rename', { path: p, to: to.replace(/\/$/, '') + '/' + p.split('/').pop() }); toast('Перемещено', 'ok'); load(path); } }); },
      chmod: () => { const r = rows.find((x) => x.name === one()); openForm({ title: 'Права доступа', fields: [{ name: 'mode', label: 'Права (восьмеричные)', value: r ? r.mode : '0644', placeholder: '0644' }, { name: 'owner', label: 'Владелец (пусто — не менять)', value: '' }, { name: 'recursive', label: 'Для вложенных файлов и папок', type: 'checkbox' }], submit: async (v) => { await api('files.chmod', { paths: selPaths(), mode: v.mode, owner: v.owner, recursive: v.recursive }); load(path); } }); },
      zip: () => openForm({ title: 'Создать архив', ok: 'Создать', fields: [{ name: 'name', label: 'Имя архива', value: (sel.size === 1 ? one() : 'archive') + '.zip', hint: '.zip или .tar.gz' }], submit: async (v) => { await api('files.compress', { paths: selPaths(), name: v.name }); toast('Архив создан', 'ok'); load(path); } }),
      unzip: async () => { toast('Распаковываю…'); try { await api('files.extract', { path: full(one()) }); toast('Распаковано', 'ok'); load(path); } catch (e) { fail(e); } },
      del: async () => {
        const inBin = path === '/var/www/.itrupanel-recycle' || path.startsWith('/var/www/.itrupanel-recycle/');
        const bin = inBin ? null : await api('files.recycle.list').catch(() => null);
        const toBin = bin && bin.enabled;
        const v = await confirmBox('Удалить ' + plural(sel.size, 'объект', 'объекта', 'объектов') + '?', h([...sel].slice(0, 8).join(', ')) + (sel.size > 8 ? '…' : '') + (toBin ? '<br>Файлы будут перенесены в корзину — их можно восстановить.' : '<br>Файлы будут удалены навсегда.'), { extra: toBin ? '<label class="check"><input type="checkbox" name="permanent"><span>Удалить навсегда, минуя корзину</span></label>' : '' });
        if (!v) return;
        try { const r = await api('files.delete', { paths: selPaths(), permanent: !!v.permanent }); if (r && r.info) toast(r.info, 'ok'); load(path); } catch (e) { fail(e); }
      },
      recycle: () => recycleBin(() => load(path)),
    };
    $$('[data-act]', box).forEach((b) => (b.onclick = () => acts[b.dataset.act]()));
    await load(path);
  },
});

// ================= IP-адреса =================
page('ips', {
  title: 'IP-адреса',
  async render(view) {
    head(view, 'IP-адреса');
    const p = panel(view);
    const rows = await api('system.ips');
    table(p, {
      rows: rows || [],
      empty: { icon: 'ip', title: 'Адресов не найдено' },
      columns: [
        { t: 'IP-адрес', r: (r) => `<b class="mono" style="font-size:15px">${h(r.ip)}</b><span class="muted">/${r.prefix}</span>` },
        { t: 'Версия', r: (r) => `<span class="tag">${r.version}</span>` },
        { t: 'Тип', r: (r) => `<span class="tag ${r.scope === 'публичный' ? 'acc' : ''}">${h(r.scope)}</span>` },
        { t: 'Интерфейс', r: (r) => h(r.iface) + (r.up ? '' : ' <span class="tag err">выключен</span>') },
      ],
      actions: (r) => [{ i: 'copy', t: 'Скопировать', fn: () => { navigator.clipboard?.writeText(r.ip); toast('Скопировано'); } }],
    });
  },
});

// ================= FTP =================
async function ftpForm() {
  const users = await api('users.list');
  if (!users.length) { toast('Сначала создайте сайт — FTP-пользователь привязывается к его владельцу', 'err'); return; }
  openForm({
    title: 'Создать FTP-пользователя', ok: 'Создать',
    fields: [
      { name: 'owner', label: 'Владелец (системный пользователь)', type: 'select', options: users.map((u) => [u.name, u.name]), value: users[0].name, hint: 'Файлы, загруженные по FTP, будут принадлежать ему' },
      { name: 'name', label: 'Имя FTP-пользователя', required: true, placeholder: 'ftp_site' },
      { name: 'password', label: 'Пароль', type: 'password', value: genPass(14), gen: true },
      { name: 'home', label: 'Домашняя папка', placeholder: '/var/www/<владелец> — по умолчанию', hint: 'Пользователь видит только эту папку' },
    ],
    submit: async (v) => {
      const r = await api('ftp.create', v);
      showSecrets('FTP-пользователь создан', [['Сервер', location.hostname], ['Порт', '21'], ['Логин', v.name], ['Пароль', r.password]]);
      route();
    },
  });
}
page('ftp', {
  title: 'FTP-пользователи',
  async render(view, params) {
    head(view, 'FTP-пользователи', { actions: [{ t: 'Создать FTP-пользователя', i: 'plus', primary: true, fn: ftpForm }] });
    const p = panel(view);
    const d = await api('ftp.list');
    if (!d.active) p.insertAdjacentHTML('beforebegin', '<p><span class="tag warn">FTP-сервер не запущен — проверьте «Информация о сервере»</span></p>');
    table(p, {
      rows: d.rows || [],
      empty: { icon: 'ftp', title: 'FTP-пользователей нет' },
      columns: [
        { t: 'Логин', r: (r) => `<b>${h(r.name)}</b>` },
        { t: 'Владелец', r: (r) => h(r.owner) },
        { t: 'Папка', r: (r) => `<span class="mono">${h(r.home)}</span>`, cls: 'hide-sm' },
        { t: 'Создан', r: (r) => fmtDate(r.created, false), cls: 'hide-sm' },
      ],
      actions: (r) => [
        { i: 'key', t: 'Сменить пароль', fn: () => openForm({ title: 'Пароль ' + r.name, ok: 'Сменить', fields: [{ name: 'password', label: 'Новый пароль', type: 'password', value: genPass(14), gen: true }], submit: async (v) => { const x = await api('ftp.password', { name: r.name, password: v.password }); showSecrets('Пароль изменён', [['Логин', r.name], ['Пароль', x.password]]); } }) },
        { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить ' + r.name + '?', 'Файлы останутся на месте.'))) return; try { await api('ftp.delete', { name: r.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (params.new && view.isConnected) ftpForm();
  },
});

// ================= Журналы =================
page('oplog', {
  title: 'Журнал операций',
  async render(view) {
    head(view, 'Журнал операций', { sub: 'Все изменения, сделанные через панель' });
    const p = panel(view);
    p.innerHTML = `<div class="toolbar"><input class="search-input" placeholder="Поиск…"><label class="check"><input type="checkbox" data-err><span>Только ошибки</span></label></div><div class="list"></div>`;
    const list = $('.list', p);
    let t;
    const load = async () => {
      const rows = await api('logs.ops', { q: $('.search-input', p).value, errors: $('[data-err]', p).checked });
      table(list, {
        rows, empty: { icon: 'oplog', title: 'Записей нет' },
        columns: [
          { t: 'Время', r: (r) => fmtDate(r.time) + `:${String(new Date(r.time).getSeconds()).padStart(2, '0')}`, cls: 'nowrap' },
          { t: 'Операция', r: (r) => h(r.action) },
          { t: 'Объект', r: (r) => `<span class="mono">${h(r.object)}</span>` },
          { t: 'Результат', r: (r) => r.status === 'ok' ? '<span class="tag ok">успешно</span>' : `<span class="tag err" title="${h(r.error)}">ошибка</span>` },
          { t: 'Кто', r: (r) => h(r.user) + ` <small class="muted">${h(r.ip)}</small>`, cls: 'hide-sm nowrap' },
          { t: 'Время, мс', r: (r) => r.ms, cls: 'hide-sm' },
        ],
        onRow: (r) => r.error && showOutput(r.action, r.error),
      });
    };
    $('.search-input', p).oninput = () => { clearTimeout(t); t = setTimeout(load, 300); };
    $('[data-err]', p).onchange = load;
    await load();
  },
});

page('visits', {
  title: 'Журнал посещений',
  async render(view) {
    head(view, 'Журнал посещений', { sub: 'Входы в панель и неудачные попытки' });
    const p = panel(view);
    const rows = await api('logs.visits');
    table(p, {
      rows, empty: { icon: 'visits', title: 'Записей нет' },
      columns: [
        { t: 'Время', r: (r) => fmtDate(r.time), cls: 'nowrap' },
        { t: 'Событие', r: (r) => ({ login: '<span class="tag ok">вход</span>', logout: '<span class="tag">выход</span>', fail: '<span class="tag err">неверный пароль</span>' }[r.kind]) },
        { t: 'Пользователь', r: (r) => h(r.user) },
        { t: 'IP', r: (r) => `<span class="mono">${h(r.ip)}</span>` },
        { t: 'Браузер', r: (r) => `<small class="muted">${h(r.ua)}</small>`, cls: 'hide-sm' },
      ],
    });
  },
});

page('wwwlogs', {
  title: 'Журналы сайтов',
  async render(view, params) {
    head(view, 'Журналы WWW-запросов');
    const p = panel(view);
    const files = await api('logs.www.files');
    if (!files.length) { p.innerHTML = `<div class="empty"><div class="circle">${icon('wwwlog')}</div><b>Сайтов пока нет</b></div>`; return; }
    const first = files.find((f) => f.site === params.site && f.kind === 'access') || files[0];
    const kinds = { access: 'доступ', error: 'ошибки nginx', php: 'ошибки PHP' };
    p.innerHTML = `<div class="toolbar"><select class="search-input" data-f>${files.map((f) => `<option value="${h(f.path)}" ${f === first ? 'selected' : ''}>${h(f.site)} — ${kinds[f.kind]} (${fmtBytes(f.size)})</option>`).join('')}</select>
      <input class="search-input" data-q placeholder="Фильтр: IP, адрес, код…"><select class="search-input" data-n style="min-width:110px"><option>100</option><option selected>300</option><option>1000</option><option>5000</option></select>
      <button class="btn" data-r>${icon('refresh')}Обновить</button><label class="check"><input type="checkbox" data-live><span>Автообновление</span></label></div><pre class="out" style="max-height:calc(100vh - 330px)"></pre>`;
    const out = $('pre', p);
    let t;
    const load = async () => {
      try {
        const lines = await api('logs.www.read', { path: $('[data-f]', p).value, lines: +$('[data-n]', p).value, q: $('[data-q]', p).value });
        out.textContent = lines?.length ? lines.join('\n') : '(записей нет)';
      } catch (e) { fail(e); }
    };
    $('[data-f]', p).onchange = load;
    $('[data-n]', p).onchange = load;
    $('[data-r]', p).onclick = load;
    $('[data-q]', p).oninput = () => { clearTimeout(t); t = setTimeout(load, 400); };
    every(5000, () => { if ($('[data-live]', p)?.checked) load(); }, view);
    await load();
  },
});

// ================= графики =================
function chartSVG(el, pts, series, { fmt = (v) => v, max } = {}) {
  const W = Math.max(300, el.clientWidth || 800), H = 200, L = 52, R = 10, T = 10, B = 26;
  if (!pts.length) { el.innerHTML = '<div class="muted" style="padding:30px 0">Данные появятся через несколько минут</div>'; return; }
  const t0 = pts[0].t, t1 = pts[pts.length - 1].t || t0 + 1;
  let ymax = max || Math.max(...pts.flatMap((p) => series.map((s) => p[s.k] || 0)), 1) * 1.15;
  const x = (t) => L + ((t - t0) / Math.max(1, t1 - t0)) * (W - L - R);
  const y = (v) => T + (1 - v / ymax) * (H - T - B);
  let g = '';
  for (let i = 0; i <= 4; i++) { const v = (ymax / 4) * i; g += `<line x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}" stroke="var(--line)"/><text x="${L - 8}" y="${y(v) + 4}" text-anchor="end" font-size="11" fill="var(--muted)">${h(fmt(v))}</text>`; }
  const span = t1 - t0;
  for (let i = 0; i <= 5; i++) { const t = t0 + (span / 5) * i; const d = new Date(t * 1000); const lab = span > 2 * 86400 ? `${d.getDate()}.${d.getMonth() + 1}` : `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`; g += `<text x="${x(t)}" y="${H - 6}" text-anchor="middle" font-size="11" fill="var(--muted)">${lab}</text>`; }
  series.forEach((s) => {
    const line = pts.map((p, i) => `${i ? 'L' : 'M'}${x(p.t).toFixed(1)},${y(p[s.k] || 0).toFixed(1)}`).join('');
    g += `<path d="${line}L${x(t1)},${y(0)}L${x(t0)},${y(0)}Z" fill="${s.c}" opacity=".12"/><path d="${line}" fill="none" stroke="${s.c}" stroke-width="2" stroke-linejoin="round"/>`;
  });
  el.innerHTML = `<svg class="chart" viewBox="0 0 ${W} ${H}" width="${W}" height="${H}">${g}</svg>`;
}

page('resources', {
  title: 'Потребление ресурсов',
  async render(view) {
    head(view, 'Потребление ресурсов');
    const top = panel(view);
    const p = panel(view);
    let hours = ls.get('res.hours', 24);
    const load = async () => {
      const d = await api('stats.resources', { hours });
      d.points = d.points || []; d.now = d.now || {};
      const n = d.now, last = d.points[d.points.length - 1] || {};
      const pct = (a, b) => (b ? Math.round((100 * a) / b) : 0);
      const barc = (v) => (v > 90 ? 'err' : v > 75 ? 'warn' : '');
      top.innerHTML = `<div class="stats">
        <div class="stat"><div class="k">Процессор</div><div class="v">${last.cpu ?? '—'}<small> %</small></div><div class="bar"><i class="${barc(last.cpu)}" style="width:${last.cpu || 0}%"></i></div></div>
        <div class="stat"><div class="k">Память</div><div class="v">${fmtBytes(n.mem_used)} <small>из ${fmtBytes(n.mem_total)}</small></div><div class="bar"><i class="${barc(pct(n.mem_used, n.mem_total))}" style="width:${pct(n.mem_used, n.mem_total)}%"></i></div></div>
        <div class="stat"><div class="k">Диск /</div><div class="v">${fmtBytes(n.disk_used)} <small>из ${fmtBytes(n.disk_total)}</small></div><div class="bar"><i class="${barc(pct(n.disk_used, n.disk_total))}" style="width:${pct(n.disk_used, n.disk_total)}%"></i></div></div>
        <div class="stat"><div class="k">Нагрузка (1 / 5 / 15 мин)</div><div class="v">${(n.load || []).map((x) => x.toFixed(2)).join(' / ')}</div></div></div>`;
      p.innerHTML = `<div class="toolbar"><div class="seg">${[[1, '1 час'], [6, '6 часов'], [24, 'Сутки'], [168, 'Неделя']].map(([v, t]) => `<button data-h="${v}" class="${v === hours ? 'on' : ''}">${t}</button>`).join('')}</div></div>
        <div class="chart-box"><h4>Процессор <span>%</span></h4><div data-c="cpu"></div><h4>Память <span>%</span></h4><div data-c="mem"></div><h4>Средняя нагрузка</h4><div data-c="load"></div><h4>Сеть <span><i style="display:inline-block;width:10px;height:10px;background:#4166f5;border-radius:3px"></i> входящий · <i style="display:inline-block;width:10px;height:10px;background:#23a26a;border-radius:3px"></i> исходящий</span></h4><div data-c="net"></div></div>`;
      $$('[data-h]', p).forEach((b) => (b.onclick = () => { hours = +b.dataset.h; ls.set('res.hours', hours); load(); }));
      chartSVG($('[data-c=cpu]', p), d.points, [{ k: 'cpu', c: '#4166f5' }], { max: 100, fmt: (v) => Math.round(v) + '%' });
      chartSVG($('[data-c=mem]', p), d.points, [{ k: 'mem', c: '#6a5cf6' }], { max: 100, fmt: (v) => Math.round(v) + '%' });
      chartSVG($('[data-c=load]', p), d.points, [{ k: 'load', c: '#d98e04' }], { fmt: (v) => v.toFixed(1) });
      chartSVG($('[data-c=net]', p), d.points, [{ k: 'rx', c: '#4166f5' }, { k: 'tx', c: '#23a26a' }], { fmt: (v) => fmtBytes(v) + '/с' });
    };
    await load();
    every(60000, load, view);
  },
});

page('traffic', {
  title: 'Использование трафика',
  async render(view) {
    head(view, 'Использование трафика', { sub: 'Исходящий трафик сайтов по журналам nginx (обновляется раз в 10 минут)' });
    const p = panel(view);
    let days = 30;
    const COLORS = ['#4166f5', '#23a26a', '#d98e04', '#6a5cf6', '#e0484d', '#0ea5b7', '#a855f7', '#64748b'];
    const load = async () => {
      const d = await api('stats.traffic', { days });
      const total = d.days.reduce((a, r) => a + r.total, 0);
      const sites = (d.sites || []).map((s) => s.site);
      const col = (s) => COLORS[sites.indexOf(s) % COLORS.length];
      p.innerHTML = `<div class="toolbar"><div class="seg">${[[7, '7 дней'], [30, '30 дней'], [90, '90 дней'], [365, 'Год']].map(([v, t]) => `<button data-d="${v}" class="${v === days ? 'on' : ''}">${t}</button>`).join('')}</div><span class="grow"></span><b>Всего: ${fmtBytes(total)}</b></div><div data-chart></div><div class="legend" style="margin:8px 0 20px">${sites.map((s) => `<span><i style="background:${col(s)}"></i>${h(s)}</span>`).join('')}</div><div data-t></div>`;
      $$('[data-d]', p).forEach((b) => (b.onclick = () => { days = +b.dataset.d; load(); }));
      const el = $('[data-chart]', p);
      const W = Math.max(300, el.clientWidth), H = 220, L = 60, B = 24, T = 10;
      const max = Math.max(...d.days.map((r) => r.total), 1) * 1.1;
      const bw = (W - L - 10) / d.days.length;
      let g = '';
      for (let i = 0; i <= 4; i++) { const v = (max / 4) * i, yy = T + (1 - v / max) * (H - T - B); g += `<line x1="${L}" x2="${W - 10}" y1="${yy}" y2="${yy}" stroke="var(--line)"/><text x="${L - 8}" y="${yy + 4}" text-anchor="end" font-size="11" fill="var(--muted)">${fmtBytes(v)}</text>`; }
      d.days.forEach((r, i) => {
        let acc = 0;
        sites.forEach((s) => {
          const v = r.sites[s] || 0; if (!v) return;
          const y1 = T + (1 - (acc + v) / max) * (H - T - B), y0 = T + (1 - acc / max) * (H - T - B);
          g += `<rect x="${L + i * bw + bw * 0.15}" y="${y1}" width="${bw * 0.7}" height="${Math.max(0, y0 - y1)}" fill="${col(s)}" rx="2"><title>${r.day} · ${h(s)}: ${fmtBytes(v)}</title></rect>`;
          acc += v;
        });
        if (d.days.length <= 31 || i % Math.ceil(d.days.length / 15) === 0) g += `<text x="${L + i * bw + bw / 2}" y="${H - 6}" text-anchor="middle" font-size="10" fill="var(--muted)">${r.day.slice(8)}.${r.day.slice(5, 7)}</text>`;
      });
      el.innerHTML = `<svg class="chart" style="height:${H}px" viewBox="0 0 ${W} ${H}" width="${W}">${g}</svg>`;
      table($('[data-t]', p), { rows: d.sites || [], empty: { icon: 'traffic', title: 'Данных пока нет', text: 'Трафик считается по журналам доступа сайтов' }, columns: [{ t: 'Сайт', r: (r) => `<i style="display:inline-block;width:10px;height:10px;border-radius:3px;background:${col(r.site)};margin-right:8px"></i>${h(r.site)}` }, { t: 'Трафик за период', r: (r) => fmtBytes(r.bytes) }, { t: 'Доля', r: (r) => (total ? ((100 * r.bytes) / total).toFixed(1) : 0) + '%' }] });
    };
    await load();
  },
});

page('notices', {
  title: 'Уведомления',
  async render(view) {
    head(view, 'Уведомления', { actions: [
      { t: 'Прочитать все', i: 'check', fn: async () => { await api('notices.read', { all: true }); route(); } },
      { t: 'Очистить', i: 'trash', fn: async () => { if (await confirmBox('Очистить уведомления?', 'Все уведомления будут удалены.')) { await api('notices.clear'); route(); } } },
    ] });
    const p = panel(view);
    const s = panel(view, 'Настройки');
    const rows = await api('notices.list');
    p.innerHTML = rows.length ? rows.map((n) => `<div class="notice ${n.read ? '' : 'unread'}"><span class="dot ${n.level === 'error' ? 'err' : n.level === 'warn' ? 'warn' : 'ok'}" style="margin-top:7px"></span><span class="text">${h(n.text)}</span><span class="when">${fmtAgo(n.time)}</span></div>`).join('') : `<div class="empty"><div class="circle">${icon('bell')}</div><b>Уведомлений нет</b>Здесь появятся предупреждения: истекающие сертификаты, заполненный диск, ошибки копий</div>`;
    const st = await api('notices.settings', {});
    s.innerHTML = `<div class="cols2"><div class="field"><label>E-mail администратора</label><input type="text" data-e value="${h(st.email)}"><div class="hint">Используется для Let’s Encrypt. Рассылка уведомлений по e-mail пока не включена.</div></div><div class="field"><label>Предупреждать, когда диск заполнен на, %</label><input type="number" data-w value="${st.disk_warn}"></div></div><button class="btn primary" data-s>Сохранить</button>`;
    $('[data-s]', s).onclick = async () => { try { await api('notices.settings', { save: true, email: $('[data-e]', s).value, disk_warn: +$('[data-w]', s).value }); toast('Сохранено', 'ok'); } catch (e) { fail(e); } };
    if (rows.some((n) => !n.read)) setTimeout(() => api('notices.read', { all: true }).then(refreshBadge), 1500);
  },
});

page('disk', {
  title: 'Использование диска',
  async render(view) {
    head(view, 'Использование диска');
    const top = panel(view, 'Разделы');
    const p = panel(view, 'Что занимает место');
    const d = await api('stats.disk');
    top.innerHTML = `<div class="stats">${d.parts.map((x) => { const pc = x.total ? Math.round((100 * x.used) / x.total) : 0; return `<div class="stat"><div class="k">${h(x.mount)} <span class="mono">${h(x.dev)}</span></div><div class="v">${fmtBytes(x.used)} <small>из ${fmtBytes(x.total)} · ${pc}%</small></div><div class="bar"><i class="${pc > 90 ? 'err' : pc > 75 ? 'warn' : ''}" style="width:${pc}%"></i></div></div>`; }).join('')}</div>`;
    const items = d.items.sort((a, b) => b.size - a.size);
    const max = Math.max(...items.map((i) => i.size), 1);
    table(p, {
      rows: items,
      columns: [
        { t: 'Объект', r: (r) => `<div class="site-name">${h(r.name)}<small class="mono">${h(r.path)}</small></div>` },
        { t: 'Тип', r: (r) => ({ user: '<span class="tag">пользователь</span>', site: '<span class="tag acc">сайт</span>', sys: '<span class="tag">система</span>', docker: '<span class="tag">Docker</span>', other: '<span class="tag warn">не распределено</span>' }[r.kind] || ''), cls: 'hide-sm' },
        { t: 'Размер', r: (r) => `<div style="min-width:160px">${fmtBytes(r.size)}<div class="bar"><i style="width:${(100 * r.size) / max}%"></i></div></div>` },
      ],
      actions: (r) => [
        r.clean && { i: 'trash', t: r.clean === 'recycle' ? 'Очистить корзину' : r.clean === 'journal' ? 'Сократить журнал до 100 МБ' : 'Очистить кэш пакетов', label: 'Очистить', fn: async () => { if (!(await confirmBox(r.clean === 'recycle' ? 'Очистить корзину?' : 'Очистить?', r.clean === 'recycle' ? 'Все файлы в корзине будут удалены навсегда.' : r.clean === 'journal' ? 'Старые записи системного журнала будут удалены, останутся последние 100 МБ.' : 'Скачанные пакеты будут удалены. Установленные программы не затрагиваются.', { ok: 'Очистить' }))) return; try { const res = await api('stats.disk.clean', { what: r.clean }); toast(res.info, 'ok'); route(); } catch (e) { fail(e); } } },
        r.kind === 'docker' && { i: 'docker', t: 'Перейти в Docker', fn: () => go('docker') },
        r.kind !== 'other' && { i: 'folder', t: 'Открыть', fn: () => go('files', { path: r.path }) },
      ],
    });
    const other = items.find((x) => x.kind === 'other');
    if (other) { const n = document.createElement('p'); n.className = 'hint'; n.textContent = '«Не распределено» — место на разделе /, которое не входит в перечисленные папки: другие программы и данные системы. Если это значение большое, проверьте папки через менеджер файлов.'; p.append(n); }
  },
});

// ================= PHP =================
page('php', {
  title: 'PHP',
  async render(view) {
    head(view, 'PHP', { sub: 'Несколько версий PHP одновременно, у каждого сайта — своя' });
    const p = panel(view);
    const load = async () => {
      const rows = await api('php.list');
      table(p, {
        rows,
        columns: [
          { t: 'Версия', r: (r) => `<b style="font-size:16px">PHP ${h(r.version)}</b> ${r.default ? '<span class="tag acc">по умолчанию</span>' : ''}` },
          { t: 'Состояние', r: (r) => r.job === 'running' ? '<span class="tag acc"><span class="spin" style="width:12px;height:12px;border-width:2px"></span> устанавливается</span>' : r.job === 'error' ? '<span class="tag err">ошибка установки</span>' : r.installed ? (r.active ? '<span class="tag ok">работает</span>' : '<span class="tag warn">остановлен</span>') : '<span class="tag">не установлен</span>' },
          { t: 'Сайтов', r: (r) => r.sites || '—' },
        ],
        actions: (r) => r.job === 'running' ? [] : r.installed ? [
          { i: 'edit', t: 'Настройки php.ini', label: 'Настройки', fn: () => phpIni(r.version) },
          { i: 'archive', t: 'Модули', label: 'Модули', fn: () => phpExt(r.version) },
          !r.default && { i: 'check', t: 'Сделать версией по умолчанию', fn: async () => { await api('php.default', { version: r.version }); load(); } },
          { i: 'refresh', t: 'Перезапустить PHP-FPM', fn: async () => { try { await api('php.restart', { version: r.version }); toast('Перезапущен', 'ok'); load(); } catch (e) { fail(e); } } },
          { i: 'trash', t: 'Удалить', danger: true, fn: async () => { if (!(await confirmBox('Удалить PHP ' + r.version + '?', 'Пакеты PHP ' + r.version + ' будут удалены.'))) return; try { await api('php.remove', { version: r.version }); load(); } catch (e) { fail(e); } } },
        ] : [{ i: 'download', t: 'Установить', label: 'Установить', fn: async () => { try { await api('php.install', { version: r.version }); toast('Установка PHP ' + r.version + ' началась (2–5 минут)'); load(); } catch (e) { fail(e); } } }],
      });
      return rows.some((r) => r.job === 'running');
    };
    await load();
    every(5000, async () => { if ($$('.spin', p).length) load(); }, view);
  },
});
const EXT_NAMES = { redis: 'Redis', memcached: 'Memcached', apcu: 'APCu (кэш)', igbinary: 'igbinary', mongodb: 'MongoDB', pgsql: 'PostgreSQL', ldap: 'LDAP', gmp: 'GMP', imap: 'IMAP', xdebug: 'Xdebug (отладка)', tidy: 'Tidy', xsl: 'XSL', snmp: 'SNMP', sodium: 'Sodium' };
async function phpExt(v) {
  const d = await api('php.ini', { version: v });
  const mods = (d.modules || []).map((m) => m.toLowerCase());
  const has = (e) => mods.includes(e) || (e === 'pgsql' && mods.includes('pdo_pgsql')) || (e === 'xdebug' && mods.some((m) => m.includes('xdebug')));
  const m = modal(`<h3>PHP ${h(v)}: модули</h3><p class="muted">Стандартные модули (mysql, gd, curl, mbstring, xml, zip, intl, imagick, opcache…) ставятся вместе с версией.</p>
    <div class="table-wrap"><table class="grid"><tbody>${d.extras.map((e) => `<tr><td><b>${h(EXT_NAMES[e] || e)}</b> <span class="muted mono">${h(e)}</span></td><td>${has(e) ? '<span class="tag ok">установлен</span>' : '<span class="tag">нет</span>'}</td><td class="actions">${has(e) ? `<button class="btn small danger" data-rm="${e}">Удалить</button>` : `<button class="btn small" data-add="${e}">Установить</button>`}</td></tr>`).join('')}</tbody></table></div>
    <div class="btns"><button class="btn primary" data-close>Готово</button></div>`, { wide: true });
  const act = async (ext, remove, b) => {
    b.disabled = true; b.innerHTML = '<span class="spin" style="width:14px;height:14px;border-width:2px"></span>';
    try { await api('php.ext', { version: v, ext, remove }); toast((remove ? 'Удалён ' : 'Установлен ') + ext, 'ok'); m.close(); phpExt(v); } catch (e) { fail(e); b.disabled = false; b.textContent = remove ? 'Удалить' : 'Установить'; }
  };
  $$('[data-add]', m.el).forEach((b) => (b.onclick = () => act(b.dataset.add, false, b)));
  $$('[data-rm]', m.el).forEach((b) => (b.onclick = () => act(b.dataset.rm, true, b)));
}
async function phpIni(v) {
  const d = await api('php.ini', { version: v });
  const labels = { memory_limit: 'memory_limit — память на скрипт', upload_max_filesize: 'upload_max_filesize — макс. размер файла', post_max_size: 'post_max_size — макс. размер POST', max_execution_time: 'max_execution_time — время работы, с', max_input_time: 'max_input_time, с', max_input_vars: 'max_input_vars', display_errors: 'display_errors (On/Off)', 'date.timezone': 'date.timezone', short_open_tag: 'short_open_tag (On/Off)', 'opcache.enable': 'opcache.enable (1/0)' };
  openForm({
    title: 'PHP ' + v + ': php.ini',
    fields: [
      { type: 'html', html: '<p class="hint">Пустое поле — значение по умолчанию PHP, оно не записывается.</p>' },
      ...d.keys.map((k) => ({ name: k, label: labels[k] || k, value: d.values[k] || '', placeholder: 'по умолчанию' })),
      { type: 'html', html: `<p class="muted">Файл: <span class="mono">${h(d.ini)}</span></p><label>Модули</label><div style="display:flex;flex-wrap:wrap;gap:6px;margin-top:6px">${(d.modules || []).filter((m) => !m.startsWith('[')).map((m) => `<span class="tag">${h(m)}</span>`).join('')}</div>` },
    ],
    submit: async (vals) => { await api('php.ini.save', { version: v, values: vals }); toast('Сохранено, PHP-FPM перезапущен', 'ok'); },
  });
}

// ================= Информация о сервере =================
// Перезагрузка сервера: после команды страница ждёт, пока панель снова ответит, и обновляется.
async function rebootServer() {
  if (!(await confirmBox('Перезагрузить сервер?', 'Сервер перезагрузится через несколько секунд. Сайты, почта и панель будут недоступны 1–3 минуты. Выполняющиеся задачи нужно сначала завершить или остановить.', { ok: 'Перезагрузить' }))) return;
  try { await api('system.reboot'); } catch (e) { fail(e); return; }
  const m = modal(`<h3>Сервер перезагружается</h3><p data-stage role="status">Ожидание перезапуска…</p><p class="hint">Страница обновится сама, когда панель снова будет доступна. Обычно это 1–3 минуты.</p>`);
  const started = Date.now();
  let down = false;
  const poll = async () => {
    if (!m.el.isConnected) return;
    try {
      const r = await fetch('/api/me', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Itrupanel': '1' }, body: '{}', cache: 'no-store' });
      if (down && r.status < 500) { location.reload(); return; }
    } catch { down = true; }
    if (Date.now() - started > 15000) down = true;
    $('[data-stage]', m.el).textContent = (down ? 'Сервер перезапускается' : 'Ожидание перезапуска') + ' · прошло ' + Math.round((Date.now() - started) / 1000) + ' с';
    setTimeout(poll, 4000);
  };
  setTimeout(poll, 4000);
}

page('info', {
  title: 'Информация о сервере',
  async render(view) {
    head(view, 'Информация о сервере', { actions: [{ t: 'Адрес и SSL панели', i: 'lock', fn: () => panelSettingsForm().catch(fail) }, { t: 'Перезагрузить сервер', i: 'refresh', fn: () => rebootServer() }] });
    const a = panel(view, 'Сервер');
    const upd = panel(view, 'Обновление панели');
    const b = panel(view, 'Службы');
    const [d, monitor, auto] = await Promise.all([api('system.info'), api('stats.monitor').catch(() => null), api('updates.auto.get').catch(() => null)]);
    if (!view.isConnected) return;
    if (auto) {
      const modes = [['off', 'Отключено: обновлять вручную'], ['patch', 'Автоматически только исправления (например, 0.2.1 → 0.2.2)'], ['all', 'Автоматически все новые выпуски']];
      upd.innerHTML = `<div class="field"><label for="au-mode">Автоматическое обновление</label><select id="au-mode">${modes.map(([v, n]) => `<option value="${v}" ${v === auto.mode ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
        <div class="field" data-au-hour ${auto.mode === 'off' ? 'style="display:none"' : ''}><label for="au-hour">Время установки</label><select id="au-hour">${[...Array(24).keys()].map((x) => `<option value="${x}" ${x === (auto.hour || 0) ? 'selected' : ''}>${String(x).padStart(2, '0')}:00</option>`).join('')}</select></div>
        <p class="muted" style="margin:0 0 14px">Версия панели: ${h(auto.version)}. Обновление проверяется по подписи и контрольной сумме, перед установкой сохраняется копия программы и настроек. О результате придёт уведомление. Обновить вручную можно в любой момент кнопкой «Обновления» в верхней панели.</p>
        <button class="btn primary" data-au-save>Сохранить</button>`;
      const sel = $('#au-mode', upd);
      sel.onchange = () => { $('[data-au-hour]', upd).style.display = sel.value === 'off' ? 'none' : ''; };
      $('[data-au-save]', upd).onclick = async () => {
        try { await api('updates.auto.save', { mode: sel.value, hour: +$('#au-hour', upd).value }); toast('Сохранено', 'ok'); } catch (e) { fail(e); }
      };
    } else upd.remove();
    const serviceRows = monitor ? monitor.services.map(s => ({...s, active:s.state === 'running', manageable:d.services.some(item => item.name === s.name)})) : d.services.map(s => ({...s, manageable:true}));
    a.innerHTML = `<div class="kv">
      <div>Имя сервера</div><div><b>${h(d.hostname)}</b></div>
      <div>Операционная система</div><div>${h(d.os)}</div>
      <div>Ядро</div><div>${h(d.kernel)} (${h(d.arch)})</div>
      <div>Процессор</div><div>${h(d.cpu || '—')} · ${plural(d.cores, 'ядро', 'ядра', 'ядер')}</div>
      <div>Память</div><div>${fmtBytes(d.mem_used)} из ${fmtBytes(d.mem_total)}</div>
      <div>Работает без перезагрузки</div><div>${fmtDuration(d.uptime)}${d.reboot_required ? ' <span class="tag warn">требуется перезагрузка</span>' : ''}</div>
      <div>Нагрузка</div><div>${(d.load || []).map((x) => x.toFixed(2)).join(' / ')}</div>
      <div>Время сервера</div><div>${h(d.time)}</div>
      <div>nginx</div><div class="mono">${h(d.versions.nginx || 'не установлен')}</div>
      <div>MariaDB</div><div class="mono">${h(d.versions.mariadb || 'не установлена')}</div>
      <div>certbot</div><div class="mono">${h(d.versions.certbot || 'не установлен')}</div>
      <div>Панель</div><div>ITRUPANEL ${h(d.panel)} · сайтов: ${d.sites}, баз: ${d.dbs}</div></div>`;
    table(b, {
      rows: serviceRows,
      columns: [
        { t: 'Служба', r: (r) => `<b>${h(r.title)}</b> <span class="muted mono">${h(r.name)}</span>` },
        { t: 'Состояние', r: (r) => `<span class="state ${dashboardServiceState(r.state || (r.active ? 'running' : 'stopped'))[1]}">${h(dashboardServiceState(r.state || (r.active ? 'running' : 'stopped'))[0])}</span>` },
      ],
      actions: (r) => r.state === 'missing' && ['postfix', 'dovecot', 'opendkim', 'rspamd'].includes(r.name) ? [
        { i: 'mail', t: 'Установить и настроить в разделе «Почта»', label: 'Настроить', fn: () => { location.hash = '#/mail'; } },
      ] : !r.manageable ? [] : [
        { i: 'refresh', t: 'Перезапустить', label: 'Перезапустить', fn: async () => { try { await api('system.service', { name: r.name, action: 'restart' }); toast('Перезапущено', 'ok'); route(); } catch (e) { fail(e); } } },
        r.active ? { i: 'stop', t: 'Остановить', fn: async () => { if (!(await confirmBox('Остановить ' + r.title + '?', 'Сайты, зависящие от службы, перестанут работать.', { ok: 'Остановить' }))) return; try { await api('system.service', { name: r.name, action: 'stop' }); route(); } catch (e) { fail(e); } } }
                 : { i: 'play', t: 'Запустить', fn: async () => { try { await api('system.service', { name: r.name, action: 'start' }); route(); } catch (e) { fail(e); } } },
      ],
    });
  },
});

// ================= Shell-клиент =================
function loadScript(src) { return new Promise((res, rej) => { if ($(`script[src="${src}"]`)) return res(); const s = document.createElement('script'); s.src = src; s.onload = res; s.onerror = rej; document.head.append(s); }); }
page('shell', {
  title: 'Shell-клиент',
  async render(view) {
    head(view, 'Shell-клиент', { sub: 'Терминал сервера с правами root. Сессия закрывается при уходе со страницы.' });
    const p = panel(view);
    if (!$('link[href="/vendor/xterm.css"]')) { const l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/vendor/xterm.css'; document.head.append(l); }
    await loadScript('/vendor/xterm.js');
    if (!view.isConnected) return;
    await loadScript('/vendor/addon-fit.js');
    if (!view.isConnected) return;
    p.innerHTML = '<div class="term-box"></div>';
    const term = new Terminal({ fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 14, cursorBlink: true, theme: { background: '#0f1424' } });
    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open($('.term-box', p));
    fit.fit();
    const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/shell');
    ws.binaryType = 'arraybuffer';
    const sendSize = () => { if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows })); };
    ws.onopen = () => { sendSize(); term.focus(); };
    ws.onmessage = (e) => term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data));
    ws.onclose = () => term.write('\r\n\x1b[33m[сессия закрыта]\x1b[0m\r\n');
    term.onData((d) => ws.readyState === 1 && ws.send(new TextEncoder().encode(d)));
    const onResize = () => { fit.fit(); sendSize(); };
    window.addEventListener('resize', onResize);
    App.cleanup = () => { window.removeEventListener('resize', onResize); ws.close(); term.dispose(); };
  },
});
