// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';

function mailDNSRecordText(r) {
  return `${r.name || 'ВНЕШНИЙ_IP'}  ${r.type}  ${r.type === 'MX' ? r.priority + ' ' : ''}${r.value || 'УКАЖИТЕ_IP'}`;
}
async function mailDNSCopy(text) {
  if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
  else {
    const input = document.createElement('textarea'); input.value = text; input.style.position = 'fixed'; input.style.opacity = '0'; document.body.append(input); input.select();
    try { if (!document.execCommand('copy')) throw new Error('Копирование недоступно. Выделите текст записи вручную.'); } finally { input.remove(); }
  }
  toast('Скопировано', 'ok');
}
function mailDNSStatus(c) {
  const labels = {ok: '✓ OK', warn: 'Проверить', error: 'Ошибка', unknown: 'Нет данных'};
  return `<span class="tag ${c.status === 'error' ? 'err' : c.status === 'ok' ? 'ok' : 'warn'}">${h(labels[c.status] || labels.unknown)}</span>`;
}
async function mailDNS(name) {
  const m = modal(`<div class="mail-dns"><div class="mail-dns-title"><h3>DNS почты · ${h(name)}</h3><button class="btn" data-close aria-label="Закрыть">×</button></div><div data-content><p class="muted">Загрузка записей…</p></div></div>`, {wide: true});
  let data, revision = 0;
  const content = $('[data-content]', m.el);
  const alive = () => m.el.isConnected;
  const recordTable = records => `<div class="table-wrap"><table><thead><tr><th>Тип</th><th>Имя записи</th><th>Значение</th><th></th></tr></thead><tbody>${records.map(r => `<tr><td>${h(r.type)}${r.type === 'MX' ? '<small>Приоритет '+r.priority+'</small>' : ''}</td><td><code>${h(r.name || 'Внешний IPv4')}</code></td><td><code class="mail-dns-value">${h(r.value || 'Укажите внешний IPv4 в параметрах записей')}</code></td><td><button class="btn" data-copy="${h(r.value)}" ${r.value ? '' : 'disabled'}>Копировать</button></td></tr>`).join('')}</tbody></table></div>`;
  function render() {
    content.innerHTML = `<div class="mail-dns-summary">${data.records.map(r => `<div><b>${h(r.kind)}</b><span data-status="${r.kind}" class="muted">Не проверено</span></div>`).join('')}</div>
      <div class="toolbar"><button class="btn primary" data-check>Проверить DNS</button><button class="btn" data-copy-all>Копировать все записи</button><button class="btn" data-preview>Применить в DNS панели</button></div>
      <p class="hint" data-checked>Записи запрашиваются напрямую у NS-серверов домена — так их видят другие почтовые серверы. Успешная проверка не гарантирует доставку писем.</p>
      <div data-results></div>
      <details class="mail-dns-options"><summary>Параметры записей · IPv4 и политика DMARC</summary><form data-options><label>Публичный IPv4 сервера<input name="ipv4" value="${h(data.options.ipv4)}" placeholder="Внешний IPv4, в том числе за NAT"></label><label>Политика DMARC<select name="policy">${[['none','none — наблюдение'],['quarantine','quarantine — в спам'],['reject','reject — отклонение']].map(([v,t])=>`<option value="${v}" ${v===data.options.policy?'selected':''}>${h(t)}</option>`).join('')}</select></label><label>Получатель отчётов (необязательно)<input name="report" type="email" value="${h(data.options.report)}" placeholder="postmaster@${h(data.domain)}"></label><p class="hint">Параметры задают шаблон и ожидаемые значения. Сохранение не меняет DNS. Для внешнего получателя rua требуется разрешение его домена. Перед ужесточением DMARC проверьте все источники отправки.</p><button class="btn" type="submit">Сохранить параметры</button><span data-save-result role="status"></span></form></details>
      <h4>1. Направьте почту на сервер</h4><p class="hint">Создайте MX домена и A-запись имени почтового сервера у вашего DNS-провайдера. Здесь указаны полные имена; если провайдер дописывает зону, используйте относительное имя, для самой зоны — @.</p>${recordTable(data.records.filter(r=>['MX','A'].includes(r.kind)))}
      <h4>2. Добавьте защитные TXT-записи</h4><p class="hint">SPF должен быть один: объедините в нём все разрешённые источники. DKIM использует ключ панели и селектор <b>itru</b>. DMARC none — режим наблюдения. Проверяется запись _dmarc именно этого домена, без вычисления наследования политики.</p>${recordTable(data.records.filter(r=>['SPF','DKIM','DMARC'].includes(r.kind)))}
      <h4>3. Настройте обратную запись PTR</h4><p class="hint">В панели провайдера сервера назначьте внешнему IP это имя. PTR не создаётся в обычной DNS-зоне домена. A-запись имени должна возвращать тот же IP.</p>${recordTable(data.records.filter(r=>r.kind==='PTR'))}
      <p class="hint">Изменения появятся после обновления кэшей DNS. Проверка записей не проверяет SMTP, репутацию IP или доступность порта 25.</p>`;
    $$('[data-copy]', content).forEach(b => b.onclick = () => mailDNSCopy(b.dataset.copy).catch(fail));
    $('[data-copy-all]',content).onclick=()=>mailDNSCopy(data.records.map(mailDNSRecordText).join('\n')).catch(fail);
    $('[data-check]',content).onclick=check;
    $('[data-preview]',content).onclick=()=>preview().catch(fail);
    $('[data-options]',content).onsubmit=async e=>{
      e.preventDefault();const form=e.currentTarget,button=$('button',form);button.disabled=true;
      try {
        const options=Object.fromEntries(new FormData(form).entries());
        const d=await api('mail.dns.settings',{name,options});if(!alive())return;
        revision++;data=d;render();toast('Параметры сохранены. DNS-записи ещё не изменены.','ok');
      } catch(e){if(alive())$('[data-save-result]',form).textContent=e.message;} finally {button.disabled=false;}
    };
  }
  async function check() {
    const seq=++revision,button=$('[data-check]',content);button.disabled=true;button.textContent='Проверка…';
    try {
      const result=await api('mail.dns.check',{name},{timeout:12000});
      if(!alive()||seq!==revision)return;
      for(const c of result.checks){const el=$(`[data-status="${c.kind}"]`,content);if(el)el.innerHTML=mailDNSStatus(c);}
      $('[data-results]',content).innerHTML=`<details class="mail-dns-results" ${result.checks.some(c=>c.status==='error')?'open':''}><summary>Подробности проверки</summary>${result.checks.map(c=>`<div><b>${h(c.kind)}</b><div>${h(c.message)}${c.actual.length?`<details><summary>Найденные значения</summary><pre>${h(c.actual.join('\n'))}</pre></details>`:''}</div></div>`).join('')}</details>`;
      $('[data-checked]',content).textContent='Проверено: '+new Date(result.checked).toLocaleString(window.PanelLanguage?.locale)+' · '+(result.nameservers?.length?'NS-серверы домена: '+result.nameservers.join(', '):'публичный DNS 1.1.1.1 и 8.8.8.8 (NS домена не найдены)')+(result.ipv4?' · IPv4 сервера: '+result.ipv4:'');
    } catch(e){if(alive()&&seq===revision){$('[data-results]',content).textContent=e.message;$$('[data-status]',content).forEach(el=>el.textContent='Нет данных');}} finally {if(alive()&&seq===revision){button.disabled=false;button.textContent='Проверить снова';}}
  }
  async function preview() {
    const p=await api('mail.dns.preview',{name});if(!alive())return;
    const w=modal(`<div class="mail-dns"><h3>Применение почтовых DNS-записей</h3><p class="hint">Будут заменены перечисленные записи в существующих зонах BIND. Остальные записи сохранятся. Это работает для внешней почты только при делегировании домена на этот DNS-сервер. PTR меняется у провайдера IP.</p><div class="mail-dns-plan">${p.changes.map(c=>`<section><b>${h(c.kind)} · ${h(c.zone||'Нет зоны')}</b>${c.blocked?`<p class="mail-dns-error">${h(c.blocked)}</p>`:''}<small>Сейчас</small><pre>${h(c.before.length?c.before.map(r=>r.name+' '+r.type+' '+(r.type==='MX'?r.prio+' ':'')+r.value).join('\n'):'Нет записи')}</pre><small>После применения</small><pre>${h(mailDNSRecordText(c.record))}</pre></section>`).join('')}</div><p class="hint">Если здесь уже есть SPF стороннего сервиса или другие MX, отредактируйте записи вручную в разделе «Управление DNS», чтобы сохранить нужные источники и маршруты.</p><div class="btns"><button class="btn" data-close>Отмена</button><button class="btn primary" data-apply ${p.can_apply?'':'disabled'}>Применить показанные изменения</button></div><p data-error role="alert"></p></div>`,{wide:true});
    $('[data-apply]',w.el).onclick=async()=>{const b=$('[data-apply]',w.el);b.disabled=true;b.textContent='Применение…';try{await api('mail.dns.apply',{name,token:p.token},{timeout:90000});w.close();toast('Записи применены в DNS панели. Дождитесь обновления DNS-кэшей.','ok');if(alive())check();}catch(e){if(w.el.isConnected){$('[data-error]',w.el).textContent=e.message;b.disabled=false;b.textContent='Повторить';}}};
  }
  try {data=await api('mail.dns',{name});if(alive()){render();check();}}catch(e){if(alive())content.textContent=e.message;}
}
