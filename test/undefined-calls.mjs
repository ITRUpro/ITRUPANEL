// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Every global function called by the panel UI must be defined in web/*.js (or by the browser).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const acorn = createRequire(import.meta.url)('../i18n/vendor/acorn.cjs');

function visit(node, cb) {
  if (!node || typeof node.type !== 'string') return;
  cb(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) value.forEach((child) => visit(child, cb));
    else if (value && typeof value.type === 'string') visit(value, cb);
  }
}

test('panel UI uses only defined functions and values', () => {
  const declared = new Set();
  const called = new Map();
  const names = (pattern) => {
    if (!pattern) return;
    if (pattern.type === 'Identifier') declared.add(pattern.name);
    else if (pattern.type === 'ObjectPattern') pattern.properties.forEach((p) => names(p.value || p.argument));
    else if (pattern.type === 'ArrayPattern') pattern.elements.forEach(names);
    else if (pattern.type === 'AssignmentPattern') names(pattern.left);
    else if (pattern.type === 'RestElement') names(pattern.argument);
  };
  for (const file of fs.readdirSync(path.join(root, 'web')).filter((f) => f.endsWith('.js'))) {
    const tree = acorn.parse(fs.readFileSync(path.join(root, 'web', file), 'utf8'), { ecmaVersion: 'latest', sourceType: 'script' });
    visit(tree, (node) => {
      if ((node.type === 'FunctionDeclaration' || node.type === 'FunctionExpression') && node.id) declared.add(node.id.name);
      if (node.type === 'VariableDeclarator') names(node.id);
      if (['FunctionDeclaration', 'FunctionExpression', 'ArrowFunctionExpression'].includes(node.type)) node.params.forEach(names);
      if (node.type === 'CatchClause') names(node.param);
      if (node.type === 'AssignmentExpression') {
        if (node.left.type === 'Identifier') declared.add(node.left.name);
        if (node.left.type === 'MemberExpression' && node.left.object.name === 'window' && node.left.property.type === 'Identifier') declared.add(node.left.property.name);
      }
      const ref = (n) => { if (n && n.type === 'Identifier') called.set(n.name, file); };
      if (node.type === 'CallExpression' || node.type === 'NewExpression') { ref(node.callee); node.arguments.forEach(ref); }
      if (node.type === 'Property' && !node.shorthand) ref(node.value);
      if (node.type === 'Property' && node.shorthand) ref(node.value);
      if (node.type === 'VariableDeclarator') ref(node.init);
      if (node.type === 'AssignmentExpression') ref(node.right);
      if (node.type === 'ArrayExpression') node.elements.forEach(ref);
      if (node.type === 'ConditionalExpression') { ref(node.consequent); ref(node.alternate); }
      if (node.type === 'LogicalExpression') { ref(node.left); ref(node.right); }
      if (node.type === 'ReturnStatement' || node.type === 'ArrowFunctionExpression') ref(node.argument || (node.expression ? node.body : null));
    });
  }
  const browser = new Set([...Object.getOwnPropertyNames(globalThis), 'fetch', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'requestAnimationFrame', 'alert', 'confirm', 'getComputedStyle', 'matchMedia', 'Terminal', 'FitAddon', 'document', 'window', 'navigator', 'location', 'history', 'localStorage', 'sessionStorage', 'ResizeObserver', 'MutationObserver', 'IntersectionObserver', 'XMLHttpRequest', 'FormData', 'Blob', 'File', 'FileReader', 'WebSocket', 'Image', 'Node', 'HTMLElement', 'Event', 'CustomEvent', 'KeyboardEvent', 'ClipboardItem', 'screen']);
  const missing = [...called].filter(([name]) => !declared.has(name) && !browser.has(name)).map(([name, file]) => `${name} (${file})`);
  assert.deepEqual(missing, []);
});
