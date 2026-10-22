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
  const login = await api('security.login.get').catch(() => null);
  if (login) {
    login.ips.list = login.ips.list || [];
    const two = panel(view, "Two-step authentication");two.replaceChildren();
    const twoNote = document.createElement('p');twoNote.className = 'hint';
    twoNote.textContent = login.totp.enabled ? "Enabled. Signing in needs the password and a one-time code from the authenticator app. Recovery codes left: " + login.totp.recovery_left + '.' : "Disabled. Once enabled, signing in will also need a one-time code from an app such as Google Authenticator, Yandex Key, Microsoft Authenticator or Aegis.";
    two.append(twoNote);
    const recovery = (codes) => {
      const m = modal(`<div class="backup-settings-head"><h3>Recovery codes</h3></div><p class="hint">Keep these codes in a safe place. Each works once instead of the app code if you lose your phone. They will not be shown again.</p><pre class="mono" style="user-select:all;font-size:15px;line-height:1.7">${codes.map(h).join('\n')}</pre><div style="display:flex;gap:10px;margin-top:14px"><button class="btn" data-copy>Copy</button><button class="btn primary" data-close>I have saved the codes</button></div>`);
      $('[data-copy]', m.el).onclick = () => { navigator.clipboard && navigator.clipboard.writeText(codes.join('\n')).then(() => toast("Copied", 'ok')).catch(() => {}); };
    };
    const ask = (title, fn, extra) => openForm({ title, ok: "Confirm", fields: [
      { name: 'password', label: "Administrator password", type: 'password', required: true },
      { name: 'code', label: "App code or recovery code", required: true },
      ...(extra || []) ], submit: fn });
    if (!login.totp.enabled) {
      const b = document.createElement('button');b.className = 'btn primary';b.textContent = "Enable";
      b.onclick = async () => {
        try {
          await loadScript('/vendor/qrcode.js');
          const r = await api('security.2fa.begin');
          const qr = qrcode(0, 'M');qr.addData(r.uri);qr.make();
          const m = modal(`<div class="backup-settings-head"><h3>Two-step authentication</h3><button class="btn" data-close>Close</button></div>
            <p>1. Open the authenticator app and add an account: scan the QR code or enter the key manually.</p>
            <div style="display:flex;gap:24px;flex-wrap:wrap;align-items:center"><div style="width:200px;height:200px;background:#fff;padding:6px;border-radius:12px">${qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true })}</div>
            <div><div class="muted">Key for manual entry</div><div class="mono" style="font-size:16px;user-select:all;word-break:break-all;max-width:320px">${h(r.secret.match(/.{1,4}/g).join(' '))}</div></div></div>
            <p style="margin-top:16px">2. Enter the six-digit code from the app:</p>
            <div style="display:flex;gap:10px"><input type="text" data-code inputmode="numeric" autocomplete="one-time-code" maxlength="8" placeholder="123456" style="max-width:160px"><button class="btn primary" data-ok>Enable</button></div><p class="err" data-err></p>`);
          const svg = $('svg', m.el);if (svg) { svg.style.width = '100%'; svg.style.height = '100%'; }
          $('[data-ok]', m.el).onclick = async () => {
            try { const x = await api('security.2fa.enable', { code: $('[data-code]', m.el).value }); m.close(); await route(); recovery(x.recovery); }
            catch (e) { $('[data-err]', m.el).textContent = e.message; }
          };
        } catch (e) { fail(e); }
      };
      two.append(b);
    } else {
      const wrap = document.createElement('div');wrap.style.cssText = 'display:flex;gap:10px;flex-wrap:wrap';
      const nb = document.createElement('button');nb.className = 'btn';nb.textContent = "New recovery codes";
      nb.onclick = () => ask("New recovery codes", async (v) => { const x = await api('security.2fa.recovery', v); await route(); recovery(x.recovery); });
      const off = document.createElement('button');off.className = 'btn danger';off.textContent = "Disable";
      off.onclick = () => ask("Turn off two-step authentication", async (v) => { await api('security.2fa.disable', v); toast("Turned off", 'ok'); route(); });
      wrap.append(nb, off);two.append(wrap);
    }
    const ips = panel(view, "Access to the panel only from selected IP addresses");ips.replaceChildren();
    const ipNote = document.createElement('p');ipNote.className = 'hint';
    ipNote.textContent = login.ips.list.length ? "The panel opens only from the addresses in the list. All others are refused." : "No restriction: the panel opens from any address. Enter addresses or networks to allow only them.";
    const area = document.createElement('textarea');area.rows = 4;area.value = login.ips.list.join('\n');area.placeholder = '203.0.113.5\n198.51.100.0/24';area.style.cssText = 'width:100%;max-width:520px;font-family:var(--mono,monospace)';area.setAttribute('aria-label', "Allowed addresses");
    const cur = document.createElement('p');cur.className = 'hint';cur.append("Your current address: ");const curB = document.createElement('b');curB.textContent = login.ips.current;cur.append(curB, '. ');
    const addMe = document.createElement('a');addMe.href = '#';addMe.textContent = "add to the list";addMe.onclick = (e) => { e.preventDefault(); if (!area.value.split(/\s+/).includes(login.ips.current)) area.value = (area.value.trim() ? area.value.trim() + '\n' : '') + login.ips.current; };cur.append(addMe);
    const warn = document.createElement('p');warn.className = 'hint';warn.textContent = "One line per address or network (for example, 198.51.100.0/24). An empty list removes the restriction. If you lose access, run on the server: itrupanel allow-all-ips";
    const save = document.createElement('button');save.className = 'btn primary';save.textContent = "Save";
    save.onclick = async () => { try { await api('security.ips.save', { list: area.value }); toast("Saved", 'ok'); route(); } catch (e) { fail(e); } };
    ips.append(ipNote, area, cur, warn, save);
  }
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
