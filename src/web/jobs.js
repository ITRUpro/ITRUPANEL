// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

function watchJob(id, { onComplete } = {}) {
  const m = modal(`<h3>Выполнение задачи</h3><p data-stage role="status">Подключение…</p><small class="muted" data-time></small><pre class="out job-log" data-log></pre><p data-result></p><div class="btns"><button class="btn danger" data-stop hidden>Остановить задачу</button><button class="btn" data-close>Закрыть окно</button></div><p class="hint">После закрытия задача продолжится на сервере. Результат останется в разделе «Задачи».</p>`, { wide: true });
  const poll = async () => {
    if (!m.el.isConnected) return;
    let again = true;
    try {
      const j = await api('jobs.get', { id }, { timeout: 15000 });
      if (!m.el.isConnected) return;
      $('h3', m.el).textContent = j.title;
      $('[data-stage]', m.el).textContent = j.stage;
      $('[data-time]', m.el).textContent = 'Прошло: ' + Math.max(0, Math.round((new Date(j.status === 'running' ? Date.now() : j.updated) - new Date(j.started)) / 1000)) + ' с' + ' · ' + ({ running: 'выполняется', done: 'завершено', error: 'ошибка' }[j.status] || j.status);
      const log = $('[data-log]', m.el), atEnd = log.scrollHeight - log.scrollTop - log.clientHeight < 50;
      log.textContent = j.log || 'Ожидание вывода службы…';
      if (atEnd) log.scrollTop = log.scrollHeight;
      again = j.status === 'running';
      const stop = $('[data-stop]', m.el);
      stop.hidden = !(again && j.cancelable);
      stop.onclick = async () => { if (!(await confirmBox('Остановить задачу?', h(j.title) + ' будет прервана. Уже сделанные изменения сохранятся.', { ok: 'Остановить' }))) return; try { await api('jobs.cancel', { id }); stop.disabled = true; } catch (e) { fail(e); } };
      const result = $('[data-result]', m.el);
      result.className = j.status === 'error' ? 'err' : '';
      result.textContent = j.error || (j.result && j.result.info) || '';
      if (!again) {
        if (onComplete) { try { await onComplete(j); } catch(e) { fail(e); } }
        if (j.result && j.result.url && /^https:\/\//.test(j.result.url)) {
          const a = document.createElement('a'); a.href = j.result.url; a.textContent = 'Открыть ' + j.result.url; a.target = '_blank'; a.rel = 'noopener'; result.append(document.createElement('br'), a);
        }
        const close = $('[data-close]', m.el);
        close.textContent = 'Закрыть и обновить';
        close.onclick = () => { m.close(); if(!onComplete)route(); };
      }
    } catch (e) {
      if (m.el.isConnected) $('[data-stage]', m.el).textContent = 'Не удалось получить статус: ' + e.message + '. Задача может продолжаться на сервере; повторяем запрос.';
    }
    if (again && m.el.isConnected) setTimeout(poll, 1500);
  };
  poll();
}

// waitJobDownload ждёт задачу подготовки файла без окна задач: в углу показывается полоса хода с процентами и
// оценкой оставшегося времени, а скачивание начинается само, когда файл готов.
function waitJobDownload(id) {
  const box = document.createElement('div');
  box.className = 'dl-progress';
  box.innerHTML = `<div class="dl-head"><b>Готовим файл для скачивания</b><button class="btn small" data-stop>Остановить</button></div><div class="dl-bar"><i></i></div><div class="dl-text muted"></div>`;
  document.body.append(box);
  const bar = $('i', box), text = $('.dl-text', box), stop = $('[data-stop]', box);
  stop.onclick = async () => { stop.disabled = true; try { await api('jobs.cancel', { id }); } catch (e) { fail(e); stop.disabled = false; } };
  const started = Date.now();
  const left = (sec) => (sec < 60 ? 'меньше минуты' : sec < 3600 ? 'около ' + Math.ceil(sec / 60) + ' мин' : 'около ' + Math.round(sec / 360) / 10 + ' ч');
  const done = () => box.remove();
  const poll = async () => {
    let again = true;
    try {
      const j = await api('jobs.get', { id }, { timeout: 15000 });
      again = j.status === 'running';
      if (again) {
        const pct = j.percent || 0, spent = (Date.now() - started) / 1000;
        bar.style.width = (pct || 3) + '%';
        bar.parentElement.classList.toggle('busy', !pct);
        let t = h(j.stage || 'Подготовка…');
        if (pct) t += ` · ${pct}%`;
        if (pct >= 3 && spent >= 5) t += ` · осталось ${left(spent / pct * (100 - pct))}`;
        else if (spent >= 60) t += ' · копия большая, подготовка может занять несколько минут';
        text.innerHTML = t;
      } else {
        done();
        if (j.status === 'done' && j.result && j.result.download) { location.href = j.result.download; return; }
        fail(new Error(j.error || 'Не удалось подготовить файл'));
        return;
      }
    } catch (e) { /* сеть моргнула: повторяем запрос */ }
    if (again && Date.now() - started < 6 * 3600 * 1000) setTimeout(poll, 1000); else if (again) done();
  };
  poll();
}

page('jobs', {
  title: 'Задачи',
  async render(view) {
    head(view, 'Задачи', { actions: [{ t: 'Обновить', i: 'refresh', fn: route }] });
    const p = panel(view);
    let busy = false;
    const load = async () => {
      if (busy || !view.isConnected) return; busy = true;
      try {
        const rows = await api('jobs.list', {}, { timeout: 15000 });
        if (!view.isConnected) return;
        table(p, { rows, empty: { icon: 'clock', title: 'Задач пока нет' }, columns: [
          { t: 'Задача', r: r => h(r.title) },
          { t: 'Этап', r: r => h(r.stage) },
          { t: 'Состояние', r: r => `<span class="tag ${r.status === 'done' ? 'ok' : r.status === 'error' ? 'err' : 'acc'}">${h({ running: 'Выполняется', done: 'Готово', error: 'Ошибка' }[r.status])}</span>` },
          { t: 'Начало', r: r => fmtDate(r.started) },
        ], onRow: r => watchJob(r.id), actions: r => [r.status === 'running' && r.cancelable && { t: 'Остановить задачу', i: 'stop', danger: true, fn: async () => { if (!(await confirmBox('Остановить задачу?', h(r.title) + ' будет прервана. Уже сделанные изменения сохранятся.', { ok: 'Остановить' }))) return; try { await api('jobs.cancel', { id: r.id }); toast('Задача останавливается', 'ok'); load(); } catch (e) { fail(e); } } }, { t: 'Журнал и результат', i: 'info', fn: () => watchJob(r.id) }] });
      } finally { busy = false; }
    };
    await load(); every(2500, () => load().catch(fail), view);
  },
});

async function panelSettingsForm() {
  const d = await api('panel.settings');
  openForm({ title: 'Адрес и сертификат панели', fields: [
    { name: 'domain', label: 'Домен панели', value: d.domain || '', placeholder: 'panel.example.ru', required: true, hint: 'Создайте для этого имени DNS-запись A на внешний адрес сервера. Панель продолжит работать на порту 3333.' },
    { name: 'mode', label: 'Сертификат', type: 'select', value: d.certificate ? 'existing' : 'le', options: [['le', 'Выпустить Let’s Encrypt'], ['existing', 'Выбрать из SSL-сертификатов'], ['self', 'Самоподписанный (браузер предупредит)']] },
    { name: 'certificate', label: 'Установленный сертификат', type: 'select', options: [['', 'Выберите сертификат'], ...d.certs.map(c => [c.name, c.name])], value: d.certificate || '', show: v => v.mode === 'existing', required: true },
    { name: 'email', label: 'E-mail для Let’s Encrypt', value: '', show: v => v.mode === 'le', hint: 'Для выпуска нужен доступ к серверу по порту 80 из интернета. Выпуск выполняется в фоновой задаче.' },
    { name: 'proxy', label: 'Открывать панель без порта', type: 'checkbox', value: !!d.proxy, show: v => d.proxy_supported && v.mode !== 'self', hint: `Адрес будет https://домен-панели/ без :${h(d.port)}. Запросы принимает nginx на порту 443 и передаёт панели. Порт ${h(d.port)} продолжит работать, если не закрыть его ниже.` },
    { name: 'close_direct', label: `Закрыть порт ${d.port} для доступа из интернета`, type: 'checkbox', value: !!d.close_direct, show: v => d.proxy_supported && v.mode !== 'self' && v.proxy, hint: `Панель будет открываться только по домену. С самого сервера порт останется доступен: если адрес по домену перестанет работать, подключитесь через SSH-туннель <span class="mono">ssh -L ${h(d.port)}:127.0.0.1:${h(d.port)} root@сервер</span> и откройте https://localhost:${h(d.port)}.` },
    { type: 'html', html: `<p class="muted">Свой сертификат и ключ сначала добавьте в разделе «SSL-сертификаты». После применения откройте новый адрес вручную; текущая вкладка не перенаправляется.</p>${d.domain ? `<a href="${h(d.url)}" target="_blank" rel="noopener">${h(d.url)}</a>` : ''}` },
  ], submit: async v => {
    const proxy = !!v.proxy && d.proxy_supported && v.mode !== 'self';
    const close = proxy && !!v.close_direct;
    if (close && !d.close_direct && !await confirmBox('Закрыть порт ' + d.port + '?', `Через 30 секунд после применения панель перестанет открываться по адресу с портом ${h(d.port)}. Работать будет только https://${h(v.domain)}/ — проверьте, что домен указывает на этот сервер.`, { ok: 'Закрыть порт' })) return false;
    const r = await api('panel.configure', { ...v, proxy, close_direct: close }); watchJob(r.job);
  } });
}
