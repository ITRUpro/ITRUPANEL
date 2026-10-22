// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source=fs.readFileSync('src/web/updates.js','utf8');
const h=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;');
test('global status prioritizes active work and failure',()=>{const c=vm.createContext({h});vm.runInContext(source,c);for(const [data,label] of [[{available:true,release:{version:'0.1.20'}},'Доступно обновление 0.1.20'],[{run:{state:'running'},available:true},'Обновление…'],[{run:{state:'error'},available:true},'Ошибка обновления'],[{error:'offline'},'Проверить обновления']]){c.data=data;assert.equal(vm.runInContext('updateButtonLabel(data)',c),label);}});
test('unverified feed failure never claims latest version',()=>{const c=vm.createContext({h});vm.runInContext(source,c);c.data={current:'0.1.15',error:'bad signature',release:{version:'0.1.15',released:'2026-10-08',notes:['<script>alert(1)</script>']}};const html=vm.runInContext('updateOverview(data)',c);assert.ok(!html.includes('Актуальная версия установлена'));assert.ok(html.includes('&lt;script>'));});
test('401 after restart requires login instead of reporting success',async()=>{const c=vm.createContext({AbortController,setTimeout,clearTimeout,fetch:async()=>({status:401})});vm.runInContext(source,c);await assert.rejects(vm.runInContext("updateRequest('status')",c),e=>e.login===true&&!e.message.includes('успешно'));});
test('check request uses authenticated same-origin POST and CSRF header',async()=>{let request;const c=vm.createContext({AbortController,setTimeout,clearTimeout,fetch:async(url,options)=>{request={url,options};return {status:200,ok:true,json:async()=>({ok:true,data:{current:'0.1.15'}})};}});vm.runInContext(source,c);await vm.runInContext("updateRequest('info', {force:true})",c);assert.equal(request.url,'/api/updates.info');assert.equal(request.options.headers['X-Itrupanel'],'1');assert.equal(request.options.credentials,'same-origin');assert.equal(request.options.body,'{"force":true}');});

function fixture(initial){
 let data=structuredClone(initial),statusError=null,allErrors=false;const calls=[],timers=new Map(),storage=new Map(),session=new Map(),nodes=new Map();let seq=0,opens=0,reloads=0;
 const get=key=>{if(!nodes.has(key))nodes.set(key,{hidden:false,disabled:false,textContent:'',innerHTML:'',value:0,classList:{toggle(){}},setAttribute(){},focus(){},removeAttribute(name){if(name==='value')this.value=null;}});return nodes.get(key);};
 const el={isConnected:false};
 const c=vm.createContext({h,AbortController,App:{me:{user:'admin'}},location:{reload(){reloads++;}},ls:{get:(k,d)=>storage.get(k)??d,set:(k,v)=>storage.set(k,v)},sessionStorage:{getItem:k=>session.get(k),setItem:(k,v)=>session.set(k,v),removeItem:k=>session.delete(k)},$:s=>s==='.modal-wrap,.overlay'?(el.isConnected?el:null):get(s),modal:()=>{opens++;el.isConnected=true;return {el,close:()=>{el.isConnected=false;}};},setTimeout:(fn,ms)=>{const id=++seq;timers.set(id,{fn,ms});return id;},clearTimeout:id=>timers.delete(id),fetch:async(url,options)=>{calls.push({url,body:JSON.parse(options.body)});if((url.endsWith('.status')||allErrors)&&statusError){if(statusError===401)return {status:401};throw Error('offline');}return {status:200,ok:true,json:async()=>({data:url.endsWith('.status')?{current:data.current,run:data.run}:url.endsWith('.apply')?data.run:data})};}});
 vm.runInContext(source,c);
 const flush=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
 return {get,calls,storage,el,opens:()=>opens,reloads:()=>reloads,setData:v=>data=structuredClone(v),error:(v,all=false)=>{statusError=v;allErrors=all;},run:code=>vm.runInContext(code,c),flush,tick:async()=>{const found=[...timers].find(([,v])=>v.ms===500);assert.ok(found,'status timer missing');timers.delete(found[0]);await found[1].fn();await flush();}};
}
const sample=state=>({current:'0.1.19',available:true,release:{version:'0.1.20',released:'2026-10-09',notes:[]},run:{id:'a'.repeat(24),version:'0.1.20',state,stage:state,total:100,downloaded:state==='running'?25:100,log:'Download verified'}});
test('ready keeps old version active and requires explicit run-scoped apply',async()=>{
 const f=fixture(sample('ready'));await f.run('showPanelUpdates()');await f.flush();
 assert.equal(f.get('[data-update-apply]').hidden,false);assert.equal(f.get('[data-update-install]').hidden,true);
 assert.equal(f.calls.filter(c=>c.url.endsWith('.apply')).length,0);
 await f.get('[data-update-apply]').onclick();await f.get('[data-update-apply]').onclick();await f.flush();
 const applies=f.calls.filter(c=>c.url.endsWith('.apply'));assert.equal(applies.length,1);assert.equal(applies[0].body.id,'a'.repeat(24));
 assert.equal(f.storage.get('pending-update').version,'0.1.20');
});
test('live transfer progress refreshes, auth loss clears stale bar without success',async()=>{
 const f=fixture(sample('running'));await f.run('showPanelUpdates()');await f.flush();assert.equal(f.get('[data-update-progress]').value,25);
 const next=sample('running');next.run.downloaded=70;f.setData(next);await f.tick();assert.equal(f.get('[data-update-progress]').value,70);
 f.error(401);await f.tick();assert.equal(f.get('[data-update-progress]').hidden,true);assert.equal(f.reloads(),0);assert.equal(f.get('[data-update-bytes]').textContent,'');assert.equal(f.get('[data-update-login]').hidden,false);assert.ok(!f.get('[data-update-error]').textContent.includes('успешно'));
});
for(const state of ['done','error','running','ready']) test('login restores no previous log and offers only an available idle update: '+state,async()=>{
 const result=sample(state);const f=fixture(result);f.storage.set('pending-update',{id:result.run.id});f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),['done','error'].includes(state)?1:0);
 f.el.isConnected=false;
 if(['done','error'].includes(state))assert.equal(f.storage.get('pending-update'),null);
 else assert.equal(f.storage.get('pending-update').id,result.run.id);
 await f.get('[data-panel-updates]').onclick();await f.flush();assert.equal(f.opens(),['done','error'].includes(state)?2:1);
});
test('apply followed by auth loss reloads once and stops the progress animation',async()=>{
 const f=fixture(sample('ready'));await f.run('showPanelUpdates()');await f.flush();
 await f.get('[data-update-apply]').onclick();await f.flush();assert.equal(f.storage.get('pending-update').applyRequested,true);
 f.error(401);await f.tick();assert.equal(f.reloads(),1);assert.equal(f.get('[data-update-progress]').hidden,true);assert.equal(f.storage.get('pending-update').reloadRequested,true);
 await f.get('[data-update-check]').onclick();await f.flush();f.error(401,true);f.run('startPanelUpdates()');await f.flush();assert.equal(f.reloads(),1);
});
test('background observer reloads after apply even when the dialog was closed',async()=>{
 const f=fixture(sample('running'));f.storage.set('pending-update',{id:'a'.repeat(24),applyRequested:true});f.error(401,true);f.run('startPanelUpdates()');await f.flush();assert.equal(f.reloads(),1);assert.equal(f.opens(),0);
});
test('login offers an available update; closing stops status requests',async()=>{
 const initial=sample('running');initial.run=null;const f=fixture(initial);f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),1);
 f.el.isConnected=false;f.setData(sample('running'));await f.run('showPanelUpdates()');await f.flush();const count=f.calls.length;f.el.isConnected=false;await f.tick();assert.equal(f.calls.length,count);
});
test('connection loss retries and shows actual failure log after recovery',async()=>{
 const f=fixture(sample('running'));await f.run('showPanelUpdates()');await f.flush();f.error('offline');await f.tick();assert.equal(f.get('[data-update-progress]').value,null);
 const failed=sample('error');failed.run.error='Health check failed; rollback completed';f.setData(failed);f.error(null);await f.tick();assert.match(f.get('[data-update-error]').textContent,/rollback completed/);
});

test('apply reload works when local storage cannot persist the pending marker',async()=>{
 const f=fixture(sample('ready'));f.run('ls.set = () => {}; ls.get = (_, fallback) => fallback;');await f.run('showPanelUpdates()');await f.flush();await f.get('[data-update-apply]').onclick();await f.flush();f.error(401);await f.tick();assert.equal(f.reloads(),1);
});

for (const state of ['done','error']) test('saved '+state+' run never opens old progress or log',async()=>{
 const initial=sample(state);if(state==='error')initial.run.error='Previous update failed';
 const f=fixture(initial);await f.run('showPanelUpdates()');await f.flush();
 assert.equal(f.get('[data-update-run]').hidden,true);
 assert.equal(f.get('[data-update-log]').textContent,'');
 assert.equal(f.get('[data-update-bytes]').textContent,'');
 assert.equal(f.get('[data-update-progress]').hidden,true);
 assert.match(f.get('[data-update-summary]').innerHTML,/0.1.20/);
 if(state==='error')assert.equal(f.get('[data-update-error]').textContent,'Previous update failed');
});
test('successful completion removes progress and log; reopening keeps them hidden',async()=>{
 const f=fixture(sample('running'));await f.run('showPanelUpdates()');await f.flush();
 assert.equal(f.get('[data-update-run]').hidden,false);
 const finished=sample('done');finished.current='0.1.20';finished.available=false;f.setData(finished);await f.tick();
 assert.equal(f.get('[data-update-run]').hidden,true);assert.equal(f.get('[data-update-log]').textContent,'');
 f.el.isConnected=false;await f.run('showPanelUpdates()');await f.flush();
 assert.equal(f.get('[data-update-run]').hidden,true);assert.equal(f.get('[data-update-log]').textContent,'');
});
test('failure during visible operation retains diagnostic log but reopening hides it',async()=>{
 const f=fixture(sample('running'));await f.run('showPanelUpdates()');await f.flush();
 const failed=sample('error');failed.run.error='Validation failed';failed.run.log='Diagnostic details';f.setData(failed);await f.tick();
 assert.equal(f.get('[data-update-run]').hidden,false);assert.equal(f.get('[data-update-log]').textContent,'Diagnostic details');
 f.el.isConnected=false;await f.run('showPanelUpdates()');await f.flush();
 assert.equal(f.get('[data-update-run]').hidden,true);assert.equal(f.get('[data-update-log]').textContent,'');
});

for (const unavailable of [{available:false},{error:'offline'},{unsupported:'platform'},{run:{state:'running'}},{run:{state:'ready'}}]) test('no login offer: '+JSON.stringify(unavailable),async()=>{
 const data={...sample('done'),...unavailable};const f=fixture(data);f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),0);
});
test('dismissal survives polling and page refresh; a new login permits the offer again',async()=>{
 const f=fixture(sample('done'));f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),1);
 f.el.isConnected=false;f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),1);
 f.run('PanelUpdates.offerChecked = false; startPanelUpdates()');await f.flush();assert.equal(f.opens(),1);
 f.run('resetUpdateOffer(); startPanelUpdates()');await f.flush();assert.equal(f.opens(),2);
 assert.equal(f.calls.filter(c=>c.url.endsWith('.start')||c.url.endsWith('.apply')).length,0);
});
test('offer waits until terms and another dialog are closed',async()=>{
 const f=fixture(sample('done'));f.run('App.me.terms_required=true; startPanelUpdates()');await f.flush();assert.equal(f.opens(),0);
 f.run('App.me.terms_required=false');f.el.isConnected=true;f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),0);
 f.el.isConnected=false;f.run('startPanelUpdates()');await f.flush();assert.equal(f.opens(),1);
});
test('update offer starts preparation after a fresh check, never applies automatically',async()=>{
 const f=fixture(sample('done'));f.run('startPanelUpdates()');await f.flush();
 f.get('[data-update-now]').onclick();await f.flush();
 assert.equal(f.calls.filter(c=>c.url.endsWith('.start')).length,1);
 assert.equal(f.calls.filter(c=>c.url.endsWith('.apply')).length,0);
});
test('update becoming unavailable after offer prevents download',async()=>{
 const f=fixture(sample('done'));f.run('startPanelUpdates()');await f.flush();
 f.setData({...sample('done'),available:false});f.get('[data-update-now]').onclick();await f.flush();
 assert.equal(f.calls.filter(c=>c.url.endsWith('.start')).length,0);
});
