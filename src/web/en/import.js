// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Импорт данных со старого сервера: подключение → просмотр → выбор → перенос.
// Старый сервер только читается. Логины и пароли хранятся в памяти панели, не на диске.

const impStatus = { ok: ['ok', "transferred"], skipped: ['warn', "skipped"], error: ['err', "error"] };

page('import', {
  title: "Import data",
  async render(view) {
    head(view, "Import data", { sub: "Moving websites, databases, mail, DNS, FTP and cron jobs from another ITRUPANEL, from FASTPANEL or from any server over SSH" });
    const host = document.createElement('div');
    view.append(host);
    let session = null;

    // ---------- шаг 1: подключение ----------
    const stepConnect = (source = 'itrupanel') => {
      host.replaceChildren();
      const p = panel(host, "1. Where to move from");
      const sources = [['itrupanel', 'ITRUPANEL'], ['fastpanel', 'FASTPANEL'], ['ssh', "Other server over SSH"]];
      const hints = {
        itrupanel: "Enter the address and administrator login of the old ITRUPANEL. The panel itself provides websites, databases, mail, DNS, FTP and cron, and files and databases are sent directly.",
        fastpanel: "Connect over SSH as root (or as a user with passwordless sudo). The lists of websites, databases, FTP and mail are read from FASTPANEL itself; if it does not answer, the server is inspected through the nginx and MySQL settings. Websites, databases (with passwords), FTP users, mail with messages and cron jobs are moved.",
        ssh: "Connect over SSH as root (or as a user with passwordless sudo). Websites are found through the nginx settings. Websites, databases and cron jobs are moved; mail, DNS and FTP only from ITRUPANEL and FASTPANEL.",
      };
      const sshFields = `<div class="cols2">
          <div class="field"><label>Server address</label><input type="text" name="ssh_host" placeholder="203.0.113.10" spellcheck="false"></div>
          <div class="field"><label>SSH port</label><input type="text" name="ssh_port" value="22" inputmode="numeric"></div>
          <div class="field"><label>User</label><input type="text" name="ssh_user" value="root" spellcheck="false" autocomplete="off"></div>
          <div class="field"><label>Password</label><input name="ssh_password" type="password" autocomplete="new-password"></div>
        </div>
        <div class="field"><label>or an SSH private key (without a passphrase, if you sign in by key)</label><textarea name="ssh_key" rows="3" spellcheck="false" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></div>`;
      const itruFields = `<div class="cols2">
          <div class="field"><label>Panel address</label><input type="text" name="panel_url" placeholder="https://old.example.com:3333" spellcheck="false"></div>
          <div class="field"><label>Administrator login</label><input type="text" name="panel_login" value="admin" spellcheck="false" autocomplete="off"></div>
          <div class="field"><label>Password</label><input name="panel_password" type="password" autocomplete="new-password"></div>
        </div>`;
      p.innerHTML = `<div class="seg" data-src>${sources.map(([v, n]) => `<button type="button" data-v="${v}" class="${v === source ? 'on' : ''}">${n}</button>`).join('')}</div>
        <p class="hint" style="margin-top:14px">${hints[source]}</p>
        ${source === 'itrupanel' ? itruFields : sshFields}
        <p class="hint">Logins, passwords and keys are kept only in the panel’s memory while the transfer runs (up to three hours) and are never written to disk or logs. Nothing is changed on the old server.</p>
        <div class="btns"><button class="btn primary" data-go>${icon('search')}Check the old server</button></div>`;
      $$('[data-src] button', p).forEach((b) => (b.onclick = () => stepConnect(b.dataset.v)));
      $('[data-go]', p).onclick = async (e) => {
        const v = Object.fromEntries($$('input,textarea', p).map((i) => [i.name, i.value]));
        const body = source === 'itrupanel'
          ? { panel: { url: v.panel_url, login: v.panel_login, password: v.panel_password } }
          : { ssh: { host: v.ssh_host, port: +v.ssh_port || 22, user: v.ssh_user, password: v.ssh_password, key: v.ssh_key } };
        e.target.disabled = true;
        try {
          const r = await api('import.analyze', body);
          watchJob(r.job, { onComplete: (j) => { if (j.status === 'done' && j.result?.session) { session = j.result; stepChoose(); } } });
        } catch (err) { fail(err); } finally { e.target.disabled = false; }
      };
    };

    // ---------- шаг 2: выбор ----------
    const stepChoose = () => {
      host.replaceChildren();
      const inv = session.inventory;
      const sel = { sites: new Set(), dbs: new Set(), cron: false, mail: new Set(), dns: new Set(), ftp: new Set(), backup: false };
      const info = panel(host, "Old server");
      info.innerHTML = `<div class="kv"><div>Address</div><div><b>${h(inv.host || '—')}</b></div>
        <div>System</div><div>${h(inv.os || '—')}</div>
        <div>Panel</div><div>${h(({ itrupanel: 'ITRUPANEL', fastpanel: 'FASTPANEL', ispmanager: 'ISPmanager', aapanel: 'aaPanel', hestia: 'Hestia' })[inv.panel] || inv.panel || "not detected")}</div>
                ${inv.panel_fingerprint ? `<div>Panel certificate</div><div class="mono">${h(inv.panel_fingerprint)}</div>` : ''}
        ${inv.fingerprint ? `<div>SSH server key</div><div class="mono">${h(inv.fingerprint)}</div>` : ''}
        <div>PHP versions</div><div>${h((inv.php_versions || []).join(', ') || '—')}</div></div>
        ${(inv.warnings || []).map((w) => `<p class="hint">⚠ ${h(w)}</p>`).join('')}
        ${inv.can_transfer ? '' : "<p class=\"err\">Moving is unavailable: this panel does not provide files and databases. Update it to version 0.1.94.</p>"}
        <div class="btns" style="margin-top:14px"><button class="btn" data-back>${icon('chevron')}Another server</button></div>`;
      $('[data-back]', info).onclick = () => { api('import.forget', { session: session.session }).catch(() => {}); session = null; stepConnect(); };

      const bar = document.createElement('div');
      bar.className = 'toolbar';
      bar.style.margin = '14px 0';
      bar.innerHTML = `<button class="btn" data-all>Select all</button><button class="btn" data-none>Clear selection</button><span class="grow"></span><span class="muted" data-count style="padding-right:6px"></span>`;
      host.append(bar);

      const checks = [];
      const section = (title, rows, columns, onPick, kind) => {
        const p = panel(host, title);
        if (!rows.length) { p.innerHTML = `<p class="muted">Not found</p>`; return; }
        table(p, { rows, columns: [{ t: '', r: (r, i) => `<input type="checkbox" data-pick="${kind}" data-key="${h(r.domain || r.name)}" aria-label="${h(r.domain || r.name)}">`, cls: 'chk' }, ...columns] });
        $$('[data-pick="' + kind + '"]', p).forEach((c) => { checks.push(c); c.onchange = () => { onPick(c.dataset.key, c.checked); recount(); }; });
      };
      section("Sites", inv.sites, [
        { t: "Domain", r: (s) => `<b>${h(s.domain)}</b>${s.aliases?.length ? `<div class="hint">${h(s.aliases.join(', '))}</div>` : ''}` },
        { t: "Owner", r: (s) => h(s.owner || '—'), cls: 'hide-sm' },
        { t: "Type", r: (s) => ({ php: 'PHP' + (s.php ? ' ' + s.php : ''), static: "static", proxy: "proxy" }[s.mode] || s.mode) + (s.ssl ? ' <span class="tag">SSL</span>' : '') },
        { t: "Size", r: (s) => (s.size ? fmtBytes(s.size) : '—'), cls: 'nowrap hide-sm' },
      ], (k, on) => (on ? sel.sites.add(k) : sel.sites.delete(k)), 'site');
      section("Databases", inv.dbs, [
        { t: "Database", r: (d) => `<b>${h(d.name)}</b>` },
        { t: "Users", r: (d) => h((d.users || []).join(', ') || '—'), cls: 'hide-sm' },
        { t: "Size", r: (d) => fmtBytes(d.size), cls: 'nowrap' },
      ], (k, on) => (on ? sel.dbs.add(k) : sel.dbs.delete(k)), 'db');

      const cron = panel(host, "Cron jobs");
      if (!inv.cron?.length) cron.innerHTML = "<p class=\"muted\">Not found</p>";
      else {
        cron.innerHTML = `<label class="check"><input type="checkbox" data-cron><span>Move cron jobs of the owners of the selected websites (${inv.cron.length})</span></label><div class="table-wrap"><table class="grid"><thead><tr><th>Owner</th><th>Schedule</th><th>Command</th></tr></thead><tbody>${inv.cron.map((c) => `<tr><td>${h(c.user)}</td><td class="mono">${h(c.schedule)}</td><td class="mono">${h(c.command)}</td></tr>`).join('')}</tbody></table></div>`;
        const c = $('[data-cron]', cron);
        checks.push(c);
        c.onchange = () => { sel.cron = c.checked; recount(); };
      }

      const addHint = (title, text) => { const el = host.lastElementChild; if (el && text) el.insertAdjacentHTML('beforeend', `<p class="hint" style="margin-top:10px">${text}</p>`); };
      section("Mail", inv.mail_domains || [], [
        { t: "Domain", r: (m) => `<b>${h(m.name)}</b>` },
        { t: "Mailboxes", r: (m) => h(String(m.boxes || 0)) },
      ], (k, on) => (on ? sel.mail.add(k) : sel.mail.delete(k)), 'mail');
      if (inv.mail_domains?.length) addHint('', "Mailboxes are moved together with their messages, plus aliases and forwards; mailbox passwords are kept. The mail certificate is self-signed at first: when the domain points to this server, issue Let’s Encrypt in the Mail section.");
      section("DNS zones", inv.dns || [], [
        { t: "Zone", r: (z) => `<b>${h(z.name)}</b>` },
      ], (k, on) => (on ? sel.dns.add(k) : sel.dns.delete(k)), 'dns');
      if (inv.dns?.length) addHint('', "The old server IP in A and AAAA records is replaced with the IP of this server; mail records (MX, SPF, DKIM, DMARC) are published again if you move the mail of this domain.");
      section("FTP users", inv.ftp || [], [
        { t: "User", r: (f) => `<b>${h(f.name)}</b>` },
        { t: "Owner", r: (f) => h(f.owner || '—'), cls: 'hide-sm' },
        { t: "Folder", r: (f) => h(f.home || '—'), cls: 'hide-sm' },
      ], (k, on) => (on ? sel.ftp.add(k) : sel.ftp.delete(k)), 'ftp');
      if (inv.ftp?.length) addHint('', "An FTP user is moved together with the website of its owner: select that website too. The password is kept.");

      const bk = inv.backup;
      const bkPanel = panel(host, "Backups");
      if (!bk) bkPanel.innerHTML = "<p class=\"muted\">Not found</p>";
      else if (bk.target === 'local' || !bk.host) bkPanel.innerHTML = "<p class=\"muted\">On the old server backups are stored only on the server itself: nothing to move.</p>";
      else {
        bkPanel.innerHTML = `<label class="check"><input type="checkbox" data-backup><span>${bk.target === 's3' || bk.target === 'local_s3' ? "Move the backup settings for the cloud (" : "Move the backup settings for FTP ("}${h(bk.host)}${bk.dir ? ", folder " + h(bk.dir) : ''})</span></label>
          <p class="hint" style="margin-top:10px">The storage address (FTP or cloud), the login or keys, the folder and the backup parameters are moved. The schedule on this server stays off until you turn it on yourself in the Backups section, so two panels do not back up the same websites at the same time. Backups that already sit on the FTP or in the cloud will be found and attached.</p>`;
        const bc = $('[data-backup]', bkPanel);
        checks.push(bc);
        bc.onchange = () => { sel.backup = bc.checked; recount(); };
      }

      const needs = panel(host, "What this server will need");
      const go = document.createElement('div');
      go.className = 'btns';
      go.style.margin = '14px 0 4px';
      go.innerHTML = `<button class="btn primary" data-run disabled>${icon('download')}Move the selection</button>`;
      host.append(go);
      const selection = () => ({ sites: [...sel.sites], dbs: [...sel.dbs], cron: sel.cron, mail: [...sel.mail], dns: [...sel.dns], ftp: [...sel.ftp], backup: sel.backup });
      let needTimer;
      const recount = () => {
        const n = sel.sites.size + sel.dbs.size + sel.mail.size + sel.dns.size + sel.ftp.size + (sel.cron ? 1 : 0) + (sel.backup ? 1 : 0);
        $('[data-count]', bar).textContent = n ? `Selected: websites ${sel.sites.size}, databases ${sel.dbs.size}${sel.mail.size ? ", mail " + sel.mail.size : ''}${sel.dns.size ? ', DNS ' + sel.dns.size : ''}${sel.ftp.size ? ', FTP ' + sel.ftp.size : ''}${sel.cron ? ', cron' : ''}${sel.backup ? ", backups" : ''}` : '';
        $('[data-run]', go).disabled = !n || !inv.can_transfer;
        clearTimeout(needTimer);
        needTimer = setTimeout(async () => {
          try {
            const r = await api('import.needs', { session: session.session, selection: selection() });
            needs.innerHTML = r.needs.length ? `<table class="grid"><tbody>${r.needs.map((x) => `<tr><td><b>${h(x.title)}</b>${x.detail ? `<div class="hint">${h(x.detail)}</div>` : ''}</td><td>${x.installed ? "<span class=\"tag ok\">already installed</span>" : "<span class=\"tag acc\">will be installed automatically</span>"}</td></tr>`).join('')}</tbody></table>` : "<p class=\"muted\">Everything needed is already here. Select websites or databases above.</p>";
          } catch (e) { needs.textContent = e.message; }
        }, 250);
      };
      recount();
      $('[data-all]', bar).onclick = () => { checks.forEach((c) => { c.checked = true; c.onchange(); }); };
      $('[data-none]', bar).onclick = () => { checks.forEach((c) => { c.checked = false; c.onchange(); }); };
      $('[data-run]', go).onclick = async () => {
        const s = selection();
        if (!(await confirmBox("Start the transfer?", `Will be moved: websites — ${s.sites.length}, databases — ${s.dbs.length}${s.mail.length ? ", mail domains — " + s.mail.length : ''}${s.dns.length ? ", DNS zones — " + s.dns.length : ''}${s.ftp.length ? ", FTP users — " + s.ftp.length : ''}${s.cron ? ", cron jobs" : ''}${s.backup ? ", backup settings" : ''}. Missing PHP versions and services are installed automatically. Nothing changes on the old server. Anything that already exists on this server under the same name is skipped.`, { ok: "Move", danger: false }))) return;
        try {
          const r = await api('import.run', { session: session.session, selection: s });
          watchJob(r.job, { onComplete: (j) => { if (j.result) stepReport(j.result, j.status); } });
        } catch (e) { fail(e); }
      };
    };

    // ---------- шаг 3: итог ----------
    const stepReport = (res, status) => {
      host.replaceChildren();
      const p = panel(host, status === 'done' ? "Transfer finished" : "Transfer finished with errors");
      p.innerHTML = `<p>${h(res.info || '')}</p>
        ${(res.warnings || []).map((w) => `<p class="hint">⚠ ${h(w)}</p>`).join('')}
        ${res.sites?.length ? `<h4 style="margin:14px 0 8px">Sites</h4><div class="table-wrap"><table class="grid"><thead><tr><th>Domain</th><th>User</th><th>Status</th><th>Size</th></tr></thead><tbody>${res.sites.map((s) => `<tr><td><b>${h(s.domain)}</b></td><td>${h(s.user)}</td><td><span class="tag ${impStatus[s.status][0]}">${impStatus[s.status][1]}</span>${s.detail ? `<div class="hint">${h(s.detail)}</div>` : ''}</td><td class="nowrap">${s.bytes ? fmtBytes(s.bytes) : ''}</td></tr>`).join('')}</tbody></table></div>` : ''}
        ${res.dbs?.length ? `<h4 style="margin:14px 0 8px">Databases</h4><div class="table-wrap"><table class="grid"><thead><tr><th>Database</th><th>User</th><th>Status</th></tr></thead><tbody>${res.dbs.map((d) => `<tr><td><b>${h(d.name)}</b></td><td>${h(d.user || '')}</td><td><span class="tag ${impStatus[d.status][0]}">${impStatus[d.status][1]}</span>${d.password_kept ? " <span class=\"tag\">password kept</span>" : ''}${d.detail ? `<div class="hint">${h(d.detail)}</div>` : ''}</td></tr>`).join('')}</tbody></table></div>` : ''}
        ${res.cron ? `<p>Cron jobs moved: <b>${res.cron}</b></p>` : ''}
        ${res.backup ? `<h4 style="margin:14px 0 8px">Backups</h4><p><span class="tag ${impStatus[res.backup.status][0]}">${impStatus[res.backup.status][1]}</span> ${res.backup.detail ? h(res.backup.detail) : ''}</p>` : ''}
        ${['mail', 'dns', 'ftp'].map((k) => (res[k]?.length ? `<h4 style="margin:14px 0 8px">${({ mail: "Mail", dns: "DNS zones", ftp: "FTP users" })[k]}</h4><div class="table-wrap"><table class="grid"><thead><tr><th>Name</th><th>Status</th></tr></thead><tbody>${res[k].map((x) => `<tr><td><b>${h(x.name)}</b></td><td><span class="tag ${impStatus[x.status][0]}">${impStatus[x.status][1]}</span>${x.detail ? `<div class="hint">${h(x.detail)}</div>` : ''}</td></tr>`).join('')}</tbody></table></div>` : '')).join('')}
        <p class="hint">SSL certificates are not moved: once the domain points to this server, issue Let’s Encrypt in the Websites section.</p>
        <div class="btns"><button class="btn" data-secrets>Show new passwords</button><button class="btn primary" data-done>Done</button></div>`;
      $('[data-secrets]', p).onclick = async () => {
        try {
          const r = await api('import.secrets', { session: session.session });
          const items = Object.entries(r.passwords || {});
          if (!items.length) { toast("No new passwords: database and FTP passwords are kept", 'ok'); return; }
          showSecrets("New passwords", items.map(([k, v]) => [k, v]), "Passwords are shown once. Set the database password in the settings of the website that uses it; give the FTP password to the user.");
        } catch (e) { fail(e); }
      };
      $('[data-done]', p).onclick = () => { if (session) api('import.forget', { session: session.session }).catch(() => {}); session = null; go('sites'); };
    };

    stepConnect();
  },
});
