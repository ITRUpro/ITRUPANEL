// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import "os"

// Первичная настройка FreeBSD: журналы, FTP и служба панели.

const bsdRCScript = `#!/bin/sh
# ITRUPANEL — панель управления сервером. Файл создаёт установщик.
#
# PROVIDE: itrupanel
# REQUIRE: LOGIN NETWORKING mysql nginx
# KEYWORD: shutdown
#
# Включение: sysrc itrupanel_enable=YES

. /etc/rc.subr

name="itrupanel"
rcvar="itrupanel_enable"
load_rc_config $name
: ${itrupanel_enable:="NO"}

pidfile="/var/run/${name}.pid"
command="/usr/sbin/daemon"
command_args="-f -r -R 3 -P ${pidfile} -o /usr/local/itrupanel/var/panel.log /usr/local/itrupanel/itrupanel"

run_rc_command "$1"
`

func bsdInstallPanelService() error {
	if err := writeFile(bsdPrefix+"/etc/rc.d/itrupanel", []byte(bsdRCScript), 0755); err != nil {
		return err
	}
	if _, err := run("sysrc", "itrupanel_enable=YES"); err != nil {
		return err
	}
	// Перезапуск. Вывод отвязан от канала: демон, запущенный службой, иначе держит его открытым
	// и ожидание команды не заканчивается.
	action := "onestart"
	if bsdServiceActive("itrupanel") {
		action = "onerestart"
	}
	_, err := run("sh", "-c", "service itrupanel "+action+" </dev/null >/dev/null 2>&1")
	return err
}

func bsdSetupLogs() error {
	os.MkdirAll(P(sockDir), 0755)
	os.MkdirAll(P(bsdPrefix+"/etc/newsyslog.conf.d"), 0755)
	return writeFile(bsdPrefix+"/etc/newsyslog.conf.d/itrupanel.conf", []byte(
		"# logfilename                     [owner:group] mode count size when flags [/pid_file] [sig_num]\n"+
			"/var/www/*/data/logs/*.log        644  14  *  @T00  GJ  /var/run/nginx.pid 30\n"), 0644)
}

// bsdSetupPureFTPd настраивает pure-ftpd: виртуальные пользователи из PureDB, TLS по возможности.
func bsdSetupPureFTPd(pem []byte) error {
	if err := writeFile("/etc/ssl/private/pure-ftpd.pem", pem, 0600); err != nil {
		return err
	}
	os.MkdirAll(P("/etc/pure-ftpd"), 0755)
	conf := readFile(bsdPrefix + "/etc/pure-ftpd.conf")
	if conf == "" {
		conf = readFile(bsdPrefix + "/etc/pure-ftpd.conf.sample")
	}
	set := map[string]string{
		"ChrootEveryone": "yes", "PureDB": ftpPDB, "PAMAuthentication": "no", "UnixAuthentication": "no",
		"PassivePortRange": "30000 30100", "MinUID": "1000", "TLS": "1", "NoAnonymous": "yes",
		"CreateHomeDir": "no", "DontResolve": "yes",
	}
	if err := writeFile(bsdPrefix+"/etc/pure-ftpd.conf", []byte(pureConfApply(conf, set)+"\n"), 0644); err != nil {
		return err
	}
	if err := service("enable", "pure-ftpd"); err != nil {
		return err
	}
	return service("restart", "pure-ftpd")
}
