# RU / EN localization

Run `node i18n/build.mjs --extract` to inventory UI text, then review translations
in `en-ui.json`. Run `node i18n/build.mjs` to generate web/en assets and the
English website. Acorn parses JavaScript; only static string literals and
template text are translated. Dynamic user data is never translated.

Server translations live in en-server.json and are embedded in the Go binary.
localization.go translates an explicit list of API presentation fields only.
OS command output, raw logs and configuration editor contents remain original.
Release descriptions have signed notes and notes_en fields. Keep placeholders
in exactly the same order and type. Never translate protocol identifiers.

build.sh regenerates the English assets (requires Node at build time, not on
the target server). Go and JavaScript tests cover preference persistence,
manual overrides, formatting and preservation of user data.
