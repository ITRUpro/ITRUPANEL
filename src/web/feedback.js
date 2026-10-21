// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
page('feedback', { title: 'Обратная связь', async render(view) {
  head(view, 'Обратная связь', { sub: 'Написать разработчику ITRUPANEL' });
  const p = panel(view);
  p.innerHTML = `<form class="feedback-form" style="max-width:720px">
    <div class="field"><label for="feedback-category">Категория</label><select id="feedback-category" name="category"><option value="bug">Ошибка</option><option value="question">Вопрос</option><option value="idea">Предложение</option></select></div>
    <div class="field"><label for="feedback-subject">Тема</label><input id="feedback-subject" name="subject" maxlength="160" required></div>
    <div class="field"><label for="feedback-email">E-mail для ответа (необязательно)</label><input id="feedback-email" name="email" type="email" maxlength="254" autocomplete="email"></div>
    <div class="field"><label for="feedback-message">Сообщение</label><textarea id="feedback-message" name="message" rows="8" maxlength="8000" required></textarea><div class="hint">До 8000 символов. Не указывайте пароли, ключи и другие секреты.</div></div>
    <div class="field"><label class="check"><input name="include_version" type="checkbox"><span>Добавить версию панели</span></label></div>
    <p class="hint">Журналы, конфигурации, домены и учётные данные автоматически не отправляются.</p>
    <div class="field"><label class="check"><input name="consent" type="checkbox" required><span>Разрешаю передать разработчику текст обращения, указанный e-mail и выбранные сведения.</span></label></div>
    <button class="btn primary" type="submit">Отправить обращение</button><p class="hint" data-feedback-state role="status" aria-live="polite"></p>
  </form>`;
  const form = $('form', p), button = $('button', form), state = $('[data-feedback-state]', form);
  let requestID = null, previousPayload = null;
  form.onsubmit = async e => {
    e.preventDefault(); if (button.disabled || !form.reportValidity()) return;
    const data = Object.fromEntries(new FormData(form));
    data.include_version = form.elements.include_version.checked; data.consent = form.elements.consent.checked;
    const payload = JSON.stringify(data);
    if (!requestID || previousPayload !== payload) { requestID = Array.from(crypto.getRandomValues(new Uint8Array(24)), b => b.toString(16).padStart(2, '0')).join(''); previousPayload = payload; }
    Array.from(form.elements).forEach(el => el.disabled = true); state.textContent = 'Отправка обращения…';
    try {
      const result = await api('feedback.send', { ...data, request_id: requestID });
      form.reset(); requestID = null; previousPayload = null;
      state.textContent = 'Обращение принято. Номер: ' + result.id;
      toast('Обращение отправлено разработчику', 'ok');
    } catch (error) { state.textContent = error.message; }
    finally { Array.from(form.elements).forEach(el => el.disabled = false); }
  };
} });
