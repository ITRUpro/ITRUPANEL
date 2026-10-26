// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Веб-серверы и PHP: версии, проверка обновлений, обновление и выбор источника пакетов.
const WS_SOURCE = { system: "System packages", stable: 'nginx.org · stable', mainline: 'nginx.org · mainline', ondrej: 'Ondřej Surý', litespeed: 'LiteSpeed', remi: 'Remi' };

async function wsUpdate(r, after) {
  if (!(await confirmBox("Update " + r.title + '?', "Version " + r.new + " will be installed from the current source. The settings are saved to a copy and restored if the update fails. Websites may briefly not respond during the restart.", { ok: "Update", danger: false }))) return;
  try { const j = await api('components.update', { id: r.id }); watchJob(j.job, { onComplete: () => after && after() }); } catch (e) { fail(e); }
}

async function wsChannels(r, after) {
  let d;
  try { d = await api('components.channels', { id: r.id }); } catch (e) { fail(e); return; }
  const m = modal(`<h3>${h(r.title)}: package source</h3><p class="hint">Choose where versions come from. The panel saves the settings, connects the source, installs the version, checks the configuration and restarts the service; if anything fails everything is restored.</p><div data-ch></div><div class="btns"><button class="btn" data-close>Close</button></div>`, { wide: true });
  const box = $('[data-ch]', m.el);
  box.innerHTML = d.channels.map((c, i) => `<div class="card" style="display:flex;gap:12px;align-items:center;justify-content:space-between;margin:8px 0;padding:12px 14px"><div><b>${h(c.title)}</b> ${c.current ? "<span class=\"tag ok\">current</span>" : ''}<br><small class="muted">${c.version ? "Latest version: " + h(c.version) : (c.note ? h(c.note) : "The version could not be determined")}</small></div>${c.current ? '' : `<button class="btn primary" data-i="${i}">Go</button>`}</div>`).join('');
  $$('[data-i]', box).forEach((b) => b.onclick = async () => {
    const c = d.channels[b.dataset.i];
    if (!(await confirmBox("Switch to “" + c.title + '»?', "Version " + r.title + " will change" + (c.version ? " to " + c.version : '') + ". The settings are saved; if anything fails the source and version are restored.", { ok: "Go", danger: false }))) return;
    try { const j = await api('components.switch', { id: r.id, channel: c.id }); m.close && m.close(); watchJob(j.job, { onComplete: () => after && after() }); } catch (e) { fail(e); }
  });
}

page('webservers', { title: "Web servers", async render(view) {
  head(view, "Web servers", { sub: "Versions of nginx, Apache, OpenLiteSpeed and PHP, checking for and installing updates", actions: [{ t: "Check for updates", i: 'refresh', primary: true, fn: async () => { try { const j = await api('components.check'); watchJob(j.job, { onComplete: () => load(true) }); } catch (e) { fail(e); } } }] });
  const p = panel(view);
  const load = async (force) => {
    const d = await api('components.list', { force: !!force });
    table(p, {
      rows: d.rows,
      columns: [
        { t: "Component", r: (r) => `<b style="font-size:16px">${h(r.title)}</b>` },
        { t: "Version", r: (r) => r.version ? h(r.version) : '<span class="muted">—</span>', cls: 'nowrap' },
        { t: "Source", r: (r) => `<span class="tag">${h(WS_SOURCE[r.source] || r.source)}</span>` },
        { t: "Status", r: (r) => r.job === 'running' ? "<span class=\"tag acc\"><span class=\"spin\" style=\"width:12px;height:12px;border-width:2px\"></span> updating</span>" : r.active ? "<span class=\"tag ok\">running</span>" : "<span class=\"tag warn\">stopped</span>" },
        { t: "Update", r: (r) => r.new ? `<span class="tag acc">available ${h(r.new)}</span>` : "<span class=\"muted\">up to date</span>" },
      ],
      actions: (r) => r.job === 'running' ? [] : [
        r.new && { i: 'download', t: "Update", label: "Update", fn: () => wsUpdate(r, () => load(true)) },
        r.switch && { i: 'edit', t: "Package source and version", label: "Source", fn: () => wsChannels(r, () => load(true)) },
      ],
    });
    return d.rows.some((r) => r.job === 'running') || d.checking;
  };
  await load();
  every(5000, async () => { if ($$('.spin', p).length) load(true); }, view);
} });
