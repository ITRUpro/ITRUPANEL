// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Compile English assets from reviewed catalogs; never translate live DOM or user data.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
const require=createRequire(import.meta.url),acorn=require('./vendor/acorn.cjs');
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const cyr=/[А-Яа-яЁё]/;
function segments(s){const out=[];for(const match of s.matchAll(/[^<>"'\n]+/g)){const key=match[0].trim();if(cyr.test(key)){const start=match.index+match[0].indexOf(key);out.push({start,end:start+key.length,key});}}return out;}
function walk(node,fn){if(!node||typeof node!=='object')return;if(node.type)fn(node);for(const [k,v]of Object.entries(node)){if(k==='parent')continue;if(Array.isArray(v)){for(const child of v)walk(child,fn);}else if(v&&typeof v==='object')walk(v,fn);}}
const files=[...fs.readdirSync(path.join(root,'web')).filter(n=>n.endsWith('.js')&&!['language.js'].includes(n)).map(n=>'web/'+n),'release/panel-assets/app.js'];
const inventory=new Map(),parsed=new Map();
for(const name of files){const source=fs.readFileSync(path.join(root,name),'utf8'),nodes=[];const tree=acorn.parse(source,{ecmaVersion:'latest',sourceType:'script'});walk(tree,node=>{const value=node.type==='Literal'&&typeof node.value==='string'?node.value:node.type==='TemplateElement'?node.value.cooked:null;if(value===null||!cyr.test(value))return;nodes.push({start:node.start,end:node.end,template:node.type==='TemplateElement',value});for(const {key}of segments(value)){if(!inventory.has(key))inventory.set(key,[]);inventory.get(key).push(name);}});parsed.set(name,{source,nodes});}
const html=fs.readFileSync(path.join(root,'release/index.html'),'utf8');for(const {key}of segments(html)){if(!inventory.has(key))inventory.set(key,[]);inventory.get(key).push('release/index.html');}
if(process.argv.includes('--extract')){fs.writeFileSync(path.join(root,'i18n/ui-inventory.json'),JSON.stringify(Object.fromEntries([...inventory].sort()),null,2)+'\n');console.log(inventory.size+' UI strings');process.exit(0);}
const catalog=JSON.parse(fs.readFileSync(path.join(root,'i18n/en-ui.json'),'utf8'));
const missing=[...inventory.keys()].filter(k=>typeof catalog[k]!=='string'||!catalog[k].trim());if(missing.length){console.error('Missing English UI translations:',JSON.stringify(missing,null,2));process.exit(1);}
function translate(s){const edits=segments(s);for(const e of edits.reverse())s=s.slice(0,e.start)+catalog[e.key]+s.slice(e.end);return s;}
for(const [name,{source,nodes}]of parsed){let out=source;for(const n of nodes.sort((a,b)=>b.start-a.start)){let text=translate(n.value);text=n.template?text.replaceAll('\\','\\\\').replaceAll('`','\\`').replaceAll('${','\\${'):JSON.stringify(text);out=out.slice(0,n.start)+text+out.slice(n.end);}
 acorn.parse(out,{ecmaVersion:'latest',sourceType:'script'});
 const dest=name.startsWith('web/')?'web/en/'+path.basename(name):'release/panel-assets/app.en.js';fs.mkdirSync(path.dirname(path.join(root,dest)),{recursive:true});fs.writeFileSync(path.join(root,dest),out);
}
let en=translate(html).replace('<html lang="ru"','<html lang="en"').replace('rel="canonical" href="https://itrupanel.com/"','rel="canonical" href="https://itrupanel.com/en/"').replace('<meta property="og:locale" content="ru_RU">','<meta property="og:locale" content="en_US">').replace('<meta property="og:locale:alternate" content="en_US">','<meta property="og:locale:alternate" content="ru_RU">').replace('"url":"https://itrupanel.com/","inLanguage"','"url":"https://itrupanel.com/en/","inLanguage"');
en=en.replace(/-ru\.jpg"/g,'-en.jpg"').replaceAll('href="/docs.html"','href="/en/docs.html"');en=en.replace(/(href|src)="panel-assets\//g,'$1="/panel-assets/').replace(/href="panel\//g,'href="/panel/').replace('href="INSTALL.txt"','href="/en/INSTALL.txt"');
fs.mkdirSync(path.join(root,'release/en'),{recursive:true});fs.writeFileSync(path.join(root,'release/en/index.html'),en);
console.log(`Compiled English panel and website: ${inventory.size} reviewed UI strings.`);
