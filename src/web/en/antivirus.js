// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Антивирус для сайтов: встроенная проверка и ClamAV, карантин и «Вылечить».
const avWeekdays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const avActions = { restore: ["Restore from backup", 'ok'], clean: ["Remove injection", 'acc'], quarantine: ["Quarantine", 'warn'], manual: ["Check manually", ''] };

function avScheduleText(d) {
  if (d.schedule === 'off') return "Scheduled scan is off";
  const at = String(d.hour).padStart(2, '0') + ':00';
  const day = window.PanelLanguage?.language === 'en' ? avWeekdays[d.weekday] : avWeekdays[d.weekday].toLowerCase();
  return d.schedule === 'daily' ? "Every day at " + at : "Weekly: " + day + ', ' + at;
}

function avSettingsForm(d) {
  openForm({
    title: "Antivirus settings", ok: "Save",
    fields: [
      { name: 'schedule', label: "Scheduled scan of all websites", type: 'seg', value: d.schedule, options: [['daily', "Every day"], ['weekly', "Once a week"], ['off', "Disabled"]] },
      { name: 'weekday', label: "Day of week", type: 'select', value: String(d.weekday), options: avWeekdays.map((n, i) => [String(i), n]), show: (v) => v.schedule === 'weekly' },
      { name: 'hour', label: "Time", type: 'select', value: String(d.hour), options: [...Array(24).keys()].map((x) => [String(x), String(x).padStart(2, '0') + ':00']), show: (v) => v.schedule !== 'off' },
      { name: 'clamav', label: "Also scan with ClamAV when installed", type: 'checkbox', value: d.clamav.enabled, hint: "ClamAV finds known viruses using signature databases. It needs about 1.5 GB of memory while scanning; servers with less than 2 GB use the built-in scan only." },
      { name: 'mirror', label: "ClamAV database mirrors", value: d.clamav.mirror || '', placeholder: 'https://clamav-mirror.ru https://mirror.truenetwork.ru/clamav', hint: "Optional; several can be separated by spaces. The official database server rejects downloads from Russia and some other countries; the panel then switches to verified mirrors automatically: clamav-mirror.ru, mirror.truenetwork.ru and packages.microsoft.com. Databases are signed by ClamAV and the signature is checked on download." },
    ],
    submit: async (v) => { await api('av.settings', { schedule: v.schedule, weekday: Number(v.weekday), hour: Number(v.hour), clamav: !!v.clamav, mirror: v.mirror }); toast("Saved", 'ok'); route(); },
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
  const m = modal(`<div class="backup-settings-head"><h3>Website cleanup · ${h(plan.site)}</h3><button class="btn" data-close>Close</button></div>
    <p class="hint">Nothing has been changed yet. Select files and click Clean up. Before replacement the current version of each file is saved to quarantine and can be restored. Other website files are not changed.${plan.backups ? '' : " <b>There are no backups with this website's files:</b> clean versions cannot be restored."}</p>
    <div class="table-wrap"><table class="grid"><thead><tr><th class="chk"><input type="checkbox" data-all ${fixable.length ? 'checked' : 'disabled'} aria-label="Select all"></th><th>File</th><th>Action</th><th>Details</th></tr></thead><tbody>
    ${items.map((it, i) => `<tr><td class="chk">${it.action === 'manual' ? '' : `<input type="checkbox" data-i="${i}" checked aria-label="${h(it.path)}">`}</td><td><span class="mono">${h(it.path)}</span><br><small class="muted">${h(it.threat)}</small></td><td><span class="tag ${avActions[it.action][1]}">${h(avActions[it.action][0])}</span></td><td>${h(it.detail)}</td></tr>`).join('')}
    </tbody></table></div><div class="btns"><button class="btn" data-close>Cancel</button><button class="btn primary" data-apply ${fixable.length ? '' : 'disabled'}>${icon('shield')}Clean up</button></div>`, { wide: true });
  const boxes = $$('[data-i]', m.el);
  $('[data-all]', m.el).onchange = (e) => boxes.forEach((b) => { b.checked = e.target.checked; });
  $('[data-apply]', m.el).onclick = async () => {
    const paths = boxes.filter((b) => b.checked).map((b) => items[b.dataset.i].path);
    if (!paths.length) { toast("Select files", 'err'); return; }
    m.close();
    try { const r = await api('av.cure.apply', { plan: plan.plan, paths }); watchJob(r.job); } catch (e) { fail(e); }
  };
}

async function avFindings(site, unicode) {
  let d;
  try { d = await api('av.findings', { site }); } catch (e) { fail(e); return; }
  const scan = d.scan;
  const m = modal(`<div class="backup-settings-head"><h3>Scan results · ${h(unicode || site)}</h3><button class="btn" data-close>Close</button></div><div data-body></div>`, { wide: true });
  const body = $('[data-body]', m.el);
  if (!scan) { body.innerHTML = "<p class=\"muted\">The website has not been scanned yet.</p>"; return; }
  const draw = () => {
    body.innerHTML = `<p class="muted">Scanned ${fmtDate(scan.finished)} · files: ${scan.files} · ${["built-in scan", scan.engines.includes('clamav') && 'ClamAV', scan.engines.includes('maldet') && 'Maldet'].filter(Boolean).join(' + ')}${scan.more ? ` · first 500 shown, more: ${scan.more}` : ''}</p>${scan.error ? `<p class="err">${h(scan.error)}</p>` : ''}
      ${scan.findings.length ? `<div class="toolbar" style="margin:6px 0 12px"><button class="btn primary" data-cure>${icon('shield')}Clean up website</button><span class="muted">The cleanup plan is shown before any changes</span></div>` : ''}<div data-table></div>`;
    table($('[data-table]', body), {
      rows: scan.findings,
      empty: { icon: 'shield', title: "No threats found" },
      columns: [
        { t: "File", r: (f) => `<span class="mono">${h(f.path)}</span>${f.line ? `<small class="muted"> · line ${f.line}</small>` : ''}${f.snippet ? `<pre class="av-snippet">${h(f.snippet)}</pre>` : ''}` },
        { t: "Threat", r: (f) => `<span class="tag ${f.severity === 'high' ? 'err' : 'warn'}">${f.severity === 'high' ? "dangerous" : "suspicious"}</span><br>${h(f.threat)}` },
        { t: "Modified", r: (f) => f.modified ? fmtDate(new Date(f.modified * 1000)) : '', cls: 'nowrap hide-sm' },
      ],
      actions: (f) => [
        { i: 'shield', t: "Clean up this file", fn: () => { m.close(); avCure(site, [f.path]); } },
        { i: 'folder', t: "Open the file folder", fn: () => { m.close(); go('files', { path: d.root + '/' + f.path.split('/').slice(0, -1).join('/') }); } },
        { i: 'lock', t: "Quarantine", fn: async () => { if (!(await confirmBox("Move to quarantine?", h(f.path) + " will be removed from the website. You can restore it in the Quarantine section.", { ok: "Quarantine" }))) return; try { await api('av.quarantine', { site, path: f.path, threat: f.threat }); scan.findings = scan.findings.filter((x) => x.path !== f.path); toast("File quarantined", 'ok'); draw(); } catch (e) { fail(e); } } },
        { i: 'check', t: "Safe — skip it", fn: async () => { try { await api('av.ignore', { site, path: f.path, sha256: f.sha256 }); scan.findings = scan.findings.filter((x) => x.path !== f.path); toast("The file will not be shown again unless it changes", 'ok'); draw(); } catch (e) { fail(e); } } },
      ],
    });
    const cure = $('[data-cure]', body);
    if (cure) cure.onclick = () => { m.close(); avCure(site); };
  };
  draw();
}

page('antivirus', {
  title: "Antivirus",
  async render(view) {
    const d = await api('av.status');
    head(view, "Antivirus", { sub: "Scan websites for viruses, web shells and malicious injections", actions: [{ t: "Scan all websites", i: 'search', primary: true, fn: () => avStartScan('*') }, { t: "Settings", i: 'edit', fn: () => avSettingsForm(d) }] });
    let high = 0, medium = 0, scanned = 0;
    d.sites.forEach((s) => { if (s.scan) { scanned++; high += s.scan.high; medium += s.scan.medium + (s.scan.more || 0); } });
    const hero = document.createElement('section'); hero.className = 'sec-hero';
    hero.innerHTML = `<div class="sec-emblem">${icon('bug')}</div><div><div class="sec-eyebrow">ITRUPANEL · ${["BUILT-IN SCAN", d.clamav.usable && d.clamav.enabled && 'CLAMAV', d.maldet.usable && d.maldet.enabled && 'MALDET'].filter(Boolean).join(' + ')}</div><h2>${high ? "Dangerous files found." : "Websites are protected."}<br>${h(avScheduleText(d))}.</h2><p>Looks for web shells, execution of encoded code, injections in PHP, JS and .htaccess, and PHP code inside images.<br>Clean up restores clean file versions from backups or removes the injection.</p></div>`;
    view.append(hero);
    const cards = document.createElement('div'); cards.className = 'sec-metrics'; cards.style.marginTop = '16px';
    cards.innerHTML = [["Websites scanned", scanned + " of " + d.sites.length, 'globe', d.last_run && new Date(d.last_run).getFullYear() > 2000 ? "Last full scan: " + fmtDate(d.last_run) : "No full scan yet"], ["Dangerous", high, 'bug', "Web shells and code execution"], ["Suspicious", medium, 'search', "Needs attention"], ["In quarantine", d.quarantine.length, 'lock', "Files can be restored"]].map(([t, v, i, s]) => `<div class="sec-metric"><div class="sec-metric-top">${h(t)}${icon(i)}</div><strong>${h(v)}</strong><span>${h(s)}</span></div>`).join('');
    view.append(cards);

    const sitesPanel = panel(view, "Sites");
    const picked = new Set();
    const bar = document.createElement('div'); bar.className = 'toolbar'; bar.style.margin = '0 0 12px';
    bar.innerHTML = `<label class="check"><input type="checkbox" data-all-sites aria-label="Select all websites"><span>Select all</span></label><button class="btn" data-scan-picked disabled>${icon('search')}Scan selected</button><span class="muted" data-picked></span>`;
    const sites = document.createElement('div');
    sitesPanel.replaceChildren(bar, sites);
    const syncPicked = () => {
      $('[data-scan-picked]', bar).disabled = !picked.size;
      $('[data-picked]', bar).textContent = picked.size ? "Selected: " + picked.size : '';
      $('[data-all-sites]', bar).checked = picked.size > 0 && picked.size === d.sites.length;
    };
    $('[data-all-sites]', bar).onchange = (e) => { picked.clear(); if (e.target.checked) d.sites.forEach((s) => picked.add(s.domain)); $$('[data-site-pick]', sites).forEach((c) => { c.checked = e.target.checked; }); syncPicked(); };
    $('[data-scan-picked]', bar).onclick = () => avStartScan([...picked]);
    table(sites, {
      rows: d.sites,
      empty: { icon: 'globe', title: "No sites yet" },
      columns: [
        { t: '', r: (s) => `<input type="checkbox" data-site-pick="${h(s.domain)}" aria-label="${h(s.unicode)}">`, cls: 'chk' },
        { t: "Site", r: (s) => `<b>${h(s.unicode)}</b>` },
        { t: "Last scan", r: (s) => s.scan ? fmtDate(s.scan.finished) : "<span class=\"muted\">not scanned</span>", cls: 'nowrap' },
        { t: "Files", r: (s) => s.scan ? h(s.scan.files) : '', cls: 'hide-sm' },
        { t: "Result", r: (s) => !s.scan ? '' : s.scan.high + s.scan.medium + (s.scan.more || 0) === 0 ? "<span class=\"tag ok\">clean</span>" : (s.scan.high ? `<span class="tag err">dangerous: ${s.scan.high}</span> ` : '') + (s.scan.medium + (s.scan.more || 0) ? `<span class="tag warn">suspicious: ${s.scan.medium + (s.scan.more || 0)}</span>` : '') },
      ],
      onRow: (s) => avFindings(s.domain, s.unicode),
      actions: (s) => [
        { i: 'search', t: "Check", fn: () => avStartScan(s.domain) },
        s.scan && { i: 'file', t: "Results", fn: () => avFindings(s.domain, s.unicode) },
        s.scan && s.scan.high + s.scan.medium > 0 && { i: 'shield', t: "Clean up", label: "Clean up", fn: () => avCure(s.domain) },
      ],
    });
    $$('[data-site-pick]', sites).forEach((c) => (c.onchange = () => { if (c.checked) picked.add(c.dataset.sitePick); else picked.delete(c.dataset.sitePick); syncPicked(); }));

    const rp = panel(view, "Antivirus rules");
    const ru = d.rules;
    rp.innerHTML = `<p>${ru.version ? `Signed rule pack: version ${h(ru.version)} from ${h(ru.released)}, rules: ${h(ru.pack)}.` : "The additional rule pack has not been downloaded yet; built-in rules are in use."} Built-in rules: ${h(ru.builtin)}.</p>
      <p class="muted">${ru.checked ? `Last update check: ${fmtDate(ru.checked)}.` : "Updates have not been checked yet."} Source: ${h(ru.source)} (fallback: itrupanel.com). The pack is signed with the ITRUPANEL release key; the panel accepts only signed and newer packs.</p>${ru.error ? `<p class="err">The last update failed: ${h(ru.error)}</p>` : ''}
      <div class="toolbar"><button class="btn" data-rules-update>${icon('refresh')}Update rules</button><label class="check"><input type="checkbox" data-rules-auto ${ru.auto ? 'checked' : ''}><span>Update rules automatically once a day</span></label></div>
      <p class="hint">Rules are updated separately from the panel, without restarting it. Database updates, rule updates and scans run one after another, not at the same time, so a weak server is not overloaded.</p>`;
    $('[data-rules-update]', rp).onclick = async () => { try { const r = await api('av.rules.update'); watchJob(r.job); } catch (e) { fail(e); } };
    $('[data-rules-auto]', rp).onchange = async (e) => { try { await api('av.rules.settings', { auto: e.target.checked }); toast("Saved", 'ok'); } catch (er) { fail(er); e.target.checked = !e.target.checked; } };

    const clam = panel(view, 'ClamAV');
    const c = d.clamav;
    clam.innerHTML = `<p>${c.installed ? (c.db_date ? `Installed. Signature databases from ${fmtDate(c.db_date)}.` : "Installed, databases not downloaded yet.") : "Not installed. The built-in scan works without it."}${c.installed && !c.enabled ? " Currently disabled in settings." : ''}${c.installed && c.db_date && !c.usable ? ` <span class="muted">${h(c.reason)}.</span>` : ''}</p>
      ${c.installed ? `<p class="muted">Databases are updated automatically once a day${c.db_checked ? `: last check ${fmtDate(c.db_checked)}, next after ${fmtDate(c.db_next)}` : ''}.</p>` : ''}${c.installed && c.db_error ? `<p class="err">The last update failed: ${h(c.db_error)}</p>` : ''}
      <div class="toolbar">${c.installed ? `<button class="btn" data-update>${icon('refresh')}Update databases</button>` : `<button class="btn primary" data-install>${icon('download')}Install ClamAV</button>`}</div>
      <p class="hint">ClamAV finds known viruses using signature databases (about 120 MB, updated automatically). If the official database server is unavailable from the server's country, the panel switches to mirrors automatically.${c.mirror ? " Currently used: " + h(c.mirror) + '.' : ''}</p>`;
    const install = $('[data-install]', clam), update = $('[data-update]', clam);
    if (install) install.onclick = async () => { if (!(await confirmBox("Install ClamAV?", "The clamav packages will be installed and signature databases downloaded (about 120 MB). ClamAV scanning needs at least 2 GB of memory.", { ok: "Install", danger: false }))) return; try { const r = await api('av.install'); watchJob(r.job); } catch (e) { fail(e); } };
    if (update) update.onclick = async () => { try { const r = await api('av.update'); watchJob(r.job); } catch (e) { fail(e); } };

    const md = panel(view, 'Maldet');
    const m = d.maldet;
    if (!m.supported) {
      md.innerHTML = "<p class=\"muted\">Maldet (Linux Malware Detect) works on Linux only. On FreeBSD the built-in scan and ClamAV are available.</p>";
    } else {
      md.innerHTML = `<p>${m.installed ? `Installed${m.version ? ": version " + h(m.version) : ''}.${m.sigs_date ? ` Signatures from ${fmtDate(m.sigs_date)}.` : ''}${m.enabled ? '' : " Currently disabled in settings."}${m.usable ? '' : ` <span class="err">${h(m.reason)}.</span>`}` : "Not installed. The built-in scan and ClamAV work without it."}</p>
        ${m.installed ? `<p class="muted">Signatures are updated automatically every 4 hours${m.checked ? `: last attempt ${fmtDate(m.checked)}, next after ${fmtDate(m.next)}` : ''}.</p>` : ''}${m.installed && m.error ? `<p class="err">The last update failed: ${h(m.error)}</p>` : ''}
        <div class="toolbar">${m.installed ? `<button class="btn" data-maldet-sigs>${icon('refresh')}Update signatures</button><button class="btn" data-maldet-install>${icon('download')}Update Maldet</button><label class="check"><input type="checkbox" data-maldet-on ${m.enabled ? 'checked' : ''}><span>Also scan with Maldet</span></label><button class="btn" data-maldet-remove>${icon('trash')}Delete</button>` : `<button class="btn primary" data-maldet-install>${icon('download')}Install Maldet</button>`}</div>
        <p class="hint">Maldet (Linux Malware Detect, R-fx Networks) is a scanner for web shells and malicious scripts on hosting servers; its signature set has about 45 thousand entries. The panel downloads a release from GitHub, turns off its file monitoring and its own cron jobs, and runs the scan itself, one after another, after the built-in scan and ClamAV. Maldet never changes files: quarantine and cure are handled by the panel. perl is required; signatures are downloaded from cdn.rfxn.com.</p>`;
      const mi = $('[data-maldet-install]', md), ms = $('[data-maldet-sigs]', md), mo = $('[data-maldet-on]', md), mr = $('[data-maldet-remove]', md);
      mi.onclick = async () => {
        if (!(await confirmBox(m.installed ? "Update Maldet?" : "Install Maldet?", m.installed ? "The latest release will be downloaded and installed; settings are kept." : "A release will be downloaded from GitHub (about 1 MB) and the signatures loaded. perl is required: the panel installs it if missing.", { ok: m.installed ? "Update" : "Install", danger: false }))) return;
        try { const r = await api('av.maldet.install'); watchJob(r.job); } catch (e) { fail(e); }
      };
      if (ms) ms.onclick = async () => { try { const r = await api('av.maldet.sigs'); watchJob(r.job); } catch (e) { fail(e); } };
      if (mo) mo.onchange = async (e) => { try { await api('av.maldet.settings', { enabled: e.target.checked }); toast("Saved", 'ok'); } catch (er) { fail(er); e.target.checked = !e.target.checked; } };
      if (mr) mr.onclick = async () => { if (!(await confirmBox("Remove Maldet?", "The program and its signatures will be removed from the server. You can install it again at any time."))) return; try { await api('av.maldet.remove'); route(); } catch (e) { fail(e); } };
    }

    const q = panel(view, "Quarantine");
    table(q, {
      rows: d.quarantine,
      empty: { icon: 'lock', title: "Quarantine is empty" },
      columns: [
        { t: "File", r: (x) => `<span class="mono">${h(x.path)}</span><br><small class="muted">${h(x.site)}</small>` },
        { t: "Reason", r: (x) => (x.reason === 'cure' ? "<span class=\"tag\">copy before cleanup</span><br>" : '') + h(x.threat || '') },
        { t: "Size", r: (x) => fmtBytes(x.size), cls: 'nowrap hide-sm' },
        { t: "Date", r: (x) => fmtDate(x.time), cls: 'nowrap' },
      ],
      actions: (x) => [
        { i: 'refresh', t: "Restore to website", fn: async () => { if (!(await confirmBox("Restore the file?", h(x.path) + " will return to its original location" + (x.reason === 'cure' ? " and replace the cleaned version" : '') + '.', { ok: "Restore", danger: false }))) return; try { await api('av.restore', { id: x.id }); toast("File restored", 'ok'); route(); } catch (e) { fail(e); } } },
        { i: 'trash', t: "Delete permanently", danger: true, fn: async () => { if (!(await confirmBox("Delete the file permanently?", h(x.path) + " will be deleted from quarantine and cannot be restored."))) return; try { await api('av.delete', { id: x.id }); route(); } catch (e) { fail(e); } } },
      ],
    });
    if (d.running) every(5000, () => { api('av.status').then((s) => { if (!s.running && view.isConnected) route(); }); }, view);
  },
});
