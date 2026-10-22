#!/usr/bin/env bash
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
# Run INSIDE a disposable Debian/Ubuntu Docker container, after installing
# Postfix/Dovecot/OpenDKIM/Rspamd/Redis/nginx/PHP/Roundcube from the OS repository.
# /tmp/itrupanel-mail-test is a cross-compiled Go test binary.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Disposable Docker container required'; exit 1; }
# A container has no systemd PID 1. Validators remain real; the daemons are
# launched directly after the configuration test, never on the host.
cat > /usr/local/bin/systemctl <<'SH'
#!/bin/sh
exit 0
SH
chmod 755 /usr/local/bin/systemctl
export PATH=/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
ITRUPANEL_NATIVE_MAIL=disposable-container /tmp/itrupanel-mail-test -test.run '^TestNativeMailRuntime$' -test.v -test.timeout 10m
mkdir -p /run/opendkim /run/rspamd /var/lib/itrupanel-spam-redis
chown opendkim:opendkim /run/opendkim
chown _rspamd:_rspamd /run/rspamd
chown redis:redis /var/lib/itrupanel-spam-redis
chmod 700 /var/lib/itrupanel-spam-redis
runuser -u redis -- redis-server /etc/itrupanel/rspamd-redis.conf --daemonize yes
rspamd -u _rspamd -g _rspamd
dovecot
opendkim -x /etc/opendkim.conf
INVOCATION_ID=itrupanel-disposable-test postfix start
if [[ -x /usr/sbin/php-fpm8.3 ]]; then
  php-fpm8.3
  nginx
fi
python3 /tmp/mail-runtime.py
