#!/bin/sh
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# ITRUPANEL для FreeBSD 14 и 15 (бета) — отдельная установка и отдельный архив.
# Запуск из распакованного архива, от root:  sh install-freebsd.sh
#                                            PHP_VERSION=8.4 sh install-freebsd.sh
#                                            PHP_VERSION=none sh install-freebsd.sh   (без PHP)
set -eu
PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH

PHP_VERSION="${PHP_VERSION:-8.3}"
HOME_DIR=/usr/local/itrupanel
HERE=$(cd "$(dirname "$0")" && pwd)

say() { printf '==> %s\n' "$*"; }
die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }

case "${1:-}" in
  -h|--help)
    echo "Установка ITRUPANEL на чистый сервер FreeBSD 14/15 (нужны root и доступ в интернет)."
    echo "  sh install-freebsd.sh                       — PHP 8.3"
    echo "  PHP_VERSION=8.4 sh install-freebsd.sh        — другая версия PHP (8.2, 8.3, 8.4, 8.5)"
    echo "  PHP_VERSION=none sh install-freebsd.sh       — без PHP и phpMyAdmin"
    echo "  PANEL_LOGIN=имя PANEL_PASSWORD=пароль sh install-freebsd.sh — свои логин и пароль (иначе случайные)"
    echo "Подробности: УСТАНОВКА-FreeBSD.txt рядом с установщиком."
    exit 0 ;;
  '') ;;
  *) die "неизвестный параметр $1; справка: sh install-freebsd.sh --help" ;;
esac

case "$PHP_VERSION" in
  none|8.2|8.3|8.4|8.5) ;;
  *) die "на FreeBSD доступны PHP 8.2, 8.3, 8.4, 8.5 (PHP_VERSION=$PHP_VERSION)" ;;
esac

[ "$(id -u)" = 0 ] || die "запустите от root"
[ "$(uname -s)" = FreeBSD ] || die "этот установщик только для FreeBSD; для Linux используйте install.sh"
RELEASE=$(freebsd-version -u 2>/dev/null || uname -r)
MAJOR=${RELEASE%%.*}
[ "$MAJOR" -ge 14 ] 2>/dev/null || die "нужна FreeBSD 14 или новее (сейчас $RELEASE)"

FREE_KB=$(df -k / | awk 'NR == 2 {print $4}')
case "$FREE_KB" in ''|*[!0-9]*) ;; *) [ "$FREE_KB" -ge 4194304 ] || die "на корневом разделе нужно не менее 4 ГБ свободного места (сейчас $((FREE_KB / 1024)) МБ)" ;; esac

case "$(uname -m)" in
  amd64|x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "неподдерживаемая архитектура $(uname -m)" ;;
esac
BIN="$HERE/bin/itrupanel-freebsd-$ARCH"
[ -f "$BIN" ] || die "не найден $BIN — распакуйте архив целиком"
chmod +x "$BIN" 2>/dev/null || true
"$BIN" version >/dev/null || die "программа не запускается на этом сервере ($ARCH)"

say "ITRUPANEL (бета для FreeBSD): FreeBSD $RELEASE, $ARCH"

export ASSUME_ALWAYS_YES=yes
say "Обновляю список пакетов"
pkg bootstrap -y >/dev/null 2>&1 || true
pkg update -q || die "не удалось обновить список пакетов (проверьте доступ в интернет)"

# MariaDB: берём первую доступную из поддерживаемых версий
MARIADB=""
for p in mariadb118-server mariadb114-server mariadb106-server; do
  if [ -n "$(pkg rquery '%n' "$p" 2>/dev/null)" ]; then MARIADB=$p; break; fi
done
[ -n "$MARIADB" ] || die "в репозитории пакетов нет MariaDB"
MARIACLIENT=$(echo "$MARIADB" | sed 's/-server$/-client/')
CERTBOT=$(pkg search -q '^py3[0-9]*-certbot-[0-9]' 2>/dev/null | sed 's/-[0-9][^-]*$//' | sort | tail -n 1)
[ -n "$CERTBOT" ] || die "в репозитории пакетов нет certbot"

say "Ставлю nginx, $MARIADB, certbot, pure-ftpd и утилиты"
pkg install -y nginx "$MARIADB" "$MARIACLIENT" "$CERTBOT" pure-ftpd bash curl rsync unzip zip sudo ca_root_nss

# Пакеты FreeBSD собираются для одной версии системы: на более старой программа «установлена», но не запускается
if nginx -v 2>&1 | grep -q 'ld-elf.so.1'; then
  die "пакеты FreeBSD собраны для более новой версии системы, чем стоит на сервере ($RELEASE): nginx не запускается. Обновите систему: freebsd-update fetch install, а если ошибка останется — freebsd-update upgrade -r <следующий выпуск> (например, 15.1-RELEASE); затем повторите установку."
fi

say "Настраиваю nginx"
NGINX_DIR=/usr/local/etc/nginx
mkdir -p "$NGINX_DIR/conf.d" /var/log/nginx /var/www
if [ ! -e "$NGINX_DIR/nginx.conf.itrupanel-orig" ] && [ -f "$NGINX_DIR/nginx.conf" ]; then
  cp -p "$NGINX_DIR/nginx.conf" "$NGINX_DIR/nginx.conf.itrupanel-orig"
fi
cat > "$NGINX_DIR/nginx.conf" <<'NGINX'
# ITRUPANEL: основной файл nginx для FreeBSD. Сайты панели подключаются из conf.d/itrupanel.conf.
user www;
worker_processes auto;
error_log /var/log/nginx/error.log;
pid /var/run/nginx.pid;
events {
    worker_connections 1024;
}
http {
    include mime.types;
    default_type application/octet-stream;
    sendfile on;
    keepalive_timeout 65;
    server_tokens off;
    include conf.d/*.conf;
}
NGINX
# certbot во FreeBSD хранит сертификаты в /usr/local/etc/letsencrypt, панель ждёт /etc/letsencrypt
mkdir -p /usr/local/etc/letsencrypt
if [ ! -e /etc/letsencrypt ]; then ln -s /usr/local/etc/letsencrypt /etc/letsencrypt; fi
# Панель везде пишет пути /etc/nginx/…: на FreeBSD это ссылка на настоящий каталог
if [ -L /etc/nginx ]; then
  :
elif [ -e /etc/nginx ]; then
  die "/etc/nginx уже существует и не является ссылкой; уберите его или перенесите в $NGINX_DIR"
else
  ln -s "$NGINX_DIR" /etc/nginx
fi

say "Копирую панель в $HOME_DIR"
mkdir -p "$HOME_DIR/etc" "$HOME_DIR/var" "$HOME_DIR/bin" "$HOME_DIR/www"
chmod 755 "$HOME_DIR" "$HOME_DIR/bin" "$HOME_DIR/www"
chmod 700 "$HOME_DIR/etc" "$HOME_DIR/var"
install -m 755 "$BIN" "$HOME_DIR/itrupanel.new"
mv -f "$HOME_DIR/itrupanel.new" "$HOME_DIR/itrupanel"
ln -sf "$HOME_DIR/itrupanel" /usr/local/bin/itrupanel

say "Настраиваю сервер"
"$HOME_DIR/itrupanel" setup "$PHP_VERSION"

FIRST=0
if [ ! -s "$HOME_DIR/etc/.admin-set" ]; then
  LOGIN="${PANEL_LOGIN:-}"
  PASS="${PANEL_PASSWORD:-}"
  if [ -z "$LOGIN" ]; then
    LOGIN="$(LC_ALL=C tr -dc 'a-z' </dev/urandom | head -c 1)$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 7)"
  fi
  if [ -z "$PASS" ]; then
    PASS="$(LC_ALL=C tr -dc 'A-HJ-NP-Za-km-z2-9' </dev/urandom | head -c 14)"
  fi
  [ "${#PASS}" -ge 8 ] || die "PANEL_PASSWORD должен быть не короче 8 символов"
  # Служба остановлена на время смены пароля: работающая панель иначе перезаписала бы файл настроек своими данными
  service itrupanel onestop >/dev/null 2>&1 </dev/null || true
  "$HOME_DIR/itrupanel" passwd "$PASS" "$LOGIN" >/dev/null || die "Логин: 3–32 символа, латинские буквы, цифры, _ . - (PANEL_LOGIN)"
  date > "$HOME_DIR/etc/.admin-set"
  FIRST=1
  service itrupanel onestart >/dev/null 2>&1 </dev/null || true
fi

IP=$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || ifconfig | awk '/inet / && $2 != "127.0.0.1" {print $2; exit}')
echo
echo "ITRUPANEL установлена (FreeBSD, бета)"
echo "  Адрес:  https://${IP:-IP-сервера}:3333"
if [ "$FIRST" = 1 ]; then
  echo "  Логин:  $LOGIN"
  echo "  Пароль: $PASS"
else
  echo "  Логин и пароль прежние. Сменить: itrupanel passwd <новый_пароль> [новый_логин]"
fi
echo "  Сертификат панели самоподписанный — браузер предупредит, это нормально."
echo "  Файрвол не менялся: откройте в нём порты 80, 443, 3333, 21 и 30000–30100 (TCP)."
