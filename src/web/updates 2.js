// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
function panelVersion(v) { return v || ''; }

const PanelUpdates = { timer: null, data: null, generation: 0, busy: false, modal: null, reloading: false, applyingRun: null };
const updateRunning = run => !!run && ['queued', 'running', 'ready'].includes(run.state);
function updateButtonLabel(d) {
  if (d?.run?.state === 'ready') return 'Готово к применению ' + d.run.version;
  if (updateRunning(d?.run)) return 'Обновление…';
  if (d?.run?.state === 'error') return 'Ошибка обновления';
  if (d?.available) return 'Доступно обновление ' + d.release.version;
  if (d?.error) return 'Проверить обновления';
  return 'Обновления';
}
async function updateRequest(name, body = {}) {
  const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 20000);
  try {
    const response = await fetch('/api/updates.' + name, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Itrupanel': '1' }, body: JSON.stringify(body), signal: controller.signal });
    if (response.status === 401) { const e = new Error('Войдите снова, чтобы получить результат обновления.'); e.login = true; throw e; }
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
  const m = modal(`<h3 id="update-offer-title">Доступно обновление ITRUPANEL</h3>
    <p>Новая версия: <b>${h(release.version)}</b></p>
    <p class="muted">Установленная версия: ${h(data.current)}</p>
    <ul class="update-notes">${(release.notes || []).map(note => `<li>${h(note)}</li>`).join('')}</ul>
    <p class="hint">Можно обновить сейчас или продолжить работу. Обновление останется доступно по кнопке в верхнем правом углу.</p>
    <div class="btns"><button class="btn" data-close>Позже</button><button class="btn primary" data-update-now>Обновить</button></div>`);
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
  button.title = d?.available ? 'Установить ITRUPANEL ' + d.release.version : 'Версия панели и журнал обновлений';
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
  return `<div class="update-versions"><span>Установлена <b>${h(panelVersion(d.current))}</b></span><span>${release ? 'Последний выпуск <b>' + h(panelVersion(release.version)) + '</b>' : 'Нет сведений о выпуске'}</span></div>
    ${d.error ? `<p class="err">Не удалось проверить обновления: ${h(d.error)}</p>` : release && !d.available ? '<p>Актуальная версия установлена.</p>' : ''}
    ${d.unsupported ? `<p class="hint">${h(d.unsupported)}</p>` : ''}
    ${release ? `<p class="muted">Выпуск от ${h(release.released)}</p><ul class="update-notes">${release.notes.map(s => `<li>${h(s)}</li>`).join('')}</ul>` : ''}
    <p class="hint">Сначала скачивается и проверяется полная сборка со всеми исправлениями. Когда подготовка завершится, нажмите «Перезапустить панель и применить». До нажатия работает текущая версия. Подготовленная сборка хранится 24 часа. При применении сохраняются программа и настройки; при ошибке запуска выполняется откат. После перезапуска страница обновится. Войдите снова; результат можно посмотреть по кнопке «Обновления».</p>`;
}
async function showPanelUpdates({startDownload = false} = {}) {
  if (PanelUpdates.modal?.isConnected) return;
  const m = modal(`<h3>Обновления ITRUPANEL</h3><div data-update-summary>Проверка выпуска…</div><div data-update-run hidden><p data-update-stage role="status"></p><progress data-update-progress max="100" value="0"></progress><p class="hint" data-update-bytes></p><pre class="out job-log" data-update-log></pre></div><p class="err" data-update-error role="alert"></p><div class="btns"><button class="btn" data-update-check>Проверить</button><button class="btn primary" data-update-install disabled>Обновить</button><button class="btn primary" data-update-apply hidden>Перезапустить панель и применить</button><button class="btn" data-update-login hidden>Войти снова</button><button class="btn" data-close>Закрыть</button></div>`, { wide: true });
  PanelUpdates.modal = m.el;
  let data = PanelUpdates.data, checking = false, starting = false, polling = false, applying = false, loginRequired = false, timer = null, observedRunID = null;
  const node = s => $(s, m.el);
  node('[data-update-login]').onclick = () => location.reload();
  const requireLogin = error => {
    loginRequired = true;
    clearTimeout(timer);
    node('[data-update-progress]').hidden = true;
    node('[data-update-bytes]').textContent = '';
    node('[data-update-stage]').textContent = 'Сеанс завершён. Войдите снова.';
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
    node('[data-update-log]').textContent = run.log || 'Подготовка…';
    const progress = node('[data-update-progress]'); progress.hidden = false;
    const downloaded = run.total > 0 && run.downloaded >= run.total;
    // The percentage represents the archive transfer, never installation completion.
    if (downloaded && run.state === 'running') progress.removeAttribute('value');
    else progress.value = run.total ? Math.min(100, Math.round(100 * run.downloaded / run.total)) : 0;
    clearFinishedUpdateMarker({run});
    node('[data-update-bytes]').textContent = run.total ? `Получено ${(run.downloaded / 1048576).toFixed(1)} из ${(run.total / 1048576).toFixed(1)} МБ` : '';
    if (run.error) node('[data-update-error]').textContent = run.error;
  };
  const render = () => {
    if (!m.el.isConnected || !data || loginRequired) return;
    node('[data-update-summary]').innerHTML = updateOverview(data);
    node('[data-update-install]').hidden = data.run?.state === 'ready' || applying;
    node('[data-update-apply]').hidden = data.run?.state !== 'ready' && !applying;
    node('[data-update-apply]').disabled = applying || data.run?.state !== 'ready';
    node('[data-update-install]').disabled = starting || !data.available || !!data.unsupported || !!data.error || updateRunning(data.run);
    node('[data-update-install]').textContent = data.available ? 'Обновить до ' + data.release.version : 'Обновить';
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
      node('[data-update-stage]').textContent = 'Статус временно недоступен. Проверяем результат после перезапуска…';
      node('[data-update-error]').textContent = e.login ? e.message : 'Связь с панелью прервана. Ожидаем перезапуска и повторяем проверку…';
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
    } catch (e) { if (m.el.isConnected) { node('[data-update-error]').textContent = e.message + ' Проверьте статус перед повторной попыткой.'; starting = false; await check(false); } }
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
        node('[data-update-stage]').textContent = 'Перезапуск панели и применение обновления';
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
