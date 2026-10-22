// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

function harness(lang='') {
 let form;
 const calls=[];
 const c=vm.createContext({document:{addEventListener(){}},window:{addEventListener(){}},
  fakeAPI:async(name,data)=>{if(name==='sites.list')return[{site:{domain:'example.test'},unicode:'example.test'}];calls.push({name,data});},
  fakeForm:o=>{form=o;},fakeToast:()=>{},fakeRoute:()=>{}});
 for(const name of ['icons.js','app.js','pages.js']) vm.runInContext(readFileSync(new URL('../src/web/'+lang+name,import.meta.url),'utf8'),c);
 vm.runInContext('api=fakeAPI;openForm=fakeForm;toast=fakeToast;route=fakeRoute;',c);
 return {c,calls,get form(){return form;}};
}
for(const lang of ['', 'en/']) {
 test('site, database and panel backups are created separately '+lang,async()=>{
  const h=harness(lang);await h.c.backupForm('files');
  assert.ok(!h.form.fields.some(f=>f.name==='databases'||f.name==='panel_settings'),'site backup has no database or panel options');
  assert.equal(h.form.fields.find(f=>f.name==='site_settings').value,false);
  await h.form.submit({site:'example.test',mode:'incremental',site_settings:true});
  let req=h.calls.at(-1);assert.equal(req.name,'backups.create');assert.equal(req.data.kind,'files');assert.equal(req.data.mode,'incremental');assert.equal(req.data.site_settings,true);
  vm.runInContext(`api=async(name,data)=>{if(name==='backups.list')return{db_sites:{shop_db:'example.test',orphan_db:''}};fakeCalls.push({name,data});};`,Object.assign(h.c,{fakeCalls:h.calls}));
  await h.c.backupForm('db');
  const sel=h.form.fields.find(f=>f.name==='db');
  assert.equal(sel.options.map(o=>o[0]).join(),'*,orphan_db,shop_db');assert.equal(sel.value,'*');
  await h.form.submit({db:'shop_db'});
  req=h.calls.at(-1);assert.equal(req.data.kind,'database');assert.equal(req.data.db,'shop_db');
  vm.runInContext('confirmBox=async()=>true;',h.c);
  await h.c.backupForm('panel');
  assert.equal(h.calls.at(-1).data.kind,'panel');
 });
 test('database backup downloads the dump and restores only the database '+lang,async()=>{
  const h=harness(lang);
  vm.runInContext(`confirmBox=async()=>true;watchJob=()=>{};api=async(name,data)=>{fakeCalls.push({name,data});return {job:'j'};};`,Object.assign(h.c,{fakeCalls:h.calls}));
  await h.c.backupDownload({id:'1',kind:'database',db:'shop_db'});
  assert.deepEqual(JSON.parse(JSON.stringify(h.calls.at(-1))),{name:'backups.extract',data:{id:'1',dbs:['shop_db']}});
  await h.c.backupRestoreForm({id:'1',kind:'database',db:'shop_db',created:'2026-10-18T00:00:00Z'});
  assert.deepEqual(JSON.parse(JSON.stringify(h.calls.at(-1))),{name:'backups.restore',data:{id:'1',options:{databases:true}}});
 });
 test('restoration never selects panel or site settings implicitly '+lang,()=>{
  const h=harness(lang);h.c.backupRestoreForm({site:'example.test',kind:'full',site_settings:true,panel_settings:true});
  for(const name of ['site_settings','panel_settings'])assert.equal(h.form.fields.find(f=>f.name===name).value,false);
 });
}

for (const lang of ['', 'en/']) {
 test('FTP discovery confirms a read-only scan and opens its job '+lang, async()=>{
  const h=harness(lang);
  vm.runInContext(`let confirmation, watched; confirmBox=async(...args)=>{confirmation=args;return false;};watchJob=id=>{watched=id;};`,h.c);
  await h.c.backupDiscoverFTP();
  assert.equal(h.calls.length,0);
  assert.equal(vm.runInContext('confirmation[2].danger',h.c),false);
  vm.runInContext(`confirmBox=async()=>true;api=async name=>{if(name!=='backups.ftp.discover')throw Error(name);return {job:'ftp-scan'};};`,h.c);
  await h.c.backupDiscoverFTP();
  assert.equal(vm.runInContext('watched',h.c),'ftp-scan');
 });
}

for(const lang of ['', 'en/']) {
 test('storage mark: every cloud kind is a cloud, only FTP stays FTP '+lang,()=>{
  const h=harness(lang);
  const is=src=>vm.runInContext(`backupInCloud({remote_source:${JSON.stringify(src)}})`,h.c);
  for(const src of ['s3-1a2b','dav-1a2b','dbx-1a2b','gdr-1a2b']) assert.equal(is(src),true,src);
  for(const src of ['','ftp-1a2b','sftp-x',undefined]) assert.equal(is(src),false,String(src));
 });
}
