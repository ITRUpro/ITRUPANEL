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
