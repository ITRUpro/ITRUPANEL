// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {webcrypto} from 'node:crypto';
function fixture(lang='') {
 let def,fail=true,reset=0;const calls=[],subject={value:'Question'},message={value:'Text'},button={disabled:false},state={textContent:''};
 const elements=[subject,message,button];elements.include_version={checked:false};elements.consent={checked:true};elements.push(elements.include_version,elements.consent);
 const form={elements,reportValidity:()=>true,reset:()=>reset++};const panel={};
 const c=vm.createContext({page:(_,d)=>def=d,head:()=>{},panel:()=>panel,$:(s)=>s==='form'?form:s==='button'?button:state,FormData:class{constructor(){return [['subject',subject.value],['message',message.value],['category','question'],['email','']]}},crypto:webcrypto,Uint8Array,toast:()=>{},api:async(name,data)=>{calls.push({name,data});if(fail)throw Error('connection failed');return {id:'abcdef123456abcdef123456'}}});
 vm.runInContext(fs.readFileSync(new URL('../web/'+lang+'feedback.js',import.meta.url),'utf8'),c);
 return {async start(){await def.render({});},async submit(){await form.onsubmit({preventDefault(){}})},calls,state,subject,elements,button,success(){fail=false},get reset(){return reset}};
}
for(const lang of ['', 'en/']) test('feedback '+lang+' retains request ID on failure and changes it for edited input',async()=>{
 const f=fixture(lang);await f.start();await f.submit();assert.equal(f.reset,0);assert.equal(f.state.textContent,'connection failed');assert.ok(f.elements.every(e=>!e.disabled));
 await f.submit();assert.equal(f.calls[0].data.request_id,f.calls[1].data.request_id);f.subject.value='Edited';await f.submit();assert.notEqual(f.calls[1].data.request_id,f.calls[2].data.request_id);
 f.success();await f.submit();assert.equal(f.calls[2].data.request_id,f.calls[3].data.request_id);assert.equal(f.reset,1);assert.match(f.state.textContent,/abcdef123456abcdef123456/);assert.equal(f.calls[3].data.include_version,false);assert.equal(f.calls[3].data.consent,true);
});
