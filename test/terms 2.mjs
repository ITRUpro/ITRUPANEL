// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
function harness(lang='ru') {
 const nodes=Object.fromEntries(['.terms-card','form','[name=agree]','[data-accept]','.err','[data-retry]','[data-exit]','.terms-document'].map(k=>[k,{isConnected:true,textContent:'',checked:false,disabled:true,hidden:false}]));
 const app={set innerHTML(v){this.html=v;},html:''};
 nodes['.terms-card'].querySelector=k=>nodes[k];
 const calls=[];let shell=0,routes=0,login=0;let failAccept=false,failLoad=false;
 const c=vm.createContext({document:{addEventListener(){},querySelector:k=>k==='#app'?app:nodes[k],querySelectorAll:()=>[],documentElement:{dataset:{}}},window:{addEventListener(){}},localStorage:{getItem:()=>null},languageControl:()=>'',panelCopyright:()=>'',setInterval(){},clearInterval(){},fakeAPI:async(n,d)=>{calls.push([n,d]);if(n==='terms.get'){if(failLoad)throw Error('offline');return {text:'Fixture terms',revision:'1.0',sha256:'fixture'}}if(n==='terms.accept'&&failAccept)throw Error('disk full');return {user:'admin',terms_required:false}},shell:()=>shell++,routed:()=>routes++,login:()=>login++});
 vm.runInContext(fs.readFileSync(lang==='en'?'src/web/en/app.js':'src/web/app.js','utf8'),c);
 vm.runInContext('api=fakeAPI;renderShell=shell;route=routed;showLogin=login;App.me={user:"admin",terms_required:true};',c);
 return {c,nodes,calls,app,shell:()=>shell,routes:()=>routes,failAccept:v=>failAccept=v,failLoad:v=>failLoad=v,login:()=>login};
}
for(const lang of ['ru','en']) {
 test(`${lang}: consent stays unchecked; valid submit unlocks only after server confirmation`,async()=>{
  const h=harness(lang);await h.c.showTerms();
  assert.equal(h.nodes['[name=agree]'].checked,false);assert.equal(h.nodes['[data-accept]'].disabled,true);
  assert.equal(h.nodes['.terms-document'].textContent,'Fixture terms');
  await h.nodes.form.onsubmit({preventDefault(){}});assert.equal(h.calls.some(x=>x[0]==='terms.accept'),false);
  h.nodes['[name=agree]'].checked=true;h.nodes['[name=agree]'].onchange();assert.equal(h.nodes['[data-accept]'].disabled,false);
  h.failAccept(true);await h.nodes.form.onsubmit({preventDefault(){}});assert.equal(h.shell(),0);assert.equal(h.nodes['.err'].textContent,'disk full');
  h.failAccept(false);await h.nodes.form.onsubmit({preventDefault(){}});assert.equal(h.shell(),1);assert.equal(h.routes(),1);
  assert.equal(h.calls.find(x=>x[0]==='terms.accept')[1].sha256,'fixture');
 });
 test(`${lang}: document failure cannot unlock; retry and exit available`,async()=>{
  const h=harness(lang);h.failLoad(true);await h.c.showTerms();assert.equal(h.nodes['[name=agree]'].disabled,true);assert.equal(h.nodes['[data-retry]'].hidden,false);
  h.failLoad(false);await h.nodes['[data-retry]'].onclick();assert.equal(h.nodes['[name=agree]'].disabled,false);
  await h.nodes['[data-exit]'].onclick();assert.equal(h.login(),1);assert.equal(h.shell(),0);
 });
}
test('hash navigation cannot render pages while terms are pending',async()=>{
 const h=harness();vm.runInContext('route = '+fs.readFileSync('src/web/app.js','utf8').match(/async function route\(\) \{[\s\S]*?\n\}/)[0]+';',h.c);await h.c.route();assert.equal(h.shell(),0);assert.equal(h.calls.length,0);
});
