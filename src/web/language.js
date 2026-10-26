// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Locale selection never translates DOM nodes, form values, paths or file contents.
(function () {
  const key = 'itrupanel.language', allowed = ['auto', 'ru', 'en'];
  function preferred() { try { const p = localStorage.getItem(key); if (allowed.includes(p)) return p; } catch {} const p = document.cookie.match(/(?:^|; )itrupanel_language_choice=(auto|ru|en)(?:;|$)/)?.[1]; return p || 'auto'; }
  function resolve(p, languages = navigator.languages || [navigator.language]) {
    if (p === 'ru' || p === 'en') return p;
    const first = (languages?.[0] || 'en').toLowerCase(); return first.startsWith('ru') ? 'ru' : 'en';
  }
  const preference = preferred(), language = resolve(preference);
  window.PanelLanguage = { preference, language, locale: language === 'ru' ? 'ru-RU' : 'en-GB', resolve };
  document.documentElement.lang = language;
  document.cookie = 'itrupanel_lang=' + language + '; Path=/; SameSite=Lax; Max-Age=31536000' + (location.protocol === 'https:' ? '; Secure' : '');
  window.languageControl = () => `<label class="language-control"><span class="sr-only">${language === 'ru' ? 'Язык интерфейса' : 'Interface language'}</span><select data-language aria-label="${language === 'ru' ? 'Язык интерфейса' : 'Interface language'}"><option value="auto" ${preference === 'auto' ? 'selected' : ''}>${language === 'ru' ? 'Авто' : 'Auto'}</option><option value="ru" ${preference === 'ru' ? 'selected' : ''}>Русский</option><option value="en" ${preference === 'en' ? 'selected' : ''}>English</option></select></label>`;
  document.addEventListener('change', e => {
    if (!e.target.matches('[data-language]')) return;
    const choice = e.target.value;
    if (!allowed.includes(choice)) return;
    if (typeof activeFileUpload !== 'undefined' && activeFileUpload) { e.target.value = preference; window.alert(language === 'ru' ? 'Дождитесь завершения загрузки файлов перед сменой языка.' : 'Wait for the file upload to finish before changing language.'); return; }
    if (document.querySelector('.overlay') && !window.confirm(language === 'ru' ? 'Смена языка перезагрузит страницу. Сохраните изменения в открытой форме перед переключением. Продолжить?' : 'Changing the language reloads this page. Save changes in the open form before switching. Continue?')) { e.target.value = preference; return; }
    try { localStorage.setItem(key, choice); } catch { /* Cookie still preserves an explicit choice. */ }
    document.cookie = 'itrupanel_language_choice=' + choice + '; Path=/; SameSite=Lax; Max-Age=31536000' + (location.protocol === 'https:' ? '; Secure' : '');
    const next = resolve(choice);
    document.cookie = 'itrupanel_lang=' + next + '; Path=/; SameSite=Lax; Max-Age=31536000' + (location.protocol === 'https:' ? '; Secure' : '');
    location.reload();
  });
  window.ITRUPANEL_BOOTSTRAP = true;
  const scripts = ['icons', 'app', 'uploads', 'pages', 'jobs', 'mail_dns', 'mail', 'firewall', 'docker', 'import', 'security', 'antivirus', 'authguard', 'webservers', 'updates', 'feedback'];
  async function boot() {
    for (const file of scripts) await new Promise((yes, no) => { const script = document.createElement('script'); script.src = (language === 'en' ? '/en/' : '/') + file + '.js'; script.onload = yes; script.onerror = no; document.head.append(script); });
    start();
  }
  boot().catch(() => { const app = document.getElementById('app'); app.textContent = language === 'ru' ? 'Не удалось загрузить интерфейс. Обновите страницу.' : 'The interface could not be loaded. Reload the page.'; });
})();
