// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// ITRUPANEL — страницы разделов.

const soon = () => toast("This section is planned for phase two");

// ================= Дашборд =================
function dashboardGauge(title, value, detail, note, routeKey, tip = '') {
  const known = value != null && Number.isFinite(value);
  const percent = known ? Math.max(0, value) : 0;
  const tone = percent >= 90 ? 'err' : percent >= 75 ? 'warn' : '';
  return `<a class="server-metric" href="#/${routeKey}" title="${h(tip)}"><div class="metric-ring ${tone}" role="img" aria-label="${h(title)}: ${known ? Math.round(percent) + '%' : "no data"}"><svg viewBox="0 0 100 100" aria-hidden="true"><circle class="ring-track" cx="50" cy="50" r="42"/><circle class="ring-value" cx="50" cy="50" r="42" pathLength="100" stroke-dasharray="${Math.min(percent, 100)} 100"/></svg><strong>${known ? Math.round(percent) + '<small>%</small>' : '—'}</strong></div><div class="metric-title">${h(title)}</div><div class="metric-detail">${detail}</div><div class="metric-note">${h(note)}</div></a>`;
}
function dashboardMarkup(d) {
  const pct = (used, total) => total > 0 ? 100 * used / total : null;
  const bytes = (used, total) => total > 0 ? `<b>${fmtBytes(used)}</b><span> / ${fmtBytes(total)}</span>` : "No data";
  const loads = (d.load || []).map((n) => Number(n).toFixed(2)).join(' / ');
  const summary = (title, ic, value, text, key) => `<a class="server-summary" href="#/${key}"><div>${h(title)}${icon(ic)}</div><strong>${h(value)}</strong><small>${h(text)}</small></a>`;
  return `<div class="server-metrics">${[
    dashboardGauge("Load", d.load?.length && d.cores > 0 ? d.load[0] / d.cores * 100 : null, h(loads || "No data"), "Over 1 / 5 / 15 minutes", 'resources', "One-minute load average / logical cores × 100. May exceed 100%."),
    dashboardGauge('CPU', d.cpu, h(plural(d.cores, "logical core", "logical cores", "logical cores")) + (d.cpu_temp != null && Number.isFinite(d.cpu_temp) ? ` · <span class="metric-temp ${d.cpu_temp >= 90 ? 'err' : d.cpu_temp >= 80 ? 'warn' : ''}" title="CPU temperature">${Math.round(d.cpu_temp)} °C</span>` : ''), "CPU usage", 'resources'),
    dashboardGauge('RAM', pct(d.mem_used, d.mem_total), bytes(d.mem_used, d.mem_total), "Used / total", 'resources'),
    dashboardGauge("Disk /", pct(d.disk_used, d.disk_total), bytes(d.disk_used, d.disk_total), "Used / total", 'disk'),
  ].join('')}</div><section class="server-overview" aria-label="Server overview"><div class="server-summaries">${[
    summary("Sites", 'globe', d.sites, `Enabled: ${d.enabled} · Disabled: ${d.suspended}`, 'sites'),
    summary('FTP', 'ftp', d.ftp, "Accounts", 'ftp'),
    summary("Databases", 'database', d.dbs, "Created in the panel", 'db'),
    summary("Warnings", 'bell', d.warnings, "Unread warnings and errors", 'notices'),
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
  return ({running: ["Running", 'ok'], stopped: ["Stopped", 'err'], failed: ["Error", 'err'], missing: ["Not installed", 'off'], masked: ["Masked", 'err'], changing: ["Transitioning", 'warn'], unknown: ["No data", 'warn']})[state] || ["No data", 'warn'];
}
function dashboardServiceTitle(s) {
  const titles = {'nginx':"nginx web server",'mariadb':"MariaDB database",'pure-ftpd':"FTP server",'cron':"cron scheduler",'crond':"cron scheduler",'named':"BIND DNS server",'rspamd':"Anti-spam · Rspamd"};
  return titles[s.name] || s.title;
}
function dashboardNeedsAttention(service) { return !['running', 'missing'].includes(service.state); }
function dashboardMonitorMarkup(d, attention = false) {
  const services = d.services || [], count = services.filter(dashboardNeedsAttention).length;
  const rows = services.filter(s => !attention || dashboardNeedsAttention(s));
  const kv = (name, value) => `<div><dt>${h(name)}</dt><dd>${h(value || '—')}</dd></div>`;
  return `<div class="monitor-layout"><section class="monitor-card monitor-services"><div class="section-head"><div><h2>${icon('resources')}Service monitoring</h2><p>Running: ${services.filter(s => s.state === 'running').length} · Need attention: ${count}</p></div></div>
    <div class="service-toolbar"><div class="filters"><button data-monitor-filter="all" class="${!attention ? 'chosen' : ''}" aria-pressed="${!attention}">All <span>${services.length}</span></button><button data-monitor-filter="attention" class="${attention ? 'chosen' : ''}" aria-pressed="${attention}">Needs attention <span>${count}</span></button></div></div>
    <div class="service-table"><table><thead><tr><th>Service</th><th>Status</th><th class="memory">Memory</th></tr></thead><tbody>${rows.map(s => { const [label,tone] = dashboardServiceState(s.state); return `<tr><td><span class="service-name">${h(dashboardServiceTitle(s))}</span><small>${h(s.name)}</small></td><td><span class="state ${tone}"><i></i>${h(label)}</span></td><td class="memory">${s.memory == null ? '—' : fmtBytes(s.memory)}</td></tr>`; }).join('') || "<tr><td colspan=\"3\" class=\"muted\">No services require attention</td></tr>"}</tbody></table></div>
    <div class="card-bottom"><span>systemd states · every 10 seconds<br>${h(new Date(d.updated).toLocaleTimeString(window.PanelLanguage?.locale || 'ru-RU'))}</span><a href="#/info">All services →</a></div></section>
    <section class="monitor-card server-info"><div class="section-head"><h2>${icon('server')}Server information</h2></div><dl>${[
      kv("Server name",d.hostname),kv("Server IP addresses",(d.ips || []).join(', ')),kv("Operating system",d.os),kv("Kernel",`${d.kernel || '—'} · ${d.arch}`),kv("CPU",`${d.cpu || d.arch} · ${d.cores}`),kv("Memory",`${fmtBytes(d.mem_used)} / ${fmtBytes(d.mem_total)}`),kv("Disk /",`${fmtBytes(d.disk_used)} / ${fmtBytes(d.disk_total)}`),kv("Works without reloading",fmtDuration(d.uptime)),kv("Time zone",`${d.timezone} (UTC${d.utc_offset >= 0 ? '+' : '−'}${Math.abs(d.utc_offset)/3600})`)
    ].join('')}</dl><div class="server-identity">${icon('server')}<div><b>ITRUPANEL ${h(panelVersion(d.version))}</b><span>${h(location.host)}</span></div></div><div class="card-bottom"><span>Current server details</span><a href="#/info">More details →</a></div></section></div>`;
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
  card.innerHTML = `<div class="section-head"><div class="filters"><button data-io-tab="network" class="chosen" aria-pressed="true">Traffic</button><button data-io-tab="disks" aria-pressed="false">Disk IO</button></div><div class="io-controls"><label><span data-io-label>Network</span>: <select data-io-device aria-label="Interface or disk"></select></label><button class="btn icon-only" data-io-expand title="Expand chart" aria-label="Expand chart" aria-pressed="false">${icon('expand')}</button></div></div><div class="io-values"></div><div class="io-chart" role="img" aria-label="Transfer speed chart"></div><div class="card-bottom"><span data-io-status role="status">Waiting for measurements…</span><span data-io-note></span></div>`;
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
    const options = (mode === 'network' && names.length ? [['all',"All interfaces"]] : []).concat(names.map(n => [n,n]));
    if (!options.some(([n]) => n === device)) { device = options[0]?.[0] || ''; previous = null; points = []; rate = null; }
    const html = options.map(([n,title]) => `<option value="${h(n)}">${h(title)}</option>`).join('');
    if (select.innerHTML !== html) select.innerHTML = html;
    select.value = device;
    select.disabled = !options.length;
  }
  function plot() {
    const el = $('.io-chart', card), W = Math.max(640, el.clientWidth || 900), H = card.classList.contains('io-expanded') ? Math.max(240, window.innerHeight - 270) : 240, left = 75, right = 35, top = 12, bottom = 32;
    const valid = points.filter(p => p.rate);
    if (!valid.length) { el.innerHTML = "<div class=\"io-empty\">Waiting for two measurements to calculate speed…</div>"; return; }
    const maximum = Math.max(1, ...valid.flatMap(p => [p.rate.incoming,p.rate.outgoing])) * 1.15;
    const end = points[points.length-1].time, start = end - 60000;
    const x = t => left + (t - start) / 60000 * (W-left-right), y = v => H-bottom - v/maximum*(H-top-bottom);
    let svg = '';
    for (let i=0;i<5;i++) { const v=maximum*i/4,yy=y(v); svg += `<line x1="${left}" y1="${yy}" x2="${W-right}" y2="${yy}" class="io-grid"/><text x="${left-8}" y="${yy+4}" text-anchor="end">${h(fmtBytes(v,2))}/s</text>`; }
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
    const values = [[network?"Outgoing":"Write",rate?.outgoing,true],[network?"Incoming":"Read",rate?.incoming,true],[network?"Total sent":"Total written",c?.out,false],[network?"Total received":"Total read",c?.in,false]];
    $('.io-values',card).innerHTML = values.map(([label,value,speed],i)=>`<div><span>${i<2?`<i style="background:${i===0?'#23a26a':'#f4a340'}"></i>`:''}${h(label)}</span><strong>${value==null?'—':fmtBytes(value,speed ? 2 : undefined)+(speed?"/s":'')}</strong></div>`).join('');
    $('[data-io-label]',card).textContent = network ? "Network" : "Disk";
    $('[data-io-note]',card).textContent = network && device==='all' ? "Sum of interfaces excluding lo; virtual networks may count traffic more than once." : "Totals since device start. Chart: last minute.";
    $('[data-io-status]',card).textContent = !c ? "No device data" : rate ? "Refreshes every second" : "Waiting for measurements…";
    plot();
  }
  function reset() { previous=null;rate=null;points=[];devices(latest);draw(); }
  select.onchange = () => { device=select.value;reset(); };
  $$('[data-io-tab]',card).forEach(button => button.onclick=()=>{
    mode=button.dataset.ioTab;device=mode==='network'?'all':'';
    $$('[data-io-tab]',card).forEach(b=>{b.classList.toggle('chosen',b===button);b.setAttribute('aria-pressed',String(b===button));});reset();
  });
  $('[data-io-expand]',card).onclick = e => { const wide=card.classList.toggle('io-expanded');e.currentTarget.setAttribute('aria-pressed',String(wide));e.currentTarget.title=wide?"Collapse chart":"Expand chart";e.currentTarget.setAttribute('aria-label',e.currentTarget.title); plot(); };
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
    unavailable() {rate=null;previous=null;draw();$('[data-io-status]',card).textContent="Server unavailable. Waiting for new data…";}
  };
}

page('dashboard', {
  title: "Dashboard",
  async render(view) {
    head(view, "Home");
    const server = document.createElement('div');
    server.className = 'server-dashboard';
    server.innerHTML = "<div class=\"server-content\"><div class=\"panel muted\">Loading server metrics…</div></div><p class=\"server-update\" role=\"status\"></p>";
    view.append(server);
    const monitor = document.createElement('div');
    monitor.className = 'dashboard-monitor';
    monitor.innerHTML = "<p class=\"monitor-status muted\" role=\"status\">Loading service states…</p><div class=\"monitor-content\"></div>";
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
        if (view.isConnected) $('.monitor-status', monitor).textContent = "Could not refresh services: " + e.message + ". Last received data is shown.";
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
        $('.server-update', server).textContent = "Updated: " + new Date(d.updated).toLocaleTimeString(window.PanelLanguage?.locale || 'ru-RU') + " · every second";
      } catch (e) {
        if (view.isConnected) { liveIO.unavailable(); $('.server-update', server).textContent = "Could not refresh metrics: " + e.message + ". Retrying in a second."; }
      } finally { busy = false; }
    };
    every(1000, refresh, view);
    await refresh();
  },
});


// ================= Сайты =================
function phpOptions(meta) {
  return [...(meta.php_all || meta.php)].reverse().map((v) => [v, 'PHP ' + v + (meta.php.includes(v) ? '' : " — will be installed")]);
}
async function siteForm(edit) {
  const meta = await api('sites.form');
  const s = edit || {};
  const isNew = !edit;
  const users = [['', "New user (based on domain name)"], ...meta.users.map((u) => [u, u])];
  const php = phpOptions(meta);
  const certs = [['', "No SSL"], ...meta.certs.map((c) => [c.name, c.name + ' (' + ({ le: 'Let’s Encrypt', self: "self-signed", custom: "custom" }[c.type] || c.type) + ')'])];
  openForm({
    title: isNew ? "Create site" : "Site " + toU(s.domain),
    ok: isNew ? "Create" : "Save",
    onChange: (v, name) => {
      if (name !== 'kind') return;
      const k = (meta.kinds || []).find((x) => x.id === v.kind);
      if (!k) return;
      v.mode = k.mode; v.template = k.template || ''; v.try_index = !!k.try_index;
      if (k.index) v.index = k.index;
    },
    fields: [
      { name: 'domain', label: "Domain", value: s.domain ? toU(s.domain) : '', required: true, readonly: !isNew, placeholder: 'example.ru', hint: isNew ? "Internationalised domains supported: пример.рф" : '' },
      { name: 'add_www', label: "Add www alias", type: 'checkbox', value: isNew, show: () => isNew },
      { name: 'aliases', label: "Aliases (space separated)", value: (s.aliases || []).map(toU).join(' '), placeholder: 'www.example.ru shop.example.ru', hint: "Leave blank to remove all aliases." },
      { name: 'user', label: "Owner (system user)", type: 'select', options: users, value: '', show: () => isNew },
      { name: 'kind', label: "What kind of site?", type: 'select', value: 'php', show: () => isNew, options: (meta.kinds || []).map((k) => [k.id, (window.PanelLanguage?.language === 'en' && k.name_en) || k.name]) },
      { type: 'html', show: () => isNew, html: (v) => { const k = (meta.kinds || []).find((x) => x.id === v.kind); return k ? `<p class="hint">${h((window.PanelLanguage?.language === 'en' && k.desc_en) || k.desc)}</p>` : ''; } },
      { name: 'mode', label: "Operating mode", type: 'seg', value: s.mode === 'php' && ['apache', 'openlitespeed'].includes(s.backend) ? s.backend : (s.mode || 'php'), options: [['php', 'nginx + PHP-FPM'], ['apache', 'nginx + Apache'], ['openlitespeed', 'nginx + OpenLiteSpeed'], ['static', "Static only"], ['proxy', "Proxy"]],
        hint: "Apache supports .htaccess; OpenLiteSpeed supports Rewrite rules in .htaccess. All modes use PHP-FPM. OpenLiteSpeed is installed when first selected. After editing .htaccess, restart OpenLiteSpeed in Server information." },
      { name: 'php', label: "PHP version", type: 'select', options: php, value: s.php || meta.default_php || '8.3', show: (v) => ['php', 'apache', 'openlitespeed'].includes(v.mode), hint: "If this version is not installed, the panel installs it on save (2–5 minutes). <a href=\"#/php\">All PHP versions</a>" },
      { name: 'php_profile', label: "PHP load", type: 'select', value: s.php_profile || '', show: (v) => ['php', 'apache', 'openlitespeed'].includes(v.mode),
        options: (meta.php_profiles || []).map((x) => [x.id, ((window.PanelLanguage?.language === 'en' && x.name_en) || x.name) + ' — ' + x.summary]),
        hint: "How many PHP processes the site keeps. “Economical” suits a weak server, “High load” a busy site or shop; the panel calculates the number of processes from the server memory. If the server hosts many sites, do not set high load for all of them." },
      { name: 'php_memory', label: "Memory per PHP script, MB (0 — default)", type: 'number', value: s.php_memory || 0, show: (v) => ['php', 'apache', 'openlitespeed'].includes(v.mode), hint: "Heavy sites (for example WooCommerce or Bitrix) usually need 256–512 MB." },
      { name: 'php_max_exec', label: "PHP script run time, seconds (0 — default)", type: 'number', value: s.php_max_exec || 0, show: (v) => ['php', 'apache', 'openlitespeed'].includes(v.mode) },
      { name: 'try_index', label: "Route all requests to index.php (friendly URLs)", type: 'checkbox', value: s.try_index ?? true, show: (v) => v.mode === 'php', hint: "Required for Laravel, X3 and most CMS platforms. Apache mode uses .htaccess for this." },
      { name: 'template', label: "Site template", type: 'select', options: (meta.templates || []).map((t) => [t.id, (window.PanelLanguage?.language === 'en' && t.name_en) || t.name]), value: s.template || '', hint: "A ready set of nginx rules for a type of site (WordPress, Laravel, Joomla, SPA…). Your own templates and their descriptions: the Templates button on the sites page." },
      { name: 'proxy_to', label: "Application address", value: s.proxy_to || '', placeholder: 'http://127.0.0.1:3000', show: (v) => v.mode === 'proxy' },
      { type: 'section', label: 'SSL' },
      { name: 'create_zone', label: "Create DNS zone for domain", type: 'checkbox', value: false, show: () => isNew, hint: "A records for a subdomain of an existing zone are added automatically" },
      { name: 'auto_ssl', label: "Automatic SSL (Let’s Encrypt)", type: 'checkbox', value: isNew ? true : !!s.auto_ssl, hint: "The certificate is issued once the domain points to this server and renewed automatically. New aliases are added to the certificate automatically." },
      { name: 'email', label: "Let’s Encrypt email (optional)", value: meta.email || '', show: (v) => v.auto_ssl && !meta.email },
      { name: 'ssl', label: "Certificate", type: 'select', options: certs, value: s.ssl || '', show: () => !isNew },
      { name: 'redirect_https', label: "Redirect HTTP → HTTPS", type: 'checkbox', value: s.redirect_https ?? true },
      { type: 'section', label: "Advanced" },
      { name: 'www_redirect', label: "www redirect", type: 'select', value: s.www_redirect ?? (isNew ? 'from_www' : ''), options: [['', "No"], ['from_www', "www.domain → domain"], ['to_www', "domain → www.domain"]] },
      { name: 'gzip', label: "gzip compression", type: 'checkbox', value: s.gzip ?? true },
      { name: 'cache_ttl', label: "Page cache (speeds the site up)", type: 'select', value: String(s.cache_ttl || 0), show: (v) => v.mode !== 'static', options: [['0', "Off"], ['1', "1 minute"], ['5', "5 minutes"], ['10', "10 minutes"], ['30', "30 minutes"], ['60', "1 hour"], ['360', "6 hours"], ['1440', "1 day"]], hint: "Ready pages are served from the nginx cache without running PHP, so the site is noticeably faster. Requests with data and address parameters, logged-in users, admin areas and carts are not cached. The Clear cache button is in the site row. For your own applications with personal pages without a cookie sign-in, choose the period carefully." },
      { name: 'static_expires', label: "Browser caching for static files", type: 'select', value: s.static_expires ?? (isNew ? '30d' : ''), options: [['', "Not set"], ['1d', "1 day"], ['7d', "7 days"], ['30d', "30 days"], ['365d', "1 year"]], show: (v) => v.mode !== 'proxy' },
      { type: 'section', label: "Site protection" },
      { name: 'guard_headers', label: "Security headers", type: 'checkbox', value: !!(s.guard || {}).headers, hint: "The browser will not let the site be embedded in another page and will not “guess” file types; with HTTPS, HSTS is added." },
      { name: 'guard_hotlink', label: "Protect images and video from hotlinking", type: 'checkbox', value: !!(s.guard || {}).hotlink, hint: "The files are not shown on other sites and do not use your traffic. Search engines, Yandex, Google and social networks are allowed." },
      { name: 'auth_user', label: "Password for the whole site: login", value: (s.guard || {}).auth_user || '', placeholder: "empty — no password", hint: "The site opens only after entering a login and password (the Let’s Encrypt check is not affected). Handy for a site under development." },
      { name: 'auth_password', label: "Password for the whole site: password", type: 'password', value: '', gen: true, show: (v) => !!(v.auth_user || '').trim(), hint: ((s.guard || {}).auth_set ? "The password is already set: leave the field empty to keep it." : "At least 8 characters.") },
      { name: 'guard_admin_ips', label: "Admin area only from these addresses", type: 'textarea', rows: 3, value: ((s.guard || {}).admin_ips || []).join('\n'), placeholder: '203.0.113.5\n198.51.100.0/24', hint: "One address or network per line. /wp-admin, /wp-login.php, /administrator, /bitrix/admin, /admin, /manager and /phpmyadmin are closed for everyone else. Empty — no restriction. Add your own address, or you will lose access." },
      { name: 'max_body', label: "Maximum upload size, MB", type: 'number', value: s.max_body || 64 },
      { name: 'index', label: "Index files", value: s.index || 'index.php index.html', show: (v) => v.mode !== 'proxy' },
      { name: 'root', label: "Root directory", value: s.root || '', placeholder: "default /var/www/<user>/data/www/<domain>", show: () => !isNew },
    ],
    submit: async (v) => {
      v.max_body = Number(v.max_body) || 64;
      v.cache_ttl = Number(v.cache_ttl) || 0;
      v.auth_user = (v.auth_user || '').trim(); v.auth_password = v.auth_user ? v.auth_password || '' : '';
      v.php_memory = Number(v.php_memory) || 0; v.php_max_exec = Number(v.php_max_exec) || 0; v.php_profile = v.php_profile || '';
      if (['apache', 'openlitespeed'].includes(v.mode)) { v.backend = v.mode; v.mode = 'php'; } else v.backend = '';
      if (v.mode === 'php' && !meta.php.includes(v.php)) toast("Installing PHP " + v.php + " — this may take a few minutes…");
      if (v.backend === 'apache' && !meta.apache) toast("Installing Apache…");
      if (v.backend === 'openlitespeed' && !meta.openlitespeed) toast("Installing OpenLiteSpeed — this may take a few minutes…");
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
    title: "Default website", ok: "Save",
    fields: [
      { name: 'site', label: "For the IP address or an unknown name, show", type: 'select', value: cur.site || '', options: [['', "The “Website not found” page"], ['-', "Nothing (close the connection)"], ...rows.map((r) => [r.site.domain, r.unicode])], hint: "For example, http://server-IP/ or a domain that points to the server but is not added to the panel. HTTPS by IP address is always rejected: there is no certificate for the IP, and another site’s certificate must not be shown." },
    ],
    submit: async (v) => { await api('sites.default.set', { site: v.site }); toast("Saved", 'ok'); route(); },
  });
}

page('sites', {
  title: "Sites",
  async render(view, params) {
    head(view, "Sites", { actions: [{ t: "Create site", i: 'plus', primary: true, fn: () => siteForm() }, { t: "Default website", i: 'globe', fn: () => defaultSiteForm() }, { t: "Templates", i: 'file', fn: () => siteTemplates() }, { t: "Tasks", i: 'clock', fn: () => go('jobs') }] });
    const p = panel(view);
    const [rows, def] = await Promise.all([api('sites.list'), api('sites.default').catch(() => ({}))]);
    table(p, {
      rows,
      empty: { icon: 'globe', title: "No sites yet", text: "<p><button class=\"btn primary\" onclick=\"siteForm()\">Create your first site</button></p>" },
      columns: [
        { t: "Domain", r: (r) => `<div class="site-name">${h(r.unicode)}${def.site === r.site.domain ? " <span class=\"tag acc\" title=\"Opens for the server IP address and names without a website\">default</span>" : ''}<small>${h((r.site.aliases || []).join(', '))}</small></div>` },
        { t: "Owner", r: (r) => h(r.site.user), cls: 'hide-sm' },
        { t: "Mode", r: (r) => r.site.mode === 'php' ? `<span class="tag acc">PHP ${h(r.site.php)}</span> <span class="tag">${(r.site.backend === 'openlitespeed' ? 'OpenLiteSpeed' : r.site.backend === 'apache' ? 'Apache' : 'nginx') + (r.web_version ? ' ' + h(r.web_version) : '')}</span>${r.site.cache_ttl > 0 ? " <span class=\"tag ok\">cache</span>" : ''}` : (r.site.mode === 'proxy' ? "<span class=\"tag\">proxy</span>" : "<span class=\"tag\">static</span>") + ` <span class="tag">nginx${r.web_version ? ' ' + h(r.web_version) : ''}</span>` },
        { t: 'SSL', r: (r) => { if (!r.site.ssl) return r.site.auto_ssl ? "<span class=\"tag acc\" title=\"Issued automatically once the domain points to the server\">auto: awaiting DNS</span>" : "<span class=\"tag\">none</span>"; const d = Math.floor((new Date(r.ssl_expires) - Date.now()) / 864e5); return `<span class="ssl-cell"><span class="tag ${d < 14 ? 'warn' : 'ok'}">${icon('lock')} ${d} days</span>${r.site.auto_ssl ? "<span class=\"tag\">auto</span>" : ''}</span>`; } },
        { t: "Status", r: (r) => r.site.suspended ? "<span class=\"tag err\">disabled</span>" : "<span class=\"tag ok\">running</span>", cls: 'hide-sm' },
      ],
      onRow: (r) => siteForm(r.site),
      actions: (r) => [
        r.site.cache_ttl > 0 && { i: 'refresh', t: "Clear cache", fn: async () => { try { const x = await api('sites.cache.purge', { domain: r.site.domain }); toast("Cache cleared: " + fmtBytes(x.freed || 0), 'ok'); } catch (e) { fail(e); } } },
        { i: 'external', t: "Open site", fn: () => window.open((r.site.ssl ? 'https://' : 'http://') + r.site.domain, '_blank') },
        { i: 'folder', t: "Site files", fn: () => go('files', { path: r.site.root }) },
        { i: 'code', t: "Custom nginx rules", fn: () => nginxCustom(r.site.domain) },
        { i: 'wwwlog', t: "Logs", fn: () => go('wwwlogs', { site: r.site.domain }) },
        { i: r.site.suspended ? 'play' : 'pause', t: r.site.suspended ? "Enable" : "Disable", fn: async () => { try { await api('sites.suspend', { domain: r.site.domain, suspend: !r.site.suspended }); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: "Delete", danger: true, fn: async () => {
          const ok = await confirmBox("Delete site " + r.unicode + '?', "nginx and PHP configuration will be deleted. Databases and certificates are retained.", { extra: "<label class=\"check\"><input type=\"checkbox\" name=\"files\"><span>Also delete site files</span></label>" });
          if (!ok) return;
          try { await api('sites.delete', { domain: r.site.domain, files: ok.files }); toast("Site deleted", 'ok'); route(); } catch (e) { fail(e); }
        } },
      ],
    });
    if (params.new && view.isConnected) siteForm();
  },
});
window.siteForm = siteForm;

// Шаблоны сайтов: готовые и свои наборы правил nginx.
async function siteTemplates() {
  const d = await api('site.templates.list');
  const en = window.PanelLanguage?.language === 'en';
  const name = (x) => (en && x.name_en) || x.name, desc = (x) => (en && x.desc_en) || x.desc;
  const m = modal(`<h3>Site templates</h3><p class="muted">A template is a ready set of nginx rules for a type of site. The panel keeps the base (ports, SSL, protection, PHP, closed service files) and the template only adds rules. Built-in templates cannot be changed, but you can copy them as your own. You can use these variables in the rules: ${d.vars.map((v) => '<code>' + h(v) + '</code>').join(' ')}.</p><div data-t></div><div class="panel" style="margin-top:14px" data-lib></div><div class="btns"><button class="btn primary" data-new>Create a template</button><button class="btn" data-close>Close</button></div>`, { wide: true });
  const edit = (x, copy) => { m.close(); openForm({
    title: x && !copy ? "Template: " + name(x) : "New template", ok: "Save", wide: true,
    fields: [
      { name: 'name', label: "Name", value: x ? (copy ? name(x) + " (copy)" : name(x)) : '', required: true },
      { name: 'desc', label: "Description", value: x ? desc(x) : '', hint: "Briefly: what kind of site the template is for." },
      { name: 'rules', label: "nginx rules (inserted inside server {})", type: 'textarea', rows: 14, value: x ? x.rules : '    location / { try_files $uri $uri/ /index.php?$args; }\n', hint: "If the template has location /, it replaces the standard panel block. The PHP handler, the Let’s Encrypt check and the closing of service files stay with the panel. nginx checks the settings before applying; on an error everything is restored as it was." },
    ],
    submit: async (v) => { const r = await api('site.templates.save', { id: x && !copy ? x.id : '', name: v.name, desc: v.desc, rules: v.rules }); toast(r.sites ? "Saved, sites updated: " + r.sites : "Saved", 'ok'); siteTemplates(); },
  }); };
  const preview = async (x) => {
    const sites = await api('sites.list');
    if (!sites.length) { toast("Create a site first", 'err'); return; }
    m.close();
    openForm({
      title: "How it looks on a site", ok: "Show", wide: true,
      fields: [{ name: 'domain', label: "Site", type: 'select', options: sites.map((r) => [r.site.domain, r.unicode]), value: sites[0].site.domain }],
      submit: async (v) => { const r = await api('site.templates.preview', { domain: v.domain, id: x.id, rules: x.rules }); const w = modal(`<h3>${h(name(x))} · ${h(v.domain)}</h3><pre class="out code" style="max-height:60vh;overflow:auto">${h(r.config)}</pre><div class="btns"><button class="btn primary" data-close>Close</button></div>`, { wide: true }); },
    });
  };
  table($('[data-t]', m.el), {
    rows: d.templates,
    columns: [
      { t: "Name", r: (x) => `<b>${h(name(x))}</b> ${x.builtin ? "<span class=\"tag\">built-in</span>" : x.library ? "<span class=\"tag acc\">from the library</span>" : ''}<br><small class="muted">${h(desc(x))}</small>` },
      { t: "Sites", r: (x) => x.sites || '—', cls: 'nowrap' },
    ],
    actions: (x) => [
      x.rules && { i: 'search', t: "How it looks on a site", fn: () => preview(x) },
      x.rules && { i: 'copy', t: "Copy as your own", fn: () => edit(x, true) },
      !x.builtin && !x.library && { i: 'edit', t: "Edit", fn: () => edit(x, false) },
      !x.builtin && !x.library && { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete the template?", h(name(x)) + " will be deleted."))) return; try { await api('site.templates.delete', { id: x.id }); m.close(); siteTemplates(); } catch (e) { fail(e); } } },
    ],
  });
  const lb = d.library || {};
  const box = $('[data-lib]', m.el);
  box.innerHTML = `<p><b>Template library</b> ${lb.version ? `· version ${h(lb.version)} from ${h(lb.released)}, templates: ${h(lb.count)}` : "· not downloaded yet"}</p>
    <p class="muted">${lb.checked ? `Last check: ${fmtDate(lb.checked)}.` : "Updates have not been checked yet."} Source: ${h(lb.source || 'GitHub')} (fallback: itrupanel.com). The pack is signed with the ITRUPANEL release key; the panel accepts only signed and newer packs.</p>${lb.error ? `<p class="err">The last update failed: ${h(lb.error)}</p>` : ''}
    <div class="toolbar"><button class="btn" data-lib-update>${icon('refresh')}Update the library</button><label class="check"><input type="checkbox" data-lib-auto ${lb.auto ? 'checked' : ''}><span>Update automatically once a day</span></label></div>`;
  $('[data-lib-update]', box).onclick = async () => { try { const r = await api('site.templates.library.update'); toast(r.info, 'ok'); m.close(); siteTemplates(); } catch (e) { fail(e); m.close(); siteTemplates(); } };
  $('[data-lib-auto]', box).onchange = async (e) => { try { await api('site.templates.library.settings', { auto: e.target.checked }); toast("Saved", 'ok'); } catch (er) { fail(er); e.target.checked = !e.target.checked; } };
  $('[data-new]', m.el).onclick = () => edit(null, false);
}
window.siteTemplates = siteTemplates;

async function nginxCustom(domain) {
  const d = await api('sites.get', { domain });
  openForm({
    title: 'nginx: ' + domain, wide: true,
    fields: [
      { type: 'html', html: "<p class=\"hint\">A location / or location ^~ / block replaces the panel default. Other handlers, including PHP, remain in the configuration. Changes are not applied if nginx validation fails.</p>" },
      { name: 'text', label: "Custom rules (inserted inside server {})", type: 'textarea', rows: 14, value: d.custom },
      { type: 'html', html: `<label>Generated configuration (read only)</label><pre class="out code" style="max-height:340px">${h(d.vhost)}</pre>` },
    ],
    submit: async (v) => { await api('sites.custom.save', { domain, text: v.text }); toast("Saved, nginx reloaded", 'ok'); },
  });
}

// ================= Базы данных =================
async function dbServerOptions() {
  const d = await api('dbservers.list');
  return d.rows.filter((r) => r.active && !r.status).map((r) => [r.name, r.main ? 'MariaDB ' + dbShortVersion(r.version) + " (main)" : r.title + " · port " + r.port]);
}
async function dbForm() {
  const sites = await api('sites.list');
  const servers = await dbServerOptions();
  openForm({
    title: "Create database", ok: "Create",
    fields: [
      { name: 'server', label: "Database server", type: 'select', options: servers, value: '', show: () => servers.length > 1, hint: "<a href=\"#/dbservers\">Add another MySQL or MariaDB version</a>" },
      { name: 'name', label: "Database name", required: true, placeholder: 'shop', hint: "Latin letters, digits and _" },
      { name: 'user', label: "User", placeholder: "defaults to database name" },
      { name: 'password', label: "Password", type: 'password', value: genPass(16), gen: true },
      { name: 'site', label: "Site (for backups)", type: 'select', options: [['', "— not linked —"], ...sites.map((s) => [s.site.domain, s.unicode])] },
      { name: 'charset', label: "Character set", type: 'select', value: 'utf8mb4', options: [['utf8mb4', "utf8mb4 (recommended)"], ['utf8', 'utf8'], ['cp1251', 'cp1251'], ['latin1', 'latin1']] },
    ],
    submit: async (v) => {
      const s = sites.find((x) => x.site.domain === v.site);
      v.owner = s ? s.site.user : '';
      const r = await api('db.create', v);
      const port = (servers.find((x) => x[0] === v.server) || [])[1] || '';
      const m = /порт (\d+)/.exec(port);
      showSecrets("Database created", [["Host", m ? '127.0.0.1:' + m[1] : 'localhost'], ["Database", v.name], ["User", v.user || v.name], ["Password", r.password]]);
      route();
    },
  });
}
// Импорт дампа в базу: файл с компьютера (загрузка с прогрессом) или файл на сервере.
function dbImportForm(name) {
  const m = modal(`<h3>Import dump into ${h(name)}</h3>
    <div class="seg" data-src><button type="button" data-v="file" class="on">From computer</button><button type="button" data-v="path">File on server</button></div>
    <div class="field" data-file style="margin-top:16px"><label>Dump file</label><input type="file" accept=".sql,.gz,.zip"></div>
    <div class="field" data-path hidden style="margin-top:16px"><label>File path</label><input type="text" placeholder="/root/backup.sql.gz" spellcheck="false"><div class="hint">${user ? "A path inside your folder, for example a file uploaded via the file manager or FTP." : "Full path to the file on the server."}</div></div>
    <p class="hint">Formats: .sql, .sql.gz and .zip containing a .sql file. Tables with the same names are replaced by the dump data.${user ? " The dump runs with privileges on this database only." : ''}</p>
    <progress max="100" value="0" hidden style="width:100%"></progress><p class="muted" data-status></p>
    <div class="btns"><button class="btn" data-close>Cancel</button><button class="btn primary" data-ok>Import</button></div>`);
  let src = 'file';
  $$('[data-src] button', m.el).forEach((b) => (b.onclick = () => { src = b.dataset.v; $$('[data-src] button', m.el).forEach((x) => x.classList.toggle('on', x === b)); $('[data-file]', m.el).hidden = src !== 'file'; $('[data-path]', m.el).hidden = src !== 'path'; }));
  const ok = $('[data-ok]', m.el), bar = $('progress', m.el), status = $('[data-status]', m.el);
  ok.onclick = async () => {
    try {
      let r;
      if (src === 'file') {
        const f = $('[data-file] input', m.el).files[0];
        if (!f) { toast("Choose a dump file", 'err'); return; }
        ok.disabled = true; bar.hidden = false;
        r = await sendFileUpload([f], '/api/db/import-upload?name=' + encodeURIComponent(name), (p) => {
          if (p.stage === 'processing') { bar.value = 100; status.textContent = "File uploaded, starting the import…"; }
          else if (p.total) { bar.value = Math.floor(p.loaded / p.total * 100); status.textContent = fmtBytes(p.loaded) + ' / ' + fmtBytes(p.total); }
        });
      } else {
        const path = $('[data-path] input', m.el).value.trim();
        if (!path) { toast("Enter the file path", 'err'); return; }
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
 openForm({title:"Database access: "+name,fields:[
  {name:'mode',label:"Remote access",type:'select',value:d.mode,options:[['local',"Local only"],['all',"From any IP"],['ips',"Specified IPs only"]]},
  {name:'ips',label:"IP addresses (space separated)",value:(d.ips||[]).join(' '),show:v=>v.mode==='ips'},
  {name:'password',label:"Database password for all allowed addresses",type:'password',required:true,gen:true,hint:"12–72 characters. This password will also apply to local connections."},
  {type:'html',html:"<p class=\"hint\">Local access is always kept. Network listening is turned on in Database servers → Network connections.</p>"}
 ],submit:async v=>{await api('db.access',{name,save:true,mode:v.mode,password:v.password,ips:v.mode==='ips'?v.ips.split(/[\s,]+/).filter(Boolean):[]});toast("Saved",'ok');}});
}
async function databaseNetworkForm(){
 const d=await api('db.network');
 openForm({title:"MariaDB network connections",fields:[
  {name:'enabled',label:"Accept network connections (IPv4)",type:'checkbox',value:d.bind_address!=='127.0.0.1'&&d.bind_address!=='localhost'&&d.bind_address!=='::1'},
  {type:'html',html:`<p>Port: ${h(d.port)}. Permissions are configured separately for each database. For external connections, also allow this port in Firewall and in your hosting provider settings.</p><p class="hint">Saving restarts MariaDB and briefly interrupts database connections. Local access remains available.</p>`}
 ],submit:async v=>{await api('db.network',{save:true,enabled:v.enabled});toast("Saved",'ok');}});
}
function dbExport(name) {
  toast("Preparing dump " + name + "… The download starts automatically");
  location.href = '/api/db/export?name=' + encodeURIComponent(name);
}

page('db', {
  title: "Databases",
  async render(view, params) {
    const d = await api('db.list');
    head(view, "Databases", { actions: [
      { t: "Create database", i: 'plus', primary: true, fn: dbForm },
      d.pma ? { t: 'phpMyAdmin', i: 'phpmyadmin', href: '/phpmyadmin/' } : { t: "Install phpMyAdmin", i: 'download', fn: async () => { toast("Installing phpMyAdmin…"); try { await api('pma.install'); toast("phpMyAdmin installed", 'ok'); route(); } catch (e) { fail(e); } } },
    ], sub: d.server ? '' : "<span class=\"tag err\">MariaDB is not running</span>" });
    const p = panel(view);
    table(p, {
      rows: d.rows,
      empty: { icon: 'database', title: "No databases" },
      columns: [
        { t: "Database", r: (r) => `<b>${h(r.db.name)}</b>` },
        { t: "User", r: (r) => h(r.db.user) },
        { t: "Server", r: (r) => `<span class="tag">${h(r.server)}</span>` + (r.port !== 3306 ? ` <small class="muted">:${r.port}</small>` : ''), cls: 'hide-sm' },
        { t: "Site", r: (r) => h(r.db.site || '—'), cls: 'hide-sm' },
        { t: "Size", r: (r) => fmtBytes(r.size), cls: 'nowrap' },
        { t: "Created", r: (r) => fmtDate(r.db.created, false), cls: 'hide-sm' },
      ],
      actions: (r) => [
        { i: 'download', t: "Download dump", fn: () => dbExport(r.db.name) },
        { i: 'upload', t: "Import dump", fn: () => dbImportForm(r.db.name) },
        {i:'shield',t:'IP',fn:()=>databaseAccessForm(r.db.name)},
        { i: 'key', t: "Change password", fn: () => openForm({ title: "User password " + r.db.user, ok: "Change", fields: [{ name: 'password', label: "New password", type: 'password', value: genPass(16), gen: true }], submit: async (v) => { const x = await api('db.password', { name: r.db.name, password: v.password }); showSecrets("Password changed", [["User", r.db.user], ["Password", x.password]]); } }) },
        { i: 'trash', t: "Delete", danger: true, fn: async () => {
          const ok = await confirmBox("Delete database " + r.db.name + '?', "All database data will be permanently deleted unless you have a backup.", { extra: "<label class=\"check\"><input type=\"checkbox\" name=\"drop_user\" checked><span>Also delete user " + h(r.db.user) + '</span></label>' });
          if (!ok) return;
          try { await api('db.delete', { name: r.db.name, drop_user: ok.drop_user }); toast("Database deleted", 'ok'); route(); } catch (e) { fail(e); }
        } },
      ],
    });
    if (params.new && view.isConnected) dbForm();
  },
});

// ================= DNS =================
async function zoneForm(d) {
  openForm({
    title: "Create DNS zone", ok: "Create",
    fields: [
      { name: 'name', label: "Domain", required: true, placeholder: 'example.ru' },
      { name: 'ip', label: "IP address for records", value: d.ip || '', hint: "Creates @ and www records" + (d.settings.mail ? ", mail, MX and SPF" : '') + " and CAA for Let’s Encrypt" },
      { name: 'ns', label: "Name servers (NS)", value: (d.settings.nameservers || []).join(' '), placeholder: "leave blank for ns1 and ns2 of this domain", hint: "Set these same name servers at your domain registrar" },
      { name: 'mail', label: "Add mail records (mail, MX, SPF)", type: 'checkbox', value: !!d.settings.mail },
    ],
    submit: async (v) => { const z = await api('dns.zone.create', v); toast("Zone created", 'ok'); go('dns', { zone: z.name }); },
  });
}
function recordForm(zone, rec, types, reload) {
  const r = rec || { type: 'A', name: '', ttl: 0, prio: 10, value: '' };
  const hints = { A: "IPv4, e.g. 203.0.113.10", AAAA: "IPv6 address", CNAME: "Hostname, e.g. example.com. (a trailing dot indicates a fully qualified name)", MX: "Mail server, e.g. mail", TXT: "Text: SPF, DKIM, domain verification", NS: "Name server, e.g. ns1.example.com.", SRV: "weight port host, e.g. 5 5060 sip.example.com.", CAA: '0 issue "letsencrypt.org"' };
  openForm({
    title: rec ? "Record " + r.type : "New record",
    fields: [
      { name: 'type', label: "Type", type: 'seg', value: r.type, options: types.map((t) => [t, t]) },
      { name: 'name', label: "Name", value: r.name === '@' ? '' : r.name, placeholder: "@ — domain itself", hint: (v) => '' },
      { type: 'html', html: (v) => `<p class="muted" style="margin:-8px 0 0">Full name: <span class="mono">${h((v.name && v.name !== '@' ? v.name + '.' : '') + zone)}</span></p>` },
      { name: 'prio', label: "Priority", type: 'number', value: r.prio || 10, show: (v) => v.type === 'MX' || v.type === 'SRV' },
      { name: 'value', label: "Value", value: r.value, required: true, rerender: false },
      { type: 'html', html: (v) => `<p class="muted" style="margin:-8px 0 0">${h(hints[v.type] || '')}</p>` },
      { name: 'ttl', label: "TTL, seconds (0 uses zone default)", type: 'number', value: r.ttl || 0 },
    ],
    submit: async (v) => { await api('dns.record.save', { zone, record: { id: r.id || '', name: v.name || '@', type: v.type, ttl: +v.ttl || 0, prio: +v.prio || 0, value: v.value } }); toast("Saved, zone updated", 'ok'); reload(); },
  });
}
page('dns', {
  title: "DNS management",
  async render(view, params) {
    const d = await api('dns.list');
    if (!view.isConnected) return;
    if (params.zone) return zonePage(view, params.zone);
    head(view, "DNS management", { sub: d.installed ? (d.active ? '' : "<span class=\"tag warn\">BIND DNS server is stopped</span>") : "<span class=\"tag warn\">BIND DNS server is not installed</span>", actions: [
      d.installed ? { t: "Create zone", i: 'plus', primary: true, fn: () => zoneForm(d) } : { t: "Install BIND", i: 'download', primary: true, fn: async () => { toast("Installing BIND…"); try { await api('dns.install'); toast("Installed", 'ok'); route(); } catch (e) { fail(e); } } },
    ] });
    const p = panel(view);
    table(p, {
      rows: d.rows,
      empty: { icon: 'dns', title: "No zones yet", text: "<p>Create a zone, then set this server’s name servers (NS) at your domain registrar.</p>" },
      columns: [
        { t: "Domain", r: (r) => `<div class="site-name">${h(r.unicode)}<small>${h(r.ns.join(', '))}</small></div>` },
        { t: "Records", r: (r) => r.records },
        { t: "Delegation", r: (r) => `<span class="tag" data-deleg="${h(r.name)}"><span class="spin" style="width:12px;height:12px;border-width:2px"></span></span>`, cls: 'hide-sm' },
        { t: "Serial number", r: (r) => `<span class="mono">${r.serial}</span>`, cls: 'hide-sm' },
      ],
      onRow: (r) => go('dns', { zone: r.name }),
      actions: (r) => [
        { i: 'edit', t: "Records", fn: () => go('dns', { zone: r.name }) },
        { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete zone " + r.unicode + '?', "All zone records will be deleted. This DNS server will stop resolving the domain."))) return; try { await api('dns.zone.delete', { name: r.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    $$('[data-deleg]', p).forEach(async (el) => {
      try { const x = await api('dns.delegation', { name: el.dataset.deleg }); el.className = 'tag ' + (x.ok ? 'ok' : 'warn'); el.textContent = x.ok ? "to this server" : (x.got.length ? "to other name servers" : "not delegated"); el.title = "Current: " + (x.got.join(', ') || '—') + "\nRequired: " + x.want.join(', '); } catch (e) { el.textContent = '—'; }
    });
    const s = panel(view, "Settings");
    s.innerHTML = `<div class="cols2"><div class="field"><label>Name servers for new zones (NS)</label><input type="text" data-ns value="${h((d.settings.nameservers || []).join(' '))}" placeholder="leave blank for ns1 and ns2 of each domain"><div class="hint">For example: ns1.mydomain.com ns2.mydomain.com. This server’s IP: <span class="mono">${h(d.ip)}</span></div></div>
      <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" data-mail ${d.settings.mail ? 'checked' : ''}><span>Include mail records in new zones (mail, MX, SPF)</span></label></div></div><button class="btn primary" data-s>Save</button>`;
    $('[data-s]', s).onclick = async () => { try { await api('dns.settings', { nameservers: $('[data-ns]', s).value, mail: $('[data-mail]', s).checked }); toast("Saved", 'ok'); } catch (e) { fail(e); } };
    if (params.new && view.isConnected) zoneForm(d);
  },
});
async function zonePage(view, name) {
  const load = async () => {
    const d = await api('dns.zone.get', { name });
    view.innerHTML = '';
    head(view, d.zone.name, { sub: "<a href=\"#/dns\">← All zones</a> · serial number <span class=\"mono\">" + d.zone.serial + '</span>', actions: [
      { t: "Add record", i: 'plus', primary: true, fn: () => recordForm(name, null, d.types, load) },
      { t: "Zone settings", i: 'edit', fn: () => openForm({ title: "Zone settings", fields: [{ name: 'email', label: "Administrator email (SOA)", value: d.zone.email }, { name: 'ttl', label: "Default TTL, seconds", type: 'number', value: d.zone.ttl }], submit: async (v) => { await api('dns.zone.settings', { name, email: v.email, ttl: +v.ttl }); load(); } }) },
      { t: "Zone file", i: 'code', fn: () => showOutput("Zone " + name, d.text) },
    ] });
    const p = panel(view);
    const order = { NS: 0, A: 1, AAAA: 2, CNAME: 3, MX: 4, TXT: 5, SRV: 6, CAA: 7 };
    const rows = d.zone.records.slice().sort((a, b) => order[a.type] - order[b.type] || a.name.localeCompare(b.name));
    table(p, {
      rows,
      empty: { icon: 'dns', title: "No records" },
      columns: [
        { t: "Name", r: (r) => `<b class="mono">${h(r.name)}</b>` },
        { t: "Type", r: (r) => `<span class="tag acc">${h(r.type)}</span>` },
        { t: "Value", r: (r) => `<span class="mono" style="word-break:break-all">${r.type === 'MX' || r.type === 'SRV' ? '<span class="muted">' + r.prio + '</span> ' : ''}${h(r.value)}</span>` },
        { t: 'TTL', r: (r) => r.ttl || '<span class="muted">' + d.zone.ttl + '</span>', cls: 'hide-sm' },
      ],
      onRow: (r) => recordForm(name, r, d.types, load),
      actions: (r) => [
        { i: 'edit', t: "Edit", fn: () => recordForm(name, r, d.types, load) },
        { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete record?", h(r.name + ' ' + r.type + ' ' + r.value)))) return; try { await api('dns.record.delete', { zone: name, ids: [r.id] }); load(); } catch (e) { fail(e); } } },
      ],
    });
  };
  await load();
}

// ================= Серверы БД =================
page('dbservers', {
  title: "Database servers",
  async render(view) {
    head(view, "Database servers", { sub: "Main MariaDB and additional MySQL/MariaDB versions, each in its own Docker container on a separate port", actions: [{ t: "Add server", i: 'plus', primary: true, fn: () => add() }, {t:"Network connections",i:'globe',fn:databaseNetworkForm}] });
    const p = panel(view);
    let data;
    const add = () => openForm({
      title: "Add database server", ok: "Install",
      fields: [
        { name: 'type', label: "Database engine", type: 'seg', value: 'mysql', options: [['mysql', 'MySQL'], ['mariadb', 'MariaDB']] },
        { name: 'version', label: "Version", type: 'select', value: '8.4', options: (v) => (data.versions[v.type] || []).slice().reverse().map((x) => [x, (v.type === 'mysql' ? 'MySQL ' : 'MariaDB ') + x]) },
        { type: 'html', html: (v) => `<p class="muted" style="margin:0">${data.docker ? '' : "Docker will be installed automatically. "}The server gets a free port (3307, 3308…) and is accessible only from this server: <span class="mono">127.0.0.1:port</span>.${v.type === 'mysql' && v.version === '5.7' ? "<br><b>MySQL 5.7</b> is available only for x86_64 and is no longer supported by its developer." : ''}</p>` },
      ],
      onChange: (v, n) => { if (n === 'type' || !data.versions[v.type].includes(v.version)) v.version = data.versions[v.type].slice(-2)[0]; },
      submit: async (v) => { await api('dbservers.create', v); toast("Installing server — this takes 1–3 minutes"); load(); },
    });
    const load = async () => {
      data = await api('dbservers.list');
      table(p, {
        rows: data.rows,
        columns: [
          { t: "Server", r: (r) => `<b>${h(r.title)}</b><br><small class="muted mono">${h(r.version)}</small>` },
          { t: "Connection", r: (r) => `<span class="mono">${h(r.host)}${r.main ? '' : ':' + r.port}</span>` },
          { t: "Databases", r: (r) => r.dbs },
          { t: "Status", r: (r) => r.status === 'installing' ? "<span class=\"tag acc\"><span class=\"spin\" style=\"width:12px;height:12px;border-width:2px\"></span> installing</span>" : r.status === 'error' ? `<span class="tag err" title="${h(r.error)}">error</span>` : r.active ? "<span class=\"tag ok\">running</span>" : "<span class=\"tag warn\">stopped</span>" },
        ],
        onRow: (r) => r.error && showOutput(r.title, r.error),
        actions: (r) => r.main ? [{ i: 'refresh', t: "Restart", fn: async () => { try { await api('system.service', { name: 'mariadb', action: 'restart' }); toast("Restarted", 'ok'); load(); } catch (e) { fail(e); } } }] : r.status === 'installing' ? [] : [
          r.status === 'error' && { i: 'refresh', t: "Retry installation", fn: async () => { await api('dbservers.action', { name: r.name, action: 'reinstall' }); load(); } },
          !r.status && { i: 'refresh', t: "Restart", fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'restart' }); load(); } catch (e) { fail(e); } } },
          !r.status && (r.active ? { i: 'stop', t: "Stop", fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'stop' }); load(); } catch (e) { fail(e); } } } : { i: 'play', t: "Start", fn: async () => { try { await api('dbservers.action', { name: r.name, action: 'start' }); load(); } catch (e) { fail(e); } } }),
          { i: 'trash', t: "Delete", danger: true, fn: async () => {
            const ok = await confirmBox("Delete " + r.title + '?', "The server container will be deleted.", { extra: "<label class=\"check\"><input type=\"checkbox\" name=\"data\"><span>Also delete data files</span></label>" });
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
  if (!sites.length) { toast("Create a site first", 'err'); return; }
  openForm({
    title: "Issue Let’s Encrypt certificate", ok: "Issue",
    fields: [
      { name: 'site', label: "Site", type: 'select', options: sites.map((s) => [s.site.domain, s.unicode]), value: sites[0].site.domain },
      { name: 'www', label: "Include www alias", type: 'checkbox', value: true },
      { name: 'aliases', label: "Include other aliases", type: 'checkbox', value: true },
      { name: 'email', label: "Email (Let’s Encrypt notifications)", value: meta.email || '' },
      { type: 'html', html: "<p class=\"muted\" style=\"margin:0\">All names must resolve to this server’s IP and port 80 must be open. Renewal is automatic. By issuing a certificate, you accept the Let’s Encrypt terms.</p>" },
    ],
    submit: async (v) => { toast("Requesting certificate…"); const r = await api('ssl.le', v); toast(r?.skipped?.length ? "Certificate issued without " + r.skipped.join(', ') + " — they point to another server. Fix DNS and issue again." : "Certificate issued and applied", r?.skipped?.length ? 'warn' : 'ok'); route(); },
  });
}
async function selfForm() {
  const sites = await api('sites.list');
  openForm({
    title: "Self-signed certificate", ok: "Create",
    fields: [
      { name: 'domains', label: "Domains or IPs (space separated)", required: true, placeholder: 'example.ru www.example.ru' },
      { name: 'name', label: "Certificate name", placeholder: "by first domain" },
      { name: 'days', label: "Duration, days", type: 'number', value: 365 },
      { name: 'site', label: "Link to site", type: 'select', options: [['', "— not connected —"], ...sites.map((s) => [s.site.domain, s.unicode])] },
    ],
    submit: async (v) => { v.days = Number(v.days); await api('ssl.self', v); toast("Certificate created", 'ok'); route(); },
  });
}
async function importForm() {
  const sites = await api('sites.list');
  openForm({
    title: "Add existing certificate", ok: "Add", wide: true,
    fields: [
      { name: 'name', label: "Certificate name", placeholder: "by certificate domain" },
      { name: 'crt', label: "Certificate (PEM)", type: 'textarea', required: true, placeholder: '-----BEGIN CERTIFICATE-----' },
      { name: 'chain', label: "Chain (CA bundle), if available", type: 'textarea', rows: 4 },
      { name: 'key', label: "Private key", type: 'textarea', required: true, placeholder: '-----BEGIN PRIVATE KEY-----' },
      { name: 'site', label: "Link to site", type: 'select', options: [['', "— not connected —"], ...sites.map((s) => [s.site.domain, s.unicode])] },
    ],
    submit: async (v) => { await api('ssl.import', v); toast("Certificate added", 'ok'); route(); },
  });
}
page('ssl', {
  title: "SSL certificates",
  async render(view, params) {
    head(view, "SSL certificates", { actions: [
      { t: 'Let’s Encrypt', i: 'plus', primary: true, fn: leForm },
      { t: "Self-signed", i: 'shield', fn: selfForm },
      { t: "Add custom", i: 'shieldcert', fn: importForm },
    ] });
    const p = panel(view);
    const rows = await api('ssl.list');
    table(p, {
      rows,
      empty: { icon: 'lock', title: "No certificates", text: "<p>Issue a free Let’s Encrypt certificate for your site.</p>" },
      columns: [
        { t: "Name", r: (r) => `<div class="site-name">${h(r.cert.name)}<small>${h((r.cert.domains || []).join(', '))}</small></div>` },
        { t: "Type", r: (r) => ({ le: '<span class="tag acc">Let’s Encrypt</span>', self: "<span class=\"tag\">self-signed</span>", custom: "<span class=\"tag\">custom</span>" }[r.cert.type]) },
        { t: "Valid until", r: (r) => `${fmtDate(r.cert.expires, false)} <span class="tag ${r.days < 0 ? 'err' : r.days < 14 ? 'warn' : 'ok'}">${r.days < 0 ? "expired" : r.days + " days"}</span>`, cls: 'nowrap' },
        { t: "Sites", r: (r) => h((r.sites || []).join(', ') || '—'), cls: 'hide-sm' },
      ],
      actions: (r) => [
        r.cert.type === 'le' && { i: 'refresh', t: "Renew now", fn: async () => { toast("Renewing…"); try { await api('ssl.renew', { name: r.cert.name }); toast("Renewed", 'ok'); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete certificate " + r.cert.name + '?', "Sites using this certificate will switch to HTTP."))) return; try { await api('ssl.delete', { name: r.cert.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (params.le && view.isConnected) leForm();
    if (params.self && view.isConnected) selfForm();
    if (params.import && view.isConnected) importForm();
  },
});

// ================= CRON =================
const CRON_PRESETS = [['', "Custom schedule"], ['* * * * *', "Every minute"], ['*/5 * * * *', "Every 5 minutes"], ['*/15 * * * *', "Every 15 minutes"], ['0 * * * *', "Every hour"], ['0 3 * * *', "Every day at 03:00"], ['0 4 * * 1', "Mondays at 04:00"], ['0 5 1 * *', "On day 1 at 05:00"], ['@reboot', "At server startup"]];
function cronHuman(s) { const p = CRON_PRESETS.find((x) => x[0] === s); return p && p[0] ? p[1] : ''; }
async function cronForm(job, users) {
  const j = job || { enabled: true, user: users[1] || 'root', schedule: '0 3 * * *' };
  openForm({
    title: job ? "CRON job" : "New CRON job",
    fields: [
      { name: 'user', label: "Run as user", type: 'select', options: users.map((u) => [u, u]), value: j.user },
      { name: 'preset', label: "Quick selection", type: 'select', options: CRON_PRESETS, value: cronHuman(j.schedule) ? j.schedule : '' },
      { name: 'schedule', label: "Schedule (minute hour day month weekday)", value: j.schedule, required: true, hint: "Example: <code>*/10 * * * *</code> — every 10 minutes" },
      { name: 'command', label: "Command", value: j.command || '', required: true, placeholder: '/usr/bin/php8.3 /var/www/user/data/www/site.ru/cron.php' },
      { name: 'comment', label: "Comment", value: j.comment || '' },
      { name: 'enabled', label: "Enabled", type: 'checkbox', value: j.enabled },
    ],
    onChange: (v, name) => { if (name === 'preset' && v.preset) v.schedule = v.preset; },
    submit: async (v) => { v.id = j.id || ''; await api('cron.save', v); toast("Saved", 'ok'); route(); },
  });
}
page('cron', {
  title: "CRON scheduler",
  async render(view) {
    const d = await api('cron.list');
    head(view, "CRON scheduler", { actions: [{ t: "Create job", i: 'plus', primary: true, fn: () => cronForm(null, d.users) }] });
    const p = panel(view);
    table(p, {
      rows: d.rows || [],
      empty: { icon: 'cron', title: "No jobs" },
      columns: [
        { t: "Schedule", r: (r) => `<span class="mono">${h(r.schedule)}</span><br><small class="muted">${h(cronHuman(r.schedule))}</small>`, cls: 'nowrap' },
        { t: "Command", r: (r) => `<span class="mono">${h(r.command)}</span>${r.comment ? `<br><small class="muted">${h(r.comment)}</small>` : ''}` },
        { t: "User", r: (r) => h(r.user), cls: 'hide-sm' },
        { t: "Status", r: (r) => r.enabled ? "<span class=\"tag ok\">enabled</span>" : "<span class=\"tag\">disabled</span>" },
      ],
      onRow: (r) => cronForm(r, d.users),
      actions: (r) => [
        { i: 'play', t: "Run now", fn: async () => { toast("Running…"); try { const o = await api('cron.run', { id: r.id }); showOutput("Result: " + r.command, (o.error ? o.error + '\n\n' : '') + (o.output || '')); } catch (e) { fail(e); } } },
        { i: r.enabled ? 'pause' : 'check', t: r.enabled ? "Disable" : "Enable", fn: async () => { try { await api('cron.save', { ...r, enabled: !r.enabled }); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete job?", h(r.command)))) return; try { await api('cron.delete', { id: r.id }); route(); } catch (e) { fail(e); } } },
      ],
    });
  },
});

// ================= Резервные копии =================
// Копии трёх видов: файлы сайтов, каждая база данных отдельно и настройки панели.
async function backupForm(tab = 'files') {
  if (tab === 'panel') {
    if (!(await confirmBox("Create a panel settings backup?", "General preferences, DNS defaults and backup settings, including the FTP password. The administrator account, panel address and certificates are excluded.", { ok: "Create", danger: false }))) return;
    try { await api('backups.create', { kind: 'panel' }); toast("Creating backup…"); route(); } catch (e) { fail(e); }
    return;
  }
  if (tab === 'db') {
    const names = Object.keys((await api('backups.list')).db_sites || {}).sort();
    if (!names.length) { toast("No databases yet", 'err'); return; }
    openForm({
      title: "Database backup", ok: "Create",
      fields: [
        { name: 'db', label: "Database", type: 'select', options: [['*', "All databases"], ...names.map((n) => [n, n])], value: '*', hint: "Each database is saved as a separate backup: download it as .sql.gz or restore it without touching website files or other databases." },
      ],
      submit: async (v) => { await api('backups.create', { kind: 'database', db: v.db }); toast("Creating backup…"); route(); },
    });
    return;
  }
  const sites = await api('sites.list');
  if (!sites.length) { toast("No sites to back up", 'err'); return; }
  openForm({
    title: "Website files backup", ok: "Create",
    fields: [
      { name: 'site', label: "Site", type: 'select', options: [['*', "All sites"], ...sites.map((s) => [s.site.domain, s.unicode])], value: '*' },
      { name: 'mode', label: "Archive mode", type: 'seg', value: 'full', options: [['full',"Full"],['incremental',"Incremental"]], hint: "An incremental backup stores file changes. The first backup is full, followed by at most six incremental ones; restoring requires the whole chain." },
      { name: 'site_settings', label: "Site settings", type: 'checkbox', value: false, hint: "Site parameters and custom nginx rules. Restoration keeps the current site directory and owner; SSL certificates must already exist on the server." },
      { type: 'html', html: "<p class=\"muted\" style=\"margin:0\">The backup is created in the background. Databases are backed up separately on the Databases tab.</p>" },
    ],
    submit: async (v) => { await api('backups.create', { ...v, kind: 'files' }); toast("Creating backup…"); route(); },
  });
}
// Скачивание копии базы — сразу дамп .sql.gz, без архива с манифестом.
async function backupDownload(r) {
  if (r.kind !== 'database') { location.href = '/api/backups/download?id=' + encodeURIComponent(r.id); return; }
  try {
    const res = await api('backups.extract', { id: r.id, dbs: [r.db] });
    waitJobDownload(res.job);
  } catch (e) { fail(e); }
}
// Содержимое копии (как в ISPmanager): файлы сайта по папкам и базы данных; выбранное можно
// скачать или восстановить. Восстанавливаются только отмеченные пути, остальные файлы сайта не меняются.
async function backupContents(r) {
  const m = modal(`<div class="backup-settings-head"><h3>Backup contents · ${h(r.site)}</h3><button class="btn" data-close>Close</button></div><p class="muted">${h(new Date(r.created).toLocaleString(window.PanelLanguage?.locale || 'ru-RU'))}</p><div data-contents><div class="loading"><span class="spin"></span></div></div>`, { wide: true });
  const body = $('[data-contents]', m.el);
  const selected = new Map();
  let current = '';
  const key = (kind, value) => kind + ':' + value;
  const act = async (what) => {
    const paths = [...selected.values()].filter(x => x.kind === 'path').map(x => x.value);
    const dbs = [...selected.values()].filter(x => x.kind === 'db').map(x => x.value);
    if (!paths.length && !dbs.length) { toast("Select files, folders or databases", 'err'); return; }
    if (what === 'download') {
      try {
        const res = await api('backups.extract', { id: r.id, paths, dbs });
        waitJobDownload(res.job);
      } catch (e) { fail(e); }
      return;
    }
    const list = [...paths.map(p => p === '' ? "whole website" : '/' + p), ...dbs.map(d => "database " + d)];
    if (!await confirmBox("Restore the selection?", "Will be replaced with the backup contents: " + list.map(h).join(', ') + ". Other files and databases stay unchanged.", { ok: "Restore" })) return;
    try { await api('backups.restore', { id: r.id, options: { paths, dbs } }); toast("Restored", 'ok'); selected.clear(); load(current); } catch (e) { fail(e); }
  };
  const load = async (dir) => {
    current = dir;
    let d;
    try { d = await api('backups.browse', { id: r.id, path: dir }); } catch (e) { body.innerHTML = `<p class="err">${h(e.message)}</p>`; return; }
    if (!m.el.isConnected) return;
    const crumbs = [`<a href="#" data-dir="">${h(r.site)}</a>`, ...dir.split('/').filter(Boolean).map((part, i, all) => `<a href="#" data-dir="${h(all.slice(0, i + 1).join('/'))}">${h(part)}</a>`)].join(' / ');
    const check = (kind, value, label) => `<input type="checkbox" data-pick="${h(key(kind, value))}" data-kind="${kind}" data-value="${h(value)}" ${selected.has(key(kind, value)) ? 'checked' : ''} aria-label="${h(label)}">`;
    let html = `<div class="toolbar" style="margin:8px 0 12px"><button class="btn primary" data-act="restore">${icon('refresh')}Restore</button><button class="btn" data-act="download">${icon('download')}Download</button><span class="muted" data-count></span></div>`;
    if (d.needs_scan) html += `<div class="panel" style="margin-bottom:12px"><p>This backup’s contents have not been read yet (the backup was connected from FTP or created before this version).</p><button class="btn" data-scan>${icon('search')}Read archive</button></div>`;
    if (d.has_db && !dir && d.dbs.length) {
      html += `<h4 style="margin:8px 0">Databases</h4><div class="table-wrap"><table class="grid"><tbody>${d.dbs.map(db => `<tr><td class="chk">${check('db', db, db)}</td><td><span class="backup-item">${icon('database')}<b>${h(db)}</b></span></td></tr>`).join('')}</tbody></table></div>`;
    }
    if (d.has_files) {
      html += `<h4 style="margin:14px 0 8px">Site files</h4><div class="muted mono" style="margin-bottom:6px">${crumbs}</div><div class="table-wrap"><table class="grid"><thead><tr><th class="chk">${check('path', dir, dir ? "Whole folder" : "Whole website")}</th><th>${dir ? "Whole folder /" + h(dir) : "Whole website"}</th><th>Size</th><th>Modified</th></tr></thead><tbody>`;
      if (dir) { const up = dir.split('/').slice(0, -1).join('/'); html += `<tr><td></td><td><span class="backup-item"><a href="#" data-dir="${h(up)}">${icon('folder')}..</a></span></td><td></td><td></td></tr>`; }
      html += d.entries.map(e => { const p = dir ? dir + '/' + e.name : e.name; return `<tr><td class="chk">${check('path', p, p)}</td><td><span class="backup-item">${e.dir ? `<a href="#" data-dir="${h(p)}">${icon('folder')}${h(e.name)}</a>` : `${icon('file')}${h(e.name)}`}</span></td><td>${e.dir ? '' : fmtBytes(e.size)}</td><td>${e.modified ? new Date(e.modified * 1000).toLocaleString(window.PanelLanguage?.locale || 'ru-RU') : ''}</td></tr>`; }).join('') || (d.needs_scan ? '' : "<tr><td></td><td class=\"muted\" colspan=\"3\">Folder is empty</td><td></td></tr>");
      html += '</tbody></table></div>';
    }
    body.innerHTML = html;
    const count = () => { $('[data-count]', body).textContent = selected.size ? "Selected: " + selected.size : ''; };
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
    if (!(await confirmBox(db ? "Restore database " + r.db + '?' : "Restore panel settings?", db ? "The current database contents will be replaced with the backup from " + fmtDate(r.created) + ". Website files and other databases stay unchanged." : "Panel settings will be replaced with the backup from " + fmtDate(r.created) + ". Websites, databases and the administrator account stay unchanged.", { ok: "Restore" }))) return;
    try { await api('backups.restore', { id: r.id, options: db ? { databases: true } : { panel_settings: true } }); toast("Restored", 'ok'); route(); } catch (e) { fail(e); }
    return;
  }
  openForm({title:"Restore " + r.site + '?',ok:"Restore",fields:[
    {name:'files',label:"Site files",type:'checkbox',value:r.kind==='full'||r.kind==='files',show:()=>r.kind==='full'||r.kind==='files'},
    {name:'databases',label:"Databases",type:'checkbox',value:r.kind==='full'||r.kind==='db',show:()=>r.kind==='full'||r.kind==='db'},
    {name:'site_settings',label:"Site settings",type:'checkbox',value:false,show:()=>!!r.site_settings},
    {name:'panel_settings',label:"Panel settings",type:'checkbox',value:false,show:()=>!!r.panel_settings},
    {type:'html',html:"<p class=\"muted\">Selected data will be replaced with the backup contents. Panel settings apply to the whole server.</p>" + (r.parent_id ? "<p class=\"muted\">The panel will assemble the full backup and subsequent changes automatically. Every archive in the chain must be available.</p>" : '')},
  ],submit:async(options)=>{await api('backups.restore',{id:r.id,options});toast("Restored",'ok');route();}});
}
async function backupDiscoverFTP() {
  if (!(await confirmBox("Find backups in the storage?", "The panel will read the saved FTP or cloud folder and add the ITRUPANEL archives it finds to the list. Files in the storage are not changed. To restore, create a website with the same domain; incremental backups need the whole chain. The first new backup will be a full one.", {ok:"Start discovery",danger:false}))) return;
  try { const r = await api('backups.ftp.discover'); watchJob(r.job); } catch (e) { fail(e); }
}
async function backupSettingsForm() {
  const m = modal(`<div class="backup-settings-head"><h3>Backup settings</h3><button class="btn" data-close>Close</button></div><div data-backup-settings><div class="loading"><span class="spin"></span></div></div>`, {wide:true});
  const body = $('[data-backup-settings]', m.el);
  try {
    const [d, f] = await Promise.all([api('backups.list'), api('backups.ftp.get')]);
    if (!m.el.isConnected) return;
    body.innerHTML = '';
    const set = panel(body, "Automatic backups");
    const where = panel(body, "Backup storage");
      const s = d.settings;
      set.innerHTML = `<div style="display:flex;gap:18px;align-items:center;flex-wrap:wrap"><label class="check"><input type="checkbox" id="bd" ${s.daily ? 'checked' : ''}><span>Create backups every day</span></label>
        <span class="muted">in</span><select id="bh" class="search-input" style="min-width:90px">${[...Array(24).keys()].map((x) => `<option ${x === s.hour ? 'selected' : ''} value="${x}">${String(x).padStart(2, '0')}:00</option>`).join('')}</select>
        <span class="muted">retain</span><input id="bk" type="number" class="search-input" style="min-width:0;width:90px" min="1" max="60" step="1" value="${s.keep}"><span class="muted">latest</span></div><div class="field" style="margin-top:18px"><label for="bm">Archive mode</label><select id="bm"><option value="full" ${s.mode!=='incremental' ? 'selected' : ''}>Full</option><option value="incremental" ${s.mode==='incremental' ? 'selected' : ''}>Incremental</option></select><p class="hint">The mode applies to website files. The latest restore points and all chain parts they need are kept. The first backup is full, followed by at most six incremental ones. Databases are always saved in full.</p></div><div style="display:flex;gap:18px;flex-wrap:wrap;margin-top:18px"><label class="check"><input type="checkbox" checked disabled><span>Files of all websites</span></label><label class="check"><input type="checkbox" id="bdb" ${s.databases !== false ? 'checked' : ''}><span>All databases, each separately</span></label><label class="check"><input type="checkbox" id="bss" ${s.site_settings ? 'checked' : ''}><span>Site settings</span></label><label class="check"><input type="checkbox" id="bps" ${s.panel_settings ? 'checked' : ''}><span>Panel settings</span></label></div><p class="muted" style="margin:12px 0 0">Backup directory: <span class="mono">${h(d.dir)}</span></p>`;
      f.s3 = f.s3 || {};
      const presets = [
        ['', "Another S3-compatible service", '', '', true, ''],
        ['aws', 'Amazon S3', 'https://s3.eu-central-1.amazonaws.com', 'eu-central-1', false, "Replace eu-central-1 with the region of your bucket — both in the address and in the Region field."],
        ['yandex', 'Yandex Object Storage', 'https://storage.yandexcloud.net', 'ru-central1', true, ''],
        ['r2', 'Cloudflare R2', 'https://ACCOUNT_ID.r2.cloudflarestorage.com', 'auto', true, "Replace ACCOUNT_ID with your Cloudflare account ID."],
        ['wasabi', 'Wasabi', 'https://s3.wasabisys.com', 'us-east-1', true, ''],
        ['do', 'DigitalOcean Spaces', 'https://nyc3.digitaloceanspaces.com', 'nyc3', true, "Replace nyc3 with the region of your Space — both in the address and in the Region field."],
      ];
      f.webdav = f.webdav || {}; f.dropbox = f.dropbox || {}; f.gdrive = f.gdrive || {};
      const services = [
        ['s3', 's3', "S3-compatible cloud (Amazon S3, Yandex Object Storage, Cloudflare R2, Wasabi…)"],
        ['yandex', 'webdav', "Yandex Disk"],
        ['mailru', 'webdav', "Mail.ru Cloud"],
        ['dav', 'webdav', "Other WebDAV (Nextcloud, ownCloud…)"],
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
        where.innerHTML = `<div class="seg" data-t>${[['local', "On this server"], ['ftp', "On FTP server"], ['both', "Local and FTP"], ['cloud', "In the cloud"], ['local_cloud', "Here and in the cloud"]].map(([v, n]) => `<button data-v="${v}" class="${v === t ? 'on' : ''}">${n}</button>`).join('')}</div>
          <div data-ftp style="margin-top:18px;${ftpOn ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>FTP server</label><input type="text" name="host" value="${h(f.ftp.host)}" placeholder="backup.example.ru"></div>
            <div class="field"><label>Port</label><input type="number" name="port" value="${f.ftp.port || 21}"></div>
            <div class="field"><label>Username</label><input type="text" name="user" value="${h(f.ftp.user)}"></div>
            <div class="field"><label>Password</label><input type="password" name="pass" value="${h(f.ftp.pass)}"></div>
            <div class="field"><label>FTP directory</label><input type="text" name="dir" value="${h(f.ftp.dir || '/itrupanel')}"></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="tls" ${f.ftp.tls ? 'checked' : ''}><span>Encryption (FTPS)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px">FTP server: the local file is deleted after upload. Local and FTP: both copies are retained.</p></div>
          <div data-cloud style="margin-top:18px;${cloudOn ? '' : 'display:none'}">
            <div class="field"><label>Cloud service</label><select data-svc>${services.map(([v, , n]) => `<option value="${v}" ${v === f.svc ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
          <div data-s3 style="${svcKind === 's3' ? '' : 'display:none'}">
            <div class="field"><label>S3 service</label><select data-preset>${presets.map(([v, n]) => `<option value="${v}">${n}</option>`).join('')}</select><div class="hint" data-preset-hint></div></div>
            <div class="cols2">
            <div class="field"><label>Storage address</label><input type="text" name="endpoint" value="${h(f.s3.endpoint || '')}" placeholder="https://s3.example.com" spellcheck="false"></div>
            <div class="field"><label>Region</label><input type="text" name="region" value="${h(f.s3.region || 'us-east-1')}" spellcheck="false"></div>
            <div class="field"><label>Bucket</label><input type="text" name="bucket" value="${h(f.s3.bucket || '')}" spellcheck="false"></div>
            <div class="field"><label>Folder in the bucket</label><input type="text" name="prefix" value="${h(f.s3.prefix === undefined ? 'itrupanel' : f.s3.prefix)}" spellcheck="false"></div>
            <div class="field"><label>Access key</label><input type="text" name="access_key" value="${h(f.s3.access_key || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>Secret key</label><input type="password" name="secret_key" value="${h(f.s3.secret_key || '')}" autocomplete="new-password"></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="path_style" ${f.s3.path_style !== false ? 'checked' : ''}><span>Address like server/bucket (path-style)</span></label></div>
            <div class="field"><label>&nbsp;</label><label class="check"><input type="checkbox" name="insecure" ${f.s3.insecure ? 'checked' : ''}><span>Do not verify the certificate (self-signed)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px">Create the bucket in advance in your cloud account and give the key permission to read, write and delete objects. “In the cloud” — the local file is deleted after the upload. “Here and in the cloud” — both copies are kept.</p></div>
          <div data-webdav style="${svcKind === 'webdav' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>WebDAV address</label><input type="text" name="url" value="${h(f.webdav.url || davUrl[f.svc] || '')}" ${davUrl[f.svc] ? 'readonly' : ''} placeholder="https://dav.example.com" spellcheck="false"></div>
            <div class="field"><label>Username</label><input type="text" name="user" value="${h(f.webdav.user || '')}" autocomplete="off" spellcheck="false" placeholder="${f.svc === 'mailru' ? "full e-mail address" : f.svc === 'yandex' ? "Yandex login" : ''}"></div>
            <div class="field"><label>App password</label><input type="password" name="pass" value="${h(f.webdav.pass || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Folder in the storage</label><input type="text" name="dir" value="${h(f.webdav.dir === undefined ? 'itrupanel' : f.webdav.dir)}" spellcheck="false"></div>
            <div class="field" ${f.svc === 'dav' ? '' : 'style="display:none"'}><label>&nbsp;</label><label class="check"><input type="checkbox" name="insecure" ${f.webdav.insecure ? 'checked' : ''}><span>Do not verify the certificate (self-signed)</span></label></div></div>
            <p class="muted" style="margin:0 0 14px" data-webdav-hint></p></div>
          <div data-dropbox style="${svcKind === 'dropbox' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>App key</label><input type="text" name="app_key" value="${h(f.dropbox.app_key || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>App secret (optional)</label><input type="password" name="app_secret" value="${h(f.dropbox.app_secret || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Folder in Dropbox</label><input type="text" name="dir" value="${h(f.dropbox.dir === undefined ? 'itrupanel' : f.dropbox.dir)}" spellcheck="false"></div>
            <div class="field"><label>Access</label><div data-connected>${f.dropbox.refresh_token ? "<span class=\"tag acc\">Connected</span> " + h(f.dropbox.account || '') : "<span class=\"muted\">not connected</span>"}</div></div></div>
            <div style="margin-bottom:10px"><button class="btn" type="button" data-connect="dropbox">${f.dropbox.refresh_token ? "Connect again" : "Connect Dropbox"}</button></div><div data-connect-box></div>
            <p class="muted" style="margin:0 0 14px">Create an app at dropbox.com/developers/apps: “Scoped access” → “App folder”, and on the “Permissions” tab tick files.metadata.read, files.content.read and files.content.write. Copy the “App key” here, press “Connect Dropbox” and allow access. The panel sees only its own folder inside Apps/&lt;app name&gt;.</p></div>
          <div data-gdrive style="${svcKind === 'gdrive' ? '' : 'display:none'}"><div class="cols2">
            <div class="field"><label>Client ID</label><input type="text" name="client_id" value="${h(f.gdrive.client_id || '')}" autocomplete="off" spellcheck="false"></div>
            <div class="field"><label>Client secret</label><input type="password" name="client_secret" value="${h(f.gdrive.client_secret || '')}" autocomplete="new-password"></div>
            <div class="field"><label>Folder in Google Drive</label><input type="text" name="dir" value="${h(f.gdrive.dir === undefined ? 'itrupanel' : f.gdrive.dir)}" spellcheck="false"></div>
            <div class="field"><label>Access</label><div data-connected>${f.gdrive.refresh_token ? "<span class=\"tag acc\">Connected</span> " + h(f.gdrive.account || '') : "<span class=\"muted\">not connected</span>"}</div></div></div>
            <div style="margin-bottom:10px"><button class="btn" type="button" data-connect="gdrive">${f.gdrive.refresh_token ? "Connect again" : "Connect Google Drive"}</button></div><div data-connect-box></div>
            <p class="muted" style="margin:0 0 14px">In console.cloud.google.com create a project and enable the “Google Drive API”. In “OAuth consent screen” choose “External” and switch the app to “In production” (otherwise access expires after 7 days). Then “Credentials” → “Create credentials” → “OAuth client ID”, type “TVs and Limited Input devices”. Copy the Client ID and Client secret here and press “Connect Google Drive”. The panel sees only the files it created.</p></div>
          </div>
          <div style="display:flex;gap:10px;margin-top:4px"><button class="btn primary" data-save>Save</button><button class="btn" data-test ${t === 'local' ? 'hidden' : ''}>Test connection</button></div>`;
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
        const davHints = { yandex: "Create an app password: id.yandex.ru → “Security” → “App passwords” → “Files (WebDAV)”. The login is your Yandex login. The main password will not work.", mailru: "Create a password for an external app: Mail.ru Cloud → “Settings” → “Passwords for external apps” → “Access to files via WebDAV”. The login is the full e-mail address.", dav: "Enter the WebDAV address of your server (for example, Nextcloud: https://cloud.example.com/remote.php/dav/files/LOGIN). An app password is recommended." };
        const dh = $('[data-webdav-hint]', where); if (dh) dh.textContent = (davHints[f.svc] || '') + " “In the cloud” deletes the local file after upload; “Here and in the cloud” keeps both copies.";
        $$('[data-connect]', where).forEach((btn) => (btn.onclick = async () => {
          keep();
          const kind = btn.dataset.connect, box = $('[data-connect-box]', btn.closest('[data-dropbox],[data-gdrive]'));
          const cfg = f[kind];
          const clientId = kind === 'dropbox' ? cfg.app_key : cfg.client_id, clientSecret = kind === 'dropbox' ? cfg.app_secret : cfg.client_secret;
          btn.disabled = true;
          const finishOk = async (r) => {
            const nf = await api('backups.ftp.get');
            f.dropbox = nf.dropbox; f.gdrive = nf.gdrive;
            toast("Connected" + (r.account ? ': ' + r.account : ''), 'ok');
            draw(f.mode);
          };
          try {
            const r = await api('backups.cloud.oauth.start', { kind, client_id: clientId, client_secret: clientSecret });
            if (kind === 'dropbox') {
              box.innerHTML = `<div class="callout"><p>1. <a href="${h(r.url)}" target="_blank" rel="noopener">Open the Dropbox page</a> and allow access.</p><p>2. Paste the code shown there:</p><div style="display:flex;gap:10px"><input type="text" data-code placeholder="Code from Dropbox" autocomplete="off" spellcheck="false"><button class="btn primary" type="button" data-confirm>Confirm</button></div></div>`;
              $('[data-confirm]', box).onclick = async () => {
                try { await finishOk(await api('backups.cloud.oauth.finish', { kind, state: r.state, code: $('[data-code]', box).value })); } catch (e) { fail(e); }
              };
            } else {
              box.innerHTML = `<div class="callout"><p>1. Open <a href="${h(r.url)}" target="_blank" rel="noopener">${h(r.url)}</a> and sign in to the right Google account.</p><p>2. Enter the code: <b style="font-size:20px;letter-spacing:2px">${h(r.user_code)}</b></p><p class="muted" data-wait><span class="spin" style="width:14px;height:14px;border-width:2px"></span> Waiting for confirmation…</p></div>`;
              const deadline = Date.now() + 15 * 60 * 1000;
              const poll = async () => {
                if (!m.el.isConnected || !box.isConnected) return;
                try {
                  const x = await api('backups.cloud.oauth.finish', { kind, state: r.state });
                  if (x.pending) { if (Date.now() < deadline) setTimeout(poll, Math.max(3, r.interval || 5) * 1000); else $('[data-wait]', box).textContent = "The waiting time has run out. Press “Connect Google Drive” again."; return; }
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
            toast(test ? result : "Saved",'ok');
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
const backupTabs = [['files', "Website files"], ['db', "Databases"], ['panel', "Panel settings"]];
// Копия лежит в облаке (S3, WebDAV, Dropbox, Google Drive), если идентификатор хранилища начинается с s3-, dav-, dbx- или gdr-; иначе это FTP.
const backupInCloud = (r) => /^(s3|dav|dbx|gdr)-/.test(r.remote_source || '');
const backupTabOf = (r) => r.kind === 'database' ? 'db' : r.kind === 'panel' ? 'panel' : 'files';
page('backups', {
  title: "Backups",
  async render(view, params) {
    let tab = backupTabs.some(([v]) => v === params.tab) ? params.tab : (ls.get('backups-tab', 'files') || 'files');
    if (!backupTabs.some(([v]) => v === tab)) tab = 'files';
    head(view, "Backups", { actions: [{ t: "Create backup", i: 'plus', primary: true, fn: () => backupForm(tab) }, { t: "Settings", fn: backupSettingsForm }, { t: "Find backups in the storage", i: 'search', fn: backupDiscoverFTP }] });
    const bar = document.createElement('div'); bar.className = 'toolbar'; bar.style.marginBottom = '12px';
    view.append(bar);
    const crumb = document.createElement('div'); crumb.className = 'backup-crumb'; crumb.hidden = true;
    view.append(crumb);
    const hint = document.createElement('p'); hint.className = 'hint'; hint.textContent = "After connecting the old storage (FTP or cloud), click “Find backups in the storage”. Manifests are checked; archive contents are checked before restoring. Imported backups are not deleted by the schedule.";
    view.append(hint);
    const p = panel(view, "Backups");
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
    const spin = "<span class=\"tag acc\"><span class=\"spin\" style=\"width:12px;height:12px;border-width:2px\"></span> creating</span>";
    const auto = (r) => r.auto ? " <span class=\"tag\">auto</span>" : '';
    const common = [
      { t: "Size", r: (r) => r.running ? '' : (r.size ? fmtBytes(r.size) : '—'), cls: 'nowrap' },
      { t: "Stored", r: (r) => r.running ? '' : [r.local !== false ? "<span class=\"tag\">server</span>" : '', r.remote ? '<span class="tag acc">' + (backupInCloud(r) ? "Cloud" : 'FTP') + '</span>' : ''].join(' '), cls: 'hide-sm' },
      { t: "Date", r: (r) => r.running ? '' : fmtDate(r.created), cls: 'nowrap' },
      { t: "Restoration", r: (r) => r.running ? '' : (r.restore_issue ? '<span class="muted">'+h(r.restore_issue)+'</span>' : (r.imported ? (backupInCloud(r) ? "Imported from the cloud" : "Imported from FTP") : '—')) },
    ];
    const columns = {
      files: [
        { t: "Site", r: (r) => `<b>${h(r.site)}</b>` },
        { t: "Contents", r: (r) => r.running ? spin : ({ full: "files and databases", files: "files", db: "databases", settings: "settings" }[r.kind] || '') + (r.site_settings ? " <span class=\"tag\">Site settings</span>" : '') + (r.panel_settings ? " <span class=\"tag\">Panel settings</span>" : '') + auto(r) },
        { t: "Mode", r: (r) => r.running ? '' : (r.mode==='incremental' ? "<span class=\"tag acc\">Incremental</span><br><small class=\"muted\">Parent: " + h(r.parent_id) + '</small>' : "<span class=\"tag\">Full</span>") },
        ...common,
      ],
      db: [
        { t: "Database", r: (r) => `<span class="backup-item">${icon('database')}<b>${h(r.db)}</b></span>` + (r.running ? ' ' + spin : auto(r)) },
        { t: "Site", r: (r) => h((data.db_sites || {})[r.db] || '—'), cls: 'hide-sm' },
        ...common,
      ],
      panel: [
        { t: "Backup", r: (r) => r.running ? spin : "Panel settings" + auto(r) },
        ...common,
      ],
    };
    const groupCols = {
      files: [{ t: "Site", r: (g) => `<span class="backup-item"><b>${h(g.key)}</b></span>` }],
      db: [{ t: "Database", r: (g) => `<span class="backup-item">${icon('database')}<b>${h(g.key)}</b></span>` }, { t: "Site", r: (g) => h((data.db_sites || {})[g.key] || '—'), cls: 'hide-sm' }],
      panel: [{ t: "What is backed up", r: () => "<b>Panel settings</b>" }],
    };
    const summary = [
      { t: "Backups", r: (g) => String(g.rows.length) + (g.running ? ' ' + spin : ''), cls: 'nowrap' },
      { t: "Latest", r: (g) => g.rows.length ? fmtDate(g.rows[0].created) : '', cls: 'nowrap' },
      { t: "Total size", r: (g) => g.size ? fmtBytes(g.size) : '—', cls: 'nowrap hide-sm' },
      { t: "Stored", r: (g) => [g.local ? "<span class=\"tag\">server</span>" : '', g.ftp ? '<span class="tag acc">FTP</span>' : '', g.cloud ? "<span class=\"tag acc\">Cloud</span>" : ''].join(' '), cls: 'hide-sm' },
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
          empty: { icon: 'backup', title: tab === 'db' ? "No database backups yet" : tab === 'panel' ? "No panel settings backups yet" : "No backups yet" },
          columns: [...groupCols[tab], ...summary],
          onRow: (g) => { sel = g.key; draw(); },
          actions: (g) => [{ i: 'folder', t: "Show backups", fn: () => { sel = g.key; draw(); } }],
        });
        return;
      }
      const g = all.find((x) => x.key === sel);
      const what = tab === 'panel' ? "<b>Panel settings</b>" : `<span class="muted">${tab === 'db' ? "Database" : "Site"}</span> <b>${h(sel)}</b>`;
      crumb.hidden = false;
      crumb.innerHTML = `<button type="button" class="btn small" data-back>← <span>${tab === 'db' ? "All databases" : tab === 'panel' ? "Back" : "All sites"}</span></button>${what}<span class="muted">backups:</span> <span>${g.rows.length}</span>`;
      $('[data-back]', crumb).onclick = () => { sel = null; draw(); };
      const running = (data.running || []).map(runningOf).filter((r) => backupTabOf(r) === tab && keyOf(r) === sel);
      table(p, {
        rows: [...running, ...g.rows],
        empty: { icon: 'backup', title: "No backups" },
        columns: columns[tab].slice(tab === 'panel' ? 0 : 1),
        actions: (r) => r.running ? [] : [
          backupIsBrowsable(r) && { i: 'folder', t: "Contents", fn: () => backupContents(r) },
          { i: 'download', t: "Download", fn: () => backupDownload(r) },
          !r.restore_issue && { i: 'refresh', t: "Restore", fn: () => backupRestoreForm(r) },
          { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete backup?", h(r.kind === 'database' ? "database " + r.db : r.kind === 'panel' ? "panel settings" : r.site) + ', ' + fmtDate(r.created)))) return; try { await api('backups.delete', { id: r.id }); load(); } catch (e) { fail(e); } } },
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
  return "<a href=\"#\" data-p=\"/\" aria-label=\"Root\">/</a>" + parts.map((name, i) => `<a href="#" data-p="/${h(parts.slice(0, i + 1).join('/'))}">${h(name)}</a>`).join('<span class="s">/</span>');
}

function folderDestination({ title, path, action, select }) {
  let selected = null, request = 0, current = path;
  const f = openForm({ title, ok: action + " here", fields: [{ type: 'html', html: `<div class="folder-picker"><label>Destination folder</label><div class="folder-address"><input type="text" data-folder-path aria-label="Destination folder path" spellcheck="false"><button class="btn" type="button" data-folder-go>Go</button></div><div class="folder-navigation"><button class="btn small" type="button" data-folder-up>↑ Back to top</button><nav class="crumbs" aria-label="Destination folder"></nav></div><div class="folder-list" aria-live="polite"></div><p class="hint">Open the target folder and click “${h(action)} here”.</p></div>` }], submit: async () => {
    if (selected == null) throw new Error("Select an accessible folder first");
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
      list.innerHTML = folders.length ? folders.map((row, i) => `<button type="button" class="folder-choice" data-folder="${i}">${icon('folder')}<span>${h(row.name)}</span><span class="muted">›</span></button>`).join('') : "<p class=\"muted\">No subfolders. You can select the current folder.</p>";
      $$('[data-folder]', list).forEach((b) => b.onclick = () => load((current === '/' ? '' : current) + '/' + folders[+b.dataset.folder].name));
      ok.disabled = false;
    } catch (e) {
      if (id !== request || !f.el.isConnected) return;
      list.innerHTML = `<p class="folder-error">${h(e.message)}</p><button type="button" class="btn small" data-retry>Retry</button>`;
      $('[data-retry]', list).onclick = () => load(target);
    }
  };
  $('[data-folder-go]', f.el).onclick = () => load(input.value);
  input.oninput = () => { ++request; selected = null; ok.disabled = true; list.innerHTML = "<p class=\"muted\">Click Go to open the entered path.</p>"; };
  input.onkeydown = (e) => { if (e.key === 'Enter') { e.preventDefault(); load(input.value); } };
  $('[data-folder-up]', f.el).onclick = () => load(current.split('/').slice(0, -1).join('/') || '/');
  load(path);
  return f;
}

// Двойной щелчок открывает редактор у любого файла, кроме заведомо двоичных (картинки, архивы, видео, документы Office, шрифты, программы);
// если файл окажется не текстовым или больше 3 МБ, сервер скажет об этом, и он скачается.
const BINARY_EXT = /\.(png|jpe?g|gif|webp|avif|ico|bmp|tiff?|heic|psd|zip|gz|tgz|bz2|xz|7z|rar|tar|zst|mp3|mp4|m4a|m4v|mov|avi|mkv|webm|wav|ogg|flac|pdf|docx?|xlsx?|pptx?|odt|ods|odp|woff2?|ttf|otf|eot|exe|dll|so|bin|iso|dmg|class|jar|phar|sqlite3?|db|mo)$/i;
// Корзина менеджера файлов: восстановление, удаление навсегда, очистка и переключатель.
async function recycleBin(onChange) {
  const m = modal(`<div class="backup-settings-head"><h3>Recycle bin</h3><button class="btn" data-close>Close</button></div><div data-bin><div class="loading"><span class="spin"></span></div></div>`, { wide: true });
  const body = $('[data-bin]', m.el);
  const picked = new Set();
  const load = async () => {
    let d;
    try { d = await api('files.recycle.list'); } catch (e) { body.innerHTML = `<p class="err">${h(e.message)}</p>`; return; }
    if (!m.el.isConnected) return;
    picked.clear();
    body.innerHTML = `<div class="toolbar" style="margin-bottom:12px"><label class="check"><input type="checkbox" data-enabled ${d.enabled ? 'checked' : ''}><span>Move deleted files to the recycle bin</span></label><span class="grow"></span><span class="muted">${d.items.length} · ${fmtBytes(d.total)}</span></div>
      <div class="toolbar" style="margin-bottom:12px"><button class="btn primary" data-restore disabled>${icon('refresh')}Restore</button><button class="btn danger" data-delete disabled>${icon('trash')}Delete permanently</button><span class="grow"></span><button class="btn danger" data-empty ${d.items.length ? '' : 'disabled'}>Empty recycle bin</button></div>
      ${d.items.length ? `<div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all aria-label="Select all"></th><th>Name</th><th>Deleted from</th><th>Size</th><th>Deleted</th></tr></thead><tbody>${d.items.map((it) => `<tr><td class="chk"><input type="checkbox" data-id="${h(it.id)}" aria-label="${h(it.name)}"></td><td><span class="fname ${it.dir ? 'dir' : ''}">${icon(it.dir ? 'folder' : 'file')}${h(it.name)}</span></td><td class="mono">${h(it.path)}</td><td class="nowrap">${fmtBytes(it.size)}</td><td class="nowrap">${fmtDate(it.deleted)}</td></tr>`).join('')}</tbody></table></div>` : `<div class="empty"><div class="circle">${icon('trash')}</div><b>The recycle bin is empty</b>${d.enabled ? "Deleted files will appear here" : "The recycle bin is off: files are deleted immediately"}</div>`}`;
    const boxes = $$('[data-id]', body);
    const sync = () => { $('[data-restore]', body).disabled = $('[data-delete]', body).disabled = !picked.size; };
    boxes.forEach((b) => (b.onchange = () => { b.checked ? picked.add(b.dataset.id) : picked.delete(b.dataset.id); sync(); }));
    const all = $('[data-all]', body);
    if (all) all.onchange = () => { boxes.forEach((b) => { b.checked = all.checked; b.checked ? picked.add(b.dataset.id) : picked.delete(b.dataset.id); }); sync(); };
    $('[data-enabled]', body).onchange = async (e) => { try { await api('files.recycle.settings', { enabled: e.target.checked }); toast(e.target.checked ? "Recycle bin enabled" : "Recycle bin disabled: files will be deleted immediately", 'ok'); } catch (err) { fail(err); e.target.checked = !e.target.checked; } };
    $('[data-restore]', body).onclick = async () => { try { const r = await api('files.recycle.restore', { ids: [...picked] }); toast(r.info, 'ok'); if (onChange) onChange(); load(); } catch (e) { fail(e); } };
    $('[data-delete]', body).onclick = async () => { if (!(await confirmBox("Delete permanently?", "The selected items (" + picked.size + ") will be deleted and cannot be restored."))) return; try { await api('files.recycle.delete', { ids: [...picked] }); load(); } catch (e) { fail(e); } };
    $('[data-empty]', body).onclick = async () => { if (!(await confirmBox("Empty the recycle bin?", "All items in the recycle bin (" + d.items.length + ', ' + fmtBytes(d.total) + ") will be deleted permanently."))) return; try { await api('files.recycle.empty'); toast("Recycle bin emptied", 'ok'); load(); } catch (e) { fail(e); } };
  };
  load();
}

page('files', {
  title: "File manager",
  async render(view, params) {
    let path = params.path || ls.get('files.path', '/var/www');
    let rows = [];
    const sel = new Set();
    head(view, "File manager");
    const box = document.createElement('div');
    box.className = 'panel dropzone file-manager';
    view.append(box);
    box.innerHTML = `<div class="crumbs"></div><div class="toolbar file-toolbar">
      <button class="btn" data-act="up" title="Back to top">${icon('chevron')}</button>
      <button class="btn" data-act="mkdir">${icon('folder')}Folder</button>
      <button class="btn" data-act="mkfile">${icon('file')}File</button>
      <button class="btn" data-act="upload">${icon('upload')}Upload</button>
      <span class="grow"></span>
      <button class="btn" data-act="download" data-need="1" title="Download" aria-label="Download">${icon('download')}</button>
      <button class="btn" data-act="rename" data-need="1">${icon('edit')}Rename</button>
      <button class="btn" data-act="copy" data-need="n">${icon('copy')}Copy</button>
      <button class="btn" data-act="move" data-need="n">Move</button>
      <button class="btn" data-act="chmod" data-need="n">Permissions</button>
      <button class="btn" data-act="zip" data-need="n">${icon('archive')}Archive</button>
      <button class="btn" data-act="unzip" data-need="1">Extract</button>
      <button class="btn danger" data-act="del" data-need="n" title="Delete" aria-label="Delete">${icon('trash')}</button>
      <button class="btn" data-act="recycle" title="Recycle bin">${icon('trash')}Recycle bin</button>
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
      if (!rows.length) { list.innerHTML = `<div class="empty"><div class="circle">${icon('folder')}</div><b>Folder is empty</b>Drop files here to upload</div>`; upd(); return; }
      list.innerHTML = `<div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all></th><th>Name</th><th>Size</th><th class="hide-sm">Permissions</th><th class="hide-sm">Owner</th><th class="hide-sm">Modified</th><th class="actions fm-more"></th></tr></thead><tbody>${rows.map((r, i) => `<tr data-i="${i}" class="${sel.has(r.name) ? 'sel' : ''}"><td class="chk"><input type="checkbox" ${sel.has(r.name) ? 'checked' : ''}></td><td><span class="fname ${r.dir ? 'dir' : ''}">${icon(r.dir ? 'folder' : 'file')}${h(r.name)}${r.link ? " <span class=\"tag\">link</span>" : ''}</span></td><td class="nowrap">${r.dir ? '' : fmtBytes(r.size)}</td><td class="mono hide-sm">${h(r.mode)}</td><td class="hide-sm">${h(r.owner)}</td><td class="nowrap hide-sm">${fmtDate(r.mtime)}</td><td class="actions fm-more"><button class="btn small icon-only" data-more title="Actions" aria-label="Actions for ${h(r.name)}">${icon('more')}</button></td></tr>`).join('')}</tbody></table></div>`;
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
      if (BINARY_EXT.test(p) && r.size > 0) { location.href = '/api/files/download?path=' + encodeURIComponent(p); return; }
      try {
        const d = await api('files.read', { path: p });
        let ed = null;
        try { await loadScript('/vendor/codemirror.js'); if (window.ITRUEditor) ed = true; } catch { ed = null; }
        const useCM = !!ed;
        ed = null;
        const barHtml = useCM
          ? `<div class="ed-bar"><span class="muted mono ed-path">${h(p)}</span><span class="grow"></span><span class="tag warn" data-dirty hidden>unsaved</span><select data-lang aria-label="Highlighting language"></select><label class="check ed-wrap"><input type="checkbox" data-wrap><span>Wrap lines</span></label><button class="btn small" type="button" data-find>${icon('search')}Search</button></div><div class="cm-host"></div><div class="ed-status muted mono" data-status></div>`
          : `<div class="muted mono" style="margin-bottom:8px">${h(p)}</div><textarea class="editor" spellcheck="false">${h(d.text)}</textarea>`;
        openForm({ title: p.split('/').pop(), wide: true, ok: "Save", fields: [{ type: 'html', html: barHtml }], submit: async () => { await api('files.write', { path: p, text: ed ? ed.getText() : $('.editor').value }); if (ed) ed.markSaved(); toast("Saved", 'ok'); return false; } });
        if (useCM) {
          const root = $('.drawer');
          $('.drawer-body', root).classList.add('ed-body');
          const dark = document.documentElement.dataset.theme === 'dark' || (!document.documentElement.dataset.theme && matchMedia('(prefers-color-scheme: dark)').matches);
          const phrases = { 'Find': "Find", 'Replace': "Replace", 'next': "next", 'previous': "previous", 'all': "all", 'match case': "match case", 'regexp': "regexp", 'by word': "by word", 'replace': "replace", 'replace all': "replace all", 'close': "close", 'Go to line': "Go to line", 'go': "go", 'current match': "current match", 'on line': "on line", 'replaced $ matches': "replaced $ matches", 'replaced match on line $': "replaced match on line $", 'Folded lines': "Folded lines", 'Unfolded lines': "Unfolded lines", 'folded code': "folded code", 'unfold': "unfold", 'Fold line': "Fold line", 'Unfold line': "Unfold line" };
          const status = $('[data-status]', root), dirtyTag = $('[data-dirty]', root), langSel = $('[data-lang]', root);
          ed = window.ITRUEditor.create($('.cm-host', root), {
            text: d.text, filename: p, dark, phrases,
            onChange: (dirty) => { dirtyTag.hidden = !dirty; },
            onSave: () => $('.drawer [data-ok]').click(),
            onStatus: (st) => { status.textContent = `Line ${st.line}, column ${st.col} · lines ${st.lines}`; },
          });
          langSel.innerHTML = ['', ...window.ITRUEditor.languages].map((l) => `<option value="${h(l)}">${l ? h(l) : "Plain text"}</option>`).join('');
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
        !many && { t: r.dir ? "Open" : (BINARY_EXT.test(r.name) ? "Open" : "Edit"), i: r.dir ? 'folder' : 'edit', fn: () => (r.dir ? load(full(r.name)) : openFile(full(r.name), r)) },
        !many && !r.dir && { t: "Download", i: 'download', fn: acts.download },
        '-',
        { t: "Copy", i: 'copy', fn: acts.copy },
        { t: "Move (cut)", i: 'external', fn: acts.move },
        !many && { t: "Rename", i: 'edit', fn: acts.rename },
        { t: "Permissions and owner", i: 'key', fn: acts.chmod },
        '-',
        { t: "Compress to archive", i: 'archive', fn: acts.zip },
        !many && archive && { t: "Extract", i: 'archive', fn: acts.unzip },
        !many && { t: "Copy path", i: 'copy', fn: () => copyText(full(r.name)) },
        '-',
        { t: many ? "Delete selected (" + sel.size + ')' : "Delete", i: 'trash', danger: true, fn: acts.del },
      ].filter(Boolean);
      contextMenu(items, x, y);
    };
    const selPaths = () => [...sel].map(full);
    const one = () => [...sel][0];
    const acts = {
      up: () => load(path.split('/').slice(0, -1).join('/') || '/'),
      mkdir: () => openForm({ title: "New folder", ok: "Create", fields: [{ name: 'name', label: "Name", required: true }], submit: async (v) => { await api('files.mkdir', { path, name: v.name }); load(path); } }),
      mkfile: () => openForm({ title: "New file", ok: "Create", fields: [{ name: 'name', label: "Name", required: true, placeholder: 'index.php' }], submit: async (v) => { await api('files.create', { path, name: v.name }); load(path); } }),
      upload: () => fileIn.click(),
      download: () => (location.href = '/api/files/download?path=' + encodeURIComponent(full(one()))),
      rename: () => openForm({ title: "Rename", fields: [{ name: 'to', label: "New name", value: one(), required: true }], submit: async (v) => { await api('files.rename', { path: full(one()), to: v.to }); load(path); } }),
      copy: () => { const paths = selPaths(); folderDestination({ title: "Copy " + plural(paths.length, "item", "items", "items"), path, action: "Copy", select: async (to) => { await api('files.copy', { paths, to }); toast("Copied", 'ok'); load(path); } }); },
      move: () => { const paths = selPaths(); folderDestination({ title: "Move " + plural(paths.length, "item", "items", "items"), path, action: "Move", select: async (to) => { for (const p of paths) await api('files.rename', { path: p, to: to.replace(/\/$/, '') + '/' + p.split('/').pop() }); toast("Moved", 'ok'); load(path); } }); },
      chmod: () => { const r = rows.find((x) => x.name === one()); openForm({ title: "Access permissions", fields: [{ name: 'mode', label: "Permissions (octal)", value: r ? r.mode : '0644', placeholder: '0644' }, { name: 'owner', label: "Owner (leave blank to keep)", value: '' }, { name: 'recursive', label: "For nested files and folders", type: 'checkbox' }], submit: async (v) => { await api('files.chmod', { paths: selPaths(), mode: v.mode, owner: v.owner, recursive: v.recursive }); load(path); } }); },
      zip: () => openForm({ title: "Create archive", ok: "Create", fields: [{ name: 'name', label: "Archive name", value: (sel.size === 1 ? one() : 'archive') + '.zip', hint: ".zip or .tar.gz" }], submit: async (v) => { await api('files.compress', { paths: selPaths(), name: v.name }); toast("Archive created", 'ok'); load(path); } }),
      unzip: async () => { toast("Extracting…"); try { await api('files.extract', { path: full(one()) }); toast("Extracted", 'ok'); load(path); } catch (e) { fail(e); } },
      del: async () => {
        const inBin = path === '/var/www/.itrupanel-recycle' || path.startsWith('/var/www/.itrupanel-recycle/');
        const bin = inBin ? null : await api('files.recycle.list').catch(() => null);
        const toBin = bin && bin.enabled;
        const v = await confirmBox("Delete " + plural(sel.size, "item", "items", "items") + '?', h([...sel].slice(0, 8).join(', ')) + (sel.size > 8 ? '…' : '') + (toBin ? "<br>The files will be moved to the recycle bin and can be restored." : "<br>The files will be deleted permanently."), { extra: toBin ? "<label class=\"check\"><input type=\"checkbox\" name=\"permanent\"><span>Delete permanently, bypassing the recycle bin</span></label>" : '' });
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
  title: "IP addresses",
  async render(view) {
    head(view, "IP addresses");
    const p = panel(view);
    const rows = await api('system.ips');
    table(p, {
      rows: rows || [],
      empty: { icon: 'ip', title: "No addresses found" },
      columns: [
        { t: "IP address", r: (r) => `<b class="mono" style="font-size:15px">${h(r.ip)}</b><span class="muted">/${r.prefix}</span>` },
        { t: "Version", r: (r) => `<span class="tag">${r.version}</span>` },
        { t: "Type", r: (r) => `<span class="tag ${r.scope === "public" ? 'acc' : ''}">${h(r.scope)}</span>` },
        { t: "Interface", r: (r) => h(r.iface) + (r.up ? '' : " <span class=\"tag err\">disabled</span>") },
      ],
      actions: (r) => [{ i: 'copy', t: "Copy", fn: () => { navigator.clipboard?.writeText(r.ip); toast("Copied"); } }],
    });
  },
});

// ================= FTP =================
async function ftpForm() {
  const users = await api('users.list');
  if (!users.length) { toast("Create a site first — the FTP user is linked to its owner", 'err'); return; }
  openForm({
    title: "Create FTP user", ok: "Create",
    fields: [
      { name: 'owner', label: "Owner (system user)", type: 'select', options: users.map((u) => [u.name, u.name]), value: users[0].name, hint: "Files uploaded via FTP will belong to this user" },
      { name: 'name', label: "FTP username", required: true, placeholder: 'ftp_site' },
      { name: 'password', label: "Password", type: 'password', value: genPass(14), gen: true },
      { name: 'home', label: "Home directory", placeholder: "/var/www/<owner> — default", hint: "The user can access only this directory" },
    ],
    submit: async (v) => {
      const r = await api('ftp.create', v);
      showSecrets("FTP user created", [["Server", location.hostname], ["Port", '21'], ["Username", v.name], ["Password", r.password]]);
      route();
    },
  });
}
page('ftp', {
  title: "FTP users",
  async render(view, params) {
    head(view, "FTP users", { actions: [{ t: "Create FTP user", i: 'plus', primary: true, fn: ftpForm }] });
    const p = panel(view);
    const d = await api('ftp.list');
    if (!d.active) p.insertAdjacentHTML('beforebegin', "<p><span class=\"tag warn\">FTP server is not running — check Server information</span></p>");
    table(p, {
      rows: d.rows || [],
      empty: { icon: 'ftp', title: "No FTP users" },
      columns: [
        { t: "Username", r: (r) => `<b>${h(r.name)}</b>` },
        { t: "Owner", r: (r) => h(r.owner) },
        { t: "Folder", r: (r) => `<span class="mono">${h(r.home)}</span>`, cls: 'hide-sm' },
        { t: "Created", r: (r) => fmtDate(r.created, false), cls: 'hide-sm' },
      ],
      actions: (r) => [
        { i: 'key', t: "Change password", fn: () => openForm({ title: "Password " + r.name, ok: "Change", fields: [{ name: 'password', label: "New password", type: 'password', value: genPass(14), gen: true }], submit: async (v) => { const x = await api('ftp.password', { name: r.name, password: v.password }); showSecrets("Password changed", [["Username", r.name], ["Password", x.password]]); } }) },
        { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Delete " + r.name + '?', "Files remain in place."))) return; try { await api('ftp.delete', { name: r.name }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (params.new && view.isConnected) ftpForm();
  },
});

// ================= Журналы =================
page('oplog', {
  title: "Activity log",
  async render(view) {
    head(view, "Activity log", { sub: "All changes made through the panel" });
    const p = panel(view);
    p.innerHTML = `<div class="toolbar"><input class="search-input" placeholder="Search…"><label class="check"><input type="checkbox" data-err><span>Errors only</span></label></div><div class="list"></div>`;
    const list = $('.list', p);
    let t;
    const load = async () => {
      const rows = await api('logs.ops', { q: $('.search-input', p).value, errors: $('[data-err]', p).checked });
      table(list, {
        rows, empty: { icon: 'oplog', title: "No records" },
        columns: [
          { t: "Time", r: (r) => fmtDate(r.time) + `:${String(new Date(r.time).getSeconds()).padStart(2, '0')}`, cls: 'nowrap' },
          { t: "Operation", r: (r) => h(r.action) },
          { t: "Object", r: (r) => `<span class="mono">${h(r.object)}</span>` },
          { t: "Result", r: (r) => r.status === 'ok' ? "<span class=\"tag ok\">successful</span>" : `<span class="tag err" title="${h(r.error)}">error</span>` },
          { t: "Who", r: (r) => h(r.user) + ` <small class="muted">${h(r.ip)}</small>`, cls: 'hide-sm nowrap' },
          { t: "Time, ms", r: (r) => r.ms, cls: 'hide-sm' },
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
  title: "Login log",
  async render(view) {
    head(view, "Login log", { sub: "Panel logins and failed attempts" });
    const p = panel(view);
    const rows = await api('logs.visits');
    table(p, {
      rows, empty: { icon: 'visits', title: "No records" },
      columns: [
        { t: "Time", r: (r) => fmtDate(r.time), cls: 'nowrap' },
        { t: "Event", r: (r) => ({ login: "<span class=\"tag ok\">login</span>", logout: "<span class=\"tag\">logout</span>", fail: "<span class=\"tag err\">incorrect password</span>" }[r.kind]) },
        { t: "User", r: (r) => h(r.user) },
        { t: 'IP', r: (r) => `<span class="mono">${h(r.ip)}</span>` },
        { t: "Browser", r: (r) => `<small class="muted">${h(r.ua)}</small>`, cls: 'hide-sm' },
      ],
    });
  },
});

page('wwwlogs', {
  title: "Site logs",
  async render(view, params) {
    head(view, "Web request logs");
    const p = panel(view);
    const files = await api('logs.www.files');
    if (!files.length) { p.innerHTML = `<div class="empty"><div class="circle">${icon('wwwlog')}</div><b>No sites yet</b></div>`; return; }
    const first = files.find((f) => f.site === params.site && f.kind === 'access') || files[0];
    const kinds = { access: "access", error: "nginx errors", php: "PHP errors" };
    p.innerHTML = `<div class="toolbar"><select class="search-input" data-f>${files.map((f) => `<option value="${h(f.path)}" ${f === first ? 'selected' : ''}>${h(f.site)} — ${kinds[f.kind]} (${fmtBytes(f.size)})</option>`).join('')}</select>
      <input class="search-input" data-q placeholder="Filter: IP, address, code…"><select class="search-input" data-n style="min-width:110px"><option>100</option><option selected>300</option><option>1000</option><option>5000</option></select>
      <button class="btn" data-r>${icon('refresh')}Update</button><label class="check"><input type="checkbox" data-live><span>Auto refresh</span></label></div><pre class="out code" style="max-height:calc(100vh - 330px)"></pre>`;
    const out = $('pre', p);
    let t;
    const load = async () => {
      try {
        const lines = await api('logs.www.read', { path: $('[data-f]', p).value, lines: +$('[data-n]', p).value, q: $('[data-q]', p).value });
        out.textContent = lines?.length ? lines.join('\n') : "(no records)";
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
  if (!pts.length) { el.innerHTML = "<div class=\"muted\" style=\"padding:30px 0\">Data will appear in a few minutes</div>"; return; }
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
  title: "Resource usage",
  async render(view) {
    head(view, "Resource usage");
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
        <div class="stat"><div class="k">CPU</div><div class="v">${last.cpu ?? '—'}<small> %</small></div><div class="bar"><i class="${barc(last.cpu)}" style="width:${last.cpu || 0}%"></i></div></div>
        <div class="stat"><div class="k">Memory</div><div class="v">${fmtBytes(n.mem_used)} <small>of ${fmtBytes(n.mem_total)}</small></div><div class="bar"><i class="${barc(pct(n.mem_used, n.mem_total))}" style="width:${pct(n.mem_used, n.mem_total)}%"></i></div></div>
        <div class="stat"><div class="k">Disk /</div><div class="v">${fmtBytes(n.disk_used)} <small>of ${fmtBytes(n.disk_total)}</small></div><div class="bar"><i class="${barc(pct(n.disk_used, n.disk_total))}" style="width:${pct(n.disk_used, n.disk_total)}%"></i></div></div>
        <div class="stat"><div class="k">Load (1 / 5 / 15 min)</div><div class="v">${(n.load || []).map((x) => x.toFixed(2)).join(' / ')}</div></div></div>`;
      p.innerHTML = `<div class="toolbar"><div class="seg">${[[1, "1 hour"], [6, "6 hours"], [24, "Day"], [168, "Week"]].map(([v, t]) => `<button data-h="${v}" class="${v === hours ? 'on' : ''}">${t}</button>`).join('')}</div></div>
        <div class="chart-box"><h4>CPU <span>%</span></h4><div data-c="cpu"></div><h4>Memory <span>%</span></h4><div data-c="mem"></div><h4>Load average</h4><div data-c="load"></div><h4>Network <span><i style="display:inline-block;width:10px;height:10px;background:#4166f5;border-radius:3px"></i> incoming · <i style="display:inline-block;width:10px;height:10px;background:#23a26a;border-radius:3px"></i> outbound</span></h4><div data-c="net"></div></div>`;
      $$('[data-h]', p).forEach((b) => (b.onclick = () => { hours = +b.dataset.h; ls.set('res.hours', hours); load(); }));
      chartSVG($('[data-c=cpu]', p), d.points, [{ k: 'cpu', c: '#4166f5' }], { max: 100, fmt: (v) => Math.round(v) + '%' });
      chartSVG($('[data-c=mem]', p), d.points, [{ k: 'mem', c: '#6a5cf6' }], { max: 100, fmt: (v) => Math.round(v) + '%' });
      chartSVG($('[data-c=load]', p), d.points, [{ k: 'load', c: '#d98e04' }], { fmt: (v) => v.toFixed(1) });
      chartSVG($('[data-c=net]', p), d.points, [{ k: 'rx', c: '#4166f5' }, { k: 'tx', c: '#23a26a' }], { fmt: (v) => fmtBytes(v) + "/s" });
    };
    await load();
    every(60000, load, view);
  },
});

page('traffic', {
  title: "Traffic usage",
  async render(view) {
    head(view, "Traffic usage", { sub: "Outbound site traffic from nginx logs (updated every 10 minutes)" });
    const p = panel(view);
    let days = 30;
    const COLORS = ['#4166f5', '#23a26a', '#d98e04', '#6a5cf6', '#e0484d', '#0ea5b7', '#a855f7', '#64748b'];
    const load = async () => {
      const d = await api('stats.traffic', { days });
      const total = d.days.reduce((a, r) => a + r.total, 0);
      const sites = (d.sites || []).map((s) => s.site);
      const col = (s) => COLORS[sites.indexOf(s) % COLORS.length];
      p.innerHTML = `<div class="toolbar"><div class="seg">${[[7, "7 days"], [30, "30 days"], [90, "90 days"], [365, "Year"]].map(([v, t]) => `<button data-d="${v}" class="${v === days ? 'on' : ''}">${t}</button>`).join('')}</div><span class="grow"></span><b>Total: ${fmtBytes(total)}</b></div><div data-chart></div><div class="legend" style="margin:8px 0 20px">${sites.map((s) => `<span><i style="background:${col(s)}"></i>${h(s)}</span>`).join('')}</div><div data-t></div>`;
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
      table($('[data-t]', p), { rows: d.sites || [], empty: { icon: 'traffic', title: "No data yet", text: "Traffic is calculated from site access logs" }, columns: [{ t: "Site", r: (r) => `<i style="display:inline-block;width:10px;height:10px;border-radius:3px;background:${col(r.site)};margin-right:8px"></i>${h(r.site)}` }, { t: "Traffic for period", r: (r) => fmtBytes(r.bytes) }, { t: "Share", r: (r) => (total ? ((100 * r.bytes) / total).toFixed(1) : 0) + '%' }] });
    };
    await load();
  },
});

page('notices', {
  title: "Notifications",
  async render(view) {
    head(view, "Notifications", { actions: [
      { t: "Mark all read", i: 'check', fn: async () => { await api('notices.read', { all: true }); route(); } },
      { t: "Clear", i: 'trash', fn: async () => { if (await confirmBox("Clear notifications?", "All notifications will be deleted.")) { await api('notices.clear'); route(); } } },
    ] });
    const p = panel(view);
    const s = panel(view, "Settings");
    const rows = await api('notices.list');
    p.innerHTML = rows.length ? rows.map((n) => `<div class="notice ${n.read ? '' : 'unread'}"><span class="dot ${n.level === 'error' ? 'err' : n.level === 'warn' ? 'warn' : 'ok'}" style="margin-top:7px"></span><span class="text">${h(n.text)}</span><span class="when">${fmtAgo(n.time)}</span></div>`).join('') : `<div class="empty"><div class="circle">${icon('bell')}</div><b>No notifications</b>Warnings appear here: expiring certificates, full disks and backup errors</div>`;
    const st = await api('notices.settings', {});
    s.innerHTML = `<div class="cols2"><div class="field"><label>Administrator email</label><input type="text" data-e value="${h(st.email)}"><div class="hint">Used for Let’s Encrypt. Email notifications are not enabled yet.</div></div><div class="field"><label>Warn when disk usage reaches, %</label><input type="number" data-w value="${st.disk_warn}"></div></div><button class="btn primary" data-s>Save</button>`;
    $('[data-s]', s).onclick = async () => { try { await api('notices.settings', { save: true, email: $('[data-e]', s).value, disk_warn: +$('[data-w]', s).value }); toast("Saved", 'ok'); } catch (e) { fail(e); } };
    if (rows.some((n) => !n.read)) setTimeout(() => api('notices.read', { all: true }).then(refreshBadge), 1500);
  },
});

page('disk', {
  title: "Disk usage",
  async render(view) {
    head(view, "Disk usage");
    const top = panel(view, "Sections");
    const p = panel(view, "What uses disk space");
    const d = await api('stats.disk');
    top.innerHTML = `<div class="stats">${d.parts.map((x) => { const pc = x.total ? Math.round((100 * x.used) / x.total) : 0; return `<div class="stat"><div class="k">${h(x.mount)} <span class="mono">${h(x.dev)}</span></div><div class="v">${fmtBytes(x.used)} <small>of ${fmtBytes(x.total)} · ${pc}%</small></div><div class="bar"><i class="${pc > 90 ? 'err' : pc > 75 ? 'warn' : ''}" style="width:${pc}%"></i></div></div>`; }).join('')}</div>`;
    const items = d.items.sort((a, b) => b.size - a.size);
    const max = Math.max(...items.map((i) => i.size), 1);
    table(p, {
      rows: items,
      columns: [
        { t: "Object", r: (r) => `<div class="site-name">${h(r.name)}<small class="mono">${h(r.path)}</small></div>` },
        { t: "Type", r: (r) => ({ user: "<span class=\"tag\">user</span>", site: "<span class=\"tag acc\">site</span>", sys: "<span class=\"tag\">system</span>", docker: '<span class="tag">Docker</span>', other: "<span class=\"tag warn\">unallocated</span>" }[r.kind] || ''), cls: 'hide-sm' },
        { t: "Size", r: (r) => `<div style="min-width:160px">${fmtBytes(r.size)}<div class="bar"><i style="width:${(100 * r.size) / max}%"></i></div></div>` },
      ],
      actions: (r) => [
        r.clean && { i: 'trash', t: r.clean === 'recycle' ? "Empty recycle bin" : r.clean === 'journal' ? "Trim the journal to 100 MB" : "Clear package cache", label: "Clear", fn: async () => { if (!(await confirmBox(r.clean === 'recycle' ? "Empty the recycle bin?" : "Clean up?", r.clean === 'recycle' ? "All files in the recycle bin will be deleted permanently." : r.clean === 'journal' ? "Old system journal entries will be deleted; the latest 100 MB are kept." : "Downloaded packages will be deleted. Installed programs are not affected.", { ok: "Clear" }))) return; try { const res = await api('stats.disk.clean', { what: r.clean }); toast(res.info, 'ok'); route(); } catch (e) { fail(e); } } },
        r.kind === 'docker' && { i: 'docker', t: "Go to Docker", fn: () => go('docker') },
        r.kind !== 'other' && { i: 'folder', t: "Open", fn: () => go('files', { path: r.path }) },
      ],
    });
    const other = items.find((x) => x.kind === 'other');
    if (other) { const n = document.createElement('p'); n.className = 'hint'; n.textContent = "Unallocated is space on the / partition outside the listed folders: other programs and system data. If it is large, check the folders in the file manager."; p.append(n); }
  },
});

// ================= PHP =================
page('php', {
  title: 'PHP',
  async render(view) {
    head(view, 'PHP', { sub: "Multiple PHP versions simultaneously, independently selected for each site" });
    const p = panel(view);
    const load = async () => {
      const rows = await api('php.list');
      table(p, {
        rows,
        columns: [
          { t: "Version", r: (r) => `<b style="font-size:16px">PHP ${h(r.version)}</b> ${r.default ? "<span class=\"tag acc\">default</span>" : ''}` },
          { t: "Status", r: (r) => r.job === 'running' ? "<span class=\"tag acc\"><span class=\"spin\" style=\"width:12px;height:12px;border-width:2px\"></span> installing</span>" : r.job === 'error' ? "<span class=\"tag err\">installation error</span>" : r.installed ? (r.active ? "<span class=\"tag ok\">running</span>" : "<span class=\"tag warn\">stopped</span>") : "<span class=\"tag\">not installed</span>" },
          { t: "Release", r: (r) => r.installed && r.release ? h(r.release) : '<span class="muted">—</span>', cls: 'nowrap' },
          { t: "Sites", r: (r) => r.sites || '—' },
        ],
        actions: (r) => r.job === 'running' ? [] : r.installed ? [
          { i: 'edit', t: "php.ini settings", label: "Settings", fn: () => phpIni(r.version) },
          { i: 'archive', t: "Modules", label: "Modules", fn: () => phpExt(r.version) },
          !r.default && { i: 'check', t: "Set as default version", fn: async () => { await api('php.default', { version: r.version }); load(); } },
          { i: 'refresh', t: "Restart PHP-FPM", fn: async () => { try { await api('php.restart', { version: r.version }); toast("Restarted", 'ok'); load(); } catch (e) { fail(e); } } },
          { i: 'trash', t: "Delete", danger: true, fn: async () => { if (!(await confirmBox("Remove PHP " + r.version + '?', "PHP packages " + r.version + " will be deleted."))) return; try { await api('php.remove', { version: r.version }); load(); } catch (e) { fail(e); } } },
        ] : [{ i: 'download', t: "Install", label: "Install", fn: async () => { try { await api('php.install', { version: r.version }); toast("PHP installation " + r.version + " started (2–5 minutes)"); load(); } catch (e) { fail(e); } } }],
      });
      return rows.some((r) => r.job === 'running');
    };
    await load();
    every(5000, async () => { if ($$('.spin', p).length) load(); }, view);
  },
});
async function phpExt(v) {
  const d = await api('php.ini', { version: v });
  const en = window.PanelLanguage?.language === 'en';
  const list = (d.catalog || []).map((x) => ({ ...x, title: en && x.title_en ? x.title_en : x.title, gtitle: en && x.group_en ? x.group_en : x.group }));
  const groups = [...new Set(list.map((x) => x.group))];
  const stateTag = (x) => x.installed ? (x.enabled ? "<span class=\"tag ok\">enabled</span>" : "<span class=\"tag warn\">disabled</span>") : "<span class=\"tag\">not installed</span>";
  const buttons = (x) => {
    if (x.available === false) return `<span class="muted">${h((en && x.note_en) || x.note || "unavailable")}</span>`;
    const b = [];
    if (!x.installed) b.push(`<button class="btn small primary" data-act="install" data-ext="${h(x.name)}">${x.builtin ? "Enable" : "Install"}</button>`);
    else {
      b.push(`<button class="btn small" data-act="${x.enabled ? 'disable' : 'enable'}" data-ext="${h(x.name)}">${x.enabled ? "Disable" : "Enable"}</button>`);
      if (!x.builtin) b.push(`<button class="btn small danger" data-act="remove" data-ext="${h(x.name)}">Delete</button>`);
    }
    return b.join(' ');
  };
  const row = (x) => `<tr data-name="${h(x.name + ' ' + x.title).toLowerCase()}"><td><b>${h(x.title)}</b> <span class="muted mono">${h(x.name)}</span></td><td>${stateTag(x)}</td><td class="actions">${buttons(x)}</td></tr>`;
  const m = modal(`<h3>PHP ${h(v)}: modules</h3><p class="muted">Pick the extensions you need: they can be installed, turned off without removing and turned on again. The main ones (mysql, gd, curl, mbstring, xml, zip, intl, opcache…) are installed with the version. Turning off a main module can stop websites that use it.</p>
    <div class="toolbar" style="margin:8px 0"><input type="search" data-q placeholder="Search by name" aria-label="Search module" style="max-width:320px"></div>
    <div style="max-height:56vh;overflow:auto"><table class="grid"><tbody>${groups.map((g) => `<tr class="group" data-group="${h(g)}"><td colspan="3"><b>${h(list.find((x) => x.group === g).gtitle)}</b></td></tr>${list.filter((x) => x.group === g).map(row).join('')}`).join('')}</tbody></table></div>
    <div class="btns"><button class="btn primary" data-close>Done</button></div>`, { wide: true });
  const filter = () => {
    const q = $('[data-q]', m.el).value.trim().toLowerCase();
    $$('tr[data-name]', m.el).forEach((r) => { r.hidden = !!q && !r.dataset.name.includes(q); });
    $$('tr.group', m.el).forEach((g) => { let n = g.nextElementSibling, any = false; while (n && n.dataset.name !== undefined) { if (!n.hidden) any = true; n = n.nextElementSibling; } g.hidden = !any; });
  };
  $('[data-q]', m.el).oninput = filter;
  const labels = { install: ["Installed ", "Install"], remove: ["Deleted ", "Delete"], enable: ["Enabled ", "Enable"], disable: ["Off ", "Disable"] };
  $$('button[data-act]', m.el).forEach((b) => (b.onclick = async () => {
    const act = b.dataset.act, ext = b.dataset.ext;
    if (act === 'remove' && !(await confirmBox("Remove the module?", ext + " will be removed from PHP " + v + ". Websites that use it may stop working.", { ok: "Delete" }))) return;
    if (act === 'disable' && !(await confirmBox("Turn off the module?", ext + " will be turned off in PHP " + v + ", but stays installed. Websites that use it may stop working.", { ok: "Disable", danger: false }))) return;
    b.disabled = true; b.innerHTML = '<span class="spin" style="width:14px;height:14px;border-width:2px"></span>';
    try { await api('php.ext', { version: v, ext, action: act }); toast(labels[act][0] + ext, 'ok'); m.close(); phpExt(v); } catch (e) { fail(e); b.disabled = false; b.textContent = labels[act][1]; }
  }));
}
async function phpIni(v) {
  const d = await api('php.ini', { version: v });
  const labels = { memory_limit: "memory_limit — memory per script", upload_max_filesize: "upload_max_filesize — maximum file size", post_max_size: "post_max_size — maximum POST size", max_execution_time: "max_execution_time — execution time, s", max_input_time: "max_input_time, s", max_input_vars: 'max_input_vars', display_errors: 'display_errors (On/Off)', 'date.timezone': 'date.timezone', short_open_tag: 'short_open_tag (On/Off)', 'opcache.enable': 'opcache.enable (1/0)' };
  openForm({
    title: 'PHP ' + v + ': php.ini',
    fields: [
      { type: 'html', html: "<p class=\"hint\">An empty field means the PHP default and is not written.</p>" },
      ...d.keys.map((k) => ({ name: k, label: labels[k] || k, value: d.values[k] || '', placeholder: "default" })),
      { type: 'html', html: `<p class="muted">File: <span class="mono">${h(d.ini)}</span></p><label>Modules</label><div style="display:flex;flex-wrap:wrap;gap:6px;margin-top:6px">${(d.modules || []).filter((m) => !m.startsWith('[')).map((m) => `<span class="tag">${h(m)}</span>`).join('')}</div>` },
    ],
    submit: async (vals) => { await api('php.ini.save', { version: v, values: vals }); toast("Saved, PHP-FPM restarted", 'ok'); },
  });
}

// ================= Информация о сервере =================
// Перезагрузка сервера: после команды страница ждёт, пока панель снова ответит, и обновляется.
async function rebootServer() {
  if (!(await confirmBox("Reboot the server?", "The server will reboot in a few seconds. Websites, mail and the panel will be unavailable for 1–3 minutes. Running jobs must finish or be stopped first.", { ok: "Reboot" }))) return;
  try { await api('system.reboot'); } catch (e) { fail(e); return; }
  const m = modal(`<h3>The server is rebooting</h3><p data-stage role="status">Waiting for restart…</p><p class="hint">The page reloads by itself when the panel is available again. This usually takes 1–3 minutes.</p>`);
  const started = Date.now();
  let down = false;
  const poll = async () => {
    if (!m.el.isConnected) return;
    try {
      const r = await fetch('/api/me', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Itrupanel': '1' }, body: '{}', cache: 'no-store' });
      if (down && r.status < 500) { location.reload(); return; }
    } catch { down = true; }
    if (Date.now() - started > 15000) down = true;
    $('[data-stage]', m.el).textContent = (down ? "The server is restarting" : "Waiting for restart") + " · elapsed " + Math.round((Date.now() - started) / 1000) + " s";
    setTimeout(poll, 4000);
  };
  setTimeout(poll, 4000);
}

page('info', {
  title: "Server information",
  async render(view) {
    head(view, "Server information", { actions: [{ t: "Panel address and SSL", i: 'lock', fn: () => panelSettingsForm().catch(fail) }, { t: "Reboot server", i: 'refresh', fn: () => rebootServer() }] });
    const a = panel(view, "Server");
    const upd = panel(view, "Panel update");
    const b = panel(view, "Services");
    const [d, monitor, auto] = await Promise.all([api('system.info'), api('stats.monitor').catch(() => null), api('updates.auto.get').catch(() => null)]);
    if (!view.isConnected) return;
    if (auto) {
      const modes = [['off', "Off: update manually"], ['patch', "Automatically only fixes (for example, 0.2.1 → 0.2.2)"], ['all', "Automatically all new releases"]];
      upd.innerHTML = `<div class="field"><label for="au-mode">Automatic update</label><select id="au-mode">${modes.map(([v, n]) => `<option value="${v}" ${v === auto.mode ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
        <div class="field" data-au-hour ${auto.mode === 'off' ? 'style="display:none"' : ''}><label for="au-hour">Installation time</label><select id="au-hour">${[...Array(24).keys()].map((x) => `<option value="${x}" ${x === (auto.hour || 0) ? 'selected' : ''}>${String(x).padStart(2, '0')}:00</option>`).join('')}</select></div>
        <p class="muted" style="margin:0 0 14px">Panel version: ${h(auto.version)}. The update is verified by its signature and checksum, and a copy of the program and settings is saved before installing. You will get a notification with the result. You can still update manually at any time with the “Updates” button in the top bar.</p>
        <button class="btn primary" data-au-save>Save</button>`;
      const sel = $('#au-mode', upd);
      sel.onchange = () => { $('[data-au-hour]', upd).style.display = sel.value === 'off' ? 'none' : ''; };
      $('[data-au-save]', upd).onclick = async () => {
        try { await api('updates.auto.save', { mode: sel.value, hour: +$('#au-hour', upd).value }); toast("Saved", 'ok'); } catch (e) { fail(e); }
      };
    } else upd.remove();
    const serviceRows = monitor ? monitor.services.map(s => ({...s, active:s.state === 'running', manageable:d.services.some(item => item.name === s.name)})) : d.services.map(s => ({...s, manageable:true}));
    a.innerHTML = `<div class="kv">
      <div>Server name</div><div><b>${h(d.hostname)}</b></div>
      <div>Operating system</div><div>${h(d.os)}</div>
      <div>Kernel</div><div>${h(d.kernel)} (${h(d.arch)})</div>
      <div>CPU</div><div>${h(d.cpu || '—')} · ${plural(d.cores, "core", "cores", "cores")}</div>
      <div>Memory</div><div>${fmtBytes(d.mem_used)} of ${fmtBytes(d.mem_total)}</div>
      <div>Works without reloading</div><div>${fmtDuration(d.uptime)}${d.reboot_required ? " <span class=\"tag warn\">reboot required</span>" : ''}</div>
      <div>Load</div><div>${(d.load || []).map((x) => x.toFixed(2)).join(' / ')}</div>
      <div>Server time</div><div>${h(d.time)}</div>
      <div>nginx</div><div class="mono">${h(d.versions.nginx || "not installed")}</div>
      ${d.versions.apache ? `<div>Apache</div><div class="mono">${h(d.versions.apache)}</div>` : ''}${d.versions.ols ? `<div>OpenLiteSpeed</div><div class="mono">${h(d.versions.ols)}</div>` : ''}
      <div>MariaDB</div><div class="mono">${h(d.versions.mariadb || "not installed")}</div>
      <div>certbot</div><div class="mono">${h(d.versions.certbot || "not installed")}</div>
      <div>Panel</div><div>ITRUPANEL ${h(d.panel)} · sites: ${d.sites}, databases: ${d.dbs}</div></div>`;
    table(b, {
      rows: serviceRows,
      columns: [
        { t: "Service", r: (r) => `<b>${h(r.title)}</b> <span class="muted mono">${h(r.name)}</span>` },
        { t: "Status", r: (r) => `<span class="state ${dashboardServiceState(r.state || (r.active ? 'running' : 'stopped'))[1]}">${h(dashboardServiceState(r.state || (r.active ? 'running' : 'stopped'))[0])}</span>` },
      ],
      actions: (r) => r.state === 'missing' && ['postfix', 'dovecot', 'opendkim', 'rspamd'].includes(r.name) ? [
        { i: 'mail', t: "Install and configure in Mail", label: "Configure", fn: () => { location.hash = '#/mail'; } },
      ] : !r.manageable ? [] : [
        { i: 'refresh', t: "Restart", label: "Restart", fn: async () => { try { await api('system.service', { name: r.name, action: 'restart' }); toast("Restarted", 'ok'); route(); } catch (e) { fail(e); } } },
        r.active ? { i: 'stop', t: "Stop", fn: async () => { if (!(await confirmBox("Stop " + r.title + '?', "Sites depending on this service will stop working.", { ok: "Stop" }))) return; try { await api('system.service', { name: r.name, action: 'stop' }); route(); } catch (e) { fail(e); } } }
                 : { i: 'play', t: "Start", fn: async () => { try { await api('system.service', { name: r.name, action: 'start' }); route(); } catch (e) { fail(e); } } },
      ],
    });
  },
});

// ================= Shell-клиент =================
function loadScript(src) { return new Promise((res, rej) => { if ($(`script[src="${src}"]`)) return res(); const s = document.createElement('script'); s.src = src; s.onload = res; s.onerror = rej; document.head.append(s); }); }
page('shell', {
  title: "Shell client",
  async render(view) {
    head(view, "Shell client", { sub: "Server terminal with root privileges. The session closes when you leave the page." });
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
    ws.onclose = () => term.write("\r\n\u001b[33m[session closed]\u001b[0m\r\n");
    term.onData((d) => ws.readyState === 1 && ws.send(new TextEncoder().encode(d)));
    const onResize = () => { fit.fit(); sendSize(); };
    window.addEventListener('resize', onResize);
    App.cleanup = () => { window.removeEventListener('resize', onResize); ws.close(); term.dispose(); };
  },
});
