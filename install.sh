#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# ITRUPANEL — установка на Debian 11–13, Ubuntu 22.04 / 24.04 / 26.04, AlmaLinux / Rocky / RHEL / CentOS Stream 8–10.
# Запуск из распакованного архива:  bash install.sh            (PHP 8.3 по умолчанию)
#                                   PHP_VERSION=8.4 bash install.sh
#                                   PHP_VERSION=none bash install.sh   (без PHP)
set -euo pipefail
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}"

PHP_VERSION="${PHP_VERSION:-8.3}"
HOME_DIR=/usr/local/itrupanel
HERE="$(cd "$(dirname "$0")" && pwd)"

c_ok=$'\e[32m'; c_err=$'\e[31m'; c_b=$'\e[1m'; c_0=$'\e[0m'
say() { echo "${c_b}==>${c_0} $*"; }
die() { echo "${c_err}Ошибка:${c_0} $*" >&2; exit 1; }

case "${1:-}" in
  -h|--help)
    echo "Установка ITRUPANEL на чистый Linux-сервер (нужны root и доступ в интернет)."
    echo "  bash install.sh                       — PHP 8.3"
    echo "  PHP_VERSION=8.4 bash install.sh        — другая версия PHP"
    echo "  PHP_VERSION=none bash install.sh       — без PHP и phpMyAdmin"
    echo "  PANEL_LOGIN=имя PANEL_PASSWORD=пароль bash install.sh — свои логин и пароль (иначе случайные)"
    echo "Подробности и обновление: УСТАНОВКА.txt рядом с установщиком."
    exit 0 ;;
  '') ;;
  *) die "неизвестный параметр $1; справка: bash install.sh --help" ;;
esac
[ "$#" -le 1 ] || die "слишком много параметров"
case "$PHP_VERSION" in
  none|5.6|7.[0-4]|8.[0-5]) ;;
  *) die "неподдерживаемое значение PHP_VERSION=$PHP_VERSION" ;;
esac
trap 'echo "Ошибка установки (строка $LINENO). Установка не завершена; проверьте сообщение выше." >&2' ERR

[ "$(id -u)" = 0 ] || die "запустите от root (bash install.sh из root-сессии)"
[ -r /etc/os-release ] || die "не найден /etc/os-release"
. /etc/os-release
FAMILY=""
case " ${ID} ${ID_LIKE:-} " in
  *" debian "*|*" ubuntu "*) FAMILY=debian ;;
  *" rhel "*|*" centos "*|*" fedora "*|*" almalinux "*|*" rocky "*) FAMILY=rhel ;;
esac
[ -n "$FAMILY" ] || die "неподдерживаемая ОС: ${PRETTY_NAME:-$ID}"

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "неподдерживаемая архитектура $(uname -m)" ;;
esac
BIN="$HERE/bin/itrupanel-linux-$ARCH"
[ -x "$BIN" ] || chmod +x "$BIN" 2>/dev/null || true
[ -f "$BIN" ] || die "не найден $BIN — распакуйте архив целиком"
"$BIN" version >/dev/null || die "бинарник не запускается на этом сервере ($ARCH)"

# Логотип и проверка сервера (онлайн-установщик уже показал их и передал ITRUPANEL_BANNER=1).
. "$HERE/install-banner.sh"
if [ -z "${ITRUPANEL_BANNER:-}" ]; then
  itru_banner
  itru_preflight "${PRETTY_NAME:-$ID}" "$ARCH" install
fi
say "ITRUPANEL: ${PRETTY_NAME}, $ARCH"

. "$HERE/installation-stats.sh"
if [ "${ITRUPANEL_INSTALL_STATS:-1}" != 0 ]; then
  echo 'Статистика новых установок: случайный ID и версия отправятся на itrupanel.com после успешного запуска.'
  echo 'Сохраняются IP сервера и страна по нему. Отключение: ITRUPANEL_INSTALL_STATS=0 bash install.sh'
fi
itru_stats_prepare || true

# На серверах с 1–2 ГБ памяти без swap менеджер пакетов (особенно dnf с метаданными EPEL)
# завершается ядром по нехватке памяти (Killed). Создаём постоянный файл подкачки.
# Отключение: ITRUPANEL_SWAP=0 bash install.sh
ensure_swap() {
  [ "${ITRUPANEL_SWAP:-1}" != 0 ] || return 0
  local mem_kb swap_kb free_mb size_mb
  mem_kb=$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)
  swap_kb=$(awk '/^SwapTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)
  [ "${mem_kb:-0}" -gt 0 ] && [ "$mem_kb" -lt 2000000 ] && [ "${swap_kb:-0}" -lt 512000 ] || return 0
  [ ! -e /swapfile ] || return 0
  free_mb=$(df -Pm / | awk 'NR==2{print $4}')
  size_mb=2048
  [ "${free_mb:-0}" -ge 8192 ] || size_mb=1024
  if [ "${free_mb:-0}" -lt $((size_mb + 2048)) ]; then
    echo "Мало памяти и места на диске для файла подкачки — установка может прерваться. Добавьте swap вручную при ошибке Killed." >&2
    return 0
  fi
  say "Памяти $((mem_kb / 1024)) МБ и нет swap — создаю файл подкачки /swapfile на ${size_mb} МБ"
  if dd if=/dev/zero of=/swapfile bs=1M count="$size_mb" status=none && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile; then
    grep -qs '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  else
    # В контейнерах (OpenVZ/LXC) swap включить нельзя — продолжаем без него
    swapoff /swapfile 2>/dev/null || true
    rm -f /swapfile
    echo "Не удалось включить файл подкачки (возможно, сервер — контейнер). Продолжаю без него." >&2
  fi
}
ensure_swap


if [ "$FAMILY" = debian ]; then
  export DEBIAN_FRONTEND=noninteractive
  # Repair known obsolete sources before the first apt update.
  "$BIN" prepare-apt-repositories
  say "Обновляю список пакетов"
  apt-get update -q
  say "Ставлю nginx, MariaDB, certbot, pure-ftpd и утилиты"
  apt-get install -y -q --no-install-recommends \
    nginx mariadb-server mariadb-client certbot pure-ftpd cron logrotate bind9 bind9-utils \
    curl ca-certificates gnupg lsb-release tar gzip unzip zip openssl rsync procps iproute2 util-linux kmod e2fsprogs
else
  say "Подключаю EPEL"
  MAJOR="${VERSION_ID%%.*}"
  if ! rpm -q epel-release >/dev/null 2>&1; then
    dnf install -y epel-release || dnf install -y "https://dl.fedoraproject.org/pub/epel/epel-release-latest-${MAJOR}.noarch.rpm"
  fi
  # В части образов хостеров epel-release уже стоит, но репозиторий выключен (enabled=0)
  dnf config-manager --set-enabled epel 2>/dev/null || sed -i '/^\[epel\]/,/^\[/ s/^enabled=0/enabled=1/' /etc/yum.repos.d/epel.repo
  if [ "$MAJOR" -ge 9 ] 2>/dev/null; then dnf config-manager --set-enabled crb 2>/dev/null || true; else dnf config-manager --set-enabled powertools 2>/dev/null || true; fi
  say "Ставлю nginx, MariaDB, certbot, pure-ftpd и утилиты"
  dnf install -y nginx mariadb-server mariadb certbot pure-ftpd cronie logrotate bind bind-utils \
    ca-certificates tar gzip unzip zip openssl rsync procps-ng iproute util-linux kmod e2fsprogs policycoreutils-python-utils
fi

say "Копирую панель в $HOME_DIR"
mkdir -p "$HOME_DIR"/{etc,var,bin,www}
chmod 755 "$HOME_DIR" "$HOME_DIR/bin" "$HOME_DIR/www"
chmod 700 "$HOME_DIR/etc" "$HOME_DIR/var"
install -m 755 "$BIN" "$HOME_DIR/itrupanel.new"
mv -f "$HOME_DIR/itrupanel.new" "$HOME_DIR/itrupanel"
ln -sf "$HOME_DIR/itrupanel" /usr/local/bin/itrupanel

mkdir -p /var/www

say "Настраиваю сервер"
"$HOME_DIR/itrupanel" setup "$PHP_VERSION"

FIRST=0
if [ ! -s "$HOME_DIR/etc/.admin-set" ]; then
  LOGIN="${PANEL_LOGIN:-}"
  PASS="${PANEL_PASSWORD:-}"
  if [ -z "$LOGIN" ]; then
    LOGIN="$(tr -dc 'a-z' </dev/urandom | head -c 1 || true)$(tr -dc 'a-z0-9' </dev/urandom | head -c 7 || true)"
  fi
  if [ -z "$PASS" ]; then
    PASS="$(tr -dc 'A-HJ-NP-Za-km-z2-9' </dev/urandom | head -c 14 || true)"
  fi
  [ "${#PASS}" -ge 8 ] || die "PANEL_PASSWORD должен быть не короче 8 символов"
  "$HOME_DIR/itrupanel" passwd "$PASS" "$LOGIN" >/dev/null || die "Логин: 3–32 символа, латинские буквы, цифры, _ . - (PANEL_LOGIN)"
  date > "$HOME_DIR/etc/.admin-set"
  FIRST=1
  if [ -d /run/systemd/system ]; then systemctl restart itrupanel; fi
fi

if [ ! -d /run/systemd/system ]; then
  # контейнер без systemd — запускаем в фоне
  pkill -f "$HOME_DIR/itrupanel$" 2>/dev/null || true
  nohup "$HOME_DIR/itrupanel" >>"$HOME_DIR/var/panel.log" 2>&1 &
fi

IP="$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}')"
echo
echo "${c_ok}${c_b}ITRUPANEL установлена${c_0}"
echo "  Адрес:  https://${IP:-IP-сервера}:3333"
if [ "$FIRST" = 1 ]; then
  echo "  Логин:  $LOGIN"
  echo "  Пароль: $PASS"
else
  echo "  Логин и пароль прежние. Сменить: itrupanel passwd <новый_пароль> [новый_логин]"
fi
echo "  Сертификат панели самоподписанный — браузер предупредит, это нормально."

# Statistics must never change the outcome of installation.
itru_stats_report || true
