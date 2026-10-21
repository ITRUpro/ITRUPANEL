#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Тест установки в контейнере: test/run.sh <имя> <образ> <порт> <лог>
set -u
name=$1 image=$2 port=$3 log=$4
src="$(cd "$(dirname "$0")/.." && pwd)"
docker rm -f "$name" >/dev/null 2>&1
docker run -d --cap-add=ALL --name "$name" -p "$port:3333" -v "$src":/src:ro "$image" sleep infinity >/dev/null
docker exec "$name" bash -c 'rm -rf /root/itrupanel && cp -r /src /root/itrupanel && cd /root/itrupanel && bash install.sh' >"$log" 2>&1
echo "exit=$?" >>"$log"
