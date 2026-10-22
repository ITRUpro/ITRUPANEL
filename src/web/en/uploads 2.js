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
      if (xhr.status === 401) return reject(new Error("Session expired. Sign in to the panel again."));
      if (xhr.status === 413) return reject(new Error("Upload exceeds the server size limit."));
      let data;
      try { data = JSON.parse(xhr.responseText); } catch { return reject(new Error("Invalid server response (HTTP " + xhr.status + ').')); }
      if (xhr.status < 200 || xhr.status >= 300 || data?.error || data?.ok !== true) {
        return reject(new Error(data?.error || "Upload error (HTTP " + xhr.status + ').'));
      }
      resolve(data.data);
    };
    xhr.onerror = () => reject(new Error("Server connection interrupted."));
    xhr.onabort = () => reject(new Error("Upload interrupted."));
    xhr.ontimeout = () => reject(new Error("Server response timed out."));
    xhr.send(body);
  });
}

function uploadRemaining(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return "calculating…";
  seconds = Math.max(1, Math.ceil(seconds));
  if (seconds < 60) return "about " + seconds + " s";
  if (seconds < 3600) return "about " + Math.ceil(seconds / 60) + " min";
  return "about " + Math.floor(seconds / 3600) + " h " + Math.ceil((seconds % 3600) / 60) + " min";
}

async function uploadFiles(selected, path, completed, endpoint = '/api/files/upload') {
  const files = Array.from(selected || []);
  if (!files.length) return;
  if (activeFileUpload) { toast("Wait for the current upload to finish.", 'err'); return; }
  activeFileUpload = true;
  $('.file-upload')?.remove();
  const card = document.createElement('section');
  card.className = 'file-upload';
  card.setAttribute('aria-label', "File upload");
  card.innerHTML = `<div class="upload-heading"><b data-title>File upload</b><button type="button" class="btn small" data-close hidden aria-label="Dismiss upload result">Close</button></div>
    <div class="upload-name"></div><div class="upload-destination"></div>
    <div class="upload-status" role="status" aria-live="polite"></div>
    <progress max="100" value="0" aria-label="File transfer progress"></progress>
    <div class="upload-numbers"><span data-bytes></span><strong data-percent>0%</strong></div>
    <div class="upload-estimate"><span data-speed></span><span data-remaining></span></div>`;
  document.body.append(card);
  $('.upload-name', card).textContent = files.length === 1 ? files[0].name : plural(files.length, "file", "files", "files");
  $('.upload-destination', card).textContent = "Destination folder: " + path;
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
    $('[data-bytes]', card).textContent = known ? fmtBytes(state.loaded) + ' / ' + fmtBytes(state.total) : fmtBytes(state.loaded) + " transferred · files: " + fmtBytes(totalSize);
    const rate = elapsed >= 1 && state.loaded > 0 ? state.loaded / elapsed : 0;
    $('[data-speed]', card).textContent = processing ? '' : rate > 0 && !stalled ? fmtBytes(rate) + "/s" : "Calculating speed…";
    $('[data-remaining]', card).textContent = processing ? "Waiting for server confirmation" : "Remaining: " + uploadRemaining(known && rate > 0 && !stalled ? (state.total - state.loaded) / rate : NaN);
    status.textContent = processing ? "Transferred. Server is finishing the save…" : stalled ? "Waiting for data transfer. Check your connection…" : state.loaded ? "Uploading files to server…" : "Connecting to server…";
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
    $('[data-bytes]', card).textContent = fmtBytes(totalSize) + ' · ' + plural(files.length, "file", "files", "files");
    $('[data-title]', card).textContent = "Upload complete";
    status.textContent = "Files saved on the server.";
    card.classList.add('upload-ok');
  } catch (e) {
    $('[data-title]', card).textContent = "Could not upload files";
    status.textContent = e.message + " Check the folder: some files may have been saved.";
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
