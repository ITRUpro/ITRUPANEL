# Contributing to ITRUPANEL

Thank you for helping. Bug reports, ideas, translations, documentation fixes and code are all welcome.

## Before you start

- Search the [issues](https://github.com/ITRUpro/ITRUPANEL/issues) to avoid duplicates.
- For a bigger change, open an issue first and describe the idea, so we can agree on the approach before you spend time.
- Security problems: see [SECURITY.md](SECURITY.md); do not post them publicly.

## Development setup

You need Go (the version in `go.mod`) and Node.js 20 or newer.

```bash
./build.sh                                   # translations, docs, and binaries for amd64 and arm64
go vet ./... && go test ./...                # Go tests
node --test test/*.mjs                       # interface tests
go run . -demo -http -listen 127.0.0.1:3399  # try the interface; system commands are not executed
```

Demo mode keeps its files in a temporary folder and never runs real system commands, so it is safe to experiment.

## Rules for changes

- **Interface text** is written in Russian in `web/*.js`; the English text is in `i18n/en-ui.json` (UI) and
  `i18n/en-server.json` (server messages). The build fails when a translation is missing.
- **Documentation:** describe every new feature in `release/docs-src.py`, in both Russian and English
  (`./build.sh` regenerates `docs.html` and `en/docs.html`).
- **Tests:** add a Go test for server behaviour; the interface has Node tests in `test/`.
- **Files:** write configuration files atomically (see `writeFileAtomic`), validate the nginx configuration before reloading
  and restore the previous one on error.
- **Secrets:** never log passwords or keys and never put real credentials in tests.
- Keep the licence header at the top of every new source file:

  ```
  Copyright (c) 2026 <your name>. ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
  ```

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Make a focused change with a clear description of what and why.
3. Run the tests above.
4. Open the pull request. By submitting it you agree that your contribution is licensed under the GPL-3.0-only, like the rest
   of the project.

## Translations

The panel has Russian and English interfaces. To improve a translation, edit `i18n/en-ui.json` or `i18n/en-server.json` and open a pull
request. Proposals for new interface languages are welcome as an issue.
