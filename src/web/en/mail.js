// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

async function mailSetupForm(data) {
  const d = data || await api('mail.list');
  openForm({ title: d.ready ? "Mail server settings" : "Configure mail server", ok: d.ready ? "Apply" : "Install", fields: [
    { name: 'hostname', label: "Mail server hostname", value: d.hostname || '', placeholder: 'mail.example.ru', required: true },
    { name: 'certificate', label: "TLS certificate for this hostname", type: 'select', value: d.certificate || '@le', options: mailCertOptions(d, "Self-signed (for initial testing)"), hint: "Let’s Encrypt is issued automatically: the name’s A record must point to this server and port 80 must be open. You can also choose a certificate from SSL — it must include the server name." },
    { name: 'email', label: "Let’s Encrypt email", value: '', show: v => v.certificate === '@le' },
    { type: 'html', html: d.ready
      ? "<p class=\"muted\">The server’s common name: Postfix introduces itself to other mail servers with it, and your IP’s PTR record should point to it. Each mail domain has its own mail name and certificate — use the SSL button in the domain table. To add a domain, use “Add domain”.</p>"
      : "<p class=\"muted\">Postfix, Dovecot and OpenDKIM will be installed. SMTP: 25, authenticated submission: 587 STARTTLS, IMAP: 993 TLS. An existing separate mail server is not migrated automatically.</p>" },
  ], submit: async v => { const r = await api('mail.setup', mailCertRequest(v)); watchJob(r.job); } });
}
// '@le' в списке сертификатов — выпустить Let's Encrypt на указанное имя при установке.
function mailCertOptions(d, selfLabel) {
  return [['@le', "Issue Let’s Encrypt certificate"], ['', selfLabel], ...d.certs.map(c => [c.name, c.name])];
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
  openForm({ title: box ? box.address : "Add mailbox", ok: box ? "Save" : "Add", fields: [
    { name: 'name', label: "Name", value: box?.name || '', placeholder: "John Smith", hint: "Optional: who uses the mailbox." },
    ...(box ? [{ name: 'address', label: "Email address", value: box.address, readonly: true }] : [
      { name: 'local', label: "Email address", placeholder: 'info', required: true },
      { name: 'domain', label: "@ domain", type: 'select', value: domains[0] || extra[0], options: [...domains.map(x => [x, x]), ...extra.map(x => [x, x + " — will be added to mail"])] },
    ]),
    { name: 'password', label: box ? "New password (leave blank to keep)" : "Password", type: 'password', gen: true, value: '', hint: "At least 12 characters. A password is generated automatically for a new mailbox if left blank." },
    { name: 'limit', label: "Limit mailbox size (quota)", type: 'checkbox', value: startQuota > 0 },
    { name: 'quota', label: "Quota", type: 'number', value: startQuota > 0 ? (gb ? startQuota / 1024 : startQuota) : 5, show: (v) => v.limit, hint: "When the mailbox is full, new mail is not delivered; existing messages are kept." },
    { name: 'unit', label: "Unit", type: 'select', value: startQuota > 0 && !gb ? 'MB' : 'GB', options: [['GB', "GB"], ['MB', "MB"]], show: (v) => v.limit },
    { name: 'enabled', label: "Status: mailbox enabled", type: 'checkbox', value: box ? box.enabled : true },
  ], onChange: (v, key) => { if (!box && key === 'domain') { const q = defQuota(v.domain); v.limit = q > 0; if (q > 0) { v.unit = q % 1024 === 0 ? 'GB' : 'MB'; v.quota = q % 1024 === 0 ? q / 1024 : q; } } }, submit: async v => {
    const address = box ? box.address : v.local + '@' + v.domain;
    const size = Number(v.quota);
    const quota = v.limit ? (v.unit === 'GB' ? size * 1024 : size) : 0;
    if (v.limit && (!Number.isFinite(size) || size <= 0 || !Number.isInteger(quota))) throw new Error("Quota: a positive number; a whole number in megabytes");
    if (!Number.isInteger(quota) || quota < 0 || quota > 1048576) throw new Error("Quota: up to 1024 GB");
    if (!box && !domains.includes(v.domain)) await api('mail.domain.add', { name: v.domain });
    const r = await api('mail.box.save', { address, name: v.name, password: v.password, enabled: !!v.enabled, edit: !!box, quota_mb: quota });
    if (!box && !v.enabled) await api('mail.box.save', { address, edit: true, enabled: false, quota_mb: quota });
    await route();
    if (r.password) {
      // Сервер для программ — mail-имя домена ящика (у нового домена — mail.<домен>).
      const fresh = await api('mail.list'), dom = (fresh.domains || []).find(x => x.name === address.split('@')[1]);
      const host = dom ? mailHost(fresh, dom) : fresh.hostname;
      const noCert = dom && dom.hostname && dom.hostname !== fresh.hostname && !dom.certificate;
      showSecrets("Mailbox details", [["Username", r.address], ["Password", r.password], ["Server", host]], "Save the password. Connection: IMAP 993 TLS; SMTP 587 STARTTLS." + (noCert ? " For " + host + " there is no certificate yet: issue it with the domain’s SSL button once the A record of " + host + " points to this server." : ''));
    }
    else toast("Saved", 'ok');
  } });
}
async function mailAliasForm(alias) {
  const d = await api('mail.list');
  if (!d.boxes.length) { toast("Create a mailbox first", 'err'); return; }
  openForm({ title: alias ? "Edit alias" : "Create alias", fields: [
    { name: 'address', label: "Alias address", value: alias?.address || '', placeholder: 'sales@example.ru', readonly: !!alias, required: true },
    { name: 'target', label: "Deliver to mailbox", type: 'select', options: d.boxes.map(b => [b.address, b.address]), value: alias?.target || d.boxes[0].address, hint: "An alias has no separate password. Mail is delivered to the selected mailbox." },
  ], submit: async v => { await api('mail.alias.save', { ...v, edit: !!alias }); toast("Alias saved", 'ok'); route(); } });
}
page('mail', {
  title: "Mail",
  async render(view, params) {
    const d = await api('mail.list');
    head(view, "Mail", { actions: d.ready ? [
      { t: "Add domain", i: 'plus', primary: true, fn: () => mailAddDomainForm(d) },
      { t: "Add mailbox", i: 'mail', fn: () => mailBoxForm() },
      { t: "Alias", i: 'mail', fn: () => mailAliasForm() },
      { t: "Server", i: 'settings', fn: () => mailSetupForm(d) },
      { t: 'MXToolbox', i: 'settings', fn: () => mailMXSettings().catch(fail) },
      { t: "Tasks", i: 'clock', fn: () => go('jobs') },
    ] : [
      { t: "Add domain", i: 'plus', primary: true, fn: () => mailAddDomainForm(d) },
      { t: "Tasks", i: 'clock', fn: () => go('jobs') },
    ] });
    if (!d.ready) {
      const p = panel(view, "Mail domains"); p.innerHTML = "<div class=\"empty\"><b>No mail domains yet</b><p>Click “Add domain” and enter a domain such as example.com. The panel installs the mail server (Postfix, Dovecot, OpenDKIM), issues a certificate for mail.example.com and, if you choose, enables Roundcube webmail and antispam. Progress is shown in Jobs.</p></div>"; return;
    }
    const spam = d.spam || {}, webmail = d.webmail || {};
    const status = panel(view);
    const chip = (ok, name) => `<span class="tag ${ok ? 'ok' : 'err'}">${h(name)}: ${ok ? "running" : "stopped"}</span>`;
    status.innerHTML = `<div class="row">${chip(d.smtp, 'SMTP')}${chip(d.imap, 'IMAP')}${chip(d.dkim, 'DKIM')}${spam.installed ? chip(d.spam_active, "Antispam") : ''}</div><p><b>Mail server: ${h(d.hostname)}</b></p><p class="hint">Mail clients: IMAP 993 TLS · SMTP 587 STARTTLS · login is the full mailbox address, server is the domain’s mail name. Webmail and antispam are enabled per domain in the table below.${d.certificate ? '' : " Server " + h(d.hostname) + " uses a self-signed certificate: issue Let’s Encrypt with the domain’s SSL button or in “Server”."}</p><div class="toolbar">${spam.installed ? "<button class=\"btn small\" data-spam>Antispam thresholds</button>" : ''}${webmail.ready ? "<button class=\"btn small\" data-webmail>Main Roundcube address</button>" : ''}</div>`;
    const spamBtn = $('[data-spam]', status), webBtn = $('[data-webmail]', status);
    if (spamBtn) spamBtn.onclick = () => mailSpamForm(d);
    if (webBtn) webBtn.onclick = () => mailWebmailForm(d);
    const domains = panel(view, "Mail domains");
    const p = panel(view, "Mailboxes");
    const usage = {};
    const refreshUsage = () => mailLoadUsage(d, usage, domains, p);
    mailDomainTable(domains, d, usage);
    mailMailboxTable(p, d, usage);
    refreshUsage().catch(() => {});
    const forwards = panel(view, "Forwarding");
    const createForward = document.createElement('button');
    createForward.className = 'btn'; createForward.textContent = "Create forwarding";
    createForward.onclick = () => mailForwardForm();
    table(forwards, { rows: d.forwards || [], empty: { icon: 'mail', title: "No forwarding rules yet", text: "<p>Forward mail to one or more addresses, optionally keeping a copy.</p>" }, columns: [
      { t: "From", r: f => h(f.address) },
      { t: "Recipients", r: f => f.targets.map(h).join('<br>') },
      { t: "Keep mailbox copy", r: f => f.keep_copy ? "Yes" : "No" },
      { t: "Status", r: f => { const box = d.boxes.find(b => b.address === f.address); return !f.enabled ? "<span class=\"tag warn\">Disabled</span>" : box && !box.enabled ? "<span class=\"tag warn\">Mailbox disabled</span>" : "<span class=\"tag ok\">Enabled</span>"; } },
    ], onRow: mailForwardForm, actions: f => [
      { t: "Edit", i: 'edit', fn: () => mailForwardForm(f) },
      { t: f.enabled ? "Disable" : "Enable", i: f.enabled ? 'stop' : 'play', fn: async () => { try { await api('mail.forward.save', {...f, enabled: !f.enabled, edit: true}); route(); } catch(e) { fail(e); } } },
      { t: "Delete", i: 'trash', danger: true, fn: async () => { if (!await confirmBox("Delete forwarding?", h(f.address))) return; try { await api('mail.forward.delete', {address: f.address}); route(); } catch(e) { fail(e); } } },
    ] });
    forwards.prepend(createForward);
    const aliases = panel(view, "Aliases");
    table(aliases, { rows: d.aliases || [], empty: { icon: 'mail', title: "No aliases yet" }, columns: [{ t: "Address", r: a => h(a.address) }, { t: "Mailbox delivery", r: a => h(a.target) }], actions: a => [
      { t: "Edit", i: 'edit', fn: () => mailAliasForm(a) },
      { t: "Delete", i: 'trash', danger: true, fn: async () => { if (!await confirmBox("Delete alias?", h(a.address))) return; try { await api('mail.alias.delete', { address: a.address }); route(); } catch (e) { fail(e); } } },
    ] });
    if (params.new) await mailBoxForm();
    if (params.domain) await mailAddDomainForm(d);
  },
});

async function mailQuotaUsage(box) {
  const q = await api('mail.quota', {address: box.address});
  const limit = q.quota_mb ? (q.quota_mb + " MiB") : "unlimited";
  showOutput("Quota: " + box.address, "Used: " + fmtBytes(q.used) + "\nLimit: " + limit + (q.quota_mb ? "\nUsed: " + Math.round(q.used / (q.quota_mb * 1048576) * 100) + '%' : ''));
}
async function mailSpamForm(data) {
  const s = data.spam || {};
  openForm({title: "Rspamd anti-spam", ok: s.installed ? "Apply" : "Install", fields: [
    {name:'enabled',label:"Filtering enabled",type:'checkbox',value:s.installed ? !!s.enabled : true},
    {name:'mark',label:"Spam tagging score",type:'number',value:s.mark || 6},
    {name:'reject',label:"Reject score",type:'number',value:s.reject || 15},
    {type:'html',html:"<p class=\"hint\">Suspicious messages receive check headers and a [SPAM] subject tag. Clear spam is rejected before delivery. The rejection threshold must exceed the tagging threshold. If the filter is unavailable, senders receive a temporary error to retry delivery. Automatic moving to Spam and antivirus attachment scanning are not included.</p>"},
  ],submit:async v=>{
    v.mark=Number(v.mark);v.reject=Number(v.reject);
    if(!Number.isInteger(v.mark)||!Number.isInteger(v.reject)||v.mark<1||v.reject<=v.mark||v.reject>100)throw new Error("Use integer thresholds: 1 ≤ tagging < rejection ≤ 100");
    const r=await api('mail.spam.setup',v);watchJob(r.job);
  }});
}
async function mailWebmailForm(data) {
  const w=data.webmail || {};
  openForm({title:"Roundcube webmail",ok:w.ready ? "Apply" : "Install",fields:[
    {name:'hostname',label:"Webmail address",value:w.hostname || data.hostname || '',placeholder:'mail.example.ru',required:true,hint:"A hostname separate from the panel and sites. Its DNS record must point to this server. You can use the mail server hostname."},
    {name:'certificate',label:"Webmail TLS certificate",type:'select',value:w.ready ? w.certificate : (data.certificate || '@le'),options:mailCertOptions(data,"Self-signed (for testing)"),hint:"Let’s Encrypt is issued automatically: the address’s A record must point to this server and port 80 must be open. If the address matches the mail server name, you can choose its certificate. A self-signed certificate triggers a browser warning."},
    {name:'email',label:"Let’s Encrypt email",value:'',show:v=>v.certificate==='@le'},
    {type:'html',html:"<p class=\"hint\">Roundcube and PHP 8.3 will be installed. Sign in with your full email address and password. Contacts and settings use a separate SQLite database; messages remain in Dovecot. Internal directories cannot be downloaded.</p>"},
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
    toast("Password copied", 'ok');
  } catch (e) {
    // Браузер не дал доступ к буферу — показываем пароль в окне с кнопкой копирования.
    let password;
    try { password = await fetchPassword(); } catch (err) { fail(err); return; }
    showSecrets("Mailbox password", [["Username", address], ["Password", password]], "The browser did not allow automatic copying — copy the password with the button.");
  }
}

async function mailForwardForm(rule) {
  const d = await api('mail.list');
  if (!d.ready) { await mailSetupForm(d); return; }
  if (!d.domains?.length) { await mailAddDomainForm(await api('mail.list')); return; }
  openForm({title:rule ? "Edit forwarding" : "Create forwarding",fields:[
    {name:'address',label:"Forwarding address",value:rule?.address || '',placeholder:'info@' + d.domains[0].name,readonly:!!rule,required:true,hint:"An address in your mail domain: an existing mailbox or a separate address without a password."},
    {name:'targets',label:"Recipients",type:'textarea',value:rule?.targets.join('\n') || '',required:true,hint:"Enter 1 to 20 addresses, separated by spaces, commas or newlines. External addresses are allowed."},
    {name:'keep_copy',label:"Keep a copy in the mailbox",type:'checkbox',value:!!rule?.keep_copy,hint:"Requires an existing mailbox at the forwarding address. If unchecked, mail is delivered only to the recipients."},
    {name:'enabled',label:"Forwarding enabled",type:'checkbox',value:rule ? rule.enabled : true},
    {type:'html',html:"<p class=\"hint\">When forwarding is disabled, an existing mailbox receives mail normally. Disabling the mailbox also disables its forwarding. Delivery to external addresses depends on recipient server policies, including SPF/DMARC.</p>"},
  ],submit:async v=>{
    const targets = String(v.targets || '').trim().split(/[\s,;]+/).filter(Boolean);
    if (!targets.length || targets.length > 20) throw new Error("Enter 1 to 20 recipients");
    await api('mail.forward.save',{...v, targets, edit:!!rule}); toast("Forwarding saved",'ok'); route();
  }});
}

function mailHost(data, domain) { return domain?.hostname || data.hostname; }
function mailSpamOn(domain, data) { return !!data.spam?.installed && !domain.no_spam; }
function mailWebmailOn(domain, data) { const host = mailHost(data, domain); return !!data.webmail?.ready && (domain.webmail || host === data.webmail.hostname); }
async function mailDomainFeature(data, name, key, on) {
  if (on && key === 'spam' && !data.spam?.installed && !await confirmBox("Install Rspamd antispam?", "Rspamd and a dedicated Redis will be installed (a few minutes). Filtering will be enabled only for " + h(name) + '.', { ok: "Install" })) return;
  if (on && key === 'webmail' && !data.webmail?.ready && !await confirmBox("Install Roundcube webmail?", "Roundcube and PHP 8.3 will be installed (a few minutes). Address: https://" + h(mailHost(data, data.domains.find(d => d.name === name))) + '/', { ok: "Install" })) return;
  try { const r = await api('mail.domain.features', { name, [key]: on }); watchJob(r.job, { onComplete: () => route() }); } catch (e) { fail(e); }
}
async function mailAddDomainForm(data) {
  const first = !data.ready;
  // Сервер настроен, а доменов ещё нет: подставляем домен из имени сервера (mail.example.ru → example.ru).
  const suggested = data.ready && !(data.domains || []).length && /^mail\./.test(data.hostname || '') ? data.hostname.slice(5) : '';
  openForm({ title: "Add mail domain", ok: first ? "Add and install" : "Add", fields: [
    { name: 'name', label: "Domain", value: suggested, placeholder: 'example.ru', required: true, hint: "The address domain: mailboxes will look like info@example.com. Enter it without mail." },
    { name: 'hostname', label: "Mail server hostname", placeholder: 'mail.example.ru', hint: "Leave empty for mail.example.com when the domain is example.com. Its A record must point to this server: mail clients connect to it and webmail opens on it." },
    { name: 'ssl', label: "Certificate", type: 'seg', value: 'le', options: [['le', 'Let’s Encrypt'], ['self', "Self-signed"]] },
    { name: 'email', label: "Let’s Encrypt email", value: '', show: v => v.ssl === 'le' },
    { name: 'webmail', label: "Roundcube webmail", type: 'checkbox', value: true },
    { name: 'spam', label: "Rspamd anti-spam", type: 'checkbox', value: true },
    { type: 'html', html: first ? "<p class=\"hint\">This is the first domain: the panel installs the Postfix, Dovecot and OpenDKIM mail server (SMTP 25, submission 587, IMAP 993), then the selected webmail and antispam.</p>" : "<p class=\"hint\">Webmail and antispam can be switched on and off later in the domain table.</p>" },
  ], submit: async v => {
    const name = v.name.trim().toLowerCase();
    if (name.startsWith('mail.') && !await confirmBox("The domain starts with mail.", "Usually you enter the address domain (for example, " + h(name.slice(5)) + "), and the panel adds the name " + h(name) + " by itself. Continue with " + h(name) + '?', { ok: "Resume" })) return;
    const r = await api('mail.domain.create', { name, hostname: v.hostname.trim(), ssl: v.ssl, email: v.ssl === 'le' ? v.email : '', webmail: !!v.webmail, spam: !!v.spam });
    watchJob(r.job, { onComplete: () => route() });
  } });
}
function mailLimit(n) { return n ? fmtBytes(n * 1048576) : "Unlimited"; }
function mailSSLLabel(domain, data) {
  const name = domain.certificate || (mailHost(data, domain) === data.hostname ? data.certificate : '');
  const c = data.certs.find(c => c.name === name);
  if (!c) return "Set up SSL";
  const days = Math.ceil((Date.parse(c.expires) - Date.now()) / 86400000);
  return days > 0 ? 'SSL · ' + days + " days" : "SSL expired";
}
async function mailDomainSettings(domain, data) {
  openForm({title:"Mail domain: " + domain.name, fields:[
    {name:'quota_mb',label:"Domain quota, MiB",type:'number',value:domain.quota_mb || 0,hint:"0 means unlimited. The sum of allocated mailbox quotas must fit within this limit. Unlimited mailboxes are not allowed in a domain with a quota."},
    {name:'max_boxes',label:"Mailbox limit",type:'number',value:domain.max_boxes || 0,hint:"0 means unlimited. Disabled mailboxes also count."},
    {name:'default_quota_mb',label:"Default mailbox quota, MiB",type:'number',value:domain.default_quota_mb || 0},
    {name:'catch_all',label:"Catch All recipient for unknown addresses",type:'select',value:domain.catch_all || '',options:[['',"Off"],...data.boxes.filter(b=>b.enabled && b.address.endsWith('@'+domain.name)).map(b=>[b.address,b.address])],hint:"Messages to unknown addresses will be delivered to this mailbox. Choose a mailbox without forwarding."},
  ],submit:async v=>{for (const k of ['quota_mb','max_boxes','default_quota_mb']) {v[k]=Number(v[k]);if(!Number.isSafeInteger(v[k])||v[k]<0)throw new Error("Enter a non-negative integer");}await api('mail.domain.save',{...v,name:domain.name});toast("Saved",'ok');route();}});
}
async function mailDomainSSL(domain, data) {
  openForm({title:'SSL: '+domain.name,fields:[
    {name:'hostname',label:"Mail server hostname",value:domain.hostname || 'mail.'+domain.name,required:true,hint:"Use this hostname for SMTP/IMAP. Its A record must point to this server. Open port 80 for Let’s Encrypt."},
    {name:'mode',label:"Certificate",type:'seg',value:'le',options:[['le',"Let's Encrypt"],['existing',"Installed certificate"],['import',"Import PEM"]]},
    {name:'certificate',label:"Select certificate",type:'select',value:domain.certificate || '',options:[['','—'],...data.certs.map(c=>[c.name,c.name])],show:v=>v.mode==='existing'},
    {name:'crt',label:"Certificate (PEM)",type:'textarea',required:true,show:v=>v.mode==='import'},
    {name:'chain',label:"Chain (CA bundle), if available",type:'textarea',show:v=>v.mode==='import'},
    {name:'key',label:"Private key",type:'textarea',required:true,show:v=>v.mode==='import'},
    {name:'email',label:"E-mail for Let's Encrypt",value:'',show:v=>v.mode==='le'},
    {type:'html',html:"<p class=\"hint\">The certificate is applied to SMTP and IMAP. Let’s Encrypt renewal is automatic. Multiple hostnames on one IP require an SNI-capable mail client. Roundcube HTTPS is configured separately.</p>"},
  ],submit:async v=>{
    if(v.mode==='import'){const c=await api('ssl.import',{name:'mail-'+Date.now(),crt:v.crt,chain:v.chain,key:v.key,site:''});v.mode='existing';v.certificate=c.name || c.Name;}
    const r=await api('mail.domain.ssl',{name:domain.name,hostname:v.hostname,mode:v.mode,certificate:v.certificate,email:v.email});watchJob(r.job);
  }});
}
function mailDomainTable(el, data, usage) {
  el.classList.add('mail-compact');
  table(el,{rows:data.domains || [],empty:{icon:'globe',title:"Add a mail domain"},columns:[
    {t:"Domain",r:d=>`<b>${h(d.name)}</b><small class="muted">${h(mailHost(data,d))}</small>`},
    {t:"Blocklists",r:d=>`<button class="btn small" data-reputation="${h(d.name)}" title="Check domain and IP">MXToolbox</button>`},
    {t:"Used / quota",r:d=>{const boxes=data.boxes.filter(b=>b.address.endsWith('@'+d.name));const known=boxes.every(b=>Number.isFinite(usage[b.address]));const used=boxes.reduce((n,b)=>n+(usage[b.address]||0),0);return `<span data-domain-usage="${h(d.name)}">${known?fmtBytes(used):'—'}</span> / ${h(mailLimit(d.quota_mb))}`;}},
    {t:"Mailboxes",r:d=>data.boxes.filter(b=>b.address.endsWith('@'+d.name)).length+' / '+(d.max_boxes||'∞')},
    {t:"Default mailbox quota",r:d=>h(mailLimit(d.default_quota_mb))},
    {t:'Catch All',r:d=>`<button class="mail-switch ${d.catch_all?'on':''}" role="switch" aria-checked="${!!d.catch_all}" aria-label="Catch All ${h(d.name)}" data-catch="${h(d.name)}" title="${h(d.catch_all || "Off")}"></button>`},
    {t:'SSL',r:d=>`<button class="btn small" data-mail-ssl="${h(d.name)}">${h(mailSSLLabel(d,data))}</button>`},
    {t:"Antispam",r:d=>{const on=mailSpamOn(d,data);return `<button class="mail-switch ${on?'on':''}" role="switch" aria-checked="${on}" aria-label="Antispam ${h(d.name)}" data-spam-domain="${h(d.name)}" title="${on?"Incoming mail is checked by Rspamd":data.spam?.installed?"No spam filtering":"Rspamd is not installed — switching on installs it"}"></button>`;}},
    {t:"Webmail",r:d=>{const host=mailHost(data,d),on=mailWebmailOn(d,data),main=data.webmail?.ready&&host===data.webmail.hostname;return `<span class="row nowrap"><button class="mail-switch ${on?'on':''}" role="switch" aria-checked="${on}" aria-label="Webmail ${h(d.name)}" data-webmail-domain="${h(d.name)}" ${main?'disabled':''} title="${main?"Main Roundcube address":on?'https://'+h(host)+'/':data.webmail?.ready?"Enable at https://"+h(host)+'/':"Roundcube is not installed — switching on installs it"}"></button>${on?`<a class="btn small" href="https://${h(host)}/" target="_blank" rel="noopener noreferrer">Open</a>`:''}</span>`;}},
  ],actions:d=>[
    {t:"DNS records",i:'dns',label:'DNS',fn:()=>mailDNS(d.name).catch(fail)},
    {t:"Edit",i:'edit',fn:()=>mailDomainSettings(d,data)},
    {t:"Delete domain",i:'trash',danger:true,fn:async()=>{if(!await confirmBox("Delete mail domain?",h(d.name)))return;try{await api('mail.domain.delete',{name:d.name});route();}catch(e){fail(e)}}},
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
  const m=modal(`<h2>MXToolbox</h2><p role="alert">${h(message)}</p><p>You can check the domain and IP on the MXToolbox website without an API key. Select an address:</p><div class="toolbar" data-mx-links>${mailMXLinks([name])}</div><p class="hint">The result opens in a new tab and is not automatically imported into the panel.</p><div class="toolbar"><button class="btn primary" data-mx-settings>MXToolbox settings</button><button class="btn" data-close>Close</button></div>`);
  $('[data-close]',m.el).onclick=m.close;
  $('[data-mx-settings]',m.el).onclick=()=>{m.close();mailMXSettings().catch(fail);};
  try{const targets=await api('mail.mxtoolbox.targets',{name},{timeout:7000});if(m.el.isConnected)$('[data-mx-links]',m.el).innerHTML=mailMXLinks(targets);}catch(_){}
}
async function mailMXSettings() {
  const state=await api('mail.mxtoolbox.settings');
  const m=modal(`<h2>MXToolbox</h2><p class="hint">In-panel checks require an API key with Blacklist Lookup access (NetworkRequests). Availability and limits depend on your MXToolbox plan. Find your key in your account: Settings → API.</p><p>${state.configured?"A key is saved. Enter a new key to replace it.":"No API key has been configured."}</p><form data-mx-form><label class="field"><span>MXToolbox API key</span><input name="key" type="password" autocomplete="off" spellcheck="false" maxlength="64" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"></label><p class="hint">The key is stored on the server and is never returned to the browser. Checks run manually; the domain, mail hostname and each IP use separate requests. Results are cached for 10 minutes.</p><p class="err" role="alert" data-mx-error></p><div class="toolbar"><button class="btn primary" type="submit">Save</button>${state.configured?"<button class=\"btn danger\" type=\"button\" data-mx-remove>Remove key</button>":''}<a class="btn" href="https://api.mxtoolbox.com/api/api-reference" target="_blank" rel="noopener noreferrer">API MXToolbox</a><button class="btn" type="button" data-close>Close</button></div></form>`);
  const form=$('[data-mx-form]',m.el),input=$('input[name="key"]',form),error=$('[data-mx-error]',form);
  const save=async remove=>{
    const key=remove?'':input.value.trim();error.textContent='';
    if(!remove && !key){error.textContent="Enter an API key";return;}
    $$('button',form).forEach(b=>b.disabled=true);
    try{await api('mail.mxtoolbox.save',{key,remove});input.value='';toast("Saved",'ok');m.close();}
    catch(e){error.textContent=e.message;}
    finally{$$('button',form).forEach(b=>b.disabled=false);}
  };
  form.onsubmit=e=>{e.preventDefault();save(false);};
  const remove=$('[data-mx-remove]',m.el);if(remove)remove.onclick=async()=>{if(await confirmBox("Remove the MXToolbox API key?","API checks will be unavailable until a key is added."))save(true);};
  $('[data-close]',m.el).onclick=m.close;
}
async function mailReputation(name, button, provider='spamhaus') {
  const original=button.textContent;button.disabled=true;button.textContent="Checking…";
  try {
    const mx=provider==='mxtoolbox';
    if(mx && !(await api('mail.mxtoolbox.settings')).configured){button.textContent=original;await mailMXFallback(name,"No API key has been configured.");return;}
    const r=await api('mail.reputation',{name,provider},{timeout:45000});
    const problems=r.rows.some(x=>x.status==='listed'||x.status==='failed'),unknown=!r.rows.length||r.rows.some(x=>x.status==='unknown');
    button.textContent=problems?"Issues found":unknown?"Check incomplete":"Check passed";
    const info=mx?"MXToolbox results for the domain, mail hostname and its public IPv4 addresses. Reopening within 10 minutes uses the cached result. Set the IP behind NAT in the domain DNS settings.":"Spamhaus ZEN checks IPs (SBL, XBL, PBL); DBL checks the domain and mail hostname. Service restrictions and DNS errors do not mean an address is unlisted.";
    const m=modal(`<h2>${h(name)} · ${mx?'MXToolbox':'Spamhaus'}</h2><p class="hint">Checked: ${h(fmtDate(r.checked))}</p><p class="hint">${h(info)}</p>${mx&&unknown?`<div class="mail-mx-fallback"><p>Some API checks could not be completed. You can check these addresses on the MXToolbox website without an API key:</p><div class="toolbar">${mailMXLinks(r.rows.map(x=>x.target))}</div></div>`:''}<div data-results class="mail-compact"></div><p class="hint">Results apply only to the checked lists. Warnings and timeouts require attention. MXToolbox test descriptions are shown in the service’s language.</p><div class="toolbar"><a class="btn" href="${mx?'https://mxtoolbox.com/blacklists.aspx':'https://check.spamhaus.org/'}" target="_blank" rel="noopener noreferrer">${mx?"Open MXToolbox":'Spamhaus'}</a>${mx?"<button class=\"btn\" data-mx-settings>MXToolbox settings</button><button class=\"btn\" data-spamhaus>Check with Spamhaus</button>":''}<button class="btn" data-close>Close</button></div>`,{wide:true});
    table($('[data-results]',m.el),{rows:r.rows,columns:[{t:"Object",r:x=>h(x.target)},{t:"List / test",r:x=>h(x.list)},{t:"Result",r:x=>`<span class="tag ${x.status==='listed'||x.status==='failed'?'err':x.status==='clear'?'ok':'warn'}">${h(x.message)}</span>`},{t:"Details",r:x=>h(x.details||(x.codes||[]).join(', '))}]});
    $('[data-close]',m.el).onclick=m.close;
    if(mx){$('[data-mx-settings]',m.el).onclick=()=>{m.close();mailMXSettings().catch(fail);};$('[data-spamhaus]',m.el).onclick=e=>mailReputation(name,e.currentTarget,'spamhaus').catch(fail);}
  } catch(e){button.textContent=original;if(provider==='mxtoolbox')await mailMXFallback(name,e.message);else throw e;}finally{button.disabled=false;}
}

function mailMailboxTable(el,data,usage) {
  el.classList.add('mail-compact');
  el.innerHTML="<div class=\"toolbar mail-box-tools\"><input data-mail-search placeholder=\"Search mailboxes\" aria-label=\"Search mailboxes\"><button class=\"btn small\" data-bulk=\"enable\">Enable</button><button class=\"btn small\" data-bulk=\"disable\">Disable</button><button class=\"btn small danger\" data-bulk=\"delete\">Delete</button><button class=\"btn small\" data-usage-refresh>Refresh usage</button></div><div data-box-table></div><div class=\"toolbar mail-pager\"><button class=\"btn small\" data-prev>←</button><span data-page></span><button class=\"btn small\" data-next>→</button><span data-selected class=\"muted\"></span></div>";
  const selected=new Set();let page=0;const size=20;const body=$('[data-box-table]',el);
  const render=()=>{
    const rows=data.boxes.filter(b=>b.address.toLowerCase().includes($('[data-mail-search]',el).value.toLowerCase()));
    page=Math.max(0,Math.min(page,Math.ceil(rows.length/size)-1));const visible=rows.slice(page*size,(page+1)*size);
    table(body,{rows:visible,empty:{icon:'mail',title:"No mailboxes"},columns:[
      {t:'',r:b=>`<input type="checkbox" data-select-box="${h(b.address)}" aria-label="${h(b.address)}" ${selected.has(b.address)?'checked':''}>`},
      {t:"Username",r:b=>`<b>${h(b.address)}</b>${b.name?`<br><small class="muted">${h(b.name)}</small>`:''}`},
      {t:"Password",r:b=>`<button class="btn small" data-copy-password="${h(b.address)}" data-known="${b.has_password?'1':''}" title="Copy password">•••••••• ${icon('copy')}</button>`},
      {t:"Connection",r:b=>`<button class="btn small" data-connection="${h(b.address)}">Copy</button>`},
      {t:"Quota",r:b=>`<span data-box-usage="${h(b.address)}">${Number.isFinite(usage[b.address])?fmtBytes(usage[b.address]):'—'}</span> / ${h(mailLimit(b.quota_mb))}`},
      {t:"Type",r:()=> "Mailbox"},
      {t:"Status",r:b=>`<button class="mail-switch ${b.enabled?'on':''}" role="switch" aria-checked="${b.enabled}" aria-label="${h(b.address)}" data-toggle-box="${h(b.address)}"></button>`},
    ],actions:b=>[
      data.webmail?.ready?{t:'Roundcube',label:'Roundcube',i:'mail',fn:()=>window.open('https://'+data.webmail.hostname+'/','_blank','noopener,noreferrer')}:null,
      {t:"Edit",i:'edit',fn:()=>mailBoxForm(b)},
      {t:"Delete",i:'trash',danger:true,fn:()=>removeBoxes([b.address])},
    ]});
    const first=$('thead th',body);if(first){first.innerHTML="<input type=\"checkbox\" data-select-all aria-label=\"Select this page\">";$('[data-select-all]',body).checked=visible.length>0&&visible.every(b=>selected.has(b.address));$('[data-select-all]',body).onchange=e=>{visible.forEach(b=>e.target.checked?selected.add(b.address):selected.delete(b.address));render();};}
    $$('[data-select-box]',body).forEach(b=>b.onchange=()=>{b.checked?selected.add(b.dataset.selectBox):selected.delete(b.dataset.selectBox);selection();});
    $$('[data-copy-password]',body).forEach(b=>b.onclick=()=>b.dataset.known?mailCopyPassword(b.dataset.copyPassword):toast("The panel does not know this mailbox password: set a new one with the Edit button at the end of the row — after that it can be copied",'warn'));
    $$('[data-connection]',body).forEach(b=>b.onclick=()=>mailCopyConnection(data,data.boxes.find(x=>x.address===b.dataset.connection)).catch(fail));
    $$('[data-toggle-box]',body).forEach(b=>b.onclick=async()=>{b.disabled=true;try{await api('mail.box.save',{address:b.dataset.toggleBox,edit:true,enabled:b.getAttribute('aria-checked')!=='true'});route();}catch(e){fail(e);b.disabled=false;}});
    $('[data-page]',el).textContent=(page+1)+' / '+Math.max(1,Math.ceil(rows.length/size))+' · '+rows.length;
    $('[data-prev]',el).disabled=page===0;$('[data-next]',el).disabled=(page+1)*size>=rows.length;selection();
  };
  const selection=()=>{$$('[data-bulk]',el).forEach(b=>b.disabled=!selected.size);$('[data-selected]',el).textContent=selected.size?"Selected: "+selected.size:'';};
  const removeBoxes=async addresses=>{if(!await confirmBox("Delete selected mailboxes?",addresses.map(h).join('<br>')+"<p>Message files remain on the server. Login and delivery will be disabled.</p>"))return;try{await api('mail.box.bulk',{addresses,action:'delete'});route();}catch(e){fail(e)}};
  $$('[data-bulk]',el).forEach(b=>b.onclick=async()=>{if(b.dataset.bulk==='delete'){await removeBoxes([...selected]);return;}b.disabled=true;try{await api('mail.box.bulk',{addresses:[...selected],action:b.dataset.bulk});route();}catch(e){fail(e);selection();}});
  $('[data-mail-search]',el).oninput=()=>{page=0;render();};$('[data-prev]',el).onclick=()=>{page--;render();};$('[data-next]',el).onclick=()=>{page++;render();};
  $('[data-usage-refresh]',el).onclick=async e=>{const b=e.currentTarget;b.disabled=true;try{await mailLoadUsage(data,usage,el.closest('main')||document,el);}catch(err){fail(err);}finally{b.disabled=false;}};
  render();
}
async function mailLoadUsage(data,usage,domains,boxes) {
  try{Object.assign(usage,await api('mail.usage'));}catch(e){$$('[data-box-usage],[data-domain-usage]',document).forEach(n=>{n.textContent='—';n.title="Could not retrieve quota usage";});throw e;}
  $$('[data-box-usage]',boxes).forEach(n=>n.textContent=Number.isFinite(usage[n.dataset.boxUsage])?fmtBytes(usage[n.dataset.boxUsage]):'—');
  $$('[data-domain-usage]',domains).forEach(n=>{const b=data.boxes.filter(x=>x.address.endsWith('@'+n.dataset.domainUsage));n.textContent=b.every(x=>Number.isFinite(usage[x.address]))?fmtBytes(b.reduce((v,x)=>v+usage[x.address],0)):'—';});
}
async function mailCopyConnection(data,box) {
  const domain=data.domains.find(d=>box.address.endsWith('@'+d.name));
  const host=mailHost(data,domain);
  const text='Login: '+box.address+'\nIMAP: '+host+':993 (TLS)\nSMTP: '+host+':587 (STARTTLS)'+(data.webmail?.ready?'\nRoundcube: https://'+data.webmail.hostname+'/':'');
  try{await navigator.clipboard.writeText(text);toast("Copied",'ok');}catch(e){showOutput("Connection settings",text);}
}
