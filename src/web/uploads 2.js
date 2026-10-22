// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Upload progress belongs to the browser session, not to a particular page render.
let activeFileUpload = false;

// url — адрес приёма: файловый менеджер или импорт дампа базы.
function sendFileUpload(files, url, progress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest(), body = new FormData();
    files.forEach((file) => body.append('file', file, file.name));
    xhr.open('POST', url);
    xhr.setRequestHeader('X-Itrupanel', '1');
    xhr.upload.onprogress = (e) => progress({ stage: 'sending', loaded: e.loaded, total: e.lengthComputable ? e.total : null });
    xhr.upload.onload = () => progress({ stage: 'processing' });
    xhr.onload = () => {
      if (xhr.status === 401) return reject(new Error('Сессия истекла. Войдите в панель заново.'));
      if (xhr.status === 413) return reject(new Error('Размер загрузки превышает ограничение сервера.'));
      let data;
      try { data = JSON.parse(xhr.responseText); } catch { return reject(new Error('Некорректный ответ сервера (HTTP ' + xhr.status + ').')); }
      if (xhr.status < 200 || xhr.status >= 300 || data?.error || data?.ok !== true) {
        return reject(new Error(data?.error || 'Ошибка загрузки (HTTP ' + xhr.status + ').'));
      }
      resolve(data.data);
    };
    xhr.onerror = () => reject(new Error('Соединение с сервером прервано.'));
    xhr.onabort = () => reject(new Error('Загрузка прервана.'));
    xhr.ontimeout = () => reject(new Error('Сервер не ответил вовремя.'));
    xhr.send(body);
  });
}

function uploadRemaining(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return 'рассчитывается…';
  seconds = Math.max(1, Math.ceil(seconds));
  if (seconds < 60) return 'около ' + seconds + ' с';
  if (seconds < 3600) return 'около ' + Math.ceil(seconds / 60) + ' мин';
  return 'около ' + Math.floor(seconds / 3600) + ' ч ' + Math.ceil((seconds % 3600) / 60) + ' мин';
}

async function uploadFiles(selected, path, completed, endpoint = '/api/files/upload') {
  const files = Array.from(selected || []);
  if (!files.length) return;
  if (activeFileUpload) { toast('Дождитесь завершения текущей загрузки.', 'err'); return; }
  activeFileUpload = true;
  $('.file-upload')?.remove();
  const card = document.createElement('section');
  card.className = 'file-upload';
  card.setAttribute('aria-label', 'Загрузка файлов');
  card.innerHTML = `<div class="upload-heading"><b data-title>Загрузка файлов</b><button type="button" class="btn small" data-close hidden aria-label="Закрыть результат загрузки">Закрыть</button></div>
    <div class="upload-name"></div><div class="upload-destination"></div>
    <div class="upload-status" role="status" aria-live="polite"></div>
    <progress max="100" value="0" aria-label="Прогресс передачи файлов"></progress>
    <div class="upload-numbers"><span data-bytes></span><strong data-percent>0%</strong></div>
    <div class="upload-estimate"><span data-speed></span><span data-remaining></span></div>`;
  document.body.append(card);
  $('.upload-name', card).textContent = files.length === 1 ? files[0].name : plural(files.length, 'файл', 'файла', 'файлов');
  $('.upload-destination', card).textContent = 'В папку: ' + path;
  $('[data-close]', card).onclick = () => card.remove();
  const bar = $('progress', card), status = $('.upload-status', card);
  const started = performance.now();
  let state = { stage: 'sending', loaded: 0, total: null }, changed = started;
  const totalSize = files.reduce((sum, file) => sum + file.size, 0);
  const render = () => {
    const now = performance.now(), elapsed = (now - started) / 1000;
    const processing = state.stage === 'processing', stalled = now - changed > 10000;
    const known = state.total !== null && state.total > 0;
    const percent = known ? Math.min(100, Math.floor(state.loaded / state.total * 100)) : null;
    if (processing) bar.value = 100;
    else if (percent !== null) bar.value = percent;
    else bar.removeAttribute('value');
    $('[data-percent]', card).textContent = processing ? '100%' : percent === null ? '—' : percent + '%';
    $('[data-bytes]', card).textContent = known ? fmtBytes(state.loaded) + ' / ' + fmtBytes(state.total) : fmtBytes(state.loaded) + ' передано · файлы: ' + fmtBytes(totalSize);
    const rate = elapsed >= 1 && state.loaded > 0 ? state.loaded / elapsed : 0;
    $('[data-speed]', card).textContent = processing ? '' : rate > 0 && !stalled ? fmtBytes(rate) + '/с' : 'Скорость: рассчитывается…';
    $('[data-remaining]', card).textContent = processing ? 'Ожидаем подтверждение сервера' : 'Осталось: ' + uploadRemaining(known && rate > 0 && !stalled ? (state.total - state.loaded) / rate : NaN);
    status.textContent = processing ? 'Передано. Сервер завершает сохранение…' : stalled ? 'Ожидаем передачу данных. Проверьте соединение…' : state.loaded ? 'Передача файлов на сервер…' : 'Соединение с сервером…';
  };
  const beforeUnload = (e) => { e.preventDefault(); e.returnValue = ''; };
  window.addEventListener('beforeunload', beforeUnload);
  render();
  const timer = setInterval(render, 1000);
  let success = false;
  try {
    await sendFileUpload(files, endpoint + '?path=' + encodeURIComponent(path), (next) => {
      if (next.loaded > state.loaded) changed = performance.now();
      state = { ...state, ...next }; render();
    });
    success = true;
    bar.value = 100;
    $('[data-percent]', card).textContent = '100%';
    $('[data-bytes]', card).textContent = fmtBytes(totalSize) + ' · ' + plural(files.length, 'файл', 'файла', 'файлов');
    $('[data-title]', card).textContent = 'Загрузка завершена';
    status.textContent = 'Файлы сохранены на сервере.';
    card.classList.add('upload-ok');
  } catch (e) {
    $('[data-title]', card).textContent = 'Не удалось загрузить файлы';
    status.textContent = e.message + ' Проверьте папку: часть файлов могла сохраниться.';
    card.classList.add('upload-error');
  } finally {
    clearInterval(timer);
    window.removeEventListener('beforeunload', beforeUnload);
    activeFileUpload = false;
    $('[data-speed]', card).textContent = '';
    $('[data-remaining]', card).textContent = '';
    $('[data-close]', card).hidden = false;
  }
  // Refresh after failures too: a multi-file upload may have saved earlier files.
  try { await completed?.(success); } catch (e) { fail(e); }
}
