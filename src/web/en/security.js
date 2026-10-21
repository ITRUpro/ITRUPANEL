// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
const securityModes = [['off', "Disabled"], ['monitor', "Monitor"], ['enforce', "Block"]];
function securityMode(mode) { return securityModes.find(x => x[0] === mode)?.[1] || mode; }
function securityForm(site) {
  const p = site.policy;
  openForm({ title: "Protection · " + site.domain, wide: true, fields: [
    { name: 'mode', label: "Protection mode", type: 'seg', value: p.mode, options: securityModes },
    { type: 'html', html: v => '<div class="sec-form-note">' + (v.mode === 'monitor' ? "Requests pass without blocking. Limit violations and denylist matches are logged." : v.mode === 'enforce' ? "Nginx rejects limit violations with HTTP 429 and denylisted addresses with HTTP 403." : "Rules are saved but do not apply to requests.") + '</div>' },
    { type: 'section', label: "Limits per IP" },
    { name: 'rate', label: "Requests per second", type: 'number', value: p.rate, required: true, hint: "From 1 to 10000. Counts requests reaching Nginx processing, including static files." },
    { name: 'burst', label: "Burst allowance", type: 'number', value: p.burst, hint: "From 0 to 10000 extra requests without delay. Allows pages with multiple resources to load." },
    { name: 'connections', label: "Concurrent requests being processed", type: 'number', value: p.connections, required: true, hint: "From 1 to 10000. Concurrent HTTP/2 and HTTP/3 requests are counted separately." },
    { type: 'section', label: "Address lists" },
    { name: 'allow', label: "Allowlist", type: 'textarea', rows: 4, value: (p.allow || []).join('\n'), placeholder: '203.0.113.10\n2001:db8::/64', hint: "One IP or CIDR per line. The allowlist bypasses limits and takes priority over the denylist." },
    { name: 'deny', label: "Denylist", type: 'textarea', rows: 4, value: (p.deny || []).join('\n'), placeholder: '198.51.100.0/24', hint: "Up to 256 addresses or networks per list. Supports IPv4 and IPv6." },
    { type: 'html', html: "<p class=\"hint\">Let’s Encrypt checks are exempt. If using a CDN, first configure the real client IP from trusted proxies only. Otherwise limits are shared by the proxy address.</p>" },
  ], submit: async v => {
    await api('security.save', { domain: site.domain, policy: { mode: v.mode, rate: Number(v.rate), burst: Number(v.burst), connections: Number(v.connections), allow: v.allow.split(/[\s,;]+/).filter(Boolean), deny: v.deny.split(/[\s,;]+/).filter(Boolean), revision: p.revision } });
    toast(App.me.demo ? "Settings saved in demo mode" : "Settings validated and applied", 'ok'); await route();
  } });
}
function securityReason(e) {
  if (e.listed === '1') return "Denylist";
  if ((e.conn || '').includes('REJECTED')) return "Connection limit";
  return "Request limit";
}
page('security', { title: "Site protection", async render(view) {
  const d = await api('security.list'), sites = d.sites || [], events = d.events || [];
  let tab = 'overview', selected = '', search = '';
  const blocked = events.filter(e => e.mode === 'enforce' && (e.listed === '1' || e.req === 'REJECTED' || e.conn === 'REJECTED')).length;
  const monitored = events.filter(e => e.mode === 'monitor').length;
  const active = sites.filter(s => s.policy.mode !== 'off' && !s.suspended).length;
  head(view, "Site protection", { sub: "HTTP request control at the Nginx layer", actions: [{ t: "Update", i: 'refresh', fn: route }] });
  const intro = document.createElement('section'); intro.className = 'sec-hero';
  intro.innerHTML = `<div class="sec-emblem">${icon('shield')}</div><div><div class="sec-eyebrow">NGINX · HTTP</div><h2>Fewer unwanted requests.<br>More resources for visitors.</h2><p>Manage limits and access for each website.<br>Start with monitoring, then enable blocking.</p></div><div class="sec-hero-side"><span class="sec-status-dot"></span><b>${active} / ${sites.length}</b><span>sites with enabled rules</span></div>`;
  view.append(intro);
  const info = document.createElement('div'); info.className = 'sec-context';
  info.innerHTML = d.demo ? "<span class=\"tag warn\">Demo mode</span> Settings are saved locally. System commands are not executed; log events are examples." : "Protection applies to websites. The admin panel and network bandwidth protection are configured separately.";
  view.append(info);
  const metrics = document.createElement('div'); metrics.className = 'sec-metrics';
  metrics.innerHTML = [[active, "Protected sites", "Out of " + sites.length + " sites", 'shield'], [blocked, "Blocked", "In the log sample", 'lock'], [monitored, "Monitor", "Without blocking visitors", 'eye'], [new Set(events.map(e => e.ip)).size, "IPs in events", "In the log sample", 'globe']].map(([n,t,sub,i]) => `<div class="sec-metric"><div class="sec-metric-top">${h(t)}${icon(i)}</div><strong>${n}</strong><span>${h(sub)}</span></div>`).join('');
  view.append(metrics);
  const nav = document.createElement('div'); nav.className = 'sec-tabs';
  nav.innerHTML = [['overview',"Overview"],['sites',"Site settings"],['events',"Event log"]].map(([id,label]) => `<button class="${id === tab ? 'on' : ''}" data-sec-tab="${id}">${label}</button>`).join('');view.append(nav);
  const content = document.createElement('div');view.append(content);
  const renderSites = () => {
    const box = panel(content, "Site rules"); box.replaceChildren();
    const list = document.createElement('div');box.append(list);
    table(list, { rows: sites, empty: { icon: 'globe', title: "Add a website first", text: "<p>Its protection settings will appear here once the site is created.</p>" }, columns: [
      { t: "Site", r: s => '<b>' + h(s.domain) + '</b>' + (s.suspended ? "<div class=\"hint\">Site suspended</div>" : '') },
      { t: "Mode", r: s => `<span class="tag ${s.policy.mode === 'enforce' ? 'ok' : s.policy.mode === 'monitor' ? 'warn' : ''}">${h(securityMode(s.policy.mode))}</span>` },
      { t: "Requests / sec", r: s => s.policy.rate + '<span class="muted"> + ' + s.policy.burst + '</span>' },
      { t: "Connections", r: s => s.policy.connections },
      { t: "IP lists", r: s => (s.policy.allow || []).length + ' / ' + (s.policy.deny || []).length },
    ], onRow: securityForm, actions: s => [{ t: "Configure protection", label: "Configure", i: 'edit', fn: () => securityForm(s) }] });
  };
  const renderEvents = () => {
    const box = panel(content, "Recent events"); box.replaceChildren();
    const filters = document.createElement('div');filters.className = 'sec-filters';
    filters.innerHTML = `<select aria-label="Site"><option value="">All sites</option>${sites.map(s => `<option value="${h(s.domain)}" ${s.domain === selected ? 'selected' : ''}>${h(s.domain)}</option>`).join('')}</select><input type="search" aria-label="Search IP or path" placeholder="Search IP or path" value="${h(search)}">`;
    box.append(filters);
    const list = document.createElement('div');box.append(list);
    const draw = () => table(list, { rows: events.filter(e => (!selected || e.domain === selected) && (!search || (e.ip + ' ' + e.uri).toLowerCase().includes(search.toLowerCase()))), empty: { icon: 'shield', title: "No events yet", text: "<p>Events appear when a limit is exceeded or an address matches the denylist.</p>" }, columns: [
      { t: "Time", r: e => h(fmtDate(e.time)) }, { t: "Site / request", r: e => `<b>${h(e.domain)}</b><div class="sec-uri" title="${h(e.uri)}">${h(e.method)} ${h(e.uri)}</div>` },
      { t: "IP address", r: e => `<span class="mono">${h(e.ip)}</span>` }, { t: "Reason", r: e => h(securityReason(e)) },
      { t: "Action", r: e => `<span class="tag ${e.mode === 'monitor' ? 'warn' : 'err'}">${e.mode === 'monitor' ? "Monitor" : "Block"}</span><div class="hint">HTTP ${e.status}</div>` },
    ] });
    $('select', filters).onchange = e => { selected = e.target.value;draw(); };
    $('input', filters).oninput = e => { search = e.target.value;draw(); };draw();
    const hint = document.createElement('p');hint.className = 'hint';hint.textContent = "Up to 300 recent events: at most 200 per site from the last 256 KB of the current log. Entries may take up to 5 seconds to appear. This is a sample, not complete daily statistics.";box.append(hint);
  };
  const draw = () => {
    content.replaceChildren();
    if (tab === 'overview') {
      const box = panel(content, "Events in the last 60 minutes");
      const now = Date.now(), buckets = Array.from({length: 12}, () => ({block:0, monitor:0}));
      events.forEach(e => {const i = Math.floor((new Date(e.time).getTime() - (now - 3600000)) / 300000);if (i >= 0 && i < 12) buckets[i][e.mode === 'monitor' ? 'monitor' : 'block']++;});
      const max = Math.max(1, ...buckets.map(b => b.block + b.monitor));
      box.innerHTML = `<div class="sec-legend"><span><i></i>Block</span><span><i></i>Monitor</span><span class="muted">Based on the log sample · 5-minute intervals</span></div><div class="sec-chart">${buckets.map((b,i) => `<div class="sec-bar" title="${b.block + b.monitor}"><div class="sec-bar-fill" style="height:${(b.block+b.monitor)/max*100}%"><div style="flex:${b.monitor};background:var(--warn)"></div><div style="flex:${b.block};background:var(--accent)"></div></div><span>${new Date(now - (12-i)*300000).toLocaleTimeString(window.PanelLanguage?.locale,{hour:'2-digit',minute:'2-digit'})}</span></div>`).join('')}</div>`;
      renderSites();
      const note = panel(content);note.innerHTML = "<b>What this module protects</b><p class=\"muted\">Limits help reduce load from frequent HTTP requests. SQL injection inspection, XSS filtering and country blocking are not included yet. Upstream filtering is required if the network link is saturated.</p>";
    } else if (tab === 'sites') renderSites(); else renderEvents();
    if (d.warnings?.length) {const p = panel(content);p.textContent = d.warnings.join('\n');}
  };
  $$('button',nav).forEach(b => b.onclick = () => {tab = b.dataset.secTab;$$('button',nav).forEach(x => x.classList.toggle('on',x === b));draw();});draw();
} });
