// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Run with: node --test test/navigation.mjs
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

const source = readFileSync(process.env.ITRUPANEL_APP_JS || new URL('../src/web/app.js', import.meta.url), 'utf8');
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function harness() {
  let active;
  class View {
    constructor() { this.isConnected = false; this.children = []; }
    set innerHTML(value) { this.children = value ? [value] : []; }
    get innerHTML() { return this.children.join(''); }
    append(value) { this.children.push(value); }
    replaceWith(next) {
      assert.equal(active, this);
      this.isConnected = false;
      next.isConnected = true;
      active = next;
    }
  }
  active = new View();
  active.isConnected = true;
  const timers = new Map();
  let timerID = 0;
  const context = vm.createContext({
    document: {
      querySelector: (selector) => selector === '#view' ? active : null,
      querySelectorAll: () => [],
      createElement: () => new View(),
      addEventListener() {},
    },
    window: { scrollTo() {}, addEventListener() {} },
    localStorage: { getItem: () => null, setItem() {} },
    setInterval: (fn) => { const id = ++timerID; timers.set(id, fn); return id; },
    clearInterval: (id) => timers.delete(id),
    URLSearchParams,
    key: 'dashboard', errors: [], badgeCalls: 0,
  });
  vm.runInContext(source, context);
  vm.runInContext(`
    App.me = { user: 'admin' };
    App.pages.dashboard = { render() {} };
    renderTabs = () => {};
    parseHash = () => ({ key: globalThis.key, params: {} });
    refreshBadge = () => { globalThis.badgeCalls++; };
    toast = (message) => globalThis.errors.push(message);
    globalThis.runRoute = route;
    globalThis.state = App;
    globalThis.schedule = every;
  `, context);
  return {
    context, timers,
    view: () => active,
    route(key) { context.key = key; return context.runRoute(); },
    page(key, render) { context.state.pages[key] = { render }; },
  };
}

test('late DNS response cannot append to the disk page', async () => {
  const h = harness(), dns = deferred();
  h.page('dns', async (view) => { await dns.promise; view.append('DNS'); });
  h.page('disk', (view) => view.append('Disk usage'));
  const pending = h.route('dns');
  await h.route('disk');
  dns.resolve();
  await pending;
  assert.equal(h.view().innerHTML, 'Disk usage');
  assert.equal(h.context.state.current, 'disk');
  assert.equal(h.context.badgeCalls, 1);
});

test('late error from an abandoned page does not appear on current page', async () => {
  const h = harness(), dns = deferred();
  h.page('dns', async () => { await dns.promise; });
  h.page('disk', (view) => view.append('Disk usage'));
  const pending = h.route('dns');
  await h.route('disk');
  dns.reject(new Error('Old DNS request failed'));
  await pending;
  assert.equal(h.context.errors.length, 0);
});

test('returning to the same route does not revive its older response', async () => {
  const h = harness(), old = deferred();
  let count = 0;
  h.page('dns', async (view) => {
    const first = ++count === 1;
    if (first) await old.promise;
    view.append(first ? 'Old DNS' : 'New DNS');
  });
  const pending = h.route('dns');
  await h.route('dns');
  old.resolve();
  await pending;
  assert.equal(h.view().innerHTML, 'New DNS');
});

test('abandoned page cannot register polling after its response arrives', async () => {
  const h = harness(), old = deferred();
  h.page('php', async (view) => {
    await old.promise;
    h.context.schedule(5000, () => {}, view);
  });
  const pending = h.route('php');
  await h.route('dashboard');
  old.resolve();
  await pending;
  assert.equal(h.timers.size, 0);
});

test('active polling is cleared at the next navigation', async () => {
  const h = harness();
  h.page('php', (view) => h.context.schedule(5000, () => {}, view));
  await h.route('php');
  assert.equal(h.timers.size, 1);
  await h.route('dashboard');
  assert.equal(h.timers.size, 0);
});

