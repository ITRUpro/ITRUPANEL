// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Почта на AlmaLinux, Rocky, CentOS Stream и RHEL: пути пакетов, Redis/Valkey для антиспама,
// репозиторий Rspamd и метки SELinux. На Debian/Ubuntu функции возвращают прежние значения.

// selinuxOn — SELinux включён (Enforcing или Permissive): метки нужны в обоих режимах,
// иначе после перехода в Enforcing почта перестанет работать.
func selinuxOn() bool {
	if cfg.Dry || osInfo.Family != "rhel" || !have("semanage") {
		return false
	}
	_, err := os.Stat("/sys/fs/selinux/enforce")
	return err == nil
}

// selinuxLabel закрепляет тип SELinux за папкой (переживает relabel) и применяет его.
func selinuxLabel(path, typ string) error {
	if !selinuxOn() {
		return nil
	}
	return selinuxLabelAlways(path, typ)
}

// selinuxLogsLabel закрепляет за журналами сайтов тип httpd_log_t: с типом веб-контента logrotate
// не может их прочитать (запрет SELinux), и ротация журналов на RHEL-системах не работала.
const siteLogsRule = "/var/www/[^/]+/data/logs(/.*)?"

func selinuxLogsLabel() {
	if !selinuxOn() || !have("restorecon") {
		return
	}
	if _, err := run("semanage", "fcontext", "-a", "-t", "httpd_log_t", siteLogsRule); err != nil {
		run("semanage", "fcontext", "-m", "-t", "httpd_log_t", siteLogsRule)
	}
	mu.Lock()
	var homes []string
	for _, u := range store.Users {
		homes = append(homes, u.Home+"/data/logs")
	}
	mu.Unlock()
	for _, d := range homes {
		if fileExists(d) {
			run("restorecon", "-RF", d)
		}
	}
}

func selinuxLabelAlways(path, typ string) error {
	rule := regexp.QuoteMeta(path) + "(/.*)?"
	if _, err := run("semanage", "fcontext", "-a", "-t", typ, rule); err != nil {
		if _, err = run("semanage", "fcontext", "-m", "-t", typ, rule); err != nil {
			return errf("SELinux: не удалось задать метку %s для %s: %s", typ, path, firstLine(err.Error()))
		}
	}
	if fileExists(path) {
		if _, err := run("restorecon", "-RF", path); err != nil {
			return err
		}
	}
	return nil
}

// selinuxPort разрешает службе порт: без метки Postfix не подключится к фильтру Rspamd,
// а Valkey/Redis не откроет свой порт.
func selinuxPort(port int, typ string) error {
	if !selinuxOn() {
		return nil
	}
	p := strconv.Itoa(port)
	if _, err := run("semanage", "port", "-a", "-t", typ, "-p", "tcp", p); err != nil {
		if _, err = run("semanage", "port", "-m", "-t", typ, "-p", "tcp", p); err != nil {
			return errf("SELinux: не удалось разрешить порт %s: %s", p, firstLine(err.Error()))
		}
	}
	return nil
}

func rhelMajor() int {
	n, _ := strconv.Atoi(strings.Split(osInfo.Version, ".")[0])
	return n
}

// spamRedisServer — программа и пользователь Redis для антиспама. В RHEL 10 Redis заменён
// совместимым Valkey.
func spamRedisServer() (bin, user string) {
	if osInfo.Family == "rhel" && (rhelMajor() >= 10 || have("valkey-server")) && !have("redis-server") {
		return "/usr/bin/valkey-server", "valkey"
	}
	return "/usr/bin/redis-server", "redis"
}

func spamRedisPackage() string {
	if osInfo.Family != "rhel" {
		return "redis-server"
	}
	if rhelMajor() >= 10 {
		return "valkey"
	}
	return "redis"
}

// ensureRspamdRepo подключает официальный репозиторий Rspamd (в EPEL его нет) — сразу по HTTPS.
func ensureRspamdRepo(j *Job) error {
	if cfg.Dry || osInfo.Family != "rhel" {
		return nil
	}
	major := rhelMajor()
	if major < 8 {
		return errf("Rspamd поддерживается на RHEL-системах 8, 9 и 10")
	}
	repo := "[rspamd]\nname=Rspamd stable repository\nbaseurl=https://rspamd.com/rpm-stable/centos-" + strconv.Itoa(major) + "/$basearch\nenabled=1\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=https://rspamd.com/rpm/rspamd.asc\n"
	if readFile("/etc/yum.repos.d/rspamd.repo") == repo {
		return nil
	}
	jobStage(j, "Подключение репозитория Rspamd")
	return writeFile("/etc/yum.repos.d/rspamd.repo", []byte(repo), 0644)
}

// roundcubePaths — каталог программы и настроек Roundcube: пакет Debian roundcube-core
// или EPEL roundcubemail.
func roundcubePaths() (share, conf string) {
	if osInfo.Family == "rhel" {
		return "/usr/share/roundcubemail", "/etc/roundcubemail"
	}
	return "/usr/share/roundcube", "/etc/roundcube"
}

func roundcubeConfigPath() string {
	_, conf := roundcubePaths()
	return conf + "/config.inc.php"
}

// systemCABundle — файл доверенных корневых сертификатов ОС.
func systemCABundle() string {
	for _, p := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"} {
		if fileExists(p) {
			return p
		}
	}
	return "/etc/ssl/certs/ca-certificates.crt"
}

// roundcubeTimers — на RHEL в пакете нет systemd-таймеров для очистки базы Roundcube.
func roundcubeTimerFiles(php string) map[string][]byte {
	share, _ := roundcubePaths()
	out := map[string][]byte{}
	for _, task := range []string{"gc", "cleandb"} {
		out["/etc/systemd/system/itrupanel-roundcube-"+task+".service"] = []byte("[Unit]\nDescription=ITRUPANEL Roundcube " + task + "\n[Service]\nType=oneshot\nUser=itruroundcube\nGroup=itruroundcube\nExecStart=" + phpCLI(php) + " " + share + "/bin/" + task + ".sh\n")
		out["/etc/systemd/system/itrupanel-roundcube-"+task+".timer"] = []byte("[Unit]\nDescription=ITRUPANEL Roundcube " + task + "\n[Timer]\nOnCalendar=daily\nRandomizedDelaySec=1h\nPersistent=true\n[Install]\nWantedBy=timers.target\n")
	}
	return out
}
