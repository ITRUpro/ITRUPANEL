// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Режим «nginx + Apache»: nginx отдаёт статику и проксирует остальное на Apache (127.0.0.1:8080),
// Apache читает .htaccess и передаёт PHP в PHP-FPM нужной версии через proxy_fcgi.

const apachePort = "8080"

var tplApache = template.Must(template.ParseFS(tplFS, "templates/apache-site.conf"))

func apacheService() string {
	if osInfo.Family == "rhel" {
		return "httpd"
	}
	return "apache2"
}

func apacheSiteDir() string {
	if osInfo.Family == "rhel" {
		return "/etc/httpd/itrupanel"
	}
	return "/etc/apache2/itrupanel"
}

// apacheReady — Apache установлен и настроен панелью (слушает 127.0.0.1:8080 за nginx). Apache,
// попавший на сервер только как зависимость пакета (например, Roundcube в Debian), не считается готовым.
func apacheReady() bool {
	if !apacheInstalled() {
		return false
	}
	if osInfo.Family == "rhel" {
		return fileExists("/etc/httpd/conf.d/itrupanel.conf")
	}
	return fileExists("/etc/apache2/conf-enabled/itrupanel.conf")
}

// cleanupPackagedApache убирает то, что пакеты Debian/Ubuntu (Roundcube и метапакет php) добавляют
// в Apache: mod_php с переключением на mpm_prefork и конфигурацию /roundcube. Панель отдаёт PHP через
// PHP-FPM, а Roundcube — через nginx. Apache панели возвращается к mpm_event; Apache, поставленный
// только как зависимость, останавливается — иначе он спорит с nginx за порт 80.
func cleanupPackagedApache() {
	if osInfo.Family == "rhel" || !apacheInstalled() {
		return
	}
	run("a2disconf", "-q", "roundcube")
	apacheDisableModPHP(true)
	if apacheReady() {
		run("a2dismod", "-f", "-q", "mpm_prefork", "mpm_worker")
		run("a2enmod", "-q", "mpm_event")
		if _, err := apacheTest(); err == nil {
			service("restart", apacheService())
		}
		return
	}
	service("stop", apacheService())
	if osInfo.Systemd {
		run("systemctl", "disable", apacheService())
	}
}

func apacheInstalled() bool {
	if osInfo.Family == "rhel" {
		return fileExists("/usr/sbin/httpd")
	}
	return fileExists("/usr/sbin/apache2")
}

// ensureApache ставит и настраивает Apache один раз (под phpInstallMu).
func ensureApache() error {
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if !apacheInstalled() {
		pkg := "apache2"
		if osInfo.Family == "rhel" {
			pkg = "httpd"
		}
		if err := pkgInstall(pkg); err != nil {
			return errf("Не удалось установить Apache: %s", firstLine(err.Error()))
		}
	}
	os.MkdirAll(P(apacheSiteDir()), 0755)
	if osInfo.Family == "rhel" {
		// Apache слушает только локальный порт: nginx занимает 80/443
		conf := readFile("/etc/httpd/conf/httpd.conf")
		var b strings.Builder
		for _, l := range strings.Split(conf, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "Listen ") {
				b.WriteString("#" + l + "\n")
				continue
			}
			b.WriteString(l + "\n")
		}
		writeFile("/etc/httpd/conf/httpd.conf", []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0644)
		os.Remove(P("/etc/httpd/conf.d/welcome.conf"))
		if fileExists("/etc/httpd/conf.d/ssl.conf") {
			os.Rename(P("/etc/httpd/conf.d/ssl.conf"), P("/etc/httpd/conf.d/ssl.conf.disabled"))
		}
		writeFile("/etc/httpd/conf.d/itrupanel.conf", []byte("# ITRUPANEL\nListen 127.0.0.1:"+apachePort+"\nIncludeOptional "+apacheSiteDir()+"/*.conf\n"), 0644)
		// Apache должен подключаться к сокетам PHP-FPM (группа nginx) и читать файлы сайтов
		run("usermod", "-aG", osInfo.WebUser, "apache")
		mu.Lock()
		for _, u := range store.Users {
			run("usermod", "-aG", u.Name, "apache")
		}
		mu.Unlock()
		run("setsebool", "-P", "httpd_can_network_connect", "1")
	} else {
		writeFile("/etc/apache2/ports.conf", []byte("# ITRUPANEL: Apache работает за nginx\nListen 127.0.0.1:"+apachePort+"\n"), 0644)
		writeFile("/etc/apache2/conf-available/itrupanel.conf", []byte("# ITRUPANEL\nServerName localhost\nIncludeOptional "+apacheSiteDir()+"/*.conf\n"), 0644)
		run("a2enconf", "itrupanel")
		run("a2dissite", "000-default")
		// mod_php требует mpm_prefork и несовместим с mpm_event: панель работает через PHP-FPM
		apacheDisableModPHP(true)
		run("a2dismod", "-f", "mpm_prefork", "mpm_worker")
		run("a2enmod", "proxy_fcgi", "setenvif", "rewrite", "remoteip", "headers", "expires", "mpm_event")
	}
	if out, err := apacheTest(); err != nil {
		return errf("Конфигурация Apache: %s", out)
	}
	return service("enable", apacheService())
}

func apacheTest() (string, error) {
	if osInfo.Family == "rhel" {
		return run("httpd", "-t")
	}
	apacheDisableModPHP(false)
	return run("apache2ctl", "-t")
}

// apacheDisableModPHP отключает модули mod_php (mods-enabled/php*.load). При all=false — только
// «битые», чей .so отсутствует (например, после удаления PHP без purge libapache2-mod-php):
// иначе apache2ctl -t падает с «Cannot load .../libphpX.Y.so» и не создаётся ни один сайт.
func apacheDisableModPHP(all bool) {
	links, _ := filepath.Glob(P("/etc/apache2/mods-enabled/php*.load"))
	for _, l := range links {
		mod := strings.TrimSuffix(filepath.Base(l), ".load")
		broken := false
		for _, line := range strings.Split(readFile("/etc/apache2/mods-enabled/"+mod+".load"), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 && f[0] == "LoadModule" {
				so := f[2]
				if !strings.HasPrefix(so, "/") {
					so = "/usr/lib/apache2/" + so
				}
				broken = broken || !fileExists(so)
			}
		}
		if !all && !broken {
			continue
		}
		if _, err := run("a2dismod", "-f", mod); err != nil {
			os.Remove(l)
			os.Remove(P("/etc/apache2/mods-enabled/" + mod + ".conf"))
		}
	}
}

func apacheVhostPath(d string) string { return apacheSiteDir() + "/" + d + ".conf" }

// applyApache пишет или удаляет vhost Apache для сайта (под mu).
func applyApache(s *Site, u *SiteUser) error {
	path := apacheVhostPath(s.Domain)
	if s.Mode != "php" || s.Backend != "apache" {
		if fileExists(path) {
			os.Remove(P(path))
			service("reload", apacheService())
		}
		return nil
	}
	if osInfo.Family == "rhel" {
		run("usermod", "-aG", s.User, "apache")
	}
	var buf bytes.Buffer
	tplApache.Execute(&buf, map[string]any{
		"Domain": s.Domain, "Aliases": strings.Join(s.Aliases, " "), "Root": s.Root, "Index": s.Index,
		"Sock": sockPath(s.Domain), "Port": apachePort, "ErrorLog": u.Home + "/data/logs/" + s.Domain + ".apache.log",
	})
	prev := readFile(path)
	if prev == buf.String() {
		return nil
	}
	if err := writeFile(path, buf.Bytes(), 0644); err != nil {
		return err
	}
	if out, err := apacheTest(); err != nil {
		if prev != "" {
			writeFile(path, []byte(prev), 0644)
		} else {
			os.Remove(P(path))
		}
		return errf("Apache: %s", out)
	}
	return service("reload", apacheService())
}
