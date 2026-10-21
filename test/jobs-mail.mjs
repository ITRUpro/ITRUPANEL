// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

test('completed job leaves result visible until explicit close', async () => {
  const nodes = Object.fromEntries(['h3','[data-stage]','[data-time]','[data-log]','[data-result]','[data-close]','[data-stop]'].map(k=>[k,{textContent:'',scrollHeight:0,scrollTop:0,clientHeight:0}]));
  let routes=0,closed=false;
  const c=vm.createContext({page(){}, modal(){return {el:{isConnected:true},close(){closed=true;}};},$(s){return nodes[s];},api:async()=>({title:'OLS',stage:'Ошибка',status:'error',started:new Date(),updated:new Date(),error:'Connection timed out',log:'apt-get exit 100'}),route(){routes++;},Date,Math,setTimeout(){throw Error('completed job polled again');}});
  vm.runInContext(fs.readFileSync('web/jobs.js','utf8'),c);
  vm.runInContext("watchJob('job')",c);
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(routes,0);assert.equal(nodes['[data-result]'].textContent,'Connection timed out');
  nodes['[data-close]'].onclick();assert.equal(routes,1);assert.equal(closed,true);
});
test('mailbox password modal opens after page refresh, not before',async()=>{
  let form;const events=[];
  const c=vm.createContext({page(){},api:async name=>name==='mail.list'?{ready:true,hostname:'mail.example.test',domains:[{name:'example.test'}]}:{address:'info@example.test',password:'generated-password'},openForm(f){form=f;},route:async()=>events.push('refresh'),showSecrets(){events.push('password');},toast(){}});
  vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);
  await vm.runInContext('mailBoxForm()',c);
  await form.submit({local:'info',domain:'example.test',password:'',enabled:true});
  assert.deepEqual(events,['refresh','password']);
});

test('mail forwarding parses multiple recipients and preserves copy setting',async()=>{
  let form;const calls=[];
  const c=vm.createContext({page(){},api:async(name,args)=>{calls.push([name,args]);return {ready:true,domains:[{name:'example.test'}]};},openForm(f){form=f;},toast(){},route(){}});
  vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);
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
  vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);
  await vm.runInContext('mailBoxForm()',c);
  for(const [quota,unit] of [[-1,'MB'],[1.5,'MB'],[1048577,'MB'],[2000,'GB'],[0,'GB']])await assert.rejects(form.submit({local:'info',domain:'example.test',limit:true,quota,unit}),/Квота/);
  await vm.runInContext('mailSpamForm({})',c);
  for(const [mark,reject] of [[6,6],[15,6],[0,10],[1,101]])await assert.rejects(form.submit({mark,reject}),/пороги/);
  assert.equal(writes,0);
});

test('mail domain form preserves quotas and catches invalid limits',async()=>{
  let form,calls=[];
  const c=vm.createContext({page(){},openForm(f){form=f;},api:async(n,v)=>calls.push([n,v]),toast(){},route(){}});
  vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);
  await vm.runInContext("mailDomainSettings({name:'example.test',quota_mb:5120,max_boxes:50,default_quota_mb:1024,catch_all:''},{boxes:[]})",c);
  await assert.rejects(form.submit({quota_mb:-1,max_boxes:50,default_quota_mb:1024}),/целое/);
  await form.submit({quota_mb:'5120',max_boxes:'50',default_quota_mb:'1024',catch_all:''});
  assert.equal(calls[0][1].quota_mb,5120);assert.equal(calls[0][1].name,'example.test');
});
test('mail SSL PEM import attaches returned certificate and never resends private key to job',async()=>{
 let form,calls=[];
 const c=vm.createContext({page(){},openForm(f){form=f;},api:async(n,v)=>{calls.push([n,v]);return n==='ssl.import'?{Name:'new-cert'}:{job:'job-1'};},watchJob(){}});
 vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);
 await vm.runInContext("mailDomainSSL({name:'example.test'},{certs:[]})",c);
 await form.submit({hostname:'mail.example.test',mode:'import',crt:'CERT',chain:'CHAIN',key:'PRIVATE'});
 assert.equal(calls[1][0],'mail.domain.ssl');assert.equal(calls[1][1].certificate,'new-cert');assert.equal(calls[1][1].key,undefined);
});
test('new mailbox picks selected domain default quota',async()=>{
 let form;
 const c=vm.createContext({page(){},openForm(f){form=f;},api:async()=>({ready:true,domains:[{name:'one.test',default_quota_mb:100},{name:'two.test',default_quota_mb:200}]})});
 vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c);await vm.runInContext('mailBoxForm()',c);
 const v={domain:'two.test',limit:true,quota:100,unit:'MB'};form.onChange(v,'domain');assert.equal(v.limit,true);assert.equal(v.quota,200);assert.equal(v.unit,'MB');
 const g={domain:'one.test'};form.onChange(g,'domain');assert.equal(g.quota,100);
 // Квота в гигабайтах переводится в мегабайты; ящик без квоты отправляет 0
 let sent;const c2=vm.createContext({page(){},openForm(f){form=f;},route(){},toast(){},showSecrets(){},api:async(n,v)=>{if(n==='mail.box.save')sent=v;return n==='mail.list'?{ready:true,domains:[{name:'one.test'}]}:{};}});
 vm.runInContext(fs.readFileSync('web/mail.js','utf8'),c2);await vm.runInContext('mailBoxForm()',c2);
 await form.submit({local:'info',domain:'one.test',limit:true,quota:5,unit:'GB',enabled:true,name:'Иван'});assert.equal(sent.quota_mb,5120);assert.equal(sent.name,'Иван');assert.equal(sent.address,'info@one.test');
 await form.submit({local:'info',domain:'one.test',limit:false,quota:5,unit:'GB',enabled:true});assert.equal(sent.quota_mb,0);
});
