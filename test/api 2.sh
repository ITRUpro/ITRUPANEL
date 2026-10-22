#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Проверка API панели в контейнере: test/api.sh <порт> <пароль> <контейнер>
port=$1 pass=$2 ct=$3
J=$(mktemp)
c() { curl -sk -b $J -c $J -H 'X-Itrupanel: 1' -H 'Content-Type: application/json' "https://127.0.0.1:$port/api/$1" -d "${2:-{\}}"; echo; }
curl -sk -c $J -H 'Content-Type: application/json' "https://127.0.0.1:$port/api/login" -d "{\"user\":\"admin\",\"password\":\"$pass\"}"; echo
echo "-- сайт";        c sites.save '{"domain":"test.local","add_www":true,"mode":"php","try_index":true,"gzip":true,"static_expires":"30d","max_body":64}'
docker exec $ct bash -c 'echo "<?php echo \"PHP \".PHP_VERSION;" > /var/www/test/data/www/test.local/index.php; rm -f /var/www/test/data/www/test.local/index.html; chown test:test /var/www/test/data/www/test.local/index.php; curl -s -H "Host: test.local" http://127.0.0.1/; echo; curl -s -o /dev/null -w "%{http_code}\n" -H "Host: test.local" http://127.0.0.1/some/pretty/url'
echo "-- база";        c db.create '{"name":"testdb","password":"Passw0rdTest1"}'
docker exec $ct mysql -utestdb -pPassw0rdTest1 -e 'select 1 as ok' testdb
echo "-- самоподп.";   c ssl.self '{"domains":"test.local www.test.local","site":"test.local"}'
docker exec $ct bash -c 'curl -sk https://127.0.0.1/ -H "Host: test.local"; echo; curl -s -o /dev/null -w "%{http_code} -> %{redirect_url}\n" -H "Host: test.local" http://127.0.0.1/'
echo "-- cron";        c cron.save '{"user":"test","schedule":"*/5 * * * *","command":"echo hi","enabled":true}'
docker exec $ct crontab -u test -l
echo "-- ftp";         c ftp.create '{"owner":"test","name":"testftp","password":"FtpPass12345"}'
docker exec $ct bash -c 'curl -s --ftp-ssl-reqd -k -u testftp:FtpPass12345 ftp://127.0.0.1/data/www/ ; echo ftp-exit=$?'
echo "-- бэкап";       c backups.create '{"site":"test.local","kind":"full"}'; sleep 6; c backups.list | head -c 400; echo
echo "-- pma";         curl -sk -b $J -o /dev/null -w "%{http_code}\n" "https://127.0.0.1:$port/phpmyadmin/"
echo "-- info";        c system.info | head -c 900; echo
echo "-- ops errors";  c logs.ops '{"errors":true}' | head -c 1500; echo
