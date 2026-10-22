// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

function page(version) {
  const mk = (cls, text) => ({ cls, textContent: text, firstChild: { nodeType: 3, nodeValue: text } });
  const els = {
    '.version, .terminal-version': [mk('v', '0.2.7'), mk('tv', '0.2.7')],
    '.sidebar-caption': [mk('sc', 'ITRUPANEL 0.2.7')],
  };
  const link = { href: 'panel/itrupanel-full-0.2.7.zip', childNodes: [{ nodeType: 1 }, { nodeType: 3, nodeValue: ' Скачать архив 0.2.7' }], getAttribute(n) { return n === 'href' ? this.href : null; }, setAttribute(n, v) { if (n === 'href') this.href = v; } };
  els['a[href*="itrupanel-full-"]'] = [link];
  const payload = Buffer.from(JSON.stringify({ version })).toString('base64');
  const c = vm.createContext({
    document: { querySelectorAll: (sel) => els[sel] || [] },
    fetch: async () => ({ ok: true, json: async () => ({ payload }) }),
    atob: (s) => Buffer.from(s, 'base64').toString('binary'), TextDecoder, Uint8Array,
  });
  vm.runInContext(fs.readFileSync(new URL('../release/panel-assets/latest.js', import.meta.url), 'utf8'), c);
  return { els, link };
}

test('site shows the version and download link from latest.json', async () => {
  const { els, link } = page('0.3.12');
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(els['.version, .terminal-version'][0].textContent, '0.3.12');
  assert.equal(els['.version, .terminal-version'][1].textContent, '0.3.12');
  assert.equal(els['.sidebar-caption'][0].firstChild.nodeValue, 'ITRUPANEL 0.3.12');
  assert.equal(link.href, 'panel/itrupanel-full-0.3.12.zip');
  assert.equal(link.childNodes[1].nodeValue, ' Скачать архив 0.3.12');
});

test('site ignores a version that is not a plain number', async () => {
  for (const bad of ['0.2.7"><script>', '../../x', '1.2', '1.2.3.4', '']) {
    const { els, link } = page(bad);
    await new Promise((r) => setTimeout(r, 30));
    assert.equal(els['.version, .terminal-version'][0].textContent, '0.2.7', bad);
    assert.equal(link.href, 'panel/itrupanel-full-0.2.7.zip', bad);
  }
});

test('both site pages load the script', () => {
  for (const f of ['../release/index.html']) assert.match(fs.readFileSync(new URL(f, import.meta.url), 'utf8'), /\/panel-assets\/latest\.js/);
});
