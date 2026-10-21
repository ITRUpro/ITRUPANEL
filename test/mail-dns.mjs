// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source=fs.readFileSync('web/mail_dns.js','utf8');
const h=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;');
test('copy includes MX priority; failed clipboard is not reported as successful',async()=>{
 let copied,notifications=0;
 const c=vm.createContext({navigator:{clipboard:{writeText:async s=>{copied=s;}}},toast(){notifications++;},h});vm.runInContext(source,c);
 assert.equal(vm.runInContext("mailDNSRecordText({name:'example.test',type:'MX',priority:10,value:'mail.example.test.'})",c),'example.test  MX  10 mail.example.test.');
 await vm.runInContext("mailDNSCopy('public DKIM')",c);assert.equal(copied,'public DKIM');assert.equal(notifications,1);
 c.navigator.clipboard.writeText=async()=>{throw Error('denied');};await assert.rejects(vm.runInContext("mailDNSCopy('value')",c),/denied/);assert.equal(notifications,1);
});
test('unknown and malformed states never render a green OK badge',()=>{const c=vm.createContext({h});vm.runInContext(source,c);for(const status of ['unknown','error','warn','unexpected']){const html=vm.runInContext(`mailDNSStatus({status:'${status}'})`,c);assert.ok(!html.includes('✓ OK'));assert.ok(!html.includes('tag ok'));}});
test('closing DNS window ignores outstanding data response',async()=>{
 let resolve;const content={innerHTML:'loading'},el={isConnected:true};
 const c=vm.createContext({h,modal:()=>({el}),$:()=>content,api:()=>new Promise(r=>resolve=r)});vm.runInContext(source,c);const pending=vm.runInContext("mailDNS('example.test')",c);el.isConnected=false;resolve({});await pending;assert.equal(content.innerHTML,'loading');
});
test('DNS lookup failure clears old success states and restores check button',async()=>{
 const nodes=new Map();const node=s=>{if(!nodes.has(s))nodes.set(s,{innerHTML:'',textContent:'',disabled:false});return nodes.get(s);};const el={isConnected:true};
 const c=vm.createContext({h,modal:()=>({el}),$:s=>node(s),$$:s=>s==='[data-status]'?[node('status')]:[],api:async name=>{if(name==='mail.dns')return {domain:'example.test',options:{ipv4:'',policy:'none',report:''},records:[]};throw Error('DNS недоступен');}});vm.runInContext(source,c);await vm.runInContext("mailDNS('example.test')",c);await new Promise(r=>setImmediate(r));assert.equal(node('[data-results]').textContent,'DNS недоступен');assert.equal(node('status').textContent,'Нет данных');assert.equal(node('[data-check]').disabled,false);
});
