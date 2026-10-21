// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
// Точка входа сборки редактора кода: CodeMirror 6 → src/web/vendor/codemirror.js (global ITRUEditor).
import { EditorState, Compartment } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightSpecialChars, drawSelection, dropCursor, rectangularSelection, crosshairCursor, highlightActiveLine } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { searchKeymap, highlightSelectionMatches, openSearchPanel } from '@codemirror/search';
import { indentOnInput, syntaxHighlighting, defaultHighlightStyle, bracketMatching, foldGutter, foldKeymap, StreamLanguage, indentUnit } from '@codemirror/language';
import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete';
import { oneDark } from '@codemirror/theme-one-dark';
import { php } from '@codemirror/lang-php';
import { html } from '@codemirror/lang-html';
import { css } from '@codemirror/lang-css';
import { javascript } from '@codemirror/lang-javascript';
import { json } from '@codemirror/lang-json';
import { markdown } from '@codemirror/lang-markdown';
import { sql } from '@codemirror/lang-sql';
import { yaml } from '@codemirror/lang-yaml';
import { xml } from '@codemirror/lang-xml';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { nginx } from '@codemirror/legacy-modes/mode/nginx';
import { properties } from '@codemirror/legacy-modes/mode/properties';
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile';
import { toml } from '@codemirror/legacy-modes/mode/toml';
import { python } from '@codemirror/legacy-modes/mode/python';
import { go } from '@codemirror/legacy-modes/mode/go';
import { diff } from '@codemirror/legacy-modes/mode/diff';
import { languageFor } from '../../src/web/vendor/editor-langs.mjs';

const legacy = (mode) => () => StreamLanguage.define(mode);
// Название языка → создатель расширения. Названия показываются в строке состояния редактора.
const LANGS = {
  PHP: () => php(), HTML: () => html(), CSS: () => css(),
  JavaScript: () => javascript({ jsx: true }), TypeScript: () => javascript({ jsx: true, typescript: true }),
  JSON: () => json(), Markdown: () => markdown(), SQL: () => sql(), YAML: () => yaml(), XML: () => xml(),
  Shell: legacy(shell), Nginx: legacy(nginx), INI: legacy(properties), Dockerfile: legacy(dockerFile),
  TOML: legacy(toml), Python: legacy(python), Go: legacy(go), Diff: legacy(diff),
};

const baseTheme = EditorView.theme({
  '&': { height: '100%', fontSize: '13px' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace', lineHeight: '1.55', overflow: 'auto' },
  '.cm-content': { padding: '8px 0' },
  '&.cm-focused': { outline: 'none' },
  '.cm-panels': { fontSize: '13px' },
});
const lightTheme = EditorView.theme({
  '&': { backgroundColor: 'var(--card, #fff)', color: 'var(--text, #1a2233)' },
  '.cm-gutters': { backgroundColor: 'transparent', color: '#8a94a8', border: 'none' },
  '.cm-activeLine': { backgroundColor: 'rgba(58,91,255,.06)' },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(58,91,255,.08)' },
}, { dark: false });

// create создаёт редактор в элементе parent.
// opts: text, filename, dark, wrap, readOnly, phrases, onChange(dirty), onSave(), onStatus({line, col, lines, language}).
function create(parent, opts = {}) {
  const themeC = new Compartment();
  const langC = new Compartment();
  const wrapC = new Compartment();
  const roC = new Compartment();
  let language = languageFor(opts.filename);
  let initial = opts.text || '';
  const status = (view) => {
    if (!opts.onStatus) return;
    const head = view.state.selection.main.head;
    const line = view.state.doc.lineAt(head);
    opts.onStatus({ line: line.number, col: head - line.from + 1, lines: view.state.doc.lines, language });
  };
  const extensions = [
    lineNumbers(), highlightActiveLineGutter(), highlightSpecialChars(), history(), foldGutter(), drawSelection(), dropCursor(),
    EditorState.allowMultipleSelections.of(true), indentOnInput(), indentUnit.of('    '),
    syntaxHighlighting(defaultHighlightStyle, { fallback: true }), bracketMatching(), closeBrackets(),
    rectangularSelection(), crosshairCursor(), highlightActiveLine(), highlightSelectionMatches(),
    keymap.of([
      { key: 'Mod-s', preventDefault: true, run: () => { if (opts.onSave) opts.onSave(); return true; } },
      ...closeBracketsKeymap, ...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, indentWithTab,
    ]),
    baseTheme,
    themeC.of(opts.dark ? oneDark : lightTheme),
    langC.of(language && LANGS[language] ? LANGS[language]() : []),
    wrapC.of(opts.wrap ? EditorView.lineWrapping : []),
    roC.of([EditorState.readOnly.of(!!opts.readOnly), EditorView.editable.of(!opts.readOnly)]),
    EditorView.updateListener.of((u) => {
      if (u.docChanged && opts.onChange) opts.onChange(u.state.doc.toString() !== initial);
      if (u.docChanged || u.selectionSet) status(u.view);
    }),
  ];
  if (opts.phrases) extensions.push(EditorState.phrases.of(opts.phrases));
  const view = new EditorView({ state: EditorState.create({ doc: initial, extensions }), parent });
  status(view);
  return {
    view,
    get language() { return language; },
    getText: () => view.state.doc.toString(),
    markSaved: () => { initial = view.state.doc.toString(); if (opts.onChange) opts.onChange(false); },
    focus: () => view.focus(),
    setDark: (dark) => view.dispatch({ effects: themeC.reconfigure(dark ? oneDark : lightTheme) }),
    setWrap: (on) => view.dispatch({ effects: wrapC.reconfigure(on ? EditorView.lineWrapping : []) }),
    setLanguage: (name) => { language = LANGS[name] ? name : ''; view.dispatch({ effects: langC.reconfigure(language ? LANGS[language]() : []) }); status(view); },
    languages: Object.keys(LANGS),
    find: () => openSearchPanel(view),
    destroy: () => view.destroy(),
  };
}

window.ITRUEditor = { create, languageFor, languages: Object.keys(LANGS) };
