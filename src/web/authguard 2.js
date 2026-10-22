// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
page('authguard', { title: 'Защита входа', async render(view) {
  const d = await api('authguard.status');
  head(view, 'Защита входа', { sub: 'Защита панели и FTP от перебора паролей', actions: [{ t: 'Обновить', i: 'refresh', fn: route }] });
  const intro = document.createElement('section'); intro.className = 'sec-hero';
  intro.innerHTML = `<div class="sec-emblem">${icon('lock')}</div><div><div class="sec-eyebrow">ITRUPANEL · PURE-FTPD</div><h2>Пять ошибок.<br>Пятнадцать минут блокировки.</h2><p>Повторные ошибки входа за 10 минут приводят к временной блокировке IP.<br>Уже открытая сессия панели продолжит работать.</p></div>`;
  view.append(intro);
  const context = document.createElement('div'); context.className = 'sec-context';
  context.textContent = d.demo ? 'Демо-режим: защита входа в панель работает локально. FTP-команды не выполняются.' : 'IP определяется по соединению с сервером. За общим прокси пользователи могут иметь один адрес.';
  view.append(context);
  const cards = document.createElement('div');cards.className = 'sec-metrics';
  cards.innerHTML = `<div class="sec-metric"><div class="sec-metric-top">Панель${icon('shield')}</div><strong>${d.warning ? 'Ошибка' : 'Включена'}</strong><span>Блокировки сохраняются после перезапуска</span></div><div class="sec-metric"><div class="sec-metric-top">FTP${icon('lock')}</div><strong>${d.ftp.active ? (d.demo ? 'Демо' : 'Включена') : 'Не включена'}</strong><span>Fail2ban · TCP ${h(d.ftp.port)}</span></div><div class="sec-metric"><div class="sec-metric-top">Заблокировано${icon('globe')}</div><strong>${d.bans.length + d.ftp.bans.length}</strong><span>IP в панели и FTP</span></div>`;
  view.append(cards);
  if (d.warning) {const warning = document.createElement('p');warning.className = 'err';warning.textContent = d.warning;view.append(warning);}
  const login = await api('security.login.get').catch(() => null);
  if (login) {
    login.ips.list = login.ips.list || [];
    const two = panel(view, 'Двухэтапная аутентификация');two.replaceChildren();
    const twoNote = document.createElement('p');twoNote.className = 'hint';
    twoNote.textContent = login.totp.enabled ? 'Включена. При входе нужен пароль и одноразовый код из приложения-аутентификатора. Осталось резервных кодов: ' + login.totp.recovery_left + '.' : 'Выключена. После включения при входе потребуется ещё одноразовый код из приложения Google Authenticator, Яндекс Ключ, Microsoft Authenticator, Aegis и подобных.';
    two.append(twoNote);
    const recovery = (codes) => {
      const m = modal(`<div class="backup-settings-head"><h3>Резервные коды</h3></div><p class="hint">Сохраните эти коды в надёжном месте. Каждый подходит один раз вместо кода из приложения, если телефон потерян. Больше они показаны не будут.</p><pre class="mono" style="user-select:all;font-size:15px;line-height:1.7">${codes.map(h).join('\n')}</pre><div style="display:flex;gap:10px;margin-top:14px"><button class="btn" data-copy>Скопировать</button><button class="btn primary" data-close>Я сохранил коды</button></div>`);
      $('[data-copy]', m.el).onclick = () => { navigator.clipboard && navigator.clipboard.writeText(codes.join('\n')).then(() => toast('Скопировано', 'ok')).catch(() => {}); };
    };
    const ask = (title, fn, extra) => openForm({ title, ok: 'Подтвердить', fields: [
      { name: 'password', label: 'Пароль администратора', type: 'password', required: true },
      { name: 'code', label: 'Код из приложения или резервный код', required: true },
      ...(extra || []) ], submit: fn });
    if (!login.totp.enabled) {
      const b = document.createElement('button');b.className = 'btn primary';b.textContent = 'Включить';
      b.onclick = async () => {
        try {
          await loadScript('/vendor/qrcode.js');
          const r = await api('security.2fa.begin');
          const qr = qrcode(0, 'M');qr.addData(r.uri);qr.make();
          const m = modal(`<div class="backup-settings-head"><h3>Двухэтапная аутентификация</h3><button class="btn" data-close>Закрыть</button></div>
            <p>1. Откройте приложение-аутентификатор и добавьте аккаунт: отсканируйте QR-код или введите ключ вручную.</p>
            <div style="display:flex;gap:24px;flex-wrap:wrap;align-items:center"><div style="width:200px;height:200px;background:#fff;padding:6px;border-radius:12px">${qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true })}</div>
            <div><div class="muted">Ключ для ввода вручную</div><div class="mono" style="font-size:16px;user-select:all;word-break:break-all;max-width:320px">${h(r.secret.match(/.{1,4}/g).join(' '))}</div></div></div>
            <p style="margin-top:16px">2. Введите шестизначный код из приложения:</p>
            <div style="display:flex;gap:10px"><input type="text" data-code inputmode="numeric" autocomplete="one-time-code" maxlength="8" placeholder="123456" style="max-width:160px"><button class="btn primary" data-ok>Включить</button></div><p class="err" data-err></p>`);
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
      const nb = document.createElement('button');nb.className = 'btn';nb.textContent = 'Новые резервные коды';
      nb.onclick = () => ask('Новые резервные коды', async (v) => { const x = await api('security.2fa.recovery', v); await route(); recovery(x.recovery); });
      const off = document.createElement('button');off.className = 'btn danger';off.textContent = 'Выключить';
      off.onclick = () => ask('Выключить двухэтапную аутентификацию', async (v) => { await api('security.2fa.disable', v); toast('Выключено', 'ok'); route(); });
      wrap.append(nb, off);two.append(wrap);
    }
    const ips = panel(view, 'Доступ к панели только с выбранных IP-адресов');ips.replaceChildren();
    const ipNote = document.createElement('p');ipNote.className = 'hint';
    ipNote.textContent = login.ips.list.length ? 'Панель открывается только с адресов из списка. Все остальные получают отказ.' : 'Ограничения нет: панель открывается с любого адреса. Укажите адреса или сети, чтобы пускать только их.';
    const area = document.createElement('textarea');area.rows = 4;area.value = login.ips.list.join('\n');area.placeholder = '203.0.113.5\n198.51.100.0/24';area.style.cssText = 'width:100%;max-width:520px;font-family:var(--mono,monospace)';area.setAttribute('aria-label', 'Разрешённые адреса');
    const cur = document.createElement('p');cur.className = 'hint';cur.append('Ваш текущий адрес: ');const curB = document.createElement('b');curB.textContent = login.ips.current;cur.append(curB, '. ');
    const addMe = document.createElement('a');addMe.href = '#';addMe.textContent = 'добавить в список';addMe.onclick = (e) => { e.preventDefault(); if (!area.value.split(/\s+/).includes(login.ips.current)) area.value = (area.value.trim() ? area.value.trim() + '\n' : '') + login.ips.current; };cur.append(addMe);
    const warn = document.createElement('p');warn.className = 'hint';warn.textContent = 'Одна строка — один адрес или сеть (например, 198.51.100.0/24). Пустой список снимает ограничение. Если вы потеряете доступ, на сервере выполните: itrupanel allow-all-ips';
    const save = document.createElement('button');save.className = 'btn primary';save.textContent = 'Сохранить';
    save.onclick = async () => { try { await api('security.ips.save', { list: area.value }); toast('Сохранено', 'ok'); route(); } catch (e) { fail(e); } };
    ips.append(ipNote, area, cur, warn, save);
  }
  const settings = panel(view, 'Защита FTP');settings.replaceChildren();
  const note = document.createElement('p');note.className = 'hint';note.textContent = 'Модуль устанавливает Fail2ban и nftables, читает журнал Pure-FTPd и блокирует только указанный FTP-порт. При первой настройке служба FTP может быть перезапущена. Это не защита от перегрузки интернет-канала.';settings.append(note);
  const button = document.createElement('button');button.className = 'btn primary';button.textContent = d.ftp.active ? 'Настроить FTP-защиту' : 'Установить и включить';
  button.onclick = () => openForm({ title: 'Защита FTP от перебора паролей', fields: [
    { name: 'port', label: 'Порт FTP', type: 'number', value: d.ftp.port, required: true, hint: 'Укажите порт подключения к Pure-FTPd. Обычно 21. Пассивные порты передачи данных здесь не нужны.' },
    { type: 'html', html: '<p class="hint">5 ошибок за 10 минут → блокировка IP на 15 минут. Локальные подключения сервера исключены из FTP-блокировок. Порт самой службы FTP не изменяется.</p>' },
  ], submit: async v => {const r = await api('authguard.ftp.enable', { port: Number(v.port) });watchJob(r.job);} });settings.append(button);
  if (d.ftp.warning) {const warning = document.createElement('p');warning.className = 'hint';warning.textContent = 'Fail2ban не подтвердил работу защиты FTP. Проверьте журнал задачи или повторите настройку.';settings.append(warning);}
  const bans = panel(view, 'Временные блокировки');bans.replaceChildren();
  const rows = [...d.bans, ...d.ftp.bans.map(ip => ({ip, service: 'ftp', until: null}))];
  table(bans, { rows, empty: { icon: 'shield', title: 'Активных блокировок нет' }, columns: [
    { t: 'IP-адрес', r: r => h(r.ip) },
    { t: 'Служба', r: r => r.service === 'panel' ? 'Панель' : 'FTP' },
    { t: 'Окончание', r: r => r.until ? fmtDate(r.until) : 'Автоматически через Fail2ban' },
  ], actions: r => [{ t: 'Разблокировать IP', label: 'Разблокировать', i: 'unlock', fn: async () => {try {await api('authguard.unban', {ip: r.ip, service: r.service});toast('Блокировка снята', 'ok');await route();} catch(e) {fail(e);}} }] });
} });
