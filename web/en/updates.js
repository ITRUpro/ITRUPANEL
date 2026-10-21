// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
function panelVersion(v) { return v || ''; }

const PanelUpdates = { timer: null, data: null, generation: 0, busy: false, modal: null, reloading: false, applyingRun: null };
const updateRunning = run => !!run && ['queued', 'running', 'ready'].includes(run.state);
function updateButtonLabel(d) {
  if (d?.run?.state === 'ready') return "Ready to apply " + d.run.version;
  if (updateRunning(d?.run)) return "Updating…";
  if (d?.run?.state === 'error') return "Update error";
  if (d?.available) return "Update available " + d.release.version;
  if (d?.error) return "Check for updates";
  return "Updates";
}
async function updateRequest(name, body = {}) {
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 20000);
  try {
    const response = await fetch('/api/updates.' + name, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Itrupanel': '1' }, body: JSON.stringify(body), signal: controller.signal });
    if (response.status === 401) { const e = new Error("Sign in again to retrieve the update result."); e.login = true; throw e; }
    const value = await response.json();
    if (!response.ok || value.error) throw new Error(value.error || 'HTTP ' + response.status);
    return value.data;
  } finally { clearTimeout(timeout); }
}
function stopPanelUpdates() {
  clearTimeout(PanelUpdates.timer); PanelUpdates.timer = null; PanelUpdates.generation++; PanelUpdates.data = null;
}
// One offer per sign-in. Page refreshes and background polling do not repeat it.
function resetUpdateOffer() {
  PanelUpdates.offerChecked = false;
  try { sessionStorage.removeItem('itrupanel-update-offer'); } catch (_) {}
}
function offerPanelUpdate(data) {
  if (!App.me || App.me.terms_required || PanelUpdates.offerChecked || data.error) return;
  const key = String(App.me.user || 'admin');
  try { if (sessionStorage.getItem('itrupanel-update-offer') === key) { PanelUpdates.offerChecked = true; return; } } catch (_) {}
  if ($('.modal-wrap,.overlay')) return;
  PanelUpdates.offerChecked = true;
  try { sessionStorage.setItem('itrupanel-update-offer', key); } catch (_) {}
  if (!data.available || !data.release || data.unsupported || updateRunning(data.run)) return;
  const release = data.release;
  const m = modal(`<h3 id="update-offer-title">ITRUPANEL update available</h3>
    <p>New version: <b>${h(release.version)}</b></p>
    <p class="muted">Installed version: ${h(data.current)}</p>
    <ul class="update-notes">${(release.notes || []).map(note => `<li>${h(note)}</li>`).join('')}</ul>
    <p class="hint">You can update now or continue working. The update will remain available using the button in the top-right corner.</p>
    <div class="btns"><button class="btn" data-close>Later</button><button class="btn primary" data-update-now>Update</button></div>`);
  const dialog = $('.modal', m.el);
  dialog.setAttribute('role', 'dialog');
  dialog.setAttribute('aria-modal', 'true');
  dialog.setAttribute('aria-labelledby', 'update-offer-title');
  $('[data-update-now]', m.el).onclick = () => { m.close(); showPanelUpdates({startDownload: true}); };
  $('[data-close]', m.el).focus();
}
function paintUpdateButton() {
  const button = $('[data-panel-updates]'); if (!button) return;
  const d = PanelUpdates.data;
  button.textContent = updateButtonLabel(d);
  button.classList.toggle('has-update', !!d?.available && !updateRunning(d?.run) && d?.run?.state !== 'error' && !d?.error);
  button.classList.toggle('update-error', !!d?.error || d?.run?.state === 'error');
  button.title = d?.available ? "Install ITRUPANEL " + d.release.version : "Panel version and update history";
}
function clearFinishedUpdateMarker(data) {
  if (['done', 'error'].includes(data.run?.state) && PanelUpdates.applyingRun === data.run.id) PanelUpdates.applyingRun = null;
  if (['done', 'error'].includes(data.run?.state) && ls.get('pending-update', null)?.id === data.run.id) ls.set('pending-update', null);
}
function reloadAfterUpdate() {
  const pending = ls.get('pending-update', null);
  if ((!pending?.applyRequested && !PanelUpdates.applyingRun) || pending?.reloadRequested || PanelUpdates.reloading) return false;
  PanelUpdates.reloading = true;
  ls.set('pending-update', { ...pending, reloadRequested: true });
  stopPanelUpdates();
  location.reload();
  return true;
}
function startPanelUpdates() {
  stopPanelUpdates(); const generation = PanelUpdates.generation;
  const button = $('[data-panel-updates]'); if (!button) return;
  button.onclick = () => showPanelUpdates();
  const poll = async () => {
    if (!App.me || generation !== PanelUpdates.generation) return;
    try {
      const data = await updateRequest('info');
      if (generation !== PanelUpdates.generation) return;
      clearFinishedUpdateMarker(data);
      PanelUpdates.data = data; paintUpdateButton();
      offerPanelUpdate(data);
    } catch (e) {
      if (generation !== PanelUpdates.generation) return;
      if (e.login) { if (!reloadAfterUpdate()) stopPanelUpdates(); return; }
      PanelUpdates.data = { ...PanelUpdates.data, error: e.message }; paintUpdateButton();
    }
    if (App.me && generation === PanelUpdates.generation) PanelUpdates.timer = setTimeout(poll, updateRunning(PanelUpdates.data?.run) ? 5000 : 60000);
  };
  poll();
}
function updateOverview(d) {
  const release = d.release;
  return `<div class="update-versions"><span>Installed <b>${h(panelVersion(d.current))}</b></span><span>${release ? "Latest release <b>" + h(panelVersion(release.version)) + '</b>' : "No release information"}</span></div>
    ${d.error ? `<p class="err">Could not check for updates: ${h(d.error)}</p>` : release && !d.available ? "<p>The latest version is installed.</p>" : ''}
    ${d.unsupported ? `<p class="hint">${h(d.unsupported)}</p>` : ''}
    ${release ? `<p class="muted">Released on ${h(release.released)}</p><ul class="update-notes">${release.notes.map(s => `<li>${h(s)}</li>`).join('')}</ul>` : ''}
    <p class="hint">First, the full release with all fixes is downloaded and verified. Once preparation is complete, click “Restart panel and apply”. The current version keeps running until you click. The prepared release is kept for 24 hours. Applying creates a backup of the program and settings; a startup failure triggers rollback. After restart, the page reloads. Sign in again and use the Updates button to view the result.</p>`;
}
async function showPanelUpdates({startDownload = false} = {}) {
  if (PanelUpdates.modal?.isConnected) return;
  const m = modal(`<h3>ITRUPANEL updates</h3><div data-update-summary>Checking release…</div><div data-update-run hidden><p data-update-stage role="status"></p><progress data-update-progress max="100" value="0"></progress><p class="hint" data-update-bytes></p><pre class="out job-log" data-update-log></pre></div><p class="err" data-update-error role="alert"></p><div class="btns"><button class="btn" data-update-check>Check</button><button class="btn primary" data-update-install disabled>Update</button><button class="btn primary" data-update-apply hidden>Restart panel and apply</button><button class="btn" data-update-login hidden>Sign in again</button><button class="btn" data-close>Close</button></div>`, { wide: true });
  PanelUpdates.modal = m.el;
  let data = PanelUpdates.data, checking = false, starting = false, polling = false, applying = false, loginRequired = false, timer = null, observedRunID = null;
  const node = s => $(s, m.el);
  node('[data-update-login]').onclick = () => location.reload();
  const requireLogin = error => {
    loginRequired = true;
    clearTimeout(timer);
    node('[data-update-progress]').hidden = true;
    node('[data-update-bytes]').textContent = '';
    node('[data-update-stage]').textContent = "Your session has ended. Sign in again.";
    node('[data-update-error]').textContent = error.message;
    node('[data-update-login]').hidden = false;
    node('[data-update-install]').disabled = true;
    node('[data-update-apply]').disabled = true;
    reloadAfterUpdate();
  };
  const renderRun = run => {
    // A saved completed run belongs to update history, not the release overview.
    // Restore active work on reopen; retain a failure log only if this dialog
    // observed that operation, so a current error remains diagnosable.
    if (updateRunning(run)) observedRunID = run.id;
    const visible = updateRunning(run) || (run?.state === 'error' && !!run.id && observedRunID === run.id);
    node('[data-update-run]').hidden = !visible;
    if (run) clearFinishedUpdateMarker({run});
    if (!visible) {
      node('[data-update-log]').textContent = '';
      node('[data-update-bytes]').textContent = '';
      node('[data-update-stage]').textContent = '';
      node('[data-update-progress]').hidden = true;
      if (run?.error) node('[data-update-error]').textContent = run.error;
      return;
    }
    node('[data-update-stage]').textContent = run.stage || run.state;
    node('[data-update-log]').textContent = run.log || "Preparing…";
    const progress = node('[data-update-progress]'); progress.hidden = false;
    const downloaded = run.total > 0 && run.downloaded >= run.total;
    // The percentage represents the archive transfer, never installation completion.
    if (downloaded && run.state === 'running') progress.removeAttribute('value');
    else progress.value = run.total ? Math.min(100, Math.round(100 * run.downloaded / run.total)) : 0;
    clearFinishedUpdateMarker({run});
    node('[data-update-bytes]').textContent = run.total ? `Received ${(run.downloaded / 1048576).toFixed(1)} of ${(run.total / 1048576).toFixed(1)} MB` : '';
    if (run.error) node('[data-update-error]').textContent = run.error;
  };
  const render = () => {
    if (!m.el.isConnected || !data || loginRequired) return;
    node('[data-update-summary]').innerHTML = updateOverview(data);
    node('[data-update-install]').hidden = data.run?.state === 'ready' || applying;
    node('[data-update-apply]').hidden = data.run?.state !== 'ready' && !applying;
    node('[data-update-apply]').disabled = applying || data.run?.state !== 'ready';
    node('[data-update-install]').disabled = starting || !data.available || !!data.unsupported || !!data.error || updateRunning(data.run);
    node('[data-update-install]').textContent = data.available ? "Update to " + data.release.version : "Update";
    renderRun(data.run);
    PanelUpdates.data = data; paintUpdateButton();
  };
  const watch = async () => {
    if (polling || loginRequired || !m.el.isConnected || !updateRunning(data?.run)) return;
    clearTimeout(timer); polling = true;
    try {
      const status = await updateRequest('status');
      if (!m.el.isConnected) return;
      data = { ...data, current: status.current, run: status.run };
      if (status.run?.state === 'done') data.available = data.release?.version !== status.current;
      node('[data-update-error]').textContent = ''; node('[data-update-login]').hidden = true;
      if (['done', 'error'].includes(status.run?.state)) applying = false;
      render();
    } catch (e) {
      if (!m.el.isConnected) return;
      if (e.login) { requireLogin(e); return; }
      node('[data-update-progress]').removeAttribute('value');
      node('[data-update-bytes]').textContent = '';
      node('[data-update-stage]').textContent = "Status temporarily unavailable. Checking the result after restart…";
      node('[data-update-error]').textContent = e.login ? e.message : "Connection to the panel was interrupted. Waiting for restart and checking again…";
      node('[data-update-login]').hidden = !e.login;
    } finally { polling = false; }
    if (m.el.isConnected && updateRunning(data?.run)) timer = setTimeout(watch, 500);
  };
  const check = async (force = false) => {
    if (checking || starting) return; checking = true; node('[data-update-check]').disabled = true;
    try { const result = await updateRequest('info', { force }); if (!m.el.isConnected) return; data = result; loginRequired = false; node('[data-update-error]').textContent = ''; render(); watch(); }
    catch (e) { if (m.el.isConnected) { if (e.login) requireLogin(e); else node('[data-update-error]').textContent = e.message; } }
    finally { checking = false; if (m.el.isConnected) node('[data-update-check]').disabled = false; }
  };
  node('[data-update-check]').onclick = () => check(true);
  node('[data-update-install]').onclick = async () => {
    if (starting || checking || !data?.available || data.error || data.unsupported || updateRunning(data.run)) return;
    starting = true; render(); node('[data-update-error]').textContent = '';
    try {
      const run = await updateRequest('start', { version: data.release.version, sha256: data.release.sha256 });
      ls.set('pending-update', {id: run.id, version: run.version});
      data.run = run; PanelUpdates.data = data; paintUpdateButton();
      if (m.el.isConnected) { render(); watch(); }
    } catch (e) { if (m.el.isConnected) { node('[data-update-error]').textContent = e.message + " Check the status before retrying."; starting = false; await check(false); } }
    finally { starting = false; render(); }
  };
  node('[data-update-apply]').onclick = async () => {
    if (applying || data?.run?.state !== 'ready') return;
    applying = true; PanelUpdates.applyingRun = data.run.id; render();
    ls.set('pending-update', {id: data.run.id, version: data.run.version, applyRequested: true});
    node('[data-update-error]').textContent = '';
    try {
      await updateRequest('apply', {id: data.run.id});
      if (m.el.isConnected) {
        node('[data-update-stage]').textContent = "Restarting the panel and applying the update";
        node('[data-update-progress]').removeAttribute('value');
        watch();
      }
    } catch (e) {
      // The request may have reached the worker even if the response was lost.
      // Re-read persisted status before allowing a retry of the same run.
      applying = false;
      if (m.el.isConnected) { node('[data-update-error]').textContent = e.message; await check(false); }
    }
  };
  render(); await check(false);
  if (startDownload && m.el.isConnected && !loginRequired && !node('[data-update-install]').disabled) await node('[data-update-install]').onclick();
}
