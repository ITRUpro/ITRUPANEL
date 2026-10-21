// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

function mailDNSRecordText(r) {
  return `${r.name || "PUBLIC_IP"}  ${r.type}  ${r.type === 'MX' ? r.priority + ' ' : ''}${r.value || "ENTER_IP"}`;
}
async function mailDNSCopy(text) {
  if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
  else {
    const input = document.createElement('textarea'); input.value = text; input.style.position = 'fixed'; input.style.opacity = '0'; document.body.append(input); input.select();
    try { if (!document.execCommand('copy')) throw new Error("Copying is unavailable. Select the record text manually."); } finally { input.remove(); }
  }
  toast("Copied", 'ok');
}
function mailDNSStatus(c) {
  const labels = {ok: '✓ OK', warn: "Check", error: "Error", unknown: "No data"};
  return `<span class="tag ${c.status === 'error' ? 'err' : c.status === 'ok' ? 'ok' : 'warn'}">${h(labels[c.status] || labels.unknown)}</span>`;
}
async function mailDNS(name) {
  const m = modal(`<div class="mail-dns"><div class="mail-dns-title"><h3>Mail DNS · ${h(name)}</h3><button class="btn" data-close aria-label="Close">×</button></div><div data-content><p class="muted">Loading records…</p></div></div>`, {wide: true});
  let data, revision = 0;
  const content = $('[data-content]', m.el);
  const alive = () => m.el.isConnected;
  const recordTable = records => `<div class="table-wrap"><table><thead><tr><th>Type</th><th>Record name</th><th>Value</th><th></th></tr></thead><tbody>${records.map(r => `<tr><td>${h(r.type)}${r.type === 'MX' ? "<small>Priority "+r.priority+'</small>' : ''}</td><td><code>${h(r.name || "Public IPv4")}</code></td><td><code class="mail-dns-value">${h(r.value || "Enter the public IPv4 in record settings")}</code></td><td><button class="btn" data-copy="${h(r.value)}" ${r.value ? '' : 'disabled'}>Copy</button></td></tr>`).join('')}</tbody></table></div>`;
  function render() {
    content.innerHTML = `<div class="mail-dns-summary">${data.records.map(r => `<div><b>${h(r.kind)}</b><span data-status="${r.kind}" class="muted">Not checked</span></div>`).join('')}</div>
      <div class="toolbar"><button class="btn primary" data-check>Check DNS</button><button class="btn" data-copy-all>Copy all records</button><button class="btn" data-preview>Apply to panel DNS</button></div>
      <p class="hint" data-checked>Records are queried directly from the domain’s name servers — the way other mail servers see them. A successful check does not guarantee delivery.</p>
      <div data-results></div>
      <details class="mail-dns-options"><summary>Record settings · IPv4 and DMARC policy</summary><form data-options><label>Server public IPv4<input name="ipv4" value="${h(data.options.ipv4)}" placeholder="Public IPv4, including behind NAT"></label><label>DMARC policy<select name="policy">${[['none',"none — monitor"],['quarantine',"quarantine — send to spam"],['reject',"reject — reject mail"]].map(([v,t])=>`<option value="${v}" ${v===data.options.policy?'selected':''}>${h(t)}</option>`).join('')}</select></label><label>Report recipient (optional)<input name="report" type="email" value="${h(data.options.report)}" placeholder="postmaster@${h(data.domain)}"></label><p class="hint">These settings define the template and expected values. Saving does not change DNS. An external rua recipient requires authorisation from its domain. Check all sending sources before enforcing stricter DMARC.</p><button class="btn" type="submit">Save settings</button><span data-save-result role="status"></span></form></details>
      <h4>1. Route mail to your server</h4><p class="hint">Create the domain MX and mail hostname A record at your DNS provider. Fully qualified names are shown here; if the provider appends the zone, use relative names and @ for the zone itself.</p>${recordTable(data.records.filter(r=>['MX','A'].includes(r.kind)))}
      <h4>2. Add authentication TXT records</h4><p class="hint">There must be only one SPF record: combine all permitted sources in it. DKIM uses the panel key and selector <b>itru</b>. DMARC none is monitoring mode. Checks the _dmarc record for this domain only, without evaluating inherited policies.</p>${recordTable(data.records.filter(r=>['SPF','DKIM','DMARC'].includes(r.kind)))}
      <h4>3. Set up the reverse PTR record</h4><p class="hint">Assign this hostname to the public IP in your server provider’s control panel. PTR is not created in the domain’s regular DNS zone. The hostname’s A record must resolve to the same IP.</p>${recordTable(data.records.filter(r=>r.kind==='PTR'))}
      <p class="hint">Changes appear after DNS caches refresh. Record checks do not test SMTP, IP reputation or port 25 accessibility.</p>`;
    $$('[data-copy]', content).forEach(b => b.onclick = () => mailDNSCopy(b.dataset.copy).catch(fail));
    $('[data-copy-all]',content).onclick=()=>mailDNSCopy(data.records.map(mailDNSRecordText).join('\n')).catch(fail);
    $('[data-check]',content).onclick=check;
    $('[data-preview]',content).onclick=()=>preview().catch(fail);
    $('[data-options]',content).onsubmit=async e=>{
      e.preventDefault();const form=e.currentTarget,button=$('button',form);button.disabled=true;
      try {
        const options=Object.fromEntries(new FormData(form).entries());
        const d=await api('mail.dns.settings',{name,options});if(!alive())return;
        revision++;data=d;render();toast("Settings saved. DNS records have not changed yet.",'ok');
      } catch(e){if(alive())$('[data-save-result]',form).textContent=e.message;} finally {button.disabled=false;}
    };
  }
  async function check() {
    const seq=++revision,button=$('[data-check]',content);button.disabled=true;button.textContent="Checking…";
    try {
      const result=await api('mail.dns.check',{name},{timeout:12000});
      if(!alive()||seq!==revision)return;
      for(const c of result.checks){const el=$(`[data-status="${c.kind}"]`,content);if(el)el.innerHTML=mailDNSStatus(c);}
      $('[data-results]',content).innerHTML=`<details class="mail-dns-results" ${result.checks.some(c=>c.status==='error')?'open':''}><summary>Check details</summary>${result.checks.map(c=>`<div><b>${h(c.kind)}</b><div>${h(c.message)}${c.actual.length?`<details><summary>Detected values</summary><pre>${h(c.actual.join('\n'))}</pre></details>`:''}</div></div>`).join('')}</details>`;
      $('[data-checked]',content).textContent="Checked: "+new Date(result.checked).toLocaleString(window.PanelLanguage?.locale)+' · '+(result.nameservers?.length?"Domain name servers: "+result.nameservers.join(', '):"public DNS 1.1.1.1 and 8.8.8.8 (domain name servers not found)")+(result.ipv4?" · Server IPv4: "+result.ipv4:'');
    } catch(e){if(alive()&&seq===revision){$('[data-results]',content).textContent=e.message;$$('[data-status]',content).forEach(el=>el.textContent="No data");}} finally {if(alive()&&seq===revision){button.disabled=false;button.textContent="Check again";}}
  }
  async function preview() {
    const p=await api('mail.dns.preview',{name});if(!alive())return;
    const w=modal(`<div class="mail-dns"><h3>Apply mail DNS records</h3><p class="hint">The listed records in existing BIND zones will be replaced. Other records will be preserved. External mail works only if the domain is delegated to this DNS server. Change PTR through your IP provider.</p><div class="mail-dns-plan">${p.changes.map(c=>`<section><b>${h(c.kind)} · ${h(c.zone||"No zone")}</b>${c.blocked?`<p class="mail-dns-error">${h(c.blocked)}</p>`:''}<small>Now</small><pre>${h(c.before.length?c.before.map(r=>r.name+' '+r.type+' '+(r.type==='MX'?r.prio+' ':'')+r.value).join('\n'):"Missing record")}</pre><small>After applying</small><pre>${h(mailDNSRecordText(c.record))}</pre></section>`).join('')}</div><p class="hint">If a third-party SPF record or other MX records already exist, edit them manually in DNS management to preserve the required sources and routes.</p><div class="btns"><button class="btn" data-close>Cancel</button><button class="btn primary" data-apply ${p.can_apply?'':'disabled'}>Apply the changes shown</button></div><p data-error role="alert"></p></div>`,{wide:true});
    $('[data-apply]',w.el).onclick=async()=>{const b=$('[data-apply]',w.el);b.disabled=true;b.textContent="Applying…";try{await api('mail.dns.apply',{name,token:p.token},{timeout:90000});w.close();toast("Records applied to the panel DNS. Wait for DNS caches to refresh.",'ok');if(alive())check();}catch(e){if(w.el.isConnected){$('[data-error]',w.el).textContent=e.message;b.disabled=false;b.textContent="Retry";}}};
  }
  try {data=await api('mail.dns',{name});if(alive()){render();check();}}catch(e){if(alive())content.textContent=e.message;}
}
