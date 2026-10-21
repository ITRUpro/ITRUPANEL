#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Сборка: bin/itrupanel-linux-{amd64,arm64}
set -e
cd "$(dirname "$0")"
"${NODE:-node}" i18n/build.mjs
"${NODE:-node}" i18n/docs.mjs
mkdir -p bin
for a in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$a go build -trimpath -ldflags="-s -w" -o bin/itrupanel-linux-$a .
done
ls -lh bin
