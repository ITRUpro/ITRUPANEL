// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

async function mailSetupForm(data) {
  const d = data || await api('mail.list');
  openForm({ title: d.ready ? 'Настройки почтового сервера' : 'Настроить почтовый сервер', ok: d.ready ? 'Применить' : 'Установить', fields: [
    { name: 'hostname', label: 'Имя почтового сервера', value: d.hostname || '', placeholder: 'mail.example.ru', required: true },
    { name: 'certificate', label: 'TLS-сертификат для этого имени', type: 'select', value: d.certificate || '@le', options: mailCertOptions(d, 'Самоподписанный (для первоначальной проверки)'), hint: 'Let’s Encrypt выпускается автоматически: A-запись имени должна указывать на этот сервер, порт 80 — открыт. Можно выбрать и сертификат из раздела SSL — имя сервера должно входить в него.' },
    { name: 'email', label: 'E-mail для Let’s Encrypt', value: '', show: v => v.certificate === '@le' },
    { type: 'html', html: d.ready
      ? '<p class="muted">Общее имя сервера: им Postfix представляется другим почтовым серверам, на него должна указывать обратная запись PTR вашего IP. У каждого почтового домена своё mail-имя и свой сертификат — кнопка «SSL» в таблице доменов. Добавить домен — кнопка «Добавить домен».</p>'
      : '<p class="muted">Будут установлены Postfix, Dovecot и OpenDKIM. SMTP: 25, отправка с авторизацией: 587 STARTTLS, IMAP: 993 TLS. Существующий отдельный почтовый сервер автоматически не переносится.</p>' },
  ], submit: async v => { const r = await api('mail.setup', mailCertRequest(v)); watchJob(r.job); } });
}
// '@le' в списке сертификатов — выпустить Let's Encrypt на указанное имя при установке.
function mailCertOptions(d, selfLabel) {
  return [['@le', 'Выпустить Let’s Encrypt'], ['', selfLabel], ...d.certs.map(c => [c.name, c.name])];
}
function mailCertRequest(v) {
  const le = v.certificate === '@le';
  return { ...v, le, certificate: le ? '' : v.certificate, email: le ? v.email : '' };
}
async function mailBoxForm(box) {
  const d = await api('mail.list');
  if (!d.ready) { await mailSetupForm(d); return; }
  const domains = (d.domains || []).map(x => x.name), extra = d.site_domains || [];
  if (!box && !domains.length && !extra.length) { await mailAddDomainForm(await api('mail.list')); return; }
  const defQuota = (name) => (d.domains || []).find(x => x.name === name)?.default_quota_mb || 0;
  const startQuota = box ? box.quota_mb : defQuota(domains[0] || '');
  const gb = startQuota > 0 && startQuota % 1024 === 0;
  openForm({ title: box ? box.address : 'Добавить почтовый ящик', ok: box ? 'Сохранить' : 'Добавить', fields: [
    { name: 'name', label: 'Имя', value: box?.name || '', placeholder: 'Иван Петров', hint: 'Необязательно: кто пользуется ящиком.' },
    ...(box ? [{ name: 'address', label: 'Email адрес', value: box.address, readonly: true }] : [
      { name: 'local', label: 'Email адрес', placeholder: 'info', required: true },
      { name: 'domain', label: '@ домен', type: 'select', value: domains[0] || extra[0], options: [...domains.map(x => [x, x]), ...extra.map(x => [x, x + ' — будет добавлен в почту'])] },
    ]),
    { name: 'password', label: box ? 'Новый пароль (пусто — оставить прежний)' : 'Пароль', type: 'password', gen: true, value: '', hint: 'Не менее 12 символов. Для нового ящика пустой пароль будет сгенерирован автоматически.' },
    { name: 'limit', label: 'Ограничить размер ящика (квота)', type: 'checkbox', value: startQuota > 0 },
    { name: 'quota', label: 'Квота', type: 'number', value: startQuota > 0 ? (gb ? startQuota / 1024 : startQuota) : 5, show: (v) => v.limit, hint: 'При заполнении ящика новая почта не доставляется; существующие письма сохраняются.' },
    { name: 'unit', label: 'Единица', type: 'select', value: startQuota > 0 && !gb ? 'MB' : 'GB', options: [['GB', 'ГБ'], ['MB', 'МБ']], show: (v) => v.limit },
    { name: 'enabled', label: 'Статус: ящик включён', type: 'checkbox', value: box ? box.enabled : true },
  ], onChange: (v, key) => { if (!box && key === 'domain') { const q = defQuota(v.domain); v.limit = q > 0; if (q > 0) { v.unit = q % 1024 === 0 ? 'GB' : 'MB'; v.quota = q % 1024 === 0 ? q / 1024 : q; } } }, submit: async v => {
    const address = box ? box.address : v.local + '@' + v.domain;
    const size = Number(v.quota);
    const quota = v.limit ? (v.unit === 'GB' ? size * 1024 : size) : 0;
    if (v.limit && (!Number.isFinite(size) || size <= 0 || !Number.isInteger(quota))) throw new Error('Квота: положительное число; в мегабайтах — целое');
    if (!Number.isInteger(quota) || quota < 0 || quota > 1048576) throw new Error('Квота: до 1024 ГБ');
    if (!box && !domains.includes(v.domain)) await api('mail.domain.add', { name: v.domain });
    const r = await api('mail.box.save', { address, name: v.name, password: v.password, enabled: !!v.enabled, edit: !!box, quota_mb: quota });
    if (!box && !v.enabled) await api('mail.box.save', { address, edit: true, enabled: false, quota_mb: quota });
    await route();
    if (r.password) {
      // Сервер для программ — mail-имя домена ящика (у нового домена — mail.<домен>).
      const fresh = await api('mail.list'), dom = (fresh.domains || []).find(x => x.name === address.split('@')[1]);
      const host = dom ? mailHost(fresh, dom) : fresh.hostname;
      const noCert = dom && dom.hostname && dom.hostname !== fresh.hostname && !dom.certificate;
      showSecrets('Данные почтового ящика', [['Логин', r.address], ['Пароль', r.password], ['Сервер', host]], 'Сохраните пароль. Подключение: IMAP 993 TLS; SMTP 587 STARTTLS.' + (noCert ? ' Для ' + host + ' ещё нет сертификата: выпустите его кнопкой «SSL» у домена, когда A-запись ' + host + ' будет указывать на этот сервер.' : ''));
    }
    else toast('Сохранено', 'ok');
  } });
}
async function mailAliasForm(alias) {
  const d = await api('mail.list');
  if (!d.boxes.length) { toast('Сначала создайте ящик', 'err'); return; }
  openForm({ title: alias ? 'Изменить псевдоним' : 'Создать псевдоним', fields: [
    { name: 'address', label: 'Адрес псевдонима', value: alias?.address || '', placeholder: 'sales@example.ru', readonly: !!alias, required: true },
    { name: 'target', label: 'Доставлять в ящик', type: 'select', options: d.boxes.map(b => [b.address, b.address]), value: alias?.target || d.boxes[0].address, hint: 'Псевдоним не имеет отдельного пароля. Доставка идёт в выбранный ящик.' },
  ], submit: async v => { await api('mail.alias.save', { ...v, edit: !!alias }); toast('Псевдоним сохранён', 'ok'); route(); } });
}
page('mail', {
  title: 'Почта',
  async render(view, params) {
    const d = await api('mail.list');
    head(view, 'Почта', { actions: d.ready ? [
      { t: 'Добавить домен', i: 'plus', primary: true, fn: () => mailAddDomainForm(d) },
      { t: 'Добавить ящик', i: 'mail', fn: () => mailBoxForm() },
      { t: 'Псевдоним', i: 'mail', fn: () => mailAliasForm() },
      { t: 'Сервер', i: 'settings', fn: () => mailSetupForm(d) },
      { t: 'MXToolbox', i: 'settings', fn: () => mailMXSettings().catch(fail) },
      { t: 'Задачи', i: 'clock', fn: () => go('jobs') },
    ] : [
      { t: 'Добавить домен', i: 'plus', primary: true, fn: () => mailAddDomainForm(d) },
      { t: 'Задачи', i: 'clock', fn: () => go('jobs') },
    ] });
    if (!d.ready) {
      const p = panel(view, 'Почтовые домены'); p.innerHTML = '<div class="empty"><b>Почтовых доменов пока нет</b><p>Нажмите «Добавить домен» и укажите домен, например example.ru. Панель сама установит почтовый сервер (Postfix, Dovecot, OpenDKIM), выпустит сертификат на mail.example.ru и по вашему выбору включит веб-почту Roundcube и антиспам. Ход установки — в разделе «Задачи».</p></div>'; return;
    }
    const spam = d.spam || {}, webmail = d.webmail || {};
    const status = panel(view);
    const chip = (ok, name) => `<span class="tag ${ok ? 'ok' : 'err'}">${h(name)}: ${ok ? 'работает' : 'остановлен'}</span>`;
    status.innerHTML = `<div class="row">${chip(d.smtp, 'SMTP')}${chip(d.imap, 'IMAP')}${chip(d.dkim, 'DKIM')}${spam.installed ? chip(d.spam_active, 'Антиспам') : ''}</div><p><b>Почтовый сервер: ${h(d.hostname)}</b></p><p class="hint">Почтовые программы: IMAP 993 TLS · SMTP 587 STARTTLS · логин — полный адрес ящика, сервер — mail-имя домена. Веб-почта и антиспам включаются у каждого домена в таблице ниже.${d.certificate ? '' : ' У сервера ' + h(d.hostname) + ' самоподписанный сертификат: выпустите Let’s Encrypt кнопкой SSL у домена или в «Сервер».'}</p><div class="toolbar">${spam.installed ? '<button class="btn small" data-spam>Пороги антиспама</button>' : ''}${webmail.ready ? '<button class="btn small" data-webmail>Основной адрес Roundcube</button>' : ''}</div>`;
    const spamBtn = $('[data-spam]', status), webBtn = $('[data-webmail]', status);
    if (spamBtn) spamBtn.onclick = () => mailSpamForm(d);
    if (webBtn) webBtn.onclick = () => mailWebmailForm(d);
    const domains = panel(view, 'Почтовые домены');
    const p = panel(view, 'Почтовые ящики');
    const usage = {};
    const refreshUsage = () => mailLoadUsage(d, usage, domains, p);
    mailDomainTable(domains, d, usage);
    mailMailboxTable(p, d, usage);
    refreshUsage().catch(() => {});
    const forwards = panel(view, 'Пересылки');
    const createForward = document.createElement('button');
    createForward.className = 'btn'; createForward.textContent = 'Создать пересылку';
    createForward.onclick = () => mailForwardForm();
    table(forwards, { rows: d.forwards || [], empty: { icon: 'mail', title: 'Пересылок пока нет', text: '<p>Отправляйте письма на один или несколько адресов, при необходимости сохраняя копию.</p>' }, columns: [
      { t: 'Откуда', r: f => h(f.address) },
      { t: 'Получатели', r: f => f.targets.map(h).join('<br>') },
      { t: 'Копия в ящике', r: f => f.keep_copy ? 'Да' : 'Нет' },
      { t: 'Состояние', r: f => { const box = d.boxes.find(b => b.address === f.address); return !f.enabled ? '<span class="tag warn">Отключена</span>' : box && !box.enabled ? '<span class="tag warn">Ящик отключён</span>' : '<span class="tag ok">Включена</span>'; } },
    ], onRow: mailForwardForm, actions: f => [
      { t: 'Изменить', i: 'edit', fn: () => mailForwardForm(f) },
      { t: f.enabled ? 'Отключить' : 'Включить', i: f.enabled ? 'stop' : 'play', fn: async () => { try { await api('mail.forward.save', {...f, enabled: !f.enabled, edit: true}); route(); } catch(e) { fail(e); } } },
      { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (!await confirmBox('Удалить пересылку?', h(f.address))) return; try { await api('mail.forward.delete', {address: f.address}); route(); } catch(e) { fail(e); } } },
    ] });
    forwards.prepend(createForward);
    const aliases = panel(view, 'Псевдонимы');
    table(aliases, { rows: d.aliases || [], empty: { icon: 'mail', title: 'Псевдонимов пока нет' }, columns: [{ t: 'Адрес', r: a => h(a.address) }, { t: 'Доставка в ящик', r: a => h(a.target) }], actions: a => [
      { t: 'Изменить', i: 'edit', fn: () => mailAliasForm(a) },
      { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (!await confirmBox('Удалить псевдоним?', h(a.address))) return; try { await api('mail.alias.delete', { address: a.address }); route(); } catch (e) { fail(e); } } },
    ] });
    if (params.new) await mailBoxForm();
    if (params.domain) await mailAddDomainForm(d);
  },
});

async function mailQuotaUsage(box) {
  const q = await api('mail.quota', {address: box.address});
  const limit = q.quota_mb ? (q.quota_mb + ' МиБ') : 'без ограничения';
  showOutput('Квота: ' + box.address, 'Использовано: ' + fmtBytes(q.used) + '\nЛимит: ' + limit + (q.quota_mb ? '\nЗаполнено: ' + Math.round(q.used / (q.quota_mb * 1048576) * 100) + '%' : ''));
}
async function mailSpamForm(data) {
  const s = data.spam || {};
  openForm({title: 'Антиспам Rspamd', ok: s.installed ? 'Применить' : 'Установить', fields: [
    {name:'enabled',label:'Фильтрация включена',type:'checkbox',value:s.installed ? !!s.enabled : true},
    {name:'mark',label:'Помечать как спам от балла',type:'number',value:s.mark || 6},
    {name:'reject',label:'Отклонять от балла',type:'number',value:s.reject || 15},
    {type:'html',html:'<p class="hint">Подозрительные письма получают заголовки проверки и пометку [SPAM] в теме. Явный спам отклоняется до доставки. Порог отклонения должен быть выше порога пометки. При недоступности фильтра отправителю возвращается временная ошибка для повторной доставки. Автоматического перемещения в «Спам» и проверки вложений антивирусом здесь нет.</p>'},
  ],submit:async v=>{
    v.mark=Number(v.mark);v.reject=Number(v.reject);
    if(!Number.isInteger(v.mark)||!Number.isInteger(v.reject)||v.mark<1||v.reject<=v.mark||v.reject>100)throw new Error('Укажите целые пороги: 1 ≤ пометка < отклонение ≤ 100');
    const r=await api('mail.spam.setup',v);watchJob(r.job);
  }});
}
async function mailWebmailForm(data) {
  const w=data.webmail || {};
  openForm({title:'Roundcube — веб-почта',ok:w.ready ? 'Применить' : 'Установить',fields:[
    {name:'hostname',label:'Адрес веб-почты',value:w.hostname || data.hostname || '',placeholder:'mail.example.ru',required:true,hint:'Отдельное от панели и сайтов имя. Его DNS-запись должна указывать на этот сервер. Можно использовать имя почтового сервера.'},
    {name:'certificate',label:'TLS-сертификат веб-почты',type:'select',value:w.ready ? w.certificate : (data.certificate || '@le'),options:mailCertOptions(data,'Самоподписанный (для проверки)'),hint:'Let’s Encrypt выпускается автоматически: A-запись адреса должна указывать на этот сервер, порт 80 — открыт. Если адрес совпадает с именем почтового сервера, можно выбрать его сертификат. Самоподписанный вызовет предупреждение браузера.'},
    {name:'email',label:'E-mail для Let’s Encrypt',value:'',show:v=>v.certificate==='@le'},
    {type:'html',html:'<p class="hint">Будут установлены Roundcube и PHP 8.3. Вход — полный адрес ящика и его пароль. Контакты и настройки хранятся в отдельной SQLite-базе; письма остаются в Dovecot. Служебные каталоги закрыты для скачивания.</p>'},
  ],submit:async v=>{const r=await api('mail.webmail.setup',mailCertRequest(v));watchJob(r.job);}});
}

// Копирование пароля ящика: запрос к панели идёт внутри ClipboardItem, иначе Safari
// отклоняет запись в буфер после асинхронного ответа.
async function mailCopyPassword(address) {
  let pending = null;
  const fetchPassword = () => (pending ||= api('mail.box.password', { address }).then(r => r.password));
  try {
    if (window.ClipboardItem && navigator.clipboard?.write) {
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': fetchPassword().then(p => new Blob([p], { type: 'text/plain' })) })]);
    } else {
      await navigator.clipboard.writeText(await fetchPassword());
    }
    toast('Пароль скопирован', 'ok');
  } catch (e) {
    // Браузер не дал доступ к буферу — показываем пароль в окне с кнопкой копирования.
    let password;
    try { password = await fetchPassword(); } catch (err) { fail(err); return; }
    showSecrets('Пароль ящика', [['Логин', address], ['Пароль', password]], 'Браузер не разрешил автоматическое копирование — скопируйте пароль кнопкой.');
  }
}

async function mailForwardForm(rule) {
  const d = await api('mail.list');
  if (!d.ready) { await mailSetupForm(d); return; }
  if (!d.domains?.length) { await mailAddDomainForm(await api('mail.list')); return; }
  openForm({title:rule ? 'Изменить пересылку' : 'Создать пересылку',fields:[
    {name:'address',label:'Адрес пересылки',value:rule?.address || '',placeholder:'info@' + d.domains[0].name,readonly:!!rule,required:true,hint:'Адрес вашего почтового домена: существующий ящик или отдельный адрес без пароля.'},
    {name:'targets',label:'Получатели',type:'textarea',value:rule?.targets.join('\n') || '',required:true,hint:'От 1 до 20 адресов через пробел, запятую или с новой строки. Можно указать внешние адреса.'},
    {name:'keep_copy',label:'Оставлять копию в ящике',type:'checkbox',value:!!rule?.keep_copy,hint:'Нужен существующий ящик с адресом пересылки. Без этой галочки письма доставляются только получателям.'},
    {name:'enabled',label:'Пересылка включена',type:'checkbox',value:rule ? rule.enabled : true},
    {type:'html',html:'<p class="hint">При отключении пересылки существующий ящик получает письма как обычно. Если сам ящик отключён, его пересылка тоже не работает. Доставка на внешние адреса зависит от правил принимающего сервера, включая SPF/DMARC.</p>'},
  ],submit:async v=>{
    const targets = String(v.targets || '').trim().split(/[\s,;]+/).filter(Boolean);
    if (!targets.length || targets.length > 20) throw new Error('Укажите от 1 до 20 получателей');
    await api('mail.forward.save',{...v, targets, edit:!!rule}); toast('Пересылка сохранена','ok'); route();
  }});
}

function mailHost(data, domain) { return domain?.hostname || data.hostname; }
function mailSpamOn(domain, data) { return !!data.spam?.installed && !domain.no_spam; }
function mailWebmailOn(domain, data) { const host = mailHost(data, domain); return !!data.webmail?.ready && (domain.webmail || host === data.webmail.hostname); }
async function mailDomainFeature(data, name, key, on) {
  if (on && key === 'spam' && !data.spam?.installed && !await confirmBox('Установить антиспам Rspamd?', 'Rspamd и отдельный Redis будут установлены на сервер (несколько минут). Проверка включится только для ' + h(name) + '.', { ok: 'Установить' })) return;
  if (on && key === 'webmail' && !data.webmail?.ready && !await confirmBox('Установить веб-почту Roundcube?', 'Roundcube и PHP 8.3 будут установлены на сервер (несколько минут). Адрес: https://' + h(mailHost(data, data.domains.find(d => d.name === name))) + '/', { ok: 'Установить' })) return;
  try { const r = await api('mail.domain.features', { name, [key]: on }); watchJob(r.job, { onComplete: () => route() }); } catch (e) { fail(e); }
}
async function mailAddDomainForm(data) {
  const first = !data.ready;
  // Сервер настроен, а доменов ещё нет: подставляем домен из имени сервера (mail.example.ru → example.ru).
  const suggested = data.ready && !(data.domains || []).length && /^mail\./.test(data.hostname || '') ? data.hostname.slice(5) : '';
  openForm({ title: 'Добавить почтовый домен', ok: first ? 'Добавить и установить' : 'Добавить', fields: [
    { name: 'name', label: 'Домен', value: suggested, placeholder: 'example.ru', required: true, hint: 'Домен адресов: ящики будут вида info@example.ru. Указывайте без mail.' },
    { name: 'hostname', label: 'Имя почтового сервера', placeholder: 'mail.example.ru', hint: 'Если оставить пустым — mail.example.ru для домена example.ru. A-запись этого имени должна указывать на этот сервер: по нему подключаются почтовые программы и открывается веб-почта.' },
    { name: 'ssl', label: 'Сертификат', type: 'seg', value: 'le', options: [['le', 'Let’s Encrypt'], ['self', 'Самоподписанный']] },
    { name: 'email', label: 'E-mail для Let’s Encrypt', value: '', show: v => v.ssl === 'le' },
    { name: 'webmail', label: 'Веб-почта Roundcube', type: 'checkbox', value: true },
    { name: 'spam', label: 'Антиспам Rspamd', type: 'checkbox', value: true },
    { type: 'html', html: first ? '<p class="hint">Это первый домен: панель установит почтовый сервер Postfix, Dovecot и OpenDKIM (SMTP 25, отправка 587, IMAP 993), а затем выбранные веб-почту и антиспам.</p>' : '<p class="hint">Веб-почту и антиспам можно включить и выключить позже переключателями в таблице доменов.</p>' },
  ], submit: async v => {
    const name = v.name.trim().toLowerCase();
    if (name.startsWith('mail.') && !await confirmBox('Домен начинается с mail.', 'Обычно указывают домен адресов (например, ' + h(name.slice(5)) + '), а имя ' + h(name) + ' панель подставит сама. Продолжить с ' + h(name) + '?', { ok: 'Продолжить' })) return;
    const r = await api('mail.domain.create', { name, hostname: v.hostname.trim(), ssl: v.ssl, email: v.ssl === 'le' ? v.email : '', webmail: !!v.webmail, spam: !!v.spam });
    watchJob(r.job, { onComplete: () => route() });
  } });
}
function mailLimit(n) { return n ? fmtBytes(n * 1048576) : 'Без ограничения'; }
function mailSSLLabel(domain, data) {
  const name = domain.certificate || (mailHost(data, domain) === data.hostname ? data.certificate : '');
  const c = data.certs.find(c => c.name === name);
  if (!c) return 'Настроить SSL';
  const days = Math.ceil((Date.parse(c.expires) - Date.now()) / 86400000);
  return days > 0 ? 'SSL · ' + days + ' дн.' : 'SSL истёк';
}
async function mailDomainSettings(domain, data) {
  openForm({title:'Почтовый домен: ' + domain.name, fields:[
    {name:'quota_mb',label:'Общая квота домена, МиБ',type:'number',value:domain.quota_mb || 0,hint:'0 — без ограничения. Сумма выделенных квот всех ящиков не может превышать этот лимит. В таком домене ящики без ограничения запрещены.'},
    {name:'max_boxes',label:'Лимит почтовых ящиков',type:'number',value:domain.max_boxes || 0,hint:'0 — без ограничения. Учитываются и отключённые ящики.'},
    {name:'default_quota_mb',label:'Квота нового ящика по умолчанию, МиБ',type:'number',value:domain.default_quota_mb || 0},
    {name:'catch_all',label:'Catch All — получатель неизвестных адресов',type:'select',value:domain.catch_all || '',options:[['','Выключен'],...data.boxes.filter(b=>b.enabled && b.address.endsWith('@'+domain.name)).map(b=>[b.address,b.address])],hint:'Письма на несуществующие адреса попадут в выбранный ящик. Выберите ящик без пересылки.'},
  ],submit:async v=>{for (const k of ['quota_mb','max_boxes','default_quota_mb']) {v[k]=Number(v[k]);if(!Number.isSafeInteger(v[k])||v[k]<0)throw new Error('Укажите целое неотрицательное число');}await api('mail.domain.save',{...v,name:domain.name});toast('Сохранено','ok');route();}});
}
async function mailDomainSSL(domain, data) {
  openForm({title:'SSL: '+domain.name,fields:[
    {name:'hostname',label:'Имя почтового сервера',value:domain.hostname || 'mail.'+domain.name,required:true,hint:'Это имя для подключения SMTP/IMAP. Его A-запись должна указывать на сервер. Для Let’s Encrypt откройте порт 80.'},
    {name:'mode',label:'Сертификат',type:'seg',value:'le',options:[['le',"Let's Encrypt"],['existing','Установленный сертификат'],['import','Импорт PEM']]},
    {name:'certificate',label:'Выберите сертификат',type:'select',value:domain.certificate || '',options:[['','—'],...data.certs.map(c=>[c.name,c.name])],show:v=>v.mode==='existing'},
    {name:'crt',label:'Сертификат (PEM)',type:'textarea',required:true,show:v=>v.mode==='import'},
    {name:'chain',label:'Цепочка (CA bundle), если есть',type:'textarea',show:v=>v.mode==='import'},
    {name:'key',label:'Закрытый ключ',type:'textarea',required:true,show:v=>v.mode==='import'},
    {name:'email',label:"E-mail для Let's Encrypt",value:'',show:v=>v.mode==='le'},
    {type:'html',html:'<p class="hint">Сертификат применяется к SMTP и IMAP. Продление Let’s Encrypt автоматическое. Для нескольких имён на одном IP почтовый клиент должен поддерживать SNI. HTTPS Roundcube настраивается отдельно.</p>'},
  ],submit:async v=>{
    if(v.mode==='import'){const c=await api('ssl.import',{name:'mail-'+Date.now(),crt:v.crt,chain:v.chain,key:v.key,site:''});v.mode='existing';v.certificate=c.name || c.Name;}
    const r=await api('mail.domain.ssl',{name:domain.name,hostname:v.hostname,mode:v.mode,certificate:v.certificate,email:v.email});watchJob(r.job);
  }});
}
function mailDomainTable(el, data, usage) {
  el.classList.add('mail-compact');
  table(el,{rows:data.domains || [],empty:{icon:'globe',title:'Добавьте почтовый домен'},columns:[
    {t:'Домен',r:d=>`<b>${h(d.name)}</b><small class="muted">${h(mailHost(data,d))}</small>`},
    {t:'Спам-листы',r:d=>`<button class="btn small" data-reputation="${h(d.name)}" title="Проверить домен и IP">MXToolbox</button>`},
    {t:'Использовано / квота',r:d=>{const boxes=data.boxes.filter(b=>b.address.endsWith('@'+d.name));const known=boxes.every(b=>Number.isFinite(usage[b.address]));const used=boxes.reduce((n,b)=>n+(usage[b.address]||0),0);return `<span data-domain-usage="${h(d.name)}">${known?fmtBytes(used):'—'}</span> / ${h(mailLimit(d.quota_mb))}`;}},
    {t:'Ящики',r:d=>data.boxes.filter(b=>b.address.endsWith('@'+d.name)).length+' / '+(d.max_boxes||'∞')},
    {t:'Квота нового ящика',r:d=>h(mailLimit(d.default_quota_mb))},
    {t:'Catch All',r:d=>`<button class="mail-switch ${d.catch_all?'on':''}" role="switch" aria-checked="${!!d.catch_all}" aria-label="Catch All ${h(d.name)}" data-catch="${h(d.name)}" title="${h(d.catch_all || 'Выключен')}"></button>`},
    {t:'SSL',r:d=>`<button class="btn small" data-mail-ssl="${h(d.name)}">${h(mailSSLLabel(d,data))}</button>`},
    {t:'Антиспам',r:d=>{const on=mailSpamOn(d,data);return `<button class="mail-switch ${on?'on':''}" role="switch" aria-checked="${on}" aria-label="Антиспам ${h(d.name)}" data-spam-domain="${h(d.name)}" title="${on?'Входящие проверяются Rspamd':data.spam?.installed?'Без проверки на спам':'Rspamd не установлен — включение установит его'}"></button>`;}},
    {t:'Веб-почта',r:d=>{const host=mailHost(data,d),on=mailWebmailOn(d,data),main=data.webmail?.ready&&host===data.webmail.hostname;return `<span class="row nowrap"><button class="mail-switch ${on?'on':''}" role="switch" aria-checked="${on}" aria-label="Веб-почта ${h(d.name)}" data-webmail-domain="${h(d.name)}" ${main?'disabled':''} title="${main?'Основной адрес Roundcube':on?'https://'+h(host)+'/':data.webmail?.ready?'Включить на https://'+h(host)+'/':'Roundcube не установлен — включение установит его'}"></button>${on?`<a class="btn small" href="https://${h(host)}/" target="_blank" rel="noopener noreferrer">Открыть</a>`:''}</span>`;}},
  ],actions:d=>[
    {t:'Записи DNS',i:'dns',label:'DNS',fn:()=>mailDNS(d.name).catch(fail)},
    {t:'Изменить',i:'edit',fn:()=>mailDomainSettings(d,data)},
    {t:'Удалить домен',i:'trash',danger:true,fn:async()=>{if(!await confirmBox('Удалить почтовый домен?',h(d.name)))return;try{await api('mail.domain.delete',{name:d.name});route();}catch(e){fail(e)}}},
  ]});
  $$('[data-mail-ssl]',el).forEach(b=>b.onclick=()=>mailDomainSSL(data.domains.find(d=>d.name===b.dataset.mailSsl),data));
  $$('[data-reputation]',el).forEach(b=>b.onclick=()=>mailReputation(b.dataset.reputation,b,'mxtoolbox').catch(fail));
  $$('[data-catch]',el).forEach(b=>b.onclick=async()=>{const d=data.domains.find(d=>d.name===b.dataset.catch);if(!d.catch_all){mailDomainSettings(d,data);return;}b.disabled=true;try{await api('mail.domain.save',{...d,catch_all:''});route();}catch(e){fail(e);b.disabled=false;}});
  $$('[data-spam-domain]',el).forEach(b=>b.onclick=()=>mailDomainFeature(data,b.dataset.spamDomain,'spam',!mailSpamOn(data.domains.find(d=>d.name===b.dataset.spamDomain),data)));
  $$('[data-webmail-domain]',el).forEach(b=>b.onclick=()=>mailDomainFeature(data,b.dataset.webmailDomain,'webmail',!mailWebmailOn(data.domains.find(d=>d.name===b.dataset.webmailDomain),data)));
}
function mailMXLinks(targets) {
  return [...new Set(targets.filter(Boolean))].map(target=>`<a class="btn small" href="https://mxtoolbox.com/SuperTool.aspx?action=${encodeURIComponent('blacklist:'+target)}&amp;run=toolpage" target="_blank" rel="noopener noreferrer">${h(target)} ↗</a>`).join('');
}
async function mailMXFallback(name, message) {
  const m=modal(`<h2>MXToolbox</h2><p role="alert">${h(message)}</p><p>Можно проверить домен и IP на сайте MXToolbox без API-ключа. Выберите адрес:</p><div class="toolbar" data-mx-links>${mailMXLinks([name])}</div><p class="hint">Результат откроется в новой вкладке и не будет автоматически перенесён в панель.</p><div class="toolbar"><button class="btn primary" data-mx-settings>Настройки MXToolbox</button><button class="btn" data-close>Закрыть</button></div>`);
  $('[data-close]',m.el).onclick=m.close;
  $('[data-mx-settings]',m.el).onclick=()=>{m.close();mailMXSettings().catch(fail);};
  try{const targets=await api('mail.mxtoolbox.targets',{name},{timeout:7000});if(m.el.isConnected)$('[data-mx-links]',m.el).innerHTML=mailMXLinks(targets);}catch(_){}
}
async function mailMXSettings() {
  const state=await api('mail.mxtoolbox.settings');
  const m=modal(`<h2>MXToolbox</h2><p class="hint">Для проверки внутри панели нужен API-ключ с доступом к Blacklist Lookup (NetworkRequests). Доступ и лимиты зависят от тарифа MXToolbox. Ключ можно получить в своём аккаунте: Settings → API.</p><p>${state.configured?'Ключ сохранён. Для замены введите новый.':'API-ключ пока не настроен.'}</p><form data-mx-form><label class="field"><span>API-ключ MXToolbox</span><input name="key" type="password" autocomplete="off" spellcheck="false" maxlength="64" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"></label><p class="hint">Ключ хранится на сервере и не возвращается в браузер. Проверки запускаются вручную; домен, имя почтового сервера и каждый IP расходуют отдельные запросы. Результат сохраняется на 10 минут.</p><p class="err" role="alert" data-mx-error></p><div class="toolbar"><button class="btn primary" type="submit">Сохранить</button>${state.configured?'<button class="btn danger" type="button" data-mx-remove>Удалить ключ</button>':''}<a class="btn" href="https://api.mxtoolbox.com/api/api-reference" target="_blank" rel="noopener noreferrer">API MXToolbox</a><button class="btn" type="button" data-close>Закрыть</button></div></form>`);
  const form=$('[data-mx-form]',m.el),input=$('input[name="key"]',form),error=$('[data-mx-error]',form);
  const save=async remove=>{
    const key=remove?'':input.value.trim();error.textContent='';
    if(!remove && !key){error.textContent='Введите API-ключ';return;}
    $$('button',form).forEach(b=>b.disabled=true);
    try{await api('mail.mxtoolbox.save',{key,remove});input.value='';toast('Сохранено','ok');m.close();}
    catch(e){error.textContent=e.message;}
    finally{$$('button',form).forEach(b=>b.disabled=false);}
  };
  form.onsubmit=e=>{e.preventDefault();save(false);};
  const remove=$('[data-mx-remove]',m.el);if(remove)remove.onclick=async()=>{if(await confirmBox('Удалить API-ключ MXToolbox?','Проверка через API будет недоступна до добавления ключа.'))save(true);};
  $('[data-close]',m.el).onclick=m.close;
}
async function mailReputation(name, button, provider='spamhaus') {
  const original=button.textContent;button.disabled=true;button.textContent='Проверка…';
  try {
    const mx=provider==='mxtoolbox';
    if(mx && !(await api('mail.mxtoolbox.settings')).configured){button.textContent=original;await mailMXFallback(name,'API-ключ пока не настроен.');return;}
    const r=await api('mail.reputation',{name,provider},{timeout:45000});
    const problems=r.rows.some(x=>x.status==='listed'||x.status==='failed'),unknown=!r.rows.length||r.rows.some(x=>x.status==='unknown');
    button.textContent=problems?'Найдены проблемы':unknown?'Проверка неполная':'Проверка пройдена';
    const info=mx?'Результаты MXToolbox для домена, имени сервера и его публичных IPv4. При повторном открытии в течение 10 минут используется сохранённый результат. IP за NAT укажите в настройках DNS домена.':'Spamhaus ZEN проверяет IP (SBL, XBL, PBL), DBL — домен и имя почтового сервера. Ограничения сервиса и ошибки DNS не означают отсутствие в списке.';
    const m=modal(`<h2>${h(name)} · ${mx?'MXToolbox':'Spamhaus'}</h2><p class="hint">Проверка: ${h(fmtDate(r.checked))}</p><p class="hint">${h(info)}</p>${mx&&unknown?`<div class="mail-mx-fallback"><p>Не все проверки через API завершились. Можно проверить адреса на сайте MXToolbox без API-ключа:</p><div class="toolbar">${mailMXLinks(r.rows.map(x=>x.target))}</div></div>`:''}<div data-results class="mail-compact"></div><p class="hint">Результаты относятся только к проверенным спискам. Предупреждения и таймауты требуют отдельного внимания. Описание проверок MXToolbox показано на языке сервиса.</p><div class="toolbar"><a class="btn" href="${mx?'https://mxtoolbox.com/blacklists.aspx':'https://check.spamhaus.org/'}" target="_blank" rel="noopener noreferrer">${mx?'Открыть MXToolbox':'Spamhaus'}</a>${mx?'<button class="btn" data-mx-settings>Настройки MXToolbox</button><button class="btn" data-spamhaus>Проверить через Spamhaus</button>':''}<button class="btn" data-close>Закрыть</button></div>`,{wide:true});
    table($('[data-results]',m.el),{rows:r.rows,columns:[{t:'Объект',r:x=>h(x.target)},{t:'Список / проверка',r:x=>h(x.list)},{t:'Результат',r:x=>`<span class="tag ${x.status==='listed'||x.status==='failed'?'err':x.status==='clear'?'ok':'warn'}">${h(x.message)}</span>`},{t:'Подробности',r:x=>h(x.details||(x.codes||[]).join(', '))}]});
    $('[data-close]',m.el).onclick=m.close;
    if(mx){$('[data-mx-settings]',m.el).onclick=()=>{m.close();mailMXSettings().catch(fail);};$('[data-spamhaus]',m.el).onclick=e=>mailReputation(name,e.currentTarget,'spamhaus').catch(fail);}
  } catch(e){button.textContent=original;if(provider==='mxtoolbox')await mailMXFallback(name,e.message);else throw e;}finally{button.disabled=false;}
}

function mailMailboxTable(el,data,usage) {
  el.classList.add('mail-compact');
  el.innerHTML='<div class="toolbar mail-box-tools"><input data-mail-search placeholder="Поиск ящика" aria-label="Поиск ящика"><button class="btn small" data-bulk="enable">Включить</button><button class="btn small" data-bulk="disable">Отключить</button><button class="btn small danger" data-bulk="delete">Удалить</button><button class="btn small" data-usage-refresh>Обновить квоты</button></div><div data-box-table></div><div class="toolbar mail-pager"><button class="btn small" data-prev>←</button><span data-page></span><button class="btn small" data-next>→</button><span data-selected class="muted"></span></div>';
  const selected=new Set();let page=0;const size=20;const body=$('[data-box-table]',el);
  const render=()=>{
    const rows=data.boxes.filter(b=>b.address.toLowerCase().includes($('[data-mail-search]',el).value.toLowerCase()));
    page=Math.max(0,Math.min(page,Math.ceil(rows.length/size)-1));const visible=rows.slice(page*size,(page+1)*size);
    table(body,{rows:visible,empty:{icon:'mail',title:'Нет почтовых ящиков'},columns:[
      {t:'',r:b=>`<input type="checkbox" data-select-box="${h(b.address)}" aria-label="${h(b.address)}" ${selected.has(b.address)?'checked':''}>`},
      {t:'Имя пользователя',r:b=>`<b>${h(b.address)}</b>${b.name?`<br><small class="muted">${h(b.name)}</small>`:''}`},
      {t:'Пароль',r:b=>`<button class="btn small" data-copy-password="${h(b.address)}" data-known="${b.has_password?'1':''}" title="Скопировать пароль">•••••••• ${icon('copy')}</button>`},
      {t:'Подключение',r:b=>`<button class="btn small" data-connection="${h(b.address)}">Копировать</button>`},
      {t:'Квота',r:b=>`<span data-box-usage="${h(b.address)}">${Number.isFinite(usage[b.address])?fmtBytes(usage[b.address]):'—'}</span> / ${h(mailLimit(b.quota_mb))}`},
      {t:'Тип',r:()=> 'Почтовый ящик'},
      {t:'Статус',r:b=>`<button class="mail-switch ${b.enabled?'on':''}" role="switch" aria-checked="${b.enabled}" aria-label="${h(b.address)}" data-toggle-box="${h(b.address)}"></button>`},
    ],actions:b=>[
      data.webmail?.ready?{t:'Roundcube',label:'Roundcube',i:'mail',fn:()=>window.open('https://'+data.webmail.hostname+'/','_blank','noopener,noreferrer')}:null,
      {t:'Изменить',i:'edit',fn:()=>mailBoxForm(b)},
      {t:'Удалить',i:'trash',danger:true,fn:()=>removeBoxes([b.address])},
    ]});
    const first=$('thead th',body);if(first){first.innerHTML='<input type="checkbox" data-select-all aria-label="Выбрать страницу">';$('[data-select-all]',body).checked=visible.length>0&&visible.every(b=>selected.has(b.address));$('[data-select-all]',body).onchange=e=>{visible.forEach(b=>e.target.checked?selected.add(b.address):selected.delete(b.address));render();};}
    $$('[data-select-box]',body).forEach(b=>b.onchange=()=>{b.checked?selected.add(b.dataset.selectBox):selected.delete(b.dataset.selectBox);selection();});
    $$('[data-copy-password]',body).forEach(b=>b.onclick=()=>b.dataset.known?mailCopyPassword(b.dataset.copyPassword):toast('Пароль этого ящика панель не знает: задайте новый кнопкой «Изменить» в конце строки — после этого он будет копироваться','warn'));
    $$('[data-connection]',body).forEach(b=>b.onclick=()=>mailCopyConnection(data,data.boxes.find(x=>x.address===b.dataset.connection)).catch(fail));
    $$('[data-toggle-box]',body).forEach(b=>b.onclick=async()=>{b.disabled=true;try{await api('mail.box.save',{address:b.dataset.toggleBox,edit:true,enabled:b.getAttribute('aria-checked')!=='true'});route();}catch(e){fail(e);b.disabled=false;}});
    $('[data-page]',el).textContent=(page+1)+' / '+Math.max(1,Math.ceil(rows.length/size))+' · '+rows.length;
    $('[data-prev]',el).disabled=page===0;$('[data-next]',el).disabled=(page+1)*size>=rows.length;selection();
  };
  const selection=()=>{$$('[data-bulk]',el).forEach(b=>b.disabled=!selected.size);$('[data-selected]',el).textContent=selected.size?'Выбрано: '+selected.size:'';};
  const removeBoxes=async addresses=>{if(!await confirmBox('Удалить выбранные ящики?',addresses.map(h).join('<br>')+'<p>Файлы писем останутся на сервере. Вход и доставка будут отключены.</p>'))return;try{await api('mail.box.bulk',{addresses,action:'delete'});route();}catch(e){fail(e)}};
  $$('[data-bulk]',el).forEach(b=>b.onclick=async()=>{if(b.dataset.bulk==='delete'){await removeBoxes([...selected]);return;}b.disabled=true;try{await api('mail.box.bulk',{addresses:[...selected],action:b.dataset.bulk});route();}catch(e){fail(e);selection();}});
  $('[data-mail-search]',el).oninput=()=>{page=0;render();};$('[data-prev]',el).onclick=()=>{page--;render();};$('[data-next]',el).onclick=()=>{page++;render();};
  $('[data-usage-refresh]',el).onclick=async e=>{const b=e.currentTarget;b.disabled=true;try{await mailLoadUsage(data,usage,el.closest('main')||document,el);}catch(err){fail(err);}finally{b.disabled=false;}};
  render();
}
async function mailLoadUsage(data,usage,domains,boxes) {
  try{Object.assign(usage,await api('mail.usage'));}catch(e){$$('[data-box-usage],[data-domain-usage]',document).forEach(n=>{n.textContent='—';n.title='Не удалось получить использование квоты';});throw e;}
  $$('[data-box-usage]',boxes).forEach(n=>n.textContent=Number.isFinite(usage[n.dataset.boxUsage])?fmtBytes(usage[n.dataset.boxUsage]):'—');
  $$('[data-domain-usage]',domains).forEach(n=>{const b=data.boxes.filter(x=>x.address.endsWith('@'+n.dataset.domainUsage));n.textContent=b.every(x=>Number.isFinite(usage[x.address]))?fmtBytes(b.reduce((v,x)=>v+usage[x.address],0)):'—';});
}
async function mailCopyConnection(data,box) {
  const domain=data.domains.find(d=>box.address.endsWith('@'+d.name));
  const host=mailHost(data,domain);
  const text='Login: '+box.address+'\nIMAP: '+host+':993 (TLS)\nSMTP: '+host+':587 (STARTTLS)'+(data.webmail?.ready?'\nRoundcube: https://'+data.webmail.hostname+'/':'');
  try{await navigator.clipboard.writeText(text);toast('Скопировано','ok');}catch(e){showOutput('Параметры подключения',text);}
}
