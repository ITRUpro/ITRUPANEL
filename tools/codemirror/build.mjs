// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Сборка редактора кода в один файл: npm install && node build.mjs  →  ../../src/web/vendor/codemirror.js
import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';
const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.join(here, '../../src/web/vendor/codemirror.js');
const banner = '/*! CodeMirror 6 (MIT, Copyright (C) 2018-2021 by Marijn Haverbeke and others), Lezer (MIT), @codemirror/theme-one-dark (MIT). Bundled for ITRUPANEL; licenses are listed in THIRD-PARTY-NOTICES.txt. */';
await build({ entryPoints: [path.join(here, 'entry.js')], bundle: true, minify: true, format: 'iife', target: ['es2020'], outfile: out, banner: { js: banner }, legalComments: 'none', logLevel: 'info' });
console.log('размер:', (fs.statSync(out).size / 1024).toFixed(0), 'КБ');
