// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

function harness() {
  let form;
  const saved = [];
  const context = vm.createContext({
    document: { addEventListener() {} }, window: { addEventListener() {} },
    fakeAPI: async (name, data) => {
      if (name === 'sites.form') return { users: [], php: ['8.3'], php_all: ['8.2', '8.3'], default_php: '8.3', certs: [], apache: false, openlitespeed: false };
      if (name === 'sites.start') { saved.push(data); return {}; }
      throw new Error(name);
    },
    fakeForm: (options) => { form = options; },
  });
  for (const name of ['icons.js', 'app.js', 'pages.js']) vm.runInContext(readFileSync(new URL('../web/' + name, import.meta.url), 'utf8'), context);
  vm.runInContext('api = globalThis.fakeAPI; openForm = globalThis.fakeForm; toast = () => {}; route = () => {}; globalThis.watchJob = () => {};', context);
  return { context, saved, form: () => form };
}

test('OpenLiteSpeed can be selected, edited and saved with its PHP version', async () => {
  const h = harness();
  await h.context.siteForm({ domain: 'example.test', mode: 'php', backend: 'openlitespeed', php: '8.3' });
  const form = h.form();
  const mode = form.fields.find(f => f.name === 'mode');
  assert.equal(mode.value, 'openlitespeed');
  assert.ok(mode.options.some(o => o[0] === 'openlitespeed'));
  assert.equal(form.fields.find(f => f.name === 'php').show({ mode: 'openlitespeed' }), true);
  await form.submit({ mode: 'openlitespeed', php: '8.3', max_body: '64' });
  assert.equal(h.saved[0].backend, 'openlitespeed');
  assert.equal(h.saved[0].mode, 'php');
  assert.equal(h.saved[0].php, '8.3');
  assert.equal(h.saved[0].domain, 'example.test');
});

test('switching an existing OLS site back to nginx clears its backend', async () => {
  const h = harness();
  await h.context.siteForm({ domain: 'example.test', mode: 'php', backend: 'openlitespeed', php: '8.3' });
  await h.form().submit({ mode: 'php', php: '8.3', max_body: 64 });
  assert.equal(h.saved[0].backend, '');
  assert.equal(h.saved[0].edit, true);
});

test('editing aliases never submits the hidden www creation default', async () => {
  const h = harness();
  await h.context.siteForm({ domain: 'example.test', aliases: ['www.example.test'], mode: 'php', php: '8.3' });
  const form = h.form();
  const www = form.fields.find(f => f.name === 'add_www');
  assert.equal(www.show({}), false);
  assert.equal(www.value, false);
  assert.equal(form.fields.find(f => f.name === 'aliases').value, 'www.example.test');
  await form.submit({ mode: 'php', php: '8.3', aliases: '', add_www: true });
  assert.equal(h.saved[0].aliases, '');
  assert.equal(h.saved[0].add_www, false);
});

test('creating a site retains the optional www shortcut', async () => {
  const h = harness();
  await h.context.siteForm();
  const www = h.form().fields.find(f => f.name === 'add_www');
  assert.equal(www.show({}), true);
  assert.equal(www.value, true);
  await h.form().submit({ domain: 'example.test', mode: 'php', php: '8.3', aliases: '', add_www: true });
  assert.equal(h.saved[0].edit, false);
  assert.equal(h.saved[0].add_www, true);
});
