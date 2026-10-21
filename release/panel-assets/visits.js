// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
(()=>{
 'use strict';
 if(window.__itruVisits || navigator.doNotTrack==='1' || window.doNotTrack==='1' || navigator.globalPrivacyControl) return;
 if(!['/','/en/','/terms.html','/en/terms.html'].includes(location.pathname)) return;
 window.__itruVisits=true;
 const id=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),n=>n.toString(16).padStart(2,'0')).join('');
 function send(){
  try {
   let visitor;
   try {let stored=JSON.parse(localStorage.getItem('itru_site_visitor')||'null');if(!stored || !/^[a-f0-9]{32}$/.test(stored.id) || Date.now()-stored.created>90*86400000) stored={id:id(),created:Date.now()};visitor=stored.id;localStorage.setItem('itru_site_visitor',JSON.stringify(stored));}catch{visitor=id();}
   let referrer='';try{referrer=new URL(document.referrer).hostname;}catch{}
   const q=new URLSearchParams(location.search), val=k=>(q.get(k)||'').replace(/[\x00-\x1f\x7f]/g,'').slice(0,100);
   const data={visitor,event:id(),path:location.pathname,referrer,source:val('utm_source'),medium:val('utm_medium'),campaign:val('utm_campaign')};
   fetch('/visits/api.php',{method:'POST',credentials:'omit',headers:{'Content-Type':'application/json'},body:JSON.stringify(data),keepalive:true,cache:'no-store'}).catch(()=>{});
  }catch{}
 }
 window.addEventListener('pageshow',e=>{if(e.persisted)send();});
 if(document.prerendering) document.addEventListener('prerenderingchange',send,{once:true});else send();
})();
