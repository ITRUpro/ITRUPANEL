// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
(function () {
 const key='itrupanel.site.language',values=['auto','ru','en'];
 let choice=document.cookie.match(/(?:^|; )itrupanel_site_language=(auto|ru|en)(?:;|$)/)?.[1]||'auto';try{const saved=localStorage.getItem(key);if(values.includes(saved))choice=saved;}catch{}
 const system=(navigator.languages?.[0]||navigator.language||'en').toLowerCase().startsWith('ru')?'ru':'en';
 const language=choice==='auto'?system:choice;
 const page=document.documentElement.lang;
 function destination(lang){const path=(location.pathname.match(/^\/(?:en\/)?(terms|docs)\.html$/)||[])[1];const page_=path?path+'.html':'';return (lang==='en'?'/en/':'/')+page_+location.search+location.hash;}
 if(page!==language){location.replace(destination(language));return;}
 document.addEventListener('DOMContentLoaded',()=>{
  const target=document.querySelector('.header-row');
  const label=document.createElement('label');label.className='site-language';
  label.innerHTML=`<span class="sr-only">${language==='ru'?'Язык':'Language'}</span><select aria-label="${language==='ru'?'Язык':'Language'}"><option value="auto">${language==='ru'?'Авто':'Auto'}</option><option value="ru">Русский</option><option value="en">English</option></select>`;
  const select=label.querySelector('select');select.value=choice;
  select.addEventListener('change',()=>{try{localStorage.setItem(key,select.value);}catch{}document.cookie='itrupanel_site_language='+select.value+'; Path=/; SameSite=Lax; Max-Age=31536000'+(location.protocol==='https:'?'; Secure':'');const next=select.value==='auto'?system:select.value;location.assign(destination(next));});
  target.insertBefore(label,document.getElementById('theme'));
  const script=document.createElement('script');script.src='/panel-assets/'+(language==='en'?'app.en.js':'app.js')+'?v=0.1.43';document.head.append(script);
 });
})();
