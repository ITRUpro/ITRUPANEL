// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

function harness() {
  const requests=[], changes=[], timers=new Set(), events=new Map(), cards=[], notices=[];
  let now=0;
  class Element {
    constructor(){this.nodes={};this.value=0;this.hidden=true;this.classes=[];this.classList={add:(c)=>this.classes.push(c)};}
    querySelector(s){return this.nodes[s] ||= new Element();}
    setAttribute(){}
    removeAttribute(s){delete this[s];}
    remove(){this.removed=true;}
  }
  class XHR {
    constructor(){this.upload={};requests.push(this);}
    open(method,url){this.method=method;this.url=url;}
    setRequestHeader(k,v){this.header=[k,v];}
    send(body){this.body=body;}
    reply(status,body){this.status=status;this.responseText=body;this.onload();}
  }
  const context=vm.createContext({
    XMLHttpRequest:XHR,FormData:class{constructor(){this.files=[];}append(...f){this.files.push(f);}},
    document:{createElement:()=>new Element(),body:{append:(card)=>cards.push(card)}},
    window:{addEventListener:(k,v)=>events.set(k,v),removeEventListener:(k)=>events.delete(k)},
    performance:{now:()=>now},setInterval:(f)=>{timers.add(f);return f;},clearInterval:(f)=>timers.delete(f),
    $:(s,el)=>el?el.querySelector(s):cards.findLast(c=>!c.removed),
    fmtBytes:(n)=>Math.round(n)+' Б',plural:(n)=>n+' файлов',toast:(t)=>notices.push(t),fail:(e)=>{throw e;},
  });
  vm.runInContext(readFileSync(new URL('../src/web/uploads.js',import.meta.url),'utf8'),context);
  const files=[{name:'<archive>.zip',size:1000}];
  return {context,requests,changes,files,cards,timers,events,notices,
    start:()=>context.sendFileUpload(files,'/api/files/upload?path='+encodeURIComponent('/var/www/a b'),p=>changes.push(p)),
    ui:(path='/target',done)=>context.uploadFiles(files,path,done),
    tick:(ms)=>{now=ms;for(const f of timers)f();},
    node:(s)=>cards.at(-1).querySelector(s),
  };
}
test('multipart progress and server processing are distinct; success needs final response',async()=>{
 const h=harness();let done=false;const p=h.start().then(()=>done=true);const x=h.requests[0];
 assert.equal(x.url,'/api/files/upload?path=%2Fvar%2Fwww%2Fa%20b');assert.deepEqual(x.header,['X-Itrupanel','1']);
 assert.equal(x.body.files[0][2],'<archive>.zip');
 x.upload.onprogress({loaded:500,total:1100,lengthComputable:true});
 assert.equal(h.changes[0].loaded,500);assert.equal(h.changes[0].total,1100);
 x.upload.onload();await Promise.resolve();assert.equal(done,false);assert.equal(h.changes[1].stage,'processing');
 x.reply(200,'{"ok":true,"data":1}');await p;assert.equal(done,true);
});
test('unknown total never invents a percentage',async()=>{
 const h=harness();const p=h.start();h.requests[0].upload.onprogress({loaded:400,total:0,lengthComputable:false});
 assert.equal(h.changes[0].total,null);h.requests[0].reply(200,'{"ok":true,"data":1}');await p;
});
for(const [status,body,message] of [[401,'','Сессия истекла'],[413,'<html>','Размер загрузки'],[500,'<html>','Некорректный ответ'],[200,'{"error":"Нет места"}','Нет места'],[200,'{}','Ошибка загрузки']]){
 test('upload rejects unsuccessful server reply '+status+' '+body,async()=>{
 const h=harness();const p=h.start();h.requests[0].reply(status,body);await assert.rejects(p,new RegExp(message));
 });
}
test('visible progress reports speed and ETA, waits for save, then releases timer and close guard',async()=>{
 const h=harness();let refreshed=0;const p=h.ui('/target',()=>refreshed++);const x=h.requests[0];
 assert.equal(h.node('.upload-name').textContent,'<archive>.zip');assert.equal(h.events.has('beforeunload'),true);
 h.tick(2000);x.upload.onprogress({loaded:500,total:1000,lengthComputable:true});
 assert.equal(h.node('progress').value,50);assert.match(h.node('[data-speed]').textContent,/250 Б\/с/);
 assert.match(h.node('[data-remaining]').textContent,/2 с/);
 x.upload.onload();assert.match(h.node('.upload-status').textContent,/завершает сохранение/);assert.equal(refreshed,0);
 x.reply(200,'{"ok":true,"data":1}');await p;
 assert.match(h.node('.upload-status').textContent,/сохранены/);assert.equal(refreshed,1);
 assert.equal(h.node('[data-close]').hidden,false);assert.equal(h.timers.size,0);assert.equal(h.events.size,0);
});
test('parallel upload blocked; network error remains visible and next attempt allowed',async()=>{
 const h=harness();const p=h.ui();await h.ui('/different');assert.equal(h.requests.length,1);assert.equal(h.notices.length,1);
 h.requests[0].onerror();await p;assert.match(h.node('.upload-status').textContent,/прервано/);assert.equal(h.timers.size,0);
 const retry=h.ui();assert.equal(h.requests.length,2);h.requests[1].reply(200,'{"ok":true,"data":1}');await retry;
});
test('stalled upload hides stale time estimate',async()=>{
 const h=harness();const p=h.ui();h.tick(1000);h.requests[0].upload.onprogress({loaded:200,total:1000,lengthComputable:true});
 h.tick(12000);assert.match(h.node('.upload-status').textContent,/Проверьте соединение/);assert.match(h.node('[data-remaining]').textContent,/рассчитывается/);
 h.requests[0].onerror();await p;
});
