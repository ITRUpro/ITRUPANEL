// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source=fs.readFileSync('src/web/mail.js','utf8');
const h=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
function load(api){
 const nodes={};let html='',rows;
 const c=vm.createContext({page(){},api,h,encodeURIComponent,Set,fmtDate:v=>v,modal(s){html=s;return{el:{isConnected:true},close(){}};},$(s){return nodes[s]??={};},table(el,options){rows=options;}});
 vm.runInContext(source,c);return{c,get html(){return html;},get rows(){return rows;}};
}
test('missing API key offers public website fallback, without paid request',async()=>{
 const calls=[];const x=load(async(n)=>{calls.push(n);return{configured:false};});
 let fallback;x.c.mailMXFallback=async(...args)=>{fallback=args;};
 const b={textContent:'MXToolbox'};await x.c.mailReputation('example.test',b,'mxtoolbox');
 assert.equal(fallback[0],'example.test');assert.deepEqual(calls,['mail.mxtoolbox.settings']);assert.equal(b.disabled,false);assert.equal(b.textContent,'MXToolbox');
});
test('API network error offers public fallback instead of reporting clear',async()=>{
 const x=load(async(n)=>{if(n.endsWith('settings'))return{configured:true};throw Error('offline');});
 let fallback;x.c.mailMXFallback=async(...args)=>{fallback=args;};const b={textContent:'MXToolbox'};
 await x.c.mailReputation('example.test',b,'mxtoolbox');assert.equal(fallback[1],'offline');assert.equal(b.textContent,'MXToolbox');assert.equal(b.disabled,false);
});
test('partial MXToolbox results retain unknown status and prefilled fallback links',async()=>{
 const x=load(async(n,args)=>n.endsWith('settings')?{configured:true}:{checked:'now',rows:[{target:'example.test',list:'Test A',status:'clear',message:'OK'},{target:'8.8.8.8',list:'Test B',status:'unknown',message:'Timeout',details:'<script>bad</script>'}]});
 const b={textContent:'MXToolbox'};await x.c.mailReputation('example.test',b,'mxtoolbox');
 assert.equal(b.textContent,'Проверка неполная');assert.match(x.html,/blacklist%3A8.8.8.8/);assert.match(x.html,/без API-ключа/);
 assert.equal(x.rows.columns[3].r(x.rows.rows[1]),'&lt;script&gt;bad&lt;/script&gt;');
});
test('successful checks do not display a misleading failure prompt',async()=>{
 const x=load(async(n)=>n.endsWith('settings')?{configured:true}:{checked:'now',rows:[{target:'example.test',list:'Test A',status:'clear',message:'OK'}]});
 const b={textContent:'MXToolbox'};await x.c.mailReputation('example.test',b,'mxtoolbox');assert.equal(b.textContent,'Проверка пройдена');assert.doesNotMatch(x.html,/mail-mx-fallback/);
});
test('MXToolbox links encode targets and contain no secrets',()=>{
 const x=load(async()=>{});const html=x.c.mailMXLinks(['a.test','a.test','8.8.8.8']);
 assert.equal((html.match(/<a /g)||[]).length,2);assert.match(html,/action=blacklist%3Aa.test&amp;run=toolpage/);assert.match(html,/rel="noopener noreferrer"/);
});
