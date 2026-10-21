// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

function harness() {
  class Element {
    constructor() { this.nodes = {}; this.groups = {}; this.isConnected = true; this.value = ''; }
    querySelector(s) { return this.nodes[s]; }
    querySelectorAll(s) { return this.groups[s] || []; }
    set innerHTML(s) {
      this.html = s;
      this.groups['[data-folder]'] = [...s.matchAll(/data-folder="(\d+)"/g)].map((m) => ({ dataset: { folder: m[1] } }));
      this.nodes['[data-retry]'] = {};
    }
    get innerHTML() { return this.html || ''; }
  }
  const el = new Element();
  for (const key of ['[data-ok]', '[data-folder-path]', '.folder-list', '.crumbs', '[data-folder-go]', '[data-folder-up]']) el.nodes[key] = new Element();
  const pending = [], chosen = [];
  let form;
  const context = vm.createContext({
    document: { addEventListener() {} }, window: { addEventListener() {} },
    fakeAPI: (name, data) => new Promise((resolve, reject) => pending.push({ name, data, resolve, reject })),
    fakeForm: (options) => { form = options; return { el }; },
  });
  for (const name of ['icons.js', 'app.js', 'pages.js']) vm.runInContext(readFileSync(new URL('../web/' + name, import.meta.url), 'utf8'), context);
  vm.runInContext('api = globalThis.fakeAPI; openForm = globalThis.fakeForm;', context);
  return {
    context, el, pending, chosen,
    open() { context.folderDestination({ title: 'Копировать', path: '/var/www', action: 'Копировать', select: (p) => chosen.push(p) }); },
    submit: () => form.submit(),
    input: el.nodes['[data-folder-path]'],
    ok: el.nodes['[data-ok]'],
    list: el.nodes['.folder-list'],
    go() { el.nodes['[data-folder-go]'].onclick(); },
  };
}
const flush = () => new Promise((resolve) => setImmediate(resolve));

test('file path is continuous, folders remain links, spaces in real names are preserved', () => {
  const h = harness();
  const path = '/var/www/oleg/data/www/oleg.ink';
  const html = h.context.pathCrumbs(path);
  assert.equal(html.replace(/<[^>]*>/g, ''), path);
  assert.match(html, /data-p="\/var\/www\/oleg\/data\/www\/oleg.ink"/);
  assert.equal(h.context.pathCrumbs('/Фото  клиентов').replace(/<[^>]*>/g, ''), '/Фото  клиентов');
  assert.equal(h.context.pathCrumbs('/').replace(/<[^>]*>/g, ''), '/');
  assert.ok(!h.context.pathCrumbs('/<script>').includes('<script>'));
});

test('folder picker lists directories only and uses server-confirmed destination', async () => {
  const h = harness(); h.open();
  assert.equal(h.ok.disabled, true);
  assert.equal(h.pending[0].name, 'files.list');
  h.pending[0].resolve({ path: '/var/www', rows: [{ name: 'oleg', dir: true }, { name: 'index.php', dir: false }] });
  await flush();
  assert.equal(h.list.groups['[data-folder]'].length, 1);
  assert.ok(!h.list.innerHTML.includes('index.php'));
  h.list.groups['[data-folder]'][0].onclick();
  assert.equal(h.pending[1].data.path, '/var/www/oleg');
  h.pending[1].resolve({ path: '/var/www/oleg', rows: [] });
  await flush();
  assert.equal(h.ok.disabled, false);
  await h.submit();
  assert.deepEqual(h.chosen, ['/var/www/oleg']);
});

test('denied folder stays unselectable and does not silently choose another folder', async () => {
  const h = harness(); h.open();
  h.pending[0].reject(new Error('Нет доступа'));
  await flush();
  assert.equal(h.ok.disabled, true);
  assert.match(h.list.innerHTML, /Нет доступа/);
  await assert.rejects(h.submit(), /выберите доступную папку/);
  assert.equal(h.chosen.length, 0);
});

test('late folder responses cannot override the last navigation', async () => {
  const h = harness(); h.open();
  h.input.value = '/new'; h.go();
  h.pending[1].resolve({ path: '/new', rows: [] }); await flush();
  h.pending[0].resolve({ path: '/var/www', rows: [] }); await flush();
  await h.submit();
  assert.deepEqual(h.chosen, ['/new']);
});

test('typing a path invalidates an outstanding selection request', async () => {
  const h = harness(); h.open();
  h.input.value = '/typed'; h.input.oninput();
  h.pending[0].resolve({ path: '/var/www', rows: [] }); await flush();
  assert.equal(h.ok.disabled, true);
  assert.equal(h.input.value, '/typed');
  await assert.rejects(h.submit(), /выберите доступную папку/);
});

test('CPU unavailable is distinct from zero; overload gauge is bounded', () => {
  const h = harness();
  assert.match(h.context.dashboardGauge('CPU', null, '', '', 'resources'), /нет данных/);
  assert.match(h.context.dashboardGauge('CPU', 0, '', '', 'resources'), /CPU: 0%/);
  const overload = h.context.dashboardGauge('Нагрузка', 180, '', '', 'resources');
  assert.match(overload, /Нагрузка: 180%/);
  assert.match(overload, /stroke-dasharray="100 100"/);
});

test('dashboard requests fresh measurements each second without overlapping requests', async () => {
  const h = harness(), rendered = [];
  let poll, interval, monitorPoll;
  const content = {}, status = {};
  const server = { querySelectorAll: () => [], className: '', innerHTML: '', querySelector: (s) => s === '.server-content' ? content : status };
  h.context.document.createElement = () => server;
  h.context.captureInterval = (ms, fn) => { if (ms === 10000) { monitorPoll = fn; return; } interval = ms; poll = fn; };
  h.context.captureData = (_, data) => rendered.push(data.cpu);
  vm.runInContext('head = () => {}; every = globalThis.captureInterval; updateDashboard = globalThis.captureData; dashboardMonitorMarkup = () => ""; createDashboardIO = () => ({update(){}, unavailable(){}}); globalThis.dashboardDef = App.pages.dashboard;', h.context);
  const dashboardAPI = h.context.fakeAPI;
  h.context.monitorAPI = (name, data) => name === 'stats.monitor' ? Promise.resolve({services: []}) : dashboardAPI(name, data);
  vm.runInContext('api = globalThis.monitorAPI;', h.context);
  const view = { isConnected: true, append() {} };
  const loading = h.context.dashboardDef.render(view);
  assert.equal(interval, 1000);
  assert.equal(h.pending.length, 1);
  await poll();
  assert.equal(h.pending.length, 1, 'slow requests must not overlap');
  h.pending[0].resolve({ cpu: 12, updated: '2026-10-06T09:00:00Z' });
  await loading;
  const next = poll();
  assert.equal(h.pending.length, 2);
  h.pending[1].resolve({ cpu: 48, updated: '2026-10-06T09:00:01Z' });
  await next;
  assert.deepEqual(rendered, [12, 48]);
  view.isConnected = false;
  await poll();
  assert.equal(h.pending.length, 2, 'leaving the page stops polling');
});

test('service attention excludes absent units, retains unknown states', () => { const h = harness(); assert.equal(h.context.dashboardNeedsAttention({state:'missing'}),false); assert.equal(h.context.dashboardNeedsAttention({state:'unknown'}),true); assert.equal(h.context.dashboardNeedsAttention({state:'stopped'}),true); assert.equal(h.context.dashboardNeedsAttention({state:'running'}),false); });

 test('IO rates use elapsed time and reject reset or stale counters', () => {
 const h=harness(); const old={in:100,out:200};
 const rate=h.context.dashboardIORate(old,{in:300,out:300},2);
 assert.equal(rate.incoming,100);assert.equal(rate.outgoing,50);
 assert.equal(h.context.dashboardIORate(old,{in:0,out:300},2),null);
 assert.equal(h.context.dashboardIORate(old,{in:300,out:300},30),null);
 assert.equal(h.context.dashboardIORate(null,{in:300,out:300},1),null);
 assert.equal(h.context.dashboardIORate(old,old,1).incoming,0);
 });
