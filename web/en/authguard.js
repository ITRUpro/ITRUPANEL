// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
page('authguard', { title: "Login protection", async render(view) {
  const d = await api('authguard.status');
  head(view, "Login protection", { sub: "Brute-force protection for the panel and FTP", actions: [{ t: "Update", i: 'refresh', fn: route }] });
  const intro = document.createElement('section'); intro.className = 'sec-hero';
  intro.innerHTML = `<div class="sec-emblem">${icon('lock')}</div><div><div class="sec-eyebrow">ITRUPANEL · PURE-FTPD</div><h2>Five failed attempts.<br>Blocked for fifteen minutes.</h2><p>Repeated login failures within 10 minutes temporarily block the IP address.<br>Existing panel sessions remain active.</p></div>`;
  view.append(intro);
  const context = document.createElement('div'); context.className = 'sec-context';
  context.textContent = d.demo ? "Demo mode: panel login protection runs locally. FTP commands are not executed." : "The IP address comes from the server connection. Users behind a shared proxy may have the same address.";
  view.append(context);
  const cards = document.createElement('div');cards.className = 'sec-metrics';
  cards.innerHTML = `<div class="sec-metric"><div class="sec-metric-top">Panel${icon('shield')}</div><strong>${d.warning ? "Error" : "Enabled"}</strong><span>Blocks survive a restart</span></div><div class="sec-metric"><div class="sec-metric-top">FTP${icon('lock')}</div><strong>${d.ftp.active ? (d.demo ? "Demo" : "Enabled") : "Not enabled"}</strong><span>Fail2ban · TCP ${h(d.ftp.port)}</span></div><div class="sec-metric"><div class="sec-metric-top">Blocked${icon('globe')}</div><strong>${d.bans.length + d.ftp.bans.length}</strong><span>IP addresses in the panel and FTP</span></div>`;
  view.append(cards);
  if (d.warning) {const warning = document.createElement('p');warning.className = 'err';warning.textContent = d.warning;view.append(warning);}
  const settings = panel(view, "FTP protection");settings.replaceChildren();
  const note = document.createElement('p');note.className = 'hint';note.textContent = "The module installs Fail2ban and nftables, reads the Pure-FTPd journal and blocks only the specified FTP port. Initial setup may restart FTP. This does not protect against network saturation.";settings.append(note);
  const button = document.createElement('button');button.className = 'btn primary';button.textContent = d.ftp.active ? "Configure FTP protection" : "Install and enable";
  button.onclick = () => openForm({ title: "FTP brute-force protection", fields: [
    { name: 'port', label: "FTP port", type: 'number', value: d.ftp.port, required: true, hint: "Enter the Pure-FTPd connection port, usually 21. Passive data ports are not needed here." },
    { type: 'html', html: "<p class=\"hint\">5 failures within 10 minutes block the IP for 15 minutes. Local server connections are excluded from FTP blocks. The FTP service port is not changed.</p>" },
  ], submit: async v => {const r = await api('authguard.ftp.enable', { port: Number(v.port) });watchJob(r.job);} });settings.append(button);
  if (d.ftp.warning) {const warning = document.createElement('p');warning.className = 'hint';warning.textContent = "Fail2ban did not confirm that FTP protection is active. Check the task log or run setup again.";settings.append(warning);}
  const bans = panel(view, "Temporary blocks");bans.replaceChildren();
  const rows = [...d.bans, ...d.ftp.bans.map(ip => ({ip, service: 'ftp', until: null}))];
  table(bans, { rows, empty: { icon: 'shield', title: "No active blocks" }, columns: [
    { t: "IP address", r: r => h(r.ip) },
    { t: "Service", r: r => r.service === 'panel' ? "Panel" : 'FTP' },
    { t: "Expires", r: r => r.until ? fmtDate(r.until) : "Automatically via Fail2ban" },
  ], actions: r => [{ t: "Unblock IP", label: "Unblock", i: 'unlock', fn: async () => {try {await api('authguard.unban', {ip: r.ip, service: r.service});toast("Block removed", 'ok');await route();} catch(e) {fail(e);}} }] });
} });
