// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
)

// Почта на FreeBSD: Postfix, Dovecot и OpenDKIM из пакетов. Панель везде пишет пути /etc/postfix и
// /etc/dovecot, поэтому на FreeBSD это ссылки на /usr/local/etc/…; файлы OpenDKIM панель
// держит в собственной папке /etc/opendkim.

func opendkimPidFile() string {
	if isBSD() {
		return "/var/run/milteropendkim/pid"
	}
	return "/run/opendkim/opendkim.pid"
}

// bsdAddSystemUser создаёт системного пользователя без входа (pw useradd) с группой того же имени.
func bsdAddSystemUser(name, home string) error {
	_, err := run("pw", "useradd", "-n", name, "-d", home, "-s", "/usr/sbin/nologin", "-c", "ITRUPANEL "+name)
	return err
}

// bsdLink делает /etc/<name> ссылкой на каталог из /usr/local/etc, если там ещё ничего нет.
func bsdLink(name string) error {
	target := bsdPrefix + "/etc/" + name
	link := "/etc/" + name
	if fi, err := os.Lstat(P(link)); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return errf("%s уже существует и не является ссылкой на %s", link, target)
	}
	if err := os.MkdirAll(P(target), 0755); err != nil {
		return err
	}
	return os.Symlink(target, P(link))
}

// bsdMailPrepare отключает sendmail, направляет mailer.conf на Postfix и готовит ссылки и службы.
func bsdMailPrepare() error {
	for _, l := range []string{"postfix", "dovecot"} {
		if err := bsdLink(l); err != nil {
			return err
		}
	}
	os.MkdirAll(P("/etc/opendkim/itrupanel/keys"), 0755)
	// Пакет OpenDKIM не создаёт пользователя; панель держит ключи в группе opendkim
	if _, err := run("id", "opendkim"); err != nil {
		if err = bsdAddSystemUser("opendkim", "/var/run/milteropendkim"); err != nil {
			return err
		}
	}
	run("service", "sendmail", "onestop")
	if _, err := run("sysrc", "sendmail_enable=NO", "sendmail_submit_enable=NO", "sendmail_outbound_enable=NO", "sendmail_msp_queue_enable=NO"); err != nil {
		return err
	}
	if err := writeFile("/etc/mail/mailer.conf", []byte(`# ITRUPANEL: почту отправляет Postfix
sendmail	/usr/local/sbin/sendmail
send-mail	/usr/local/sbin/sendmail
mailq		/usr/local/sbin/sendmail
newaliases	/usr/local/sbin/sendmail
hoststat	/usr/local/sbin/sendmail
purgestat	/usr/local/sbin/sendmail
`), 0644); err != nil {
		return err
	}
	// Postfix читает /etc/aliases.db: без базы каждая служба пишет ошибку в журнал
	run("newaliases")
	// OpenDKIM: конфигурацию пишет панель; сокет задан в rc.conf, потому что rc-скрипт передаёт его в командной строке
	_, err := run("sysrc", "milteropendkim_cfgfile=/etc/opendkim.conf", "milteropendkim_uid=opendkim", "milteropendkim_gid=opendkim", "milteropendkim_socket=inet:8891@127.0.0.1")
	return err
}

// ---------- антиспам: Rspamd и отдельный Redis ----------

const bsdSpamRedisRC = bsdPrefix + "/etc/rc.d/itrupanel_spam_redis"

func spamUser() string {
	if isBSD() {
		return "rspamd"
	}
	return "_rspamd"
}

// spamRedisUnitPath — файл службы отдельного Redis: unit systemd или rc-скрипт FreeBSD.
func spamRedisUnitPath() string {
	if isBSD() {
		return bsdSpamRedisRC
	}
	return "/etc/systemd/system/itrupanel-spam-redis.service"
}

func spamRedisUnit(redisBin, redisUser string) string {
	if !isBSD() {
		return "" // для Linux содержимое задано в spamFiles
	}
	return `#!/bin/sh
# ITRUPANEL: отдельный Redis для Rspamd (порт 11379, только локально). Файл создаёт панель.
#
# PROVIDE: itrupanel_spam_redis
# REQUIRE: LOGIN
# BEFORE: rspamd
# KEYWORD: shutdown

. /etc/rc.subr

name="itrupanel_spam_redis"
rcvar="itrupanel_spam_redis_enable"
load_rc_config $name
: ${itrupanel_spam_redis_enable:="NO"}

pidfile="/var/run/${name}.pid"
procname="` + redisBin + `"
command="/usr/sbin/daemon"
command_args="-u ` + redisUser + ` -p ${pidfile} ` + redisBin + ` /etc/itrupanel/rspamd-redis.conf"
start_precmd="${name}_prestart"

itrupanel_spam_redis_prestart()
{
	install -d -o ` + redisUser + ` -g ` + redisUser + ` -m 0700 /var/lib/itrupanel-spam-redis
}

run_rc_command "$1"
`
}

// bsdSpamPrepare готовит Rspamd: ссылка /etc/rspamd и папка данных Redis.
func bsdSpamPrepare() error {
	if err := bsdLink("rspamd"); err != nil {
		return err
	}
	if err := os.MkdirAll(P("/var/lib/itrupanel-spam-redis"), 0700); err != nil {
		return err
	}
	_, err := run("chown", "redis:redis", "/var/lib/itrupanel-spam-redis")
	return err
}
