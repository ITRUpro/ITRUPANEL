// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

test('firewall form omits hidden port and submits correct partial address list', async () => {
 let form, call;
 const c=vm.createContext({page(){},openForm(f){form=f},api:async(op,body)=>{call={op,body}},toast(){},route:async()=>{}});
 vm.runInContext(fs.readFileSync('web/firewall.js','utf8'),c);
 vm.runInContext('firewallForm(null,{revision:17,enabled:false})',c);
 await form.submit({name:'ICMP restriction',action:'deny_selected',protocol:'icmp',port:'3306',source:'0.0.0.0',blocked:'192.0.2.1, 198.51.100.0/24\n203.0.113.5',trusted:'10.0.0.1',enabled:true});
 assert.equal(call.op,'firewall.save'); assert.equal(call.body.revision,17);
 assert.equal(call.body.rule.port,'');assert.deepEqual(Array.from(call.body.rule.addresses),['192.0.2.1','198.51.100.0/24','203.0.113.5']);
});
test('enable confirmation uses an explicit enable label', async()=>{
 let pageDef, actions, confirmation, change;
 const c=vm.createContext({page(k,d){pageDef=d},api:async(op,body)=>op==='firewall.list'?{state:{ready:true,enabled:false,revision:4,rules:[]},active:false,protected_ports:[22,3333]}:(change={op,body}),head(v,t,o){actions=o.actions},panel(){return {}},h:s=>s,table(){},confirmBox:async(...args)=>{confirmation=args;return true},route:async()=>{},fail(e){throw e}});
 vm.runInContext(fs.readFileSync('web/firewall.js','utf8'),c);
 await pageDef.render({});await actions.find(a=>a.t==='Включить фильтрацию').fn();
 assert.equal(confirmation[2].ok,'Включить');assert.equal(change.op,'firewall.enable');assert.equal(change.body.enabled,true);
});
