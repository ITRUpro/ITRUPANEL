// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Импорт данных со старого сервера: подключение → просмотр → выбор → перенос.
// Старый сервер только читается. Логины и пароли хранятся в памяти панели, не на диске.

const impStatus = { ok: ['ok', 'перенесено'], skipped: ['warn', 'пропущено'], error: ['err', 'ошибка'] };

page('import', {
  title: 'Импорт данных',
  async render(view) {
    head(view, 'Импорт данных', { sub: 'Перенос сайтов, баз, почты, DNS, FTP и заданий cron с другой ITRUPANEL, с FASTPANEL или с любого сервера по SSH' });
    const host = document.createElement('div');
    view.append(host);
    let session = null;

    // ---------- шаг 1: подключение ----------
    const stepConnect = (source = 'itrupanel') => {
      host.replaceChildren();
      const p = panel(host, '1. Откуда переносим');
      const sources = [['itrupanel', 'ITRUPANEL'], ['fastpanel', 'FASTPANEL'], ['ssh', 'Другой сервер по SSH']];
      const hints = {
        itrupanel: 'Укажите адрес и данные администратора старой панели ITRUPANEL. Панель сама отдаст сайты, базы, почту, DNS, FTP и cron, а файлы и базы будут переданы напрямую.',
        fastpanel: 'Подключение по SSH под root (или под пользователем с sudo без пароля). Список сайтов, баз, FTP и почты читается из самой панели FASTPANEL, а если она не ответит, сервер просматривается по настройкам nginx и MySQL. Переносятся сайты, базы (с паролями), FTP-пользователи, почта с письмами и cron.',
        ssh: 'Подключение по SSH под root (или под пользователем с sudo без пароля). Сайты определяются по настройкам nginx. Переносятся сайты, базы и cron; почта, DNS и FTP — только с ITRUPANEL и FASTPANEL.',
      };
      const sshFields = `<div class="cols2">
          <div class="field"><label>Адрес сервера</label><input type="text" name="ssh_host" placeholder="203.0.113.10" spellcheck="false"></div>
          <div class="field"><label>Порт SSH</label><input type="text" name="ssh_port" value="22" inputmode="numeric"></div>
          <div class="field"><label>Пользователь</label><input type="text" name="ssh_user" value="root" spellcheck="false" autocomplete="off"></div>
          <div class="field"><label>Пароль</label><input name="ssh_password" type="password" autocomplete="new-password"></div>
        </div>
        <div class="field"><label>или закрытый ключ SSH (без пароля, если вход по ключу)</label><textarea name="ssh_key" rows="3" spellcheck="false" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"></textarea></div>`;
      const itruFields = `<div class="cols2">
          <div class="field"><label>Адрес панели</label><input type="text" name="panel_url" placeholder="https://old.example.com:3333" spellcheck="false"></div>
          <div class="field"><label>Логин администратора</label><input type="text" name="panel_login" value="admin" spellcheck="false" autocomplete="off"></div>
          <div class="field"><label>Пароль</label><input name="panel_password" type="password" autocomplete="new-password"></div>
        </div>`;
      p.innerHTML = `<div class="seg" data-src>${sources.map(([v, n]) => `<button type="button" data-v="${v}" class="${v === source ? 'on' : ''}">${n}</button>`).join('')}</div>
        <p class="hint" style="margin-top:14px">${hints[source]}</p>
        ${source === 'itrupanel' ? itruFields : sshFields}
        <p class="hint">Логины, пароли и ключи хранятся только в памяти панели на время переноса (до трёх часов), на диск и в журналы не записываются. На старом сервере ничего не изменяется.</p>
        <div class="btns"><button class="btn primary" data-go>${icon('search')}Проверить старый сервер</button></div>`;
      $$('[data-src] button', p).forEach((b) => (b.onclick = () => stepConnect(b.dataset.v)));
      $('[data-go]', p).onclick = async (e) => {
        const v = Object.fromEntries($$('input,textarea', p).map((i) => [i.name, i.value]));
        const body = source === 'itrupanel'
          ? { panel: { url: v.panel_url, login: v.panel_login, password: v.panel_password } }
          : { ssh: { host: v.ssh_host, port: +v.ssh_port || 22, user: v.ssh_user, password: v.ssh_password, key: v.ssh_key } };
        e.target.disabled = true;
        try {
          const r = await api('import.analyze', body);
          watchJob(r.job, { onComplete: (j) => { if (j.status === 'done' && j.result?.session) { session = j.result; stepChoose(); } } });
        } catch (err) { fail(err); } finally { e.target.disabled = false; }
      };
    };

    // ---------- шаг 2: выбор ----------
    const stepChoose = () => {
      host.replaceChildren();
      const inv = session.inventory;
      const sel = { sites: new Set(), dbs: new Set(), cron: false, mail: new Set(), dns: new Set(), ftp: new Set(), backup: false };
      const info = panel(host, 'Старый сервер');
      info.innerHTML = `<div class="kv"><div>Адрес</div><div><b>${h(inv.host || '—')}</b></div>
        <div>Система</div><div>${h(inv.os || '—')}</div>
        <div>Панель</div><div>${h(({ itrupanel: 'ITRUPANEL', fastpanel: 'FASTPANEL', ispmanager: 'ISPmanager', aapanel: 'aaPanel', hestia: 'Hestia' })[inv.panel] || inv.panel || 'не определена')}</div>
                ${inv.panel_fingerprint ? `<div>Сертификат панели</div><div class="mono">${h(inv.panel_fingerprint)}</div>` : ''}
        ${inv.fingerprint ? `<div>Ключ SSH-сервера</div><div class="mono">${h(inv.fingerprint)}</div>` : ''}
        <div>Версии PHP</div><div>${h((inv.php_versions || []).join(', ') || '—')}</div></div>
        ${(inv.warnings || []).map((w) => `<p class="hint">⚠ ${h(w)}</p>`).join('')}
        ${inv.can_transfer ? '' : '<p class="err">Перенос недоступен: эта панель не отдаёт файлы и базы. Обновите её до версии 0.1.94.</p>'}
        <div class="btns" style="margin-top:14px"><button class="btn" data-back>${icon('chevron')}Другой сервер</button></div>`;
      $('[data-back]', info).onclick = () => { api('import.forget', { session: session.session }).catch(() => {}); session = null; stepConnect(); };

      const bar = document.createElement('div');
      bar.className = 'toolbar';
      bar.style.margin = '14px 0';
      bar.innerHTML = `<button class="btn" data-all>Выбрать всё</button><button class="btn" data-none>Снять выбор</button><span class="grow"></span><span class="muted" data-count style="padding-right:6px"></span>`;
      host.append(bar);

      const checks = [];
      const section = (title, rows, columns, onPick, kind) => {
        const p = panel(host, title);
        if (!rows.length) { p.innerHTML = `<p class="muted">Не найдено</p>`; return; }
        table(p, { rows, columns: [{ t: '', r: (r, i) => `<input type="checkbox" data-pick="${kind}" data-key="${h(r.domain || r.name)}" aria-label="${h(r.domain || r.name)}">`, cls: 'chk' }, ...columns] });
        $$('[data-pick="' + kind + '"]', p).forEach((c) => { checks.push(c); c.onchange = () => { onPick(c.dataset.key, c.checked); recount(); }; });
      };
      section('Сайты', inv.sites, [
        { t: 'Домен', r: (s) => `<b>${h(s.domain)}</b>${s.aliases?.length ? `<div class="hint">${h(s.aliases.join(', '))}</div>` : ''}` },
        { t: 'Владелец', r: (s) => h(s.owner || '—'), cls: 'hide-sm' },
        { t: 'Тип', r: (s) => ({ php: 'PHP' + (s.php ? ' ' + s.php : ''), static: 'статический', proxy: 'прокси' }[s.mode] || s.mode) + (s.ssl ? ' <span class="tag">SSL</span>' : '') },
        { t: 'Размер', r: (s) => (s.size ? fmtBytes(s.size) : '—'), cls: 'nowrap hide-sm' },
      ], (k, on) => (on ? sel.sites.add(k) : sel.sites.delete(k)), 'site');
      section('Базы данных', inv.dbs, [
        { t: 'База', r: (d) => `<b>${h(d.name)}</b>` },
        { t: 'Пользователи', r: (d) => h((d.users || []).join(', ') || '—'), cls: 'hide-sm' },
        { t: 'Размер', r: (d) => fmtBytes(d.size), cls: 'nowrap' },
      ], (k, on) => (on ? sel.dbs.add(k) : sel.dbs.delete(k)), 'db');

      const cron = panel(host, 'Задания cron');
      if (!inv.cron?.length) cron.innerHTML = '<p class="muted">Не найдено</p>';
      else {
        cron.innerHTML = `<label class="check"><input type="checkbox" data-cron><span>Перенести задания cron владельцев выбранных сайтов (${inv.cron.length})</span></label><div class="table-wrap"><table class="grid"><thead><tr><th>Владелец</th><th>Расписание</th><th>Команда</th></tr></thead><tbody>${inv.cron.map((c) => `<tr><td>${h(c.user)}</td><td class="mono">${h(c.schedule)}</td><td class="mono">${h(c.command)}</td></tr>`).join('')}</tbody></table></div>`;
        const c = $('[data-cron]', cron);
        checks.push(c);
        c.onchange = () => { sel.cron = c.checked; recount(); };
      }

      const addHint = (title, text) => { const el = host.lastElementChild; if (el && text) el.insertAdjacentHTML('beforeend', `<p class="hint" style="margin-top:10px">${text}</p>`); };
      section('Почта', inv.mail_domains || [], [
        { t: 'Домен', r: (m) => `<b>${h(m.name)}</b>` },
        { t: 'Ящиков', r: (m) => h(String(m.boxes || 0)) },
      ], (k, on) => (on ? sel.mail.add(k) : sel.mail.delete(k)), 'mail');
      if (inv.mail_domains?.length) addHint('', 'Переносятся ящики вместе с письмами, псевдонимы и пересылки; пароли ящиков сохраняются. Сертификат почты сначала самоподписанный: когда домен будет указывать на этот сервер, выпустите Let’s Encrypt в разделе «Почта».');
      section('DNS-зоны', inv.dns || [], [
        { t: 'Зона', r: (z) => `<b>${h(z.name)}</b>` },
      ], (k, on) => (on ? sel.dns.add(k) : sel.dns.delete(k)), 'dns');
      if (inv.dns?.length) addHint('', 'IP старого сервера в записях A и AAAA заменяется на IP этого сервера; почтовые записи (MX, SPF, DKIM, DMARC) публикуются заново, если вы переносите почту этого домена.');
      section('FTP-пользователи', inv.ftp || [], [
        { t: 'Пользователь', r: (f) => `<b>${h(f.name)}</b>` },
        { t: 'Владелец', r: (f) => h(f.owner || '—'), cls: 'hide-sm' },
        { t: 'Папка', r: (f) => h(f.home || '—'), cls: 'hide-sm' },
      ], (k, on) => (on ? sel.ftp.add(k) : sel.ftp.delete(k)), 'ftp');
      if (inv.ftp?.length) addHint('', 'FTP-пользователь переносится вместе с сайтом своего владельца: выберите и этот сайт. Пароль сохраняется.');

      const bk = inv.backup;
      const bkPanel = panel(host, 'Резервное копирование');
      if (!bk) bkPanel.innerHTML = '<p class="muted">Не найдено</p>';
      else if (bk.target === 'local' || !bk.host) bkPanel.innerHTML = '<p class="muted">На старом сервере копии хранятся только на самом сервере: переносить нечего.</p>';
      else {
        bkPanel.innerHTML = `<label class="check"><input type="checkbox" data-backup><span>${bk.target === 's3' || bk.target === 'local_s3' ? 'Перенести настройки копирования в облако (' : 'Перенести настройки копирования на FTP ('}${h(bk.host)}${bk.dir ? ', папка ' + h(bk.dir) : ''})</span></label>
          <p class="hint" style="margin-top:10px">Переносятся адрес хранилища (FTP или облака), логин или ключи, папка и параметры копирования. Расписание на этом сервере остаётся выключенным, пока вы сами не включите его в разделе «Резервные копии»: так две панели не будут одновременно копировать одни и те же сайты. Копии, которые уже лежат на FTP или в облаке, будут найдены и подключены.</p>`;
        const bc = $('[data-backup]', bkPanel);
        checks.push(bc);
        bc.onchange = () => { sel.backup = bc.checked; recount(); };
      }

      const needs = panel(host, 'Что понадобится на этом сервере');
      const go = document.createElement('div');
      go.className = 'btns';
      go.style.margin = '14px 0 4px';
      go.innerHTML = `<button class="btn primary" data-run disabled>${icon('download')}Перенести выбранное</button>`;
      host.append(go);
      const selection = () => ({ sites: [...sel.sites], dbs: [...sel.dbs], cron: sel.cron, mail: [...sel.mail], dns: [...sel.dns], ftp: [...sel.ftp], backup: sel.backup });
      let needTimer;
      const recount = () => {
        const n = sel.sites.size + sel.dbs.size + sel.mail.size + sel.dns.size + sel.ftp.size + (sel.cron ? 1 : 0) + (sel.backup ? 1 : 0);
        $('[data-count]', bar).textContent = n ? `Выбрано: сайтов ${sel.sites.size}, баз ${sel.dbs.size}${sel.mail.size ? ', почты ' + sel.mail.size : ''}${sel.dns.size ? ', DNS ' + sel.dns.size : ''}${sel.ftp.size ? ', FTP ' + sel.ftp.size : ''}${sel.cron ? ', cron' : ''}${sel.backup ? ', копирование' : ''}` : '';
        $('[data-run]', go).disabled = !n || !inv.can_transfer;
        clearTimeout(needTimer);
        needTimer = setTimeout(async () => {
          try {
            const r = await api('import.needs', { session: session.session, selection: selection() });
            needs.innerHTML = r.needs.length ? `<table class="grid"><tbody>${r.needs.map((x) => `<tr><td><b>${h(x.title)}</b>${x.detail ? `<div class="hint">${h(x.detail)}</div>` : ''}</td><td>${x.installed ? '<span class="tag ok">уже установлено</span>' : '<span class="tag acc">будет установлено автоматически</span>'}</td></tr>`).join('')}</tbody></table>` : '<p class="muted">Всё необходимое уже есть. Выберите сайты или базы выше.</p>';
          } catch (e) { needs.textContent = e.message; }
        }, 250);
      };
      recount();
      $('[data-all]', bar).onclick = () => { checks.forEach((c) => { c.checked = true; c.onchange(); }); };
      $('[data-none]', bar).onclick = () => { checks.forEach((c) => { c.checked = false; c.onchange(); }); };
      $('[data-run]', go).onclick = async () => {
        const s = selection();
        if (!(await confirmBox('Начать перенос?', `Будет перенесено: сайтов — ${s.sites.length}, баз данных — ${s.dbs.length}${s.mail.length ? ', почтовых доменов — ' + s.mail.length : ''}${s.dns.length ? ', DNS-зон — ' + s.dns.length : ''}${s.ftp.length ? ', FTP-пользователей — ' + s.ftp.length : ''}${s.cron ? ', задания cron' : ''}${s.backup ? ', настройки резервного копирования' : ''}. Недостающие версии PHP и службы установятся автоматически. На старом сервере ничего не изменится. Всё, что уже есть на этом сервере под тем же именем, пропускается.`, { ok: 'Перенести', danger: false }))) return;
        try {
          const r = await api('import.run', { session: session.session, selection: s });
          watchJob(r.job, { onComplete: (j) => { if (j.result) stepReport(j.result, j.status); } });
        } catch (e) { fail(e); }
      };
    };

    // ---------- шаг 3: итог ----------
    const stepReport = (res, status) => {
      host.replaceChildren();
      const p = panel(host, status === 'done' ? 'Перенос завершён' : 'Перенос завершён с ошибками');
      p.innerHTML = `<p>${h(res.info || '')}</p>
        ${(res.warnings || []).map((w) => `<p class="hint">⚠ ${h(w)}</p>`).join('')}
        ${res.sites?.length ? `<h4 style="margin:14px 0 8px">Сайты</h4><div class="table-wrap"><table class="grid"><thead><tr><th>Домен</th><th>Пользователь</th><th>Состояние</th><th>Размер</th></tr></thead><tbody>${res.sites.map((s) => `<tr><td><b>${h(s.domain)}</b></td><td>${h(s.user)}</td><td><span class="tag ${impStatus[s.status][0]}">${impStatus[s.status][1]}</span>${s.detail ? `<div class="hint">${h(s.detail)}</div>` : ''}</td><td class="nowrap">${s.bytes ? fmtBytes(s.bytes) : ''}</td></tr>`).join('')}</tbody></table></div>` : ''}
        ${res.dbs?.length ? `<h4 style="margin:14px 0 8px">Базы данных</h4><div class="table-wrap"><table class="grid"><thead><tr><th>База</th><th>Пользователь</th><th>Состояние</th></tr></thead><tbody>${res.dbs.map((d) => `<tr><td><b>${h(d.name)}</b></td><td>${h(d.user || '')}</td><td><span class="tag ${impStatus[d.status][0]}">${impStatus[d.status][1]}</span>${d.password_kept ? ' <span class="tag">пароль сохранён</span>' : ''}${d.detail ? `<div class="hint">${h(d.detail)}</div>` : ''}</td></tr>`).join('')}</tbody></table></div>` : ''}
        ${res.cron ? `<p>Заданий cron перенесено: <b>${res.cron}</b></p>` : ''}
        ${res.backup ? `<h4 style="margin:14px 0 8px">Резервное копирование</h4><p><span class="tag ${impStatus[res.backup.status][0]}">${impStatus[res.backup.status][1]}</span> ${res.backup.detail ? h(res.backup.detail) : ''}</p>` : ''}
        ${['mail', 'dns', 'ftp'].map((k) => (res[k]?.length ? `<h4 style="margin:14px 0 8px">${({ mail: 'Почта', dns: 'DNS-зоны', ftp: 'FTP-пользователи' })[k]}</h4><div class="table-wrap"><table class="grid"><thead><tr><th>Имя</th><th>Состояние</th></tr></thead><tbody>${res[k].map((x) => `<tr><td><b>${h(x.name)}</b></td><td><span class="tag ${impStatus[x.status][0]}">${impStatus[x.status][1]}</span>${x.detail ? `<div class="hint">${h(x.detail)}</div>` : ''}</td></tr>`).join('')}</tbody></table></div>` : '')).join('')}
        <p class="hint">SSL-сертификаты не переносятся: когда домен будет указывать на этот сервер, выпустите Let’s Encrypt в разделе «Сайты».</p>
        <div class="btns"><button class="btn" data-secrets>Показать новые пароли</button><button class="btn primary" data-done>Готово</button></div>`;
      $('[data-secrets]', p).onclick = async () => {
        try {
          const r = await api('import.secrets', { session: session.session });
          const items = Object.entries(r.passwords || {});
          if (!items.length) { toast('Новых паролей нет: пароли баз и FTP сохранены', 'ok'); return; }
          showSecrets('Новые пароли', items.map(([k, v]) => [k, v]), 'Пароли показываются один раз. Пароль базы укажите в настройках сайта, который её использует; пароль FTP сообщите пользователю.');
        } catch (e) { fail(e); }
      };
      $('[data-done]', p).onclick = () => { if (session) api('import.forget', { session: session.session }).catch(() => {}); session = null; go('sites'); };
    };

    stepConnect();
  },
});
