// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

function load(extra = {}) {
  const c = vm.createContext({ page() {}, watchJob() {}, toast() {}, route: async () => {}, fail(e) { throw e; }, h: s => String(s), icon: () => '', genPass: () => 'secret', ...extra });
  vm.runInContext(fs.readFileSync('web/docker.js', 'utf8'), c);
  return c;
}

test('container form sends trimmed lists and numeric limits', async () => {
  let form, call, job;
  const c = load({ openForm(f) { form = f; }, api: async (op, body) => { call = { op, body }; return { job: 'j1' }; }, watchJob(id) { job = id; } });
  vm.runInContext('dockerContainerForm([{name:"bridge",driver:"bridge"},{name:"none",driver:"null"}], "redis:7")', c);
  assert.equal(form.fields.find(f => f.name === 'image').value, 'redis:7');
  assert.deepEqual(Array.from(form.fields.find(f => f.name === 'network').options, o => o[0]), ['bridge']);
  await form.submit({ image: ' redis:7 ', name: ' cache ', ports: '127.0.0.1:6379:6379\n\n', volumes: ' data:/data ', env: 'A=1\r\n\nB= two ', restart: 'always', network: 'bridge', command: '', memory: '128', cpus: '0,5' });
  assert.equal(call.op, 'docker.container.create');
  assert.equal(call.body.image, 'redis:7'); assert.equal(call.body.name, 'cache');
  assert.deepEqual(Array.from(call.body.ports), ['127.0.0.1:6379:6379']);
  assert.deepEqual(Array.from(call.body.volumes), ['data:/data']);
  assert.deepEqual(Array.from(call.body.env), ['A=1', 'B= two ']);
  assert.equal(call.body.memory, 128); assert.equal(call.body.cpus, 0.5); assert.equal(job, 'j1');
});

test('docker page offers installation when Docker is missing', async () => {
  let pageDef, installed;
  const el = { innerHTML: '', };
  const c = load({ page(k, d) { pageDef = d; }, api: async op => op === 'docker.status' ? { installed: false } : (installed = op, { job: 'j' }), head() {}, panel: () => el, $: () => ({ set onclick(fn) { el.click = fn; } }), $$: () => [] });
  await pageDef.render({ isConnected: true }, {});
  assert.match(el.innerHTML, /Docker не установлен/);
  await el.click();
  assert.equal(installed, 'docker.install');
});
