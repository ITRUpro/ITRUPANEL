// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

function watchJob(id, { onComplete } = {}) {
  const m = modal(`<h3>Task execution</h3><p data-stage role="status">Connecting…</p><small class="muted" data-time></small><pre class="out job-log" data-log></pre><p data-result></p><div class="btns"><button class="btn danger" data-stop hidden>Stop job</button><button class="btn" data-close>Close window</button></div><p class="hint">The task continues on the server after closing this window. The result remains in Tasks.</p>`, { wide: true });
  const poll = async () => {
    if (!m.el.isConnected) return;
    let again = true;
    try {
      const j = await api('jobs.get', { id }, { timeout: 15000 });
      if (!m.el.isConnected) return;
      $('h3', m.el).textContent = j.title;
      $('[data-stage]', m.el).textContent = j.stage;
      $('[data-time]', m.el).textContent = "Elapsed: " + Math.max(0, Math.round((new Date(j.status === 'running' ? Date.now() : j.updated) - new Date(j.started)) / 1000)) + " s" + ' · ' + ({ running: "running", done: "completed", error: "error" }[j.status] || j.status);
      const log = $('[data-log]', m.el), atEnd = log.scrollHeight - log.scrollTop - log.clientHeight < 50;
      log.textContent = j.log || "Waiting for service output…";
      if (atEnd) log.scrollTop = log.scrollHeight;
      again = j.status === 'running';
      const stop = $('[data-stop]', m.el);
      stop.hidden = !(again && j.cancelable);
      stop.onclick = async () => { if (!(await confirmBox("Stop the job?", h(j.title) + " will be interrupted. Changes already made are kept.", { ok: "Stop" }))) return; try { await api('jobs.cancel', { id }); stop.disabled = true; } catch (e) { fail(e); } };
      const result = $('[data-result]', m.el);
      result.className = j.status === 'error' ? 'err' : '';
      result.textContent = j.error || (j.result && j.result.info) || '';
      if (!again) {
        if (onComplete) { try { await onComplete(j); } catch(e) { fail(e); } }
        if (j.result && j.result.url && /^https:\/\//.test(j.result.url)) {
          const a = document.createElement('a'); a.href = j.result.url; a.textContent = "Open " + j.result.url; a.target = '_blank'; a.rel = 'noopener'; result.append(document.createElement('br'), a);
        }
        const close = $('[data-close]', m.el);
        close.textContent = "Close and refresh";
        close.onclick = () => { m.close(); if(!onComplete)route(); };
      }
    } catch (e) {
      if (m.el.isConnected) $('[data-stage]', m.el).textContent = "Could not get status: " + e.message + ". The task may still be running on the server; retrying the request.";
    }
    if (again && m.el.isConnected) setTimeout(poll, 1500);
  };
  poll();
}

page('jobs', {
  title: "Tasks",
  async render(view) {
    head(view, "Tasks", { actions: [{ t: "Update", i: 'refresh', fn: route }] });
    const p = panel(view);
    let busy = false;
    const load = async () => {
      if (busy || !view.isConnected) return; busy = true;
      try {
        const rows = await api('jobs.list', {}, { timeout: 15000 });
        if (!view.isConnected) return;
        table(p, { rows, empty: { icon: 'clock', title: "No tasks yet" }, columns: [
          { t: "Task", r: r => h(r.title) },
          { t: "Stage", r: r => h(r.stage) },
          { t: "Status", r: r => `<span class="tag ${r.status === 'done' ? 'ok' : r.status === 'error' ? 'err' : 'acc'}">${h({ running: "Running", done: "Done", error: "Error" }[r.status])}</span>` },
          { t: "Start", r: r => fmtDate(r.started) },
        ], onRow: r => watchJob(r.id), actions: r => [r.status === 'running' && r.cancelable && { t: "Stop job", i: 'stop', danger: true, fn: async () => { if (!(await confirmBox("Stop the job?", h(r.title) + " will be interrupted. Changes already made are kept.", { ok: "Stop" }))) return; try { await api('jobs.cancel', { id: r.id }); toast("The job is stopping", 'ok'); load(); } catch (e) { fail(e); } } }, { t: "Log and result", i: 'info', fn: () => watchJob(r.id) }] });
      } finally { busy = false; }
    };
    await load(); every(2500, () => load().catch(fail), view);
  },
});

async function panelSettingsForm() {
  const d = await api('panel.settings');
  openForm({ title: "Panel address and certificate", fields: [
    { name: 'domain', label: "Panel domain", value: d.domain || '', placeholder: 'panel.example.ru', required: true, hint: "Create an A record for this hostname pointing to the server’s public IP. The panel will continue to use port 3333." },
    { name: 'mode', label: "Certificate", type: 'select', value: d.certificate ? 'existing' : 'le', options: [['le', "Issue Let’s Encrypt certificate"], ['existing', "Choose from SSL certificates"], ['self', "Self-signed (browser warning)"]] },
    { name: 'certificate', label: "Installed certificate", type: 'select', options: [['', "Select certificate"], ...d.certs.map(c => [c.name, c.name])], value: d.certificate || '', show: v => v.mode === 'existing', required: true },
    { name: 'email', label: "Let’s Encrypt email", value: '', show: v => v.mode === 'le', hint: "Issuing requires internet access to the server on port 80. The process runs as a background task." },
    { name: 'proxy', label: "Open the panel without a port", type: 'checkbox', value: !!d.proxy, show: v => d.proxy_supported && v.mode !== 'self', hint: `The address will be https://panel-domain/ without :${h(d.port)}. nginx accepts requests on port 443 and passes them to the panel. Port ${h(d.port)} keeps working unless you close it below.` },
    { name: 'close_direct', label: `Close port ${d.port} to internet access`, type: 'checkbox', value: !!d.close_direct, show: v => d.proxy_supported && v.mode !== 'self' && v.proxy, hint: `The panel will open only by domain. The port stays reachable from the server itself: if the domain address stops working, connect through an SSH tunnel <span class="mono">ssh -L ${h(d.port)}:127.0.0.1:${h(d.port)} root@server</span> and open https://localhost:${h(d.port)}.` },
    { type: 'html', html: `<p class="muted">Add your certificate and key in SSL certificates first. After applying, open the new address manually; this tab will not redirect.</p>${d.domain ? `<a href="${h(d.url)}" target="_blank" rel="noopener">${h(d.url)}</a>` : ''}` },
  ], submit: async v => {
    const proxy = !!v.proxy && d.proxy_supported && v.mode !== 'self';
    const close = proxy && !!v.close_direct;
    if (close && !d.close_direct && !await confirmBox("Close port " + d.port + '?', `30 seconds after applying, the panel stops opening at the address with port ${h(d.port)}. Only https://${h(v.domain)}/ will work — make sure the domain points to this server.`, { ok: "Close port" })) return false;
    const r = await api('panel.configure', { ...v, proxy, close_direct: close }); watchJob(r.job);
  } });
}
