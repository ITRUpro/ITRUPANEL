// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// runSetup — первичная настройка сервера (вызывается из install.sh после установки пакетов).
// Повторный запуск безопасен: файлы перезаписываются, данные панели не трогаются.
func runSetup(php string) error {
	detectOS()
	var setupErrors []error
	step := func(name string, fn func() error) bool {
		fmt.Printf("  • %s… ", name)
		if err := fn(); err != nil {
			fmt.Printf("ошибка\n    %v\n", err)
			setupErrors = append(setupErrors, fmt.Errorf("%s: %w", name, err))
			return false
		}
		fmt.Println("готово")
		return true
	}
	for _, d := range []string{vhostDir, customDir, sslDir, acmeRoot + "/.well-known/acme-challenge", sockDir, backupDir, cfg.Home + "/bin", cfg.Home + "/www"} {
		if err := os.MkdirAll(P(d), 0755); err != nil {
			return fmt.Errorf("каталог %s: %w", d, err)
		}
	}
	// Только проход: архивы 0600, служебные папки 0700; восстановление файлов идёт от владельца сайта.
	os.Chmod(P(backupDir), 0711)
	os.Chmod(P(cfg.Home+"/etc"), 0700)
	os.Chmod(P(cfg.Home+"/var"), 0700)

	step("nginx", func() error {
		g, _ := tplFS.ReadFile("templates/nginx-global.conf")
		if err := writeFile("/etc/nginx/conf.d/itrupanel.conf", g, 0644); err != nil {
			return err
		}
		if err := ensureNginxDefault(); err != nil {
			return err
		}
		if _, err := run("nginx", "-t"); err != nil {
			return err
		}
		return service("enable", "nginx")
	})
	step("каталоги и ротация журналов", func() error {
		if err := writeFile("/etc/tmpfiles.d/itrupanel.conf", []byte("d "+sockDir+" 0755 root root -\n"), 0644); err != nil {
			return err
		}
		return writeFile("/etc/logrotate.d/itrupanel", []byte(`/var/www/*/data/logs/*.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
}
`), 0644)
	})
	step("MariaDB", func() error {
		if err := service("enable", "mariadb"); err != nil {
			return err
		}
		for i := 0; i < 15; i++ {
			if _, err := mysql("SELECT 1;"); err == nil {
				// убираем анонимных пользователей и тестовую базу (как mysql_secure_installation)
				mysql("DELETE FROM mysql.global_priv WHERE User='';")
				mysql("DROP DATABASE IF EXISTS test; FLUSH PRIVILEGES;")
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("MariaDB не отвечает")
	})
	step("FTP (pure-ftpd)", setupPureFTPd)
	step("DNS-сервер BIND", setupBind)
	step("cron", func() error { return service("enable", cronService()) })
	if osInfo.Family == "rhel" {
		step("SELinux", func() error {
			if out, _ := run("getenforce"); strings.TrimSpace(out) == "Disabled" || !have("getenforce") {
				return nil
			}
			sh(`semanage fcontext -a -t httpd_sys_rw_content_t "/var/www(/.*)?" 2>/dev/null || semanage fcontext -m -t httpd_sys_rw_content_t "/var/www(/.*)?"`)
			sh(`semanage fcontext -a -t httpd_sys_rw_content_t "` + cfg.Home + `/www(/.*)?" 2>/dev/null || true`)
			// Сокеты PHP-FPM: с меткой var_run_t PHP-FPM не может удалить свой сокет при перезапуске
			// и не запускается («Address already in use», AlmaLinux 10).
			sh(`semanage fcontext -a -t httpd_var_run_t "` + sockDir + `(/.*)?" 2>/dev/null || semanage fcontext -m -t httpd_var_run_t "` + sockDir + `(/.*)?"`)
			selinuxLogsLabel()
			run("restorecon", "-R", "/var/www", cfg.Home+"/www", sockDir)
			run("setsebool", "-P", "httpd_can_network_connect", "1")
			run("setsebool", "-P", "httpd_setrlimit", "1")
			run("setsebool", "-P", "ftpd_full_access", "1")
			run("semanage", "port", "-a", "-t", "http_port_t", "-p", "tcp", "8088")
			return nil
		})
	}
	if php != "" && php != "none" {
		phpOK := step("PHP "+php+" (несколько минут)", func() error {
			if phpIsInstalled(php) {
				return service("enable", phpService(php))
			}
			return phpInstall(php)
		})
		if phpOK {
			mu.Lock()
			if store.Settings.DefaultPHP == "" {
				store.Settings.DefaultPHP = php
				save()
			}
			mu.Unlock()
			step("phpMyAdmin", installPMA)
		}
	}
	// Веб-серверы за nginx ставятся сразу, чтобы режимы Apache и OpenLiteSpeed были готовы без
	// ожидания при создании сайта. Их ошибка (например, недоступный репозиторий) не прерывает
	// установку панели: веб-сервер установится позже при первом выборе.
	optional := func(name string, fn func() error) {
		fmt.Printf("  • %s… ", name)
		if err := fn(); err != nil {
			fmt.Printf("пропущено\n    %v\n    Установится при первом выборе этого режима для сайта.\n", firstLine(err.Error()))
			return
		}
		fmt.Println("готово")
	}
	// На серверах меньше 2 ГБ ещё два веб-сервера вместе с MariaDB, PHP-FPM и dnf/apt
	// исчерпывают память, поэтому там они ставятся только при первом выборе для сайта.
	smallHost := setupSmallMemory()
	optional("Apache (за nginx)", func() error {
		if apacheReady() {
			return nil
		}
		if smallHost {
			return errf("памяти меньше 2 ГБ")
		}
		return ensureApache()
	})
	optional("OpenLiteSpeed (за nginx, несколько минут)", func() error {
		if !cfg.Dry && !osInfo.Systemd {
			return errf("нужен systemd")
		}
		if smallHost {
			return errf("памяти меньше 2 ГБ")
		}
		return ensureOLS()
	})
	step("файрвол", setupFirewall)
	if len(setupErrors) > 0 {
		return errors.Join(setupErrors...)
	}
	step("служба itrupanel", func() error {
		if !osInfo.Systemd {
			return nil
		}
		unit := `[Unit]
Description=ITRUPANEL — панель управления сервером
After=network.target

[Service]
ExecStart=` + cfg.Home + `/itrupanel
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`
		if err := writeFile("/etc/systemd/system/itrupanel.service", []byte(unit), 0644); err != nil {
			return err
		}
		if _, err := run("systemctl", "daemon-reload"); err != nil {
			return err
		}
		if _, err := run("systemctl", "enable", "itrupanel"); err != nil {
			return err
		}
		_, err := run("systemctl", "restart", "itrupanel")
		return err
	})
	return errors.Join(setupErrors...)
}

// setupSmallMemory — у сервера меньше 2 ГБ памяти (без учёта swap).
func setupSmallMemory() bool {
	if cfg.Dry {
		return false
	}
	total, _ := memInfo()
	return total > 0 && total < 2000<<20
}

func setupPureFTPd() error {
	if !have("pure-pw") {
		return fmt.Errorf("pure-ftpd не установлен")
	}
	writeFile(ftpPasswd, []byte(readFile(ftpPasswd)), 0600)
	run("pure-pw", "mkdb", ftpPDB, "-f", ftpPasswd)
	host, _ := os.Hostname()
	crt, key, _ := selfSigned([]string{host}, 3650)
	pem := append(append([]byte{}, key...), crt...)
	if osInfo.Family == "rhel" {
		writeFile("/etc/pki/pure-ftpd/pure-ftpd.pem", pem, 0600)
		conf := readFile("/etc/pure-ftpd/pure-ftpd.conf")
		set := map[string]string{
			"ChrootEveryone": "yes", "PureDB": ftpPDB, "PAMAuthentication": "no", "UnixAuthentication": "no",
			"PassivePortRange": "30000 30100", "MinUID": "1000", "TLS": "1", "CertFile": "/etc/pki/pure-ftpd/pure-ftpd.pem",
			"NoAnonymous": "yes", "CreateHomeDir": "no", "DontResolve": "yes",
		}
		var b bytes.Buffer
		seen := map[string]bool{}
		for _, line := range strings.Split(conf, "\n") {
			t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			fs := strings.Fields(t)
			if len(fs) == 0 {
				b.WriteString(line + "\n")
				continue
			}
			k := fs[0]
			if v, ok := set[k]; ok && !seen[k] {
				b.WriteString(k + "    " + v + "\n")
				seen[k] = true
				continue
			}
			b.WriteString(line + "\n")
		}
		for k, v := range set {
			if !seen[k] {
				b.WriteString(k + "    " + v + "\n")
			}
		}
		writeFile("/etc/pure-ftpd/pure-ftpd.conf", bytes.TrimRight(b.Bytes(), "\n"), 0644)
	} else {
		writeFile("/etc/ssl/private/pure-ftpd.pem", pem, 0600)
		c := "/etc/pure-ftpd/conf/"
		for k, v := range map[string]string{"ChrootEveryone": "yes", "PureDB": ftpPDB, "PassivePortRange": "30000 30100", "MinUID": "1000", "TLS": "1", "DontResolve": "yes", "NoAnonymous": "yes", "UnixAuthentication": "no", "PAMAuthentication": "no"} {
			writeFile(c+k, []byte(v+"\n"), 0644)
		}
		os.MkdirAll(P("/etc/pure-ftpd/auth"), 0755)
		os.Remove(P("/etc/pure-ftpd/auth/65unix"))
		os.Remove(P("/etc/pure-ftpd/auth/70pam"))
		os.Symlink("../conf/PureDB", P("/etc/pure-ftpd/auth/50puredb"))
	}
	if err := service("enable", "pure-ftpd"); err != nil {
		return err
	}
	if osInfo.Systemd {
		run("systemctl", "restart", "pure-ftpd")
	}
	return nil
}

func setupFirewall() error {
	ports := []string{"80", "443", "3333", "21", "53"}
	if have("ufw") {
		if out, _ := run("ufw", "status"); strings.Contains(out, "Status: active") {
			for _, p := range ports {
				run("ufw", "allow", p+"/tcp")
			}
			run("ufw", "allow", "30000:30100/tcp")
			run("ufw", "allow", "53/udp")
			return nil
		}
	}
	if have("firewall-cmd") {
		if out, _ := run("firewall-cmd", "--state"); strings.Contains(out, "running") {
			for _, p := range ports {
				run("firewall-cmd", "--permanent", "--add-port="+p+"/tcp")
			}
			run("firewall-cmd", "--permanent", "--add-port=30000-30100/tcp")
			run("firewall-cmd", "--permanent", "--add-port=53/udp")
			run("firewall-cmd", "--reload")
		}
	}
	return nil
}

func init() {
	log.SetFlags(log.LstdFlags)
}
