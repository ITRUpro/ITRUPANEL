// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Собирает страницы документации release/docs.html и release/en/docs.html из release/docs-content.json.
// Каркас (шапка, счётчики, подвал) берётся из страниц условий, чтобы меню и тема работали одинаково.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const rel = (p) => path.join(root, 'release', p);
const data = JSON.parse(fs.readFileSync(rel('docs-content.json'), 'utf8'));

// Защита: каждый пункт меню панели обязан быть описан в документации (источник — release/docs-src.py).
{
  const app = fs.readFileSync(path.join(root, 'web/app.js'), 'utf8');
  const nav = app.slice(app.indexOf('const NAV = ['), app.indexOf('const NAV_GROUPS'));
  const ids = [...nav.matchAll(/^\s*\['([a-z0-9]+)',/gm)].map((m) => m[1]);
  const have = new Set(data.sections.map((s) => s.id));
  const missing = ids.filter((id) => !have.has(id));
  if (missing.length) {
    console.error('В документации нет разделов для пунктов меню панели: ' + missing.join(', ') + '. Добавьте их в release/docs-src.py и запустите python3 release/docs-src.py.');
    process.exit(1);
  }
}

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

const DOCS_SCRIPT = `<script>(function(){var toc=document.querySelector('.docs-toc');if(!toc)return;var links=[].slice.call(toc.querySelectorAll('a[href^="#"]'));var secs=links.map(function(a){return document.getElementById(a.getAttribute('href').slice(1));});var cur=-1,tick=false;function mark(){tick=false;var i=0,line=140;for(var k=0;k<secs.length;k++){if(secs[k]&&secs[k].getBoundingClientRect().top<=line)i=k;}if(window.innerHeight+window.scrollY>=document.documentElement.scrollHeight-4)i=secs.length-1;if(i===cur)return;cur=i;links.forEach(function(a,k){a.classList.toggle('active',k===i);if(k===i)a.setAttribute('aria-current','true');else a.removeAttribute('aria-current');});var a=links[i],t=a.offsetTop,h=toc.clientHeight;if(t<toc.scrollTop+40||t+a.offsetHeight>toc.scrollTop+h-40)toc.scrollTop=Math.max(0,t-h/2);}function req(){if(!tick){tick=true;setTimeout(mark,40);}}window.addEventListener('scroll',req,{passive:true});window.addEventListener('resize',req);window.addEventListener('hashchange',req);mark();})();</script>`;

const L = {
  ru: {
    file: 'terms.html', out: 'docs.html', url: 'https://itrupanel.com/docs.html', home: '/', prefix: '',
    title: 'Документация ITRUPANEL — бесплатная панель для сервера и бесплатная панель для хостинга',
    desc: 'Документация ITRUPANEL: все разделы бесплатной панели для сервера и бесплатной панели для хостинга — сайты, базы данных, почта, DNS, SSL, резервные копии, безопасность и импорт данных — и как они работают.',
    eyebrow: 'ДОКУМЕНТАЦИЯ', h1: 'Документация ITRUPANEL',
    intro: 'ITRUPANEL — бесплатная панель для сервера и бесплатная панель для хостинга с открытым исходным кодом. Здесь описаны все разделы панели и то, как они работают.',
    kw: 'бесплатная панель для сервера, бесплатная панель для хостинга, документация панели управления сервером, панель управления хостингом, ITRUPANEL',
    toc: 'Разделы', lead: 'Что это', how: 'Как это работает', feats: 'Возможности', note: 'Важно', nav: 'Документация', top: 'Наверх',
  },
  en: {
    file: 'en/terms.html', out: 'en/docs.html', url: 'https://itrupanel.com/en/docs.html', home: '/en/', prefix: '/en',
    title: 'ITRUPANEL documentation — free server control panel and free hosting control panel',
    desc: 'ITRUPANEL documentation: every section of the free server control panel and free hosting control panel — websites, databases, mail, DNS, SSL, backups, security and data import — and how they work.',
    eyebrow: 'DOCUMENTATION', h1: 'ITRUPANEL documentation',
    intro: 'ITRUPANEL is a free server control panel and a free hosting control panel with open source code. This page describes every section of the panel and how it works.',
    kw: 'free server control panel, free hosting control panel, server control panel documentation, hosting control panel, ITRUPANEL',
    toc: 'Sections', lead: 'What it is', how: 'How it works', feats: 'Features', note: 'Important', nav: 'Documentation', top: 'Back to top',
  },
};

for (const lang of ['ru', 'en']) {
  const c = L[lang];
  let shell = fs.readFileSync(rel(c.file), 'utf8');
  const head = shell.slice(0, shell.indexOf('<main'));
  let h = head
    .replace(/<title>.*?<\/title>/, `<title>${esc(c.title)}</title>`)
    .replace(/<meta name="description" content="[^"]*">(<meta name="keywords" content="[^"]*">)?/, `<meta name="description" content="${esc(c.desc)}"><meta name="keywords" content="${esc(c.kw)}">`)
    .replace(/<link rel="canonical" href="[^"]*">/, `<link rel="canonical" href="${c.url}">`)
    .replace(/<link rel="alternate" hreflang="ru" href="[^"]*">/, '<link rel="alternate" hreflang="ru" href="https://itrupanel.com/docs.html">')
    .replace(/<link rel="alternate" hreflang="en" href="[^"]*">/, '<link rel="alternate" hreflang="en" href="https://itrupanel.com/en/docs.html">')
    .replace('<link rel="stylesheet" href="/panel-assets/terms.css?v=2">', '<link rel="stylesheet" href="/panel-assets/terms.css?v=2"><link rel="stylesheet" href="/panel-assets/docs.css?v=2">');
  if (!h.includes('docs.css')) throw new Error('terms.css link not found in ' + c.file);
  if (!h.includes('href="https://itrupanel.com/forum/">')) throw new Error('nav not found');
  h = h.replace(/<a href="[^"]*docs\.html"[^>]*>[^<]*<\/a>/g, '');
  h = h.replace(/(<a href="https:\/\/itrupanel\.com\/forum\/">)/, `<a href="${c.prefix}/docs.html" aria-current="page">${c.nav}</a>$1`);
  const g = (id) => data.groups.find((x) => x.id === id)[lang];
  const sections = data.sections;
  const toc = data.groups.map((gr) => {
    const items = sections.filter((s) => s.group === gr.id).map((s) => `<a href="#${s.id}">${esc(s[lang].title)}</a>`).join('');
    return items ? `<div class="docs-toc-group"><b>${esc(gr[lang])}</b>${items}</div>` : '';
  }).join('');
  const body = data.groups.map((gr) => {
    const items = sections.filter((s) => s.group === gr.id).map((s) => {
      const t = s[lang];
      return `<section class="docs-section" id="${s.id}"><h3>${esc(t.title)}</h3>`
        + `<p class="docs-lead">${esc(t.lead)}</p>`
        + `<h4>${esc(c.how)}</h4><p>${esc(t.how)}</p>`
        + `<h4>${esc(c.feats)}</h4><ul>${t.features.map((f) => `<li>${esc(f)}</li>`).join('')}</ul>`
        + (t.note ? `<p class="docs-note"><b>${esc(c.note)}.</b> ${esc(t.note)}</p>` : '')
        + '</section>';
    }).join('');
    return items ? `<div class="docs-group" id="group-${gr.id}"><h2>${esc(gr[lang])}</h2>${items}</div>` : '';
  }).join('');
  const html = h
    + `<main class="container docs-main"><span class="eyebrow">${c.eyebrow}</span><h1>${esc(c.h1)}</h1><p class="docs-intro">${esc(c.intro)}</p>`
    + `<div class="docs-layout"><aside class="docs-toc" aria-label="${esc(c.toc)}"><div class="docs-toc-title">${esc(c.toc)}</div>${toc}</aside><div class="docs-content">${body}</div></div></main>`
    + `<footer class="legal-footer"><div class="container">© 2026 Oleg Sergeevich Dmitriev · ITRUPANEL · <a href="${c.home}">${lang === 'ru' ? 'На главную' : 'Home'}</a> · <a href="#">${esc(c.top)}</a></div></footer>${DOCS_SCRIPT}<script src="/panel-assets/visits.js?v=1" defer></script></body></html>`;
  fs.mkdirSync(path.dirname(rel(c.out)), { recursive: true });
  fs.writeFileSync(rel(c.out), html);
  console.log('wrote', c.out, html.length);
}
