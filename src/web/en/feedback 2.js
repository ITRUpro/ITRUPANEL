// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
page('feedback', { title: "Feedback", async render(view) {
  head(view, "Feedback", { sub: "Contact the ITRUPANEL developer" });
  const p = panel(view);
  p.innerHTML = `<form class="feedback-form" style="max-width:720px">
    <div class="field"><label for="feedback-category">Category</label><select id="feedback-category" name="category"><option value="bug">Error</option><option value="question">Question</option><option value="idea">Suggestion</option></select></div>
    <div class="field"><label for="feedback-subject">Theme</label><input id="feedback-subject" name="subject" maxlength="160" required></div>
    <div class="field"><label for="feedback-email">Reply email (optional)</label><input id="feedback-email" name="email" type="email" maxlength="254" autocomplete="email"></div>
    <div class="field"><label for="feedback-message">Message</label><textarea id="feedback-message" name="message" rows="8" maxlength="8000" required></textarea><div class="hint">Up to 8000 characters. Do not include passwords, keys or other secrets.</div></div>
    <div class="field"><label class="check"><input name="include_version" type="checkbox"><span>Include panel version</span></label></div>
    <p class="hint">Logs, configuration files, domains and credentials are not sent automatically.</p>
    <div class="field"><label class="check"><input name="consent" type="checkbox" required><span>I agree to send the message, supplied email and selected details to the developer.</span></label></div>
    <button class="btn primary" type="submit">Send message</button><p class="hint" data-feedback-state role="status" aria-live="polite"></p>
  </form>`;
  const form = $('form', p), button = $('button', form), state = $('[data-feedback-state]', form);
  let requestID = null, previousPayload = null;
  form.onsubmit = async e => {
    e.preventDefault(); if (button.disabled || !form.reportValidity()) return;
    const data = Object.fromEntries(new FormData(form));
    data.include_version = form.elements.include_version.checked; data.consent = form.elements.consent.checked;
    const payload = JSON.stringify(data);
    if (!requestID || previousPayload !== payload) { requestID = Array.from(crypto.getRandomValues(new Uint8Array(24)), b => b.toString(16).padStart(2, '0')).join(''); previousPayload = payload; }
    Array.from(form.elements).forEach(el => el.disabled = true); state.textContent = "Sending message…";
    try {
      const result = await api('feedback.send', { ...data, request_id: requestID });
      form.reset(); requestID = null; previousPayload = null;
      state.textContent = "Message received. Reference: " + result.id;
      toast("Message sent to the developer", 'ok');
    } catch (error) { state.textContent = error.message; }
    finally { Array.from(form.elements).forEach(el => el.disabled = false); }
  };
} });
