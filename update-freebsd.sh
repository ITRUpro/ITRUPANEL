#!/bin/sh
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Обновление установленной панели на FreeBSD без повторной настройки всех служб.
set -eu
PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
if [ "${1:-}" = --help ]; then
  echo 'Из папки новой сборки, в root-сессии: sh update-freebsd.sh'
  echo 'Требуется установленная панель в /usr/local/itrupanel.'
  exit 0
fi
[ "$#" = 0 ] || { echo 'Неизвестный параметр'; exit 1; }
[ "$(id -u)" = 0 ] || { echo 'Откройте root-сессию и повторите: sh update-freebsd.sh'; exit 1; }
[ "$(uname -s)" = FreeBSD ] || { echo 'Этот скрипт только для FreeBSD.'; exit 1; }
PANEL_DIR=/usr/local/itrupanel
HERE=$(cd "$(dirname "$0")" && pwd)
[ -f "$PANEL_DIR/etc/state.json" ] && [ -x "$PANEL_DIR/itrupanel" ] || { echo 'Панель не установлена. Для новой установки: sh install-freebsd.sh'; exit 1; }
case "$(uname -m)" in
  amd64|x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo 'Неизвестная архитектура'; exit 1 ;;
esac
BIN="$HERE/bin/itrupanel-freebsd-$ARCH"
chmod +x "$BIN"
VERSION=$("$BIN" version)

# Оболочка, открытая в панели, принадлежит службе itrupanel: её остановка прервала бы обновление.
# Поэтому из такой оболочки работа передаётся отдельному процессу (daemon).
inside_panel() {
  pid=$$
  while [ "$pid" -gt 1 ]; do
    cmd=$(ps -o command= -p "$pid" 2>/dev/null | cut -d' ' -f1)
    [ "$cmd" = "$PANEL_DIR/itrupanel" ] && return 0
    pid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')
    [ -n "$pid" ] || break
  done
  return 1
}
if [ "${ITRUPANEL_UPDATE_DETACHED:-}" != 1 ] && inside_panel; then
  umask 077
  STAGE=$(mktemp -d /var/tmp/itrupanel-update.XXXXXXXX)
  mkdir -p "$STAGE/bin" "$PANEL_DIR/var"
  cp -- "$0" "$STAGE/update-freebsd.sh"
  cp -- "$BIN" "$STAGE/bin/itrupanel-freebsd-$ARCH"
  LOG="$PANEL_DIR/var/update-$(date +%Y%m%d-%H%M%S).log"
  : > "$LOG"
  ITRUPANEL_UPDATE_DETACHED=1 daemon -f -o "$LOG" /bin/sh "$STAGE/update-freebsd.sh"
  echo 'Обновление запущено отдельным процессом. Shell-клиент закроется при перезапуске панели.'
  echo "Журнал обновления: $LOG"
  exit 0
fi

LOCK=/var/run/itrupanel-update.lock
if [ -e "$LOCK" ] && kill -0 "$(cat "$LOCK" 2>/dev/null)" 2>/dev/null; then
  echo 'Другое обновление ITRUPANEL уже выполняется.' >&2
  exit 1
fi
echo $$ > "$LOCK"
trap 'rm -f "$LOCK"' EXIT

"$BIN" check-update-from "$("$PANEL_DIR/itrupanel" version)"
BACKUP_DIR="$PANEL_DIR/var/before-update-$(date +%Y%m%d-%H%M%S)-$$"
mkdir -m 700 -p "$BACKUP_DIR"
cp -p "$PANEL_DIR/itrupanel" "$BACKUP_DIR/itrupanel"

ok=0
rollback() {
  [ "$ok" = 1 ] && return 0
  echo "Ошибка обновления. Восстанавливаю предыдущую программу из $BACKUP_DIR."
  service itrupanel onestop >/dev/null 2>&1 || true
  "$BIN" rollback-update "$BACKUP_DIR" || echo "ВНИМАНИЕ: откат настроек завершился с ошибкой; сохранена копия $BACKUP_DIR" >&2
  install -m 755 "$BACKUP_DIR/itrupanel" "$PANEL_DIR/itrupanel.rollback" && mv -f "$PANEL_DIR/itrupanel.rollback" "$PANEL_DIR/itrupanel"
  service itrupanel onestart >/dev/null 2>&1 </dev/null || true
  echo 'Проверьте сообщение об ошибке выше. Обновление не завершено.'
}
trap 'rollback; rm -f "$LOCK"' EXIT

service itrupanel onestop </dev/null >/dev/null 2>&1 || true
cp -p "$PANEL_DIR/etc/state.json" "$BACKUP_DIR/state.json"
"$BIN" migrate-update "$BACKUP_DIR"
install -m 755 "$BIN" "$PANEL_DIR/itrupanel.new"
mv -f "$PANEL_DIR/itrupanel.new" "$PANEL_DIR/itrupanel"
service itrupanel onestart </dev/null >/dev/null 2>&1

healthy=0
i=0
while [ "$i" -lt 30 ]; do
  if service itrupanel onestatus >/dev/null 2>&1 && "$PANEL_DIR/itrupanel" health-check >/dev/null 2>&1; then
    healthy=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ "$healthy" = 1 ] || { echo 'Новая версия не прошла проверку работоспособности.' >&2; exit 1; }
ok=1
# Рабочие папки, оставленные онлайн-установщиком и отдельным процессом обновления, больше не нужны
case "$HERE" in
  /var/tmp/itrupanel-download.*/itrupanel) rm -rf "$(dirname "$HERE")" ;;
  /var/tmp/itrupanel-update.*) rm -rf "$HERE" ;;
esac
echo "ITRUPANEL обновлена до $VERSION. Резервная копия: $BACKUP_DIR"
echo 'Откройте панель на прежнем адресе (обычно https://IP_СЕРВЕРА:3333) и обновите страницу.'
