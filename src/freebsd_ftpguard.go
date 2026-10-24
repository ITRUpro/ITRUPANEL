// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Защита FTP от перебора паролей на FreeBSD: fail2ban читает журнал pure-ftpd (/var/log/xferlog,
// его пишет syslog) и блокирует адреса правилами pf внутри якоря f2b/*.

const bsdFTPLog = "/var/log/xferlog"

func bsdFTPGuardConfig(port int) string {
	return fmt.Sprintf(`%s[%s]
enabled = true
filter = pure-ftpd
backend = auto
logpath = %s
port = %d
protocol = tcp
maxretry = 5
findtime = 600
bantime = 900
ignoreip = 127.0.0.0/8 ::1
ignoreself = true
action = pf[name=itrupanel-ftp, port="%%(port)s", protocol=tcp]
`, ftpGuardHeader, ftpGuardJail, bsdFTPLog, port)
}

func enableFTPGuardBSD(j *Job, port int) (err error) {
	if !have("pure-pw") {
		return errf("Требуется Pure-FTPd")
	}
	old, e := os.ReadFile(P(ftpGuardPath))
	if e == nil && !strings.HasPrefix(string(old), ftpGuardHeader) {
		return errf("Конфигурация FTP-защиты создана вручную; требуется проверка администратора")
	}
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	jobStage(j, "Проверка и установка Fail2ban")
	if !have("fail2ban-client") && !cfg.Dry {
		pkg := "py312-fail2ban"
		if out, _ := run("pkg", "search", "-q", "^py3[0-9]*-fail2ban-[0-9]"); out != "" {
			pkg = regexp.MustCompile(`-[0-9][^-]*$`).ReplaceAllString(strings.Fields(out)[0], "")
		}
		if err = bsdPkgCheck(pkg); err != nil {
			return err
		}
		if err = liveCommand(j, 30*time.Minute, "pkg", "install", "-y", pkg); err != nil {
			return err
		}
	}
	// В журнале должны быть числовые адреса: без DontResolve имя узла не совпадёт с фильтром
	conf := readFile(bsdPrefix + "/etc/pure-ftpd.conf")
	if !strings.Contains(conf, "\nDontResolve    yes") && !strings.Contains(conf, "\nDontResolve yes") {
		if err = writeFile(bsdPrefix+"/etc/pure-ftpd.conf", []byte(pureConfApply(conf, map[string]string{"DontResolve": "yes"})+"\n"), 0644); err != nil {
			return err
		}
		service("restart", "pure-ftpd")
	}
	if !fileExists(bsdFTPLog) {
		writeFile(bsdFTPLog, nil, 0640)
	}
	jobStage(j, "Подготовка pf для блокировок")
	if !fileExists(fwConfig) {
		empty, _ := fwScriptPF(FirewallState{}, nil)
		if err = writeFile(fwConfig, []byte(empty), 0600); err != nil {
			return err
		}
	}
	if err = bsdFirewallPrepare(); err != nil {
		return err
	}
	if err = bsdFirewallStart(); err != nil {
		return err
	}
	if err = writeFile(ftpGuardPath, []byte(ftpGuardConfig(port)), 0644); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if len(old) > 0 {
				writeFile(ftpGuardPath, old, 0644)
			} else {
				os.Remove(P(ftpGuardPath))
			}
		}
	}()
	jobStage(j, "Проверка конфигурации и запуск защиты")
	if _, err = runTimeout(30*time.Second, "", "fail2ban-client", "-t"); err != nil {
		return ftpGuardStartupError(err)
	}
	if _, err = run("sysrc", "fail2ban_enable=YES"); err != nil {
		return err
	}
	if _, err = run("sh", "-c", "service fail2ban onerestart </dev/null >/dev/null 2>&1"); err != nil {
		return ftpGuardStartupError(err)
	}
	jobStage(j, "Ожидание готовности Fail2ban")
	if err = waitFTPGuard(60*time.Second, 500*time.Millisecond); err != nil {
		return ftpGuardStartupError(err)
	}
	if _, err = runTimeout(10*time.Second, "", "fail2ban-client", "status", ftpGuardJail); err != nil {
		return ftpGuardStartupError(err)
	}
	return nil
}
