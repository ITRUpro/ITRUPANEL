// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

test('completed job leaves result visible until explicit close', async () => {
  const nodes = Object.fromEntries(['h3','[data-stage]','[data-time]','[data-log]','[data-result]','[data-close]','[data-stop]'].map(k=>[k,{textContent:'',scrollHeight:0,scrollTop:0,clientHeight:0}]));
  let routes=0,closed=false;
  const c=vm.createContext({page(){}, modal(){return {el:{isConnected:true},close(){closed=true;}};},$(s){return nodes[s];},api:async()=>({title:'OLS',stage:'Ошибка',status:'error',started:new Date(),updated:new Date(),error:'Connection timed out',log:'apt-get exit 100'}),route(){routes++;},Date,Math,setTimeout(){throw Error('completed job polled again');}});
  vm.runInContext(fs.readFileSync('src/web/jobs.js','utf8'),c);
  vm.runInContext("watchJob('job')",c);
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(routes,0);assert.equal(nodes['[data-result]'].textContent,'Connection timed out');
  nodes['[data-close]'].onclick();assert.equal(routes,1);assert.equal(closed,true);
});
test('mailbox password modal opens after page refresh, not before',async()=>{
  let form;const events=[];
  const c=vm.createContext({page(){},api:async name=>name==='mail.list'?{ready:true,hostname:'mail.example.test',domains:[{name:'example.test'}]}:{address:'info@example.test',password:'generated-password'},openForm(f){form=f;},route:async()=>events.push('refresh'),showSecrets(){events.push('password');},toast(){}});
  vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);
  await vm.runInContext('mailBoxForm()',c);
  await form.submit({local:'info',domain:'example.test',password:'',enabled:true});
  assert.deepEqual(events,['refresh','password']);
});

test('mail forwarding parses multiple recipients and preserves copy setting',async()=>{
  let form;const calls=[];
  const c=vm.createContext({page(){},api:async(name,args)=>{calls.push([name,args]);return {ready:true,domains:[{name:'example.test'}]};},openForm(f){form=f;},toast(){},route(){}});
  vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);
  await vm.runInContext('mailForwardForm()',c);
  await form.submit({address:'info@example.test',targets:'a@example.test, b@external.test\nc@external.test',keep_copy:true,enabled:true});
  const args=calls.at(-1)[1];assert.equal(calls.at(-1)[0],'mail.forward.save');
  assert.deepEqual(Array.from(args.targets),['a@example.test','b@external.test','c@external.test']);
  assert.equal(args.keep_copy,true);assert.equal(args.edit,false);
  await assert.rejects(form.submit({targets:' '}),/получателей/);
});
test('quota and spam forms reject invalid limits before calling API',async()=>{
  let form;let writes=0;
  const c=vm.createContext({page(){},api:async(name)=>{if(name!=='mail.list')writes++;return {ready:true,domains:[{name:'example.test'}]};},openForm(f){form=f;}});
  vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);
  await vm.runInContext('mailBoxForm()',c);
  for(const [quota,unit] of [[-1,'MB'],[1.5,'MB'],[1048577,'MB'],[2000,'GB'],[0,'GB']])await assert.rejects(form.submit({local:'info',domain:'example.test',limit:true,quota,unit}),/Квота/);
  await vm.runInContext('mailSpamForm({})',c);
  for(const [mark,reject] of [[6,6],[15,6],[0,10],[1,101]])await assert.rejects(form.submit({mark,reject}),/пороги/);
  assert.equal(writes,0);
});

test('mail domain form preserves quotas and catches invalid limits',async()=>{
  let form,calls=[];
  const c=vm.createContext({page(){},openForm(f){form=f;},api:async(n,v)=>calls.push([n,v]),toast(){},route(){}});
  vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);
  await vm.runInContext("mailDomainSettings({name:'example.test',quota_mb:5120,max_boxes:50,default_quota_mb:1024,catch_all:''},{boxes:[]})",c);
  await assert.rejects(form.submit({quota_mb:-1,max_boxes:50,default_quota_mb:1024}),/целое/);
  await form.submit({quota_mb:'5120',max_boxes:'50',default_quota_mb:'1024',catch_all:''});
  assert.equal(calls[0][1].quota_mb,5120);assert.equal(calls[0][1].name,'example.test');
});
test('mail SSL PEM import attaches returned certificate and never resends private key to job',async()=>{
 let form,calls=[];
 const c=vm.createContext({page(){},openForm(f){form=f;},api:async(n,v)=>{calls.push([n,v]);return n==='ssl.import'?{Name:'new-cert'}:{job:'job-1'};},watchJob(){}});
 vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);
 await vm.runInContext("mailDomainSSL({name:'example.test'},{certs:[]})",c);
 await form.submit({hostname:'mail.example.test',mode:'import',crt:'CERT',chain:'CHAIN',key:'PRIVATE'});
 assert.equal(calls[1][0],'mail.domain.ssl');assert.equal(calls[1][1].certificate,'new-cert');assert.equal(calls[1][1].key,undefined);
});
test('new mailbox picks selected domain default quota',async()=>{
 let form;
 const c=vm.createContext({page(){},openForm(f){form=f;},api:async()=>({ready:true,domains:[{name:'one.test',default_quota_mb:100},{name:'two.test',default_quota_mb:200}]})});
 vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c);await vm.runInContext('mailBoxForm()',c);
 const v={domain:'two.test',limit:true,quota:100,unit:'MB'};form.onChange(v,'domain');assert.equal(v.limit,true);assert.equal(v.quota,200);assert.equal(v.unit,'MB');
 const g={domain:'one.test'};form.onChange(g,'domain');assert.equal(g.quota,100);
 // Квота в гигабайтах переводится в мегабайты; ящик без квоты отправляет 0
 let sent;const c2=vm.createContext({page(){},openForm(f){form=f;},route(){},toast(){},showSecrets(){},api:async(n,v)=>{if(n==='mail.box.save')sent=v;return n==='mail.list'?{ready:true,domains:[{name:'one.test'}]}:{};}});
 vm.runInContext(fs.readFileSync('src/web/mail.js','utf8'),c2);await vm.runInContext('mailBoxForm()',c2);
 await form.submit({local:'info',domain:'one.test',limit:true,quota:5,unit:'GB',enabled:true,name:'Иван'});assert.equal(sent.quota_mb,5120);assert.equal(sent.name,'Иван');assert.equal(sent.address,'info@one.test');
 await form.submit({local:'info',domain:'one.test',limit:false,quota:5,unit:'GB',enabled:true});assert.equal(sent.quota_mb,0);
});

// Скачивание подготовленного файла (база или выбранное из копии) идёт без окна задачи: в углу — полоса хода.
function downloadHarness(lang, answer) {
  const out = { toasts: [], fails: [], modals: 0, href: null, boxes: 0, removed: 0, texts: [], widths: [] };
  const els = {};
  const el = (k) => (els[k] ||= { style: {}, parentElement: { classList: { toggle() {} } }, set innerHTML(v) { out.texts.push(v); }, set textContent(v) { out.texts.push(v); } });
  const c = vm.createContext({
    page() {}, h: (x) => String(x), $: (sel) => el(sel), confirmBox() {}, route() {}, setTimeout, Date,
    toast: (m) => out.toasts.push(m), fail: (e) => out.fails.push(e.message), modal: () => { out.modals++; return { el: {}, close() {} }; },
    api: async () => answer(), location: { set href(v) { out.href = v; } },
    document: { createElement: () => ({ set className(v) {}, set innerHTML(v) {}, remove() { out.removed++; } }), body: { append() { out.boxes++; } } },
  });
  vm.runInContext(fs.readFileSync(new URL('../src/web/' + lang + 'jobs.js', import.meta.url), 'utf8'), c);
  return { c, out, el };
}
for (const lang of ['', 'en/']) {
  test('download job: no dialog window, progress in the corner, the file starts as soon as it is ready ' + lang, async () => {
    let status = 'running';
    const { c, out, el } = downloadHarness(lang, () => status === 'running' ? { status, percent: 40, stage: 'Распаковка архива: часть 1 из 2' } : { status, result: { download: '/api/backups/extracted?f=x.sql.gz' } });
    c.waitJobDownload('job1');
    await new Promise((r) => setTimeout(r, 50));
    assert.equal(out.modals, 0, 'окно задачи не открывается');
    assert.equal(out.boxes, 1, 'в углу показана полоса хода');
    assert.equal(el('i').style.width, '40%');
    assert.ok(out.texts.some((t) => String(t).includes('40%')), 'проценты показаны');
    assert.equal(out.href, null, 'пока файл не готов, скачивание не запускается');
    status = 'done';
    await new Promise((r) => setTimeout(r, 1300));
    assert.equal(out.href, '/api/backups/extracted?f=x.sql.gz', 'скачивание запущено сразу');
    assert.equal(out.removed, 1, 'полоса убрана');
    assert.equal(out.fails.length, 0);
  });
  test('download job: an error is a message, not a window ' + lang, async () => {
    const { c, out } = downloadHarness(lang, () => ({ status: 'error', error: 'нет места' }));
    c.waitJobDownload('job2');
    await new Promise((r) => setTimeout(r, 100));
    assert.deepEqual(out.fails, ['нет места']);
    assert.equal(out.modals, 0);
    assert.equal(out.removed, 1);
    assert.equal(out.href, null);
  });
}

test('spam form sends greylisting and DNSBL settings and validates them', async () => {
  let form; const calls = [];
  const c = vm.createContext({ page() {}, openForm(f) { form = f; }, api: async (n, v) => { calls.push([n, v]); return { job: 'j' }; }, watchJob() {} });
  vm.runInContext(fs.readFileSync(new URL('../src/web/mail.js', import.meta.url), 'utf8'), c);
  const catalog = [{ id: 'spamhaus', zone: 'zen.spamhaus.org', title: 'Spamhaus ZEN' }, { id: 'nixspam', zone: 'ix.dnsbl.manitu.net', title: 'NiX' }];
  await vm.runInContext('mailSpamForm', c)({ spam: { installed: true, enabled: true, mark: 6, reject: 15, rbl_skip: ['nixspam'], rbl_custom: [{ zone: 'a.example.net', score: 2 }] }, rbl_catalog: catalog });
  // Поля показывают текущее состояние
  const val = (name) => form.fields.find((f) => f.name === name).value;
  assert.equal(val('rbl'), true); assert.equal(val('rbl_spamhaus'), true); assert.equal(val('rbl_nixspam'), false); assert.equal(val('rbl_custom'), 'a.example.net 2');
  const base = { enabled: true, mark: 6, reject: 15, greylist: true, greylist_score: 4, greylist_delay: 7, rbl: true, rbl_spamhaus: true, rbl_nixspam: false, rbl_custom: 'bl.example.net 3.5\nx.example.org' };
  await form.submit(base);
  const body = calls.at(-1)[1];
  assert.equal(calls.at(-1)[0], 'mail.spam.setup');
  assert.equal(body.greylist, true); assert.equal(body.greylist_score, 4); assert.equal(body.greylist_delay, 7);
  assert.equal(body.rbl_off, false); assert.deepEqual(Array.from(body.rbl_skip), ['nixspam']);
  assert.deepEqual(JSON.parse(JSON.stringify(body.rbl_custom)), [{ zone: 'bl.example.net', score: 3.5 }, { zone: 'x.example.org', score: 3 }]);
  await form.submit({ ...base, rbl: false });
  assert.equal(calls.at(-1)[1].rbl_off, true);
  const before = calls.length;
  await assert.rejects(form.submit({ ...base, greylist_score: 6 }), /Greylisting/);
  await assert.rejects(form.submit({ ...base, greylist_delay: 99 }), /Задержка/);
  await assert.rejects(form.submit({ ...base, rbl_custom: 'a.example.net 50' }), /Свои списки/);
  assert.equal(calls.length, before, 'ошибочные значения не отправляются на сервер');
});
