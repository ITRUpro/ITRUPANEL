// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// IDs are permanent. Keep every migration so skipped releases remain supported.
type updateMigration struct {
	ID    string
	Paths func() ([]string, error)
	Apply func() error
}
type migrationLedger struct {
	Applied map[string]string `json:"applied"`
}
type updateSnapshotFile struct {
	Path     string
	Exists   bool
	Data     []byte
	Mode     uint32
	UID, GID int
}
type updateSnapshot struct {
	Files    []updateSnapshotFile
	Services []string
}

func migrationLedgerPath() string { return filepath.Join(cfg.Home, "etc", "update-migrations.json") }
func panelMigrations() []updateMigration {
	return []updateMigration{
		{"001-state-defaults", func() ([]string, error) { return nil, nil }, saveChecked},
		{"001a-nginx-global", func() ([]string, error) {
			if strings.HasPrefix(readFile("/etc/nginx/conf.d/itrupanel.conf"), "# ITRUPANEL:") {
				return []string{P("/etc/nginx/conf.d/itrupanel.conf")}, nil
			}
			return nil, nil
		}, func() error {
			path := "/etc/nginx/conf.d/itrupanel.conf"
			old := readFile(path)
			if !strings.HasPrefix(old, "# ITRUPANEL:") {
				return nil
			}
			lines, depth := strings.Split(old, "\n"), 0
			var result []string
			directive := regexp.MustCompile(`^\s*server_tokens\s+(on|off|build)\s*;\s*(#.*)?$`)
			for _, line := range lines {
				if depth == 0 && directive.MatchString(line) {
					continue
				}
				result = append(result, line)
				code := strings.SplitN(line, "#", 2)[0]
				depth += strings.Count(code, "{") - strings.Count(code, "}")
			}
			next := strings.Join(result, "\n")
			if next == old {
				return nil
			}
			if err := writeFile(path, []byte(next), 0644); err != nil {
				return err
			}
			return nginxTestReload()
		}},
		{"002-pma-writable-sessions", func() ([]string, error) {
			if !fileExists(cfg.Home + "/www/phpmyadmin/index.php") {
				return nil, nil
			}
			paths := []string{P(cfg.Home + "/www/phpmyadmin/config.inc.php"), P(vhostDir + "/000-itrupanel-pma.conf")}
			for _, v := range phpAll {
				paths = append(paths, P(phpPoolDir(v)+"/itru_pma.conf"))
			}
			return paths, nil
		}, func() error {
			if fileExists(cfg.Home + "/www/phpmyadmin/index.php") {
				return installPMA()
			}
			return nil
		}},
		{"003-ols-access-log", func() ([]string, error) {
			if olsManaged() {
				return []string{P(olsConfig)}, nil
			}
			return nil, nil
		}, func() error {
			if !olsManaged() {
				return nil
			}
			if e := repairOLSAccessLog(); e != nil {
				return e
			}
			if olsInstalled() {
				if _, e := runTimeout(30*time.Second, "", olsRoot+"/bin/openlitespeed", "-t"); e != nil {
					return e
				}
				if serviceActive("lsws") {
					return service("restart", "lsws")
				}
			}
			return nil
		}},
		{"004-mail-quota-config", func() ([]string, error) {
			if !store.Mail.Ready || !fileExists(mailOwned) {
				return nil, nil
			}
			files, e := mailConfigFiles(store.Mail)
			var paths []string
			for p := range files {
				paths = append(paths, P(p))
			}
			return paths, e
		}, func() error {
			if !store.Mail.Ready || !fileExists(mailOwned) {
				return nil
			}
			v, e := runTimeout(15*time.Second, "", "dovecot", "--version")
			if e != nil {
				return e
			}
			if !cfg.Dry && !strings.HasPrefix(v, "2.3.") && !strings.HasPrefix(v, "2.4.") {
				return errf("Неизвестная версия Dovecot: %s", v)
			}
			if !cfg.Dry {
				store.Mail.Dovecot24 = strings.HasPrefix(v, "2.4.")
			}
			if _, e = applyMail(store.Mail); e != nil {
				return e
			}
			return saveChecked()
		}},
		{"005-roundcube-support-domain", func() ([]string, error) {
			if store.Mail.Webmail.Ready && fileExists(webmailOwned) {
				return []string{P("/etc/roundcube/config.inc.php")}, nil
			}
			return nil, nil
		}, func() error {
			if !store.Mail.Webmail.Ready || !fileExists(webmailOwned) {
				return nil
			}
			path := "/etc/roundcube/config.inc.php"
			old := readFile(path)
			if !strings.Contains(old, "// ITRUPANEL: managed Roundcube") {
				return nil
			}
			line := regexp.MustCompile(`(?m)^\$config\['support_url'\]\s*=.*;$`)
			next := line.ReplaceAllLiteralString(old, "$config['support_url'] = 'https://itrupanel.com/forum/';")
			if next == old {
				return nil
			}
			if err := writeFile(path, []byte(next), 0640); err != nil {
				return err
			}
			_, err := run("chown", "root:itruroundcube", path)
			return err
		}},
		{"006-suspended-site-page", func() ([]string, error) {
			var paths []string
			for _, s := range store.Sites {
				if s.Suspended && strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					paths = append(paths, P(vhostPath(s.Domain)))
				}
			}
			return paths, nil
		}, func() error {
			changed := false
			for _, s := range store.Sites {
				if !s.Suspended || !strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					continue
				}
				u := findUser(s.User)
				if u == nil {
					return errf("Нет пользователя %s", s.User)
				}
				b, err := renderSiteNginx(s, u, "")
				if err != nil {
					return err
				}
				if err = writeFile(vhostPath(s.Domain), b, 0644); err != nil {
					return err
				}
				changed = true
			}
			if changed {
				return nginxTestReload()
			}
			return nil
		}},
		// Уже установленные серверы: отключаем mod_php с отсутствующим .so (apache2ctl -t падал
		// с «Cannot load libphpX.Y.so») и включаем OpenLiteSpeed по основному имени lshttpd, если
		// раньше enable псевдонима lsws.service завершился ошибкой. Ошибки не прерывают обновление.
		{"007-apache-modphp-ols-service", func() ([]string, error) { return nil, nil }, func() error {
			if osInfo.Family != "rhel" && apacheInstalled() {
				apacheDisableModPHP(false)
				if _, e := apacheTest(); e == nil && serviceActive(apacheService()) {
					service("reload", apacheService())
				}
			}
			if osInfo.Systemd && olsInstalled() && olsManaged() && !serviceActive("lsws") {
				if e := olsTest(); e == nil {
					olsRunConfig(readFile(olsConfig))
				}
			}
			return nil
		}},
		// OpenLiteSpeed без сайтов завершается при запуске, и systemd перезапускал его бесконечно
		// (в том числе после миграции 007). Останавливаем службу, если сайтов нет, и возвращаем
		// lsws.service как ссылку на lshttpd.service вместо отдельной копии юнита.
		{"008-ols-no-sites-restart-loop", func() ([]string, error) { return nil, nil }, func() error {
			if !osInfo.Systemd || !olsInstalled() || !olsManaged() {
				return nil
			}
			olsRestoreAliasUnit()
			run("systemctl", "daemon-reload")
			conf := readFile(olsConfig)
			if !strings.Contains(conf, "\nvirtualhost ") || olsTest() == nil {
				olsRunConfig(conf)
			}
			return nil
		}},
		// Контейнеры MySQL/MariaDB со стандартными настройками занимали 400+ МБ без нагрузки.
		// На серверах с памятью меньше 4 ГБ пересоздаём их в экономном режиме; данные хранятся
		// отдельно от контейнера и не затрагиваются. Ошибки не прерывают обновление.
		{"009-db-containers-low-memory", func() ([]string, error) { return nil, nil }, func() error {
			if !lowMemoryHost() || !have("docker") {
				return nil
			}
			mu.Lock()
			var list []DBServer
			for _, s := range store.DBServers {
				if s.Status == "" {
					list = append(list, *s)
				}
			}
			mu.Unlock()
			for _, s := range list {
				running, _ := run("docker", "inspect", "-f", "{{.State.Running}}", dbContainer(s.Name))
				if strings.TrimSpace(running) != "true" || dbContainerLowMemory(s.Name) {
					continue
				}
				srv := s
				if err := startDBContainer(&srv); err != nil {
					log.Printf("migration 009: %s: %v", s.Name, err)
				}
			}
			return nil
		}},
		// Пакеты Roundcube/php в Debian могли поставить Apache с mod_php и включить mpm_prefork и
		// конфигурацию /roundcube. Убираем это; Apache-сайты панели продолжают работать через PHP-FPM.
		{"011-packaged-apache-cleanup", func() ([]string, error) { return nil, nil }, func() error {
			if osInfo.Family == "rhel" || !apacheInstalled() {
				return nil
			}
			links, _ := filepath.Glob(P("/etc/apache2/mods-enabled/php*.load"))
			if len(links) == 0 && !fileExists("/etc/apache2/conf-enabled/roundcube.conf") {
				return nil
			}
			cleanupPackagedApache()
			return nil
		}},
		// Rspamd с Hyperscan занимал около 600 МБ на сервере с 1 ГБ. На серверах с памятью меньше
		// 4 ГБ включаем экономный режим; applyMail проверяет конфигурацию и откатывает её при ошибке.
		{"012-rspamd-low-memory", func() ([]string, error) {
			if !store.Mail.Ready || !store.Mail.Spam.Installed || !fileExists(mailOwned) || !lowMemoryHost() {
				return nil, nil
			}
			return []string{P("/etc/rspamd/local.d/options.inc"), P("/etc/rspamd/local.d/neural.conf")}, nil
		}, func() error {
			if !store.Mail.Ready || !store.Mail.Spam.Installed || !fileExists(mailOwned) || !lowMemoryHost() {
				return nil
			}
			if _, e := applyMail(store.Mail); e != nil {
				log.Printf("migration 012: %v", e)
			}
			return nil
		}},
		// Debian 11: после окончания поддержки пакеты bullseye-security есть только на archive.debian.org,
		// иначе установка PHP, почты и Docker из панели завершается ошибкой 404.
		{"013-debian11-security-archive", func() ([]string, error) { return nil, nil }, func() error {
			if osInfo.ID != "debian" || strings.Split(osInfo.Version, ".")[0] != "11" {
				return nil
			}
			if e := repairAPTSources(archiveBullseyeSecurity, ".itrupanel-before-bullseye-security-archive"); e != nil {
				log.Printf("migration 013: %v", e)
				return nil
			}
			if _, e := runTimeout(5*time.Minute, "", "apt-get", "update", "-q"); e != nil {
				log.Printf("migration 013: apt-get update: %v", firstLine(e.Error()))
			}
			return nil
		}},
		// RHEL: повторная настройка дописывала allow-recursion в named.conf, после чего BIND не запускался.
		{"014-rhel-named-recursion", func() ([]string, error) {
			if osInfo.Family != "rhel" || strings.Count(readFile("/etc/named.conf"), "allow-recursion") < 2 {
				return nil, nil
			}
			return []string{P("/etc/named.conf")}, nil
		}, func() error {
			if osInfo.Family != "rhel" || strings.Count(readFile("/etc/named.conf"), "allow-recursion") < 2 {
				return nil
			}
			if e := writeFile("/etc/named.conf", []byte(rhelNamedConf(readFile("/etc/named.conf"))), 0640); e != nil {
				return e
			}
			run("chown", "root:named", "/etc/named.conf")
			if e := service("restart", "named"); e != nil {
				log.Printf("migration 014: %v", e)
			}
			return nil
		}},
		// Форма php.ini записывала пустые поля как «max_input_vars = » (это 0): phpMyAdmin и формы
		// сайтов переставали получать параметры. Пустые строки комментируются — PHP берёт умолчания.
		{"015-php-ini-empty-values", func() ([]string, error) {
			var paths []string
			for _, v := range phpInstalled() {
				if _, changed := repairEmptyIniValues(readFile(phpIni(v))); changed {
					paths = append(paths, P(phpIni(v)))
				}
			}
			return paths, nil
		}, func() error {
			for _, v := range phpInstalled() {
				fixed, changed := repairEmptyIniValues(readFile(phpIni(v)))
				if !changed {
					continue
				}
				if e := writeFile(phpIni(v), []byte(fixed), 0644); e != nil {
					return e
				}
				if e := service("restart", phpService(v)); e != nil {
					log.Printf("migration 015: PHP %s: %v", v, e)
				}
			}
			return nil
		}},
		// SELinux: файлы сайтов, скопированные (cp -a) или перемещённые из других папок, сохраняли
		// чужую метку, и PHP-FPM отвечал «No input file specified». Восстанавливаем метки /var/www.
		{"016-selinux-web-labels", func() ([]string, error) { return nil, nil }, func() error {
			if selinuxEnabled() && fileExists("/var/www") {
				if _, e := runTimeout(30*time.Minute, "", "restorecon", "-R", "/var/www"); e != nil {
					log.Printf("migration 016: %v", firstLine(e.Error()))
				}
			}
			return nil
		}},
		// .user.ini из aaPanel (open_basedir=/www/wwwroot/...) после переноса сайта запрещал PHP
		// открывать скрипты: «No input file specified». Пулы получают open_basedir от панели.
		{"017-php-pool-open-basedir", func() ([]string, error) {
			var paths []string
			for _, s := range store.Sites {
				if f := phpPoolDir(s.PHP) + "/" + poolName(s.Domain) + ".conf"; s.Mode == "php" && s.PHP != "" && fileExists(f) {
					paths = append(paths, P(f))
				}
			}
			return paths, nil
		}, func() error {
			restart := map[string]bool{}
			for _, s := range store.Sites {
				f := phpPoolDir(s.PHP) + "/" + poolName(s.Domain) + ".conf"
				if s.Mode != "php" || s.PHP == "" || !fileExists(f) || strings.Contains(readFile(f), "open_basedir") {
					continue
				}
				u := findUser(s.User)
				if u == nil {
					continue
				}
				b, err := renderPHPPool(s, u)
				if err != nil {
					log.Printf("migration 017: %s: %v", s.Domain, err)
					continue
				}
				if err = writeFile(f, b, 0644); err != nil {
					return err
				}
				restart[s.PHP] = true
			}
			for v := range restart {
				if err := service("restart", phpService(v)); err != nil {
					log.Printf("migration 017: PHP %s: %v", v, err)
				}
			}
			return nil
		}},
		// RHEL: репозиторий LiteSpeed по HTTP у части хостеров отвечал сотни байт в секунду, и dnf
		// не мог установить ничего (в том числе PHP). Переводим его на HTTPS.
		{"018-litespeed-repo-https", func() ([]string, error) {
			if osInfo.Family != "rhel" || !olsRepoConfigured() {
				return nil, nil
			}
			return olsRepoFiles(), nil
		}, func() error {
			if osInfo.Family != "rhel" || !olsRepoConfigured() {
				return nil
			}
			return olsHTTPSRepo()
		}},
		// Сайт по умолчанию с перенаправлением на HTTPS отправлял заход по IP на https://IP, который
		// отклоняется. Теперь IP и чужие имена перебрасываются на домен сайта.
		{"024-default-site-redirect", func() ([]string, error) {
			if s := findSite(store.Settings.DefaultSite); s != nil {
				return []string{P(vhostPath(s.Domain))}, nil
			}
			return nil, nil
		}, func() error {
			mu.Lock()
			defer mu.Unlock()
			s := findSite(store.Settings.DefaultSite)
			u := (*SiteUser)(nil)
			if s != nil {
				u = findUser(s.User)
			}
			if u == nil {
				return nil
			}
			b, err := renderSiteNginx(s, u, readFile(customPath(s.Domain)))
			if err != nil {
				return err
			}
			if err = writeFile(vhostPath(s.Domain), b, 0644); err != nil {
				return err
			}
			return nginxTestReload()
		}},
		// Журналы сайтов на RHEL с SELinux имели тип веб-контента: logrotate не мог их прочитать.
		{"025-selinux-site-logs", func() ([]string, error) { return nil, nil }, func() error {
			selinuxLogsLabel()
			return nil
		}},
		// Redis антиспама на RHEL-системах с SELinux не запускался из-за NoNewPrivileges.
		{"023-spam-redis-selinux", func() ([]string, error) {
			const unit = "/etc/systemd/system/itrupanel-spam-redis.service"
			if osInfo.Family != "rhel" || !strings.Contains(readFile(unit), "NoNewPrivileges=true") {
				return nil, nil
			}
			return []string{P(unit)}, nil
		}, func() error {
			const unit = "/etc/systemd/system/itrupanel-spam-redis.service"
			if osInfo.Family != "rhel" || !strings.Contains(readFile(unit), "NoNewPrivileges=true") {
				return nil
			}
			if err := writeFile(unit, []byte(strings.Replace(readFile(unit), "NoNewPrivileges=true\n", "", 1)), 0644); err != nil {
				return err
			}
			run("systemctl", "daemon-reload")
			if _, err := run("systemctl", "is-enabled", "itrupanel-spam-redis"); err == nil {
				run("systemctl", "reset-failed", "itrupanel-spam-redis")
				service("restart", "itrupanel-spam-redis")
			}
			return nil
		}},
		// SELinux в режиме Permissive раньше считался выключенным: почта и сайты остались без
		// меток и сломались бы при переходе в Enforcing. Метки расставляются заново.
		{"022-selinux-labels-permissive", func() ([]string, error) { return nil, nil }, func() error {
			if !selinuxEnabled() {
				return nil
			}
			if fileExists(mailRoot) {
				if err := labelMailStore(); err != nil {
					log.Printf("migration 022: %v", err)
				}
			}
			relabelWeb("/var/www/")
			if have("semanage") {
				if err := selinuxLabelAlways(sockDir, "httpd_var_run_t"); err != nil {
					log.Printf("migration 022: %v", err)
				}
				for _, v := range phpInstalled() {
					if !serviceActive(phpService(v)) {
						run("systemctl", "reset-failed", phpService(v))
						service("restart", phpService(v))
					}
				}
			}
			return nil
		}},
		// Сервер по умолчанию: по IP-адресу и чужим именам вместо первого сайта по алфавиту или
		// стандартной страницы nginx открывается страница «Сайт не найден», HTTPS отклоняется.
		{"021-nginx-default-server", func() ([]string, error) {
			// sites-enabled/default — символьная ссылка пакета nginx: снимок её не принимает, а сам
			// файл остаётся в sites-available, поэтому ссылка просто удаляется.
			return []string{P(nginxDefaultPath), P(manualDenyConf), P(rhelNginxConf)}, nil
		}, func() error {
			mu.Lock()
			defer mu.Unlock()
			if err := ensureNginxDefault(); err != nil {
				return err
			}
			if err := nginxTestReload(); err != nil {
				// Чужой default_server в своих правилах: обновление не прерываем, работаем как раньше.
				log.Printf("migration 021: %v", err)
				os.Remove(P(nginxDefaultPath))
				return nginxTestReload()
			}
			return nil
		}},
		// Изолированный PHP (chroot), дисковые лимиты и обёртки заданий CRON больше не используются:
		// конфигурации всех сайтов и crontab пересобираются по обычной схеме, служба подготовки
		// квот удаляется, а ранее выставленные лимиты снимаются, чтобы не ограничивать сайты.
		{"020-single-admin-cleanup", func() ([]string, error) {
			var paths []string
			for _, s := range store.Sites {
				if strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					paths = append(paths, P(vhostPath(s.Domain)))
					if s.Mode == "php" && s.PHP != "" {
						paths = append(paths, P(phpPoolDir(s.PHP)+"/"+poolName(s.Domain)+".conf"))
					}
				}
			}
			return paths, nil
		}, func() error {
			mu.Lock()
			for _, s := range store.Sites {
				if !strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					continue
				}
				if err := applySite(s, s.PHP); err != nil {
					log.Printf("migration 020: %s: %v", s.Domain, err)
				}
			}
			for _, u := range store.Users {
				if err := writeCrontab(u.Name); err != nil {
					log.Printf("migration 020: crontab %s: %v", u.Name, err)
				}
				if have("setquota") {
					run("setquota", "-u", u.Name, "0", "0", "0", "0", "-a")
				}
			}
			// Сохранение убирает из state.json данные, которые панель больше не читает.
			err := saveChecked()
			mu.Unlock()
			if err != nil {
				return err
			}
			const unit = "/etc/systemd/system/itrupanel-quota-bootstrap.service"
			if fileExists(unit) {
				run("systemctl", "disable", "itrupanel-quota-bootstrap.service")
				os.Remove(P(unit))
				run("systemctl", "daemon-reload")
			}
			return nil
		}},
		// Перенаправления на уровне server (https, www, свои правила) срабатывали раньше location
		// проверки Let’s Encrypt, и выпуск падал на алиасах. Пересобираем конфиги сайтов с break.
		{"019-site-acme-before-redirects", func() ([]string, error) {
			var paths []string
			for _, s := range store.Sites {
				if strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					paths = append(paths, P(vhostPath(s.Domain)))
				}
			}
			return paths, nil
		}, func() error {
			changed := false
			for _, s := range store.Sites {
				if !strings.HasPrefix(readFile(vhostPath(s.Domain)), "# ITRUPANEL:") {
					continue
				}
				u := findUser(s.User)
				if u == nil {
					log.Printf("migration 019: %s: нет пользователя %s", s.Domain, s.User)
					continue
				}
				b, err := renderSiteNginx(s, u, readFile(customPath(s.Domain)))
				if err != nil {
					log.Printf("migration 019: %s: %v", s.Domain, err)
					continue
				}
				if err = writeFile(vhostPath(s.Domain), b, 0644); err != nil {
					return err
				}
				changed = true
			}
			if changed {
				return nginxTestReload()
			}
			return nil
		}},
	}
}
func loadMigrationLedger() (migrationLedger, error) {
	l := migrationLedger{Applied: map[string]string{}}
	b, e := os.ReadFile(migrationLedgerPath())
	if os.IsNotExist(e) {
		return l, nil
	}
	if e != nil {
		return l, e
	}
	e = json.Unmarshal(b, &l)
	if l.Applied == nil {
		l.Applied = map[string]string{}
	}
	return l, e
}
func snapshotUpdate(paths []string, services []string) (updateSnapshot, error) {
	s := updateSnapshot{Services: services}
	seen := map[string]bool{}
	sort.Strings(paths)
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		f := updateSnapshotFile{Path: p}
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			s.Files = append(s.Files, f)
			continue
		}
		if e != nil {
			return s, e
		}
		// Do not replace administrator symlinks or special files during migration.
		if !st.Mode().IsRegular() {
			return s, errf("Перед обновлением проверьте нестандартный файл %s", p)
		}
		if st.Size() > 32<<20 {
			return s, errf("Слишком большой файл настроек: %s", p)
		}
		f.Exists = true
		f.Mode = uint32(st.Mode().Perm())
		if stat, ok := st.Sys().(*syscall.Stat_t); ok {
			f.UID = int(stat.Uid)
			f.GID = int(stat.Gid)
		}
		f.Data, e = os.ReadFile(p)
		if e != nil {
			return s, e
		}
		s.Files = append(s.Files, f)
	}
	return s, nil
}
func restoreUpdateSnapshot(s updateSnapshot) error {
	var result error
	for _, f := range s.Files {
		var e error
		if f.Exists {
			e = os.MkdirAll(filepath.Dir(f.Path), 0700)
			if e == nil {
				var tmp *os.File
				tmp, e = os.CreateTemp(filepath.Dir(f.Path), ".rollback-*")
				if e == nil {
					tmpName := tmp.Name()
					_, e = tmp.Write(f.Data)
					if e == nil {
						e = tmp.Chmod(os.FileMode(f.Mode))
					}
					if e == nil && !cfg.Dry {
						e = tmp.Chown(f.UID, f.GID)
					}
					ce := tmp.Close()
					if e == nil {
						e = ce
					}
					if e == nil {
						e = os.Rename(tmpName, f.Path)
					}
					os.Remove(tmpName)
				}
			}
		} else {
			e = os.Remove(f.Path)
			if os.IsNotExist(e) {
				e = nil
			}
		}
		if e != nil {
			result = errors.Join(result, fmt.Errorf("%s: %w", f.Path, e))
		}
	}
	if result != nil {
		return result
	}
	for _, svc := range s.Services {
		if e := service("restart", svc); e != nil {
			result = errors.Join(result, e)
		}
	}
	return result
}
func runUpdateMigrations(backup string, migrations []updateMigration) error {
	if !filepath.IsAbs(backup) {
		return errf("Нужен абсолютный путь резервной копии")
	}
	if e := os.MkdirAll(backup, 0700); e != nil {
		return e
	}
	l, e := loadMigrationLedger()
	if e != nil {
		return e
	}
	paths := []string{statePath(), migrationLedgerPath()}
	var pending []updateMigration
	for _, m := range migrations {
		if _, ok := l.Applied[m.ID]; ok {
			continue
		}
		p, e := m.Paths()
		if e != nil {
			return fmt.Errorf("Миграция %s: %w", m.ID, e)
		}
		paths = append(paths, p...)
		pending = append(pending, m)
	}
	var services []string
	candidates := []string{"nginx", "lsws"}
	for _, v := range phpInstalled() {
		candidates = append(candidates, phpService(v))
	}
	if store.Mail.Ready {
		candidates = append(candidates, mailServices(store.Mail)...)
	}
	for _, svc := range candidates {
		if serviceActive(svc) {
			services = append(services, svc)
		}
	}
	snapshot, e := snapshotUpdate(paths, services)
	if e != nil {
		return e
	}
	snapshotPath := filepath.Join(backup, "migration-snapshot.json")
	if _, e = os.Stat(snapshotPath); !os.IsNotExist(e) {
		return errf("Резервная копия миграций уже существует")
	}
	if e = writeUpdateJSON(snapshotPath, snapshot); e != nil {
		return e
	}
	for _, m := range pending {
		fmt.Println("Миграция:", m.ID)
		if e = m.Apply(); e != nil {
			return fmt.Errorf("Миграция %s: %w", m.ID, e)
		}
		l.Applied[m.ID] = Version
	}
	return writeUpdateJSON(migrationLedgerPath(), l)
}
func rollbackUpdateMigrations(backup string) error {
	b, e := os.ReadFile(filepath.Join(backup, "migration-snapshot.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var snapshot updateSnapshot
	if e = json.Unmarshal(b, &snapshot); e != nil {
		return e
	}
	return restoreUpdateSnapshot(snapshot)
}
