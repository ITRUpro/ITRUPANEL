#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Обновление установленной панели без повторной настройки всех служб.
set -euo pipefail
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}"
if [ "${1:-}" = --help ]; then
  echo 'Из папки новой сборки, в root-сессии: bash update.sh'
  echo 'Требуется systemd и установленная панель в /usr/local/itrupanel.'
  exit 0
fi
[ "$#" = 0 ] || { echo 'Неизвестный параметр'; exit 1; }
[ "$(id -u)" = 0 ] || { echo 'Откройте root-сессию и повторите: bash update.sh'; exit 1; }
[ -d /run/systemd/system ] || { echo 'Этот скрипт обновления требует systemd.'; exit 1; }
PANEL_DIR=/usr/local/itrupanel
HERE="$(cd "$(dirname "$0")" && pwd)"
[ -f "$PANEL_DIR/etc/state.json" ] && [ -x "$PANEL_DIR/itrupanel" ] || { echo 'Панель не установлена. Для новой установки: bash install.sh'; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo 'Неизвестная архитектура'; exit 1 ;;
esac
BIN="$HERE/bin/itrupanel-linux-$ARCH"
chmod +x "$BIN"
VERSION="$("$BIN" version)"

# A shell opened by the panel belongs to itrupanel.service. Stopping that unit
# kills the shell and its children, so hand the work to a separate systemd unit.
detach_from_panel() {
  local group in_panel=0 stage unit logfile
  if [ -r /proc/self/cgroup ]; then
    while IFS= read -r group; do
      case "$group" in */itrupanel.service|*/itrupanel.service/*) in_panel=1 ;; esac
    done </proc/self/cgroup
  fi
  [ "$in_panel" = 1 ] || return 0
  command -v systemd-run >/dev/null || { echo 'Обновляйте из отдельной SSH-сессии: systemd-run не найден.' >&2; exit 1; }
  umask 077
  stage=$(mktemp -d /var/tmp/itrupanel-update.XXXXXXXX)
  unit="itrupanel-update-$(date +%Y%m%d-%H%M%S)-$$"
  logfile="/usr/local/itrupanel/var/$unit.log"
  if ! (
    set -e
    mkdir -p /usr/local/itrupanel/var || exit 1
    : >"$logfile" || exit 1
    cp -- "$HERE/update.sh" "$stage/entry.sh" || exit 1
    mkdir -p "$stage/bin" || exit 1
    cp -- "$BIN" "$stage/bin/itrupanel-linux-$ARCH" || exit 1
    cat >"$stage/run.sh" <<'WORKER'
#!/usr/bin/env bash
set -euo pipefail
stage="$(cd "$(dirname "$0")" && pwd)"
exec >>"$1" 2>&1
trap 'rm -rf -- "$stage"' EXIT
export TMPDIR="$stage"
echo 'Обновление выполняется отдельно от службы панели.'
bash "$stage/entry.sh"
WORKER
  ); then
    rm -rf -- "$stage"
    echo 'Не удалось подготовить независимое обновление. Панель не остановлена.' >&2
    exit 1
  fi
  echo 'Запускаю обновление в отдельной службе. Shell-клиент закроется при перезапуске панели.'
  echo "Журнал обновления: $logfile"
  echo "Проверка через SSH: systemctl status $unit --no-pager -l"
  if ! systemd-run --unit="$unit" --description='ITRUPANEL independent update' /bin/bash "$stage/run.sh" "$logfile"; then
    rm -rf -- "$stage"
    echo 'Отдельная служба не запущена. Обновление остановлено до изменения панели.' >&2
    exit 1
  fi
  echo 'Обновление передано systemd. Дождитесь завершения в журнале и откройте панель снова.'
  exit 0
}

detach_from_panel
command -v flock >/dev/null || { echo 'Нужна утилита flock из util-linux.' >&2; exit 1; }
exec 8>/run/lock/itrupanel-update.lock
flock -n 8 || { echo 'Другое обновление ITRUPANEL уже выполняется.' >&2; exit 1; }
"$BIN" check-update-from "$("$PANEL_DIR/itrupanel" version)"
BACKUP_DIR="$PANEL_DIR/var/before-update-$(date +%Y%m%d-%H%M%S)-$$"
mkdir -m 700 -p "$BACKUP_DIR"
cp -a "$PANEL_DIR/itrupanel" "$BACKUP_DIR/itrupanel"
restore_on_error() {
  local code=$?
  trap - ERR TERM INT
  systemctl stop itrupanel || true
  "$BIN" rollback-update "$BACKUP_DIR" || echo "ВНИМАНИЕ: откат настроек завершился с ошибкой; сохранена копия $BACKUP_DIR" >&2
  echo "Ошибка обновления. Восстанавливаю предыдущую программу из $BACKUP_DIR."
  install -m 755 "$BACKUP_DIR/itrupanel" "$PANEL_DIR/itrupanel.rollback" && mv -f "$PANEL_DIR/itrupanel.rollback" "$PANEL_DIR/itrupanel"
  systemctl restart itrupanel || true
  echo 'Проверьте сообщение об ошибке выше. Обновление не завершено.'
  exit "$code"
}
trap restore_on_error ERR
trap 'false' TERM INT
systemctl stop itrupanel
cp -a "$PANEL_DIR/etc/state.json" "$BACKUP_DIR/state.json"
"$BIN" migrate-update "$BACKUP_DIR"
install -m 755 "$BIN" "$PANEL_DIR/itrupanel.new"
mv -f "$PANEL_DIR/itrupanel.new" "$PANEL_DIR/itrupanel"
systemctl start itrupanel
healthy=0
for attempt in $(seq 1 30); do
  if systemctl is-active --quiet itrupanel && "$PANEL_DIR/itrupanel" health-check >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 1
done
[ "$healthy" = 1 ] || { echo 'Новая версия не прошла проверку работоспособности.' >&2; false; }
trap - ERR TERM INT
echo "ITRUPANEL обновлена до $VERSION. Резервная копия: $BACKUP_DIR"
echo 'Откройте панель на прежнем адресе (обычно https://IP_СЕРВЕРА:3333) и обновите страницу.'
