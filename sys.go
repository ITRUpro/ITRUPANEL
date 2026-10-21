// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// P переводит системный путь в реальный: на сервере без изменений, в demo — внутрь demo-root.
func P(path string) string {
	if cfg.Root == "/" || cfg.Root == "" {
		return path
	}
	return filepath.Join(cfg.Root, path)
}

// systemPath makes administrative commands available even in a restricted login PATH.
func systemPath(current string) string {
	dirs := []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}
	seen := map[string]bool{}
	var result []string
	for _, dir := range append(dirs, filepath.SplitList(current)...) {
		if dir != "" && !seen[dir] {
			result = append(result, dir)
			seen[dir] = true
		}
	}
	return strings.Join(result, string(os.PathListSeparator))
}

// run выполняет команду и возвращает объединённый вывод. В demo только пишет в лог.
func run(name string, args ...string) (string, error) {
	return runInput("", name, args...)
}

func runInput(stdin, name string, args ...string) (string, error) {
	return runTimeout(10*time.Minute, stdin, name, args...)
}

func runTimeout(t time.Duration, stdin, name string, args ...string) (string, error) {
	if cfg.Dry {
		log.Printf("[demo] %s %s", name, strings.Join(args, " "))
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8", "DEBIAN_FRONTEND=noninteractive")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if len(s) > 2000 {
			s = s[len(s)-2000:]
		}
		return s, fmt.Errorf("%s: %w\n%s", name, err, s)
	}
	return s, nil
}

// sh выполняет строку через bash (только для собственных, не пользовательских строк).
func sh(script string) (string, error) { return run("bash", "-c", script) }

func have(bin string) bool {
	if cfg.Dry {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	real := P(path)
	if err := os.MkdirAll(filepath.Dir(real), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(real), ".itrupanel-write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(perm); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, real)
}

func readFile(path string) string {
	b, _ := os.ReadFile(P(path))
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(P(path))
	return err == nil
}

func randToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// randPass — пароль из букв и цифр, без похожих символов.
func randPass(n int) string {
	const abc = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// ---------- ОС ----------

type OSInfo struct {
	ID, Version, Pretty string
	Family              string // debian | rhel
	WebUser             string // www-data | nginx
	Systemd             bool
}

var osInfo OSInfo

func detectOS() {
	osInfo = OSInfo{ID: "debian", Version: "12", Family: "debian", WebUser: "www-data", Systemd: true, Pretty: "Debian (demo)"}
	if cfg.Dry {
		return
	}
	f, err := os.Open("/etc/os-release")
	if err == nil {
		defer f.Close()
		kv := map[string]string{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if ok {
				kv[k] = strings.Trim(v, `"`)
			}
		}
		osInfo.ID, osInfo.Version, osInfo.Pretty = kv["ID"], kv["VERSION_ID"], kv["PRETTY_NAME"]
		like := kv["ID"] + " " + kv["ID_LIKE"]
		if strings.Contains(like, "rhel") || strings.Contains(like, "fedora") || strings.Contains(like, "centos") {
			osInfo.Family, osInfo.WebUser = "rhel", "nginx"
		}
	}
	_, err = os.Stat("/run/systemd/system")
	osInfo.Systemd = err == nil
}

// service: start | stop | restart | reload | enable
func service(action, name string) error {
	if name == "lsws" && action == "reload" {
		action = "restart"
	}
	if name == "lsws" && !osInfo.Systemd {
		if action == "enable" {
			action = "start"
		}
		_, err := run(olsRoot+"/bin/lswsctrl", action)
		return err
	}
	if osInfo.Systemd {
		if name == "lsws" {
			name = olsServiceName()
		}
		if action == "enable" {
			_, err := run("systemctl", "enable", "--now", name)
			return err
		}
		_, err := run("systemctl", action, name)
		return err
	}
	// Без systemd (контейнер) — запускаем демоны напрямую.
	// Остановка должна завершать службу, а не попадать в ветку её запуска.
	if action == "stop" {
		switch {
		case name == "nginx":
			_, err := run("nginx", "-s", "quit")
			return err
		case strings.Contains(name, "fpm"):
			_, err := run("pkill", "-f", "php-fpm: master process.*"+fpmConfDir(name))
			return err
		case name == "mariadb":
			admin := "mariadb-admin"
			if !have(admin) {
				admin = "mysqladmin"
			}
			_, err := run(admin, "shutdown")
			return err
		case name == "cron" || name == "crond":
			_, err := run("pkill", "-x", name)
			return err
		case strings.HasPrefix(name, "pure-ftpd"):
			_, err := run("pkill", "-x", "pure-ftpd")
			return err
		}
	}
	switch {
	case name == "nginx":
		if action == "reload" || action == "restart" {
			if _, err := run("nginx", "-s", "reload"); err == nil {
				return nil
			}
		}
		_, err := run("nginx")
		return err
	case strings.Contains(name, "fpm"):
		bin := fpmBinary(name)
		run("pkill", "-f", "php-fpm: master process.*"+fpmConfDir(name))
		time.Sleep(300 * time.Millisecond)
		_, err := run(bin, "-D")
		return err
	case name == "mariadb":
		if action == "start" || action == "enable" || action == "restart" {
			_, err := sh("[ -d /var/lib/mysql/mysql ] || (mariadb-install-db --user=mysql --datadir=/var/lib/mysql >/dev/null 2>&1 || mysql_install_db --user=mysql >/dev/null 2>&1); mkdir -p /run/mysqld /var/run/mariadb && chown mysql:mysql /run/mysqld /var/run/mariadb; pgrep -x mariadbd >/dev/null || pgrep -x mysqld >/dev/null || (nohup mysqld_safe >/dev/null 2>&1 &) ; sleep 4")
			return err
		}
	case name == "apache2" || name == "httpd":
		ctl := "apache2ctl"
		if name == "httpd" {
			ctl = "httpd"
		}
		if action == "stop" {
			_, err := run(ctl, "-k", "stop")
			return err
		}
		if _, err := run(ctl, "-k", "graceful"); err == nil && serviceActive(name) {
			return nil
		}
		_, err := run(ctl, "-k", "start")
		return err
	case name == "named":
		if action == "stop" {
			_, err := run("rndc", "stop")
			return err
		}
		if _, err := run("rndc", "reload"); err == nil {
			return nil
		}
		// Без ключа rndc (контейнер без systemd) перезагрузка не проходит: перезапускаем named,
		// иначе второй экземпляр не сможет занять порт 53
		if _, err := run("pgrep", "-x", "named"); err == nil {
			run("pkill", "-x", "named")
			time.Sleep(time.Second)
		}
		u := "bind"
		if osInfo.Family == "rhel" {
			u = "named"
		}
		_, err := run("named", "-u", u)
		return err
	case name == "cron" || name == "crond":
		if action != "stop" {
			_, err := sh("pgrep -x " + name + " >/dev/null || " + name)
			return err
		}
	case strings.HasPrefix(name, "pure-ftpd"):
		run("pkill", "-x", "pure-ftpd")
		_, err := run("bash", "-c", pureftpdCmd())
		return err
	}
	return nil
}

func serviceActive(name string) bool {
	if cfg.Dry {
		return true
	}
	if osInfo.Systemd {
		if name == "lsws" {
			name = olsServiceName()
		}
		out, _ := exec.Command("systemctl", "is-active", name).Output()
		return strings.TrimSpace(string(out)) == "active"
	}
	pat := map[string]string{"nginx": "nginx: master", "mariadb": "mariadbd|mysqld", "apache2": "apache2 -k|/usr/sbin/apache2", "httpd": "/usr/sbin/httpd", "cron": "^cron", "crond": "crond", "pure-ftpd": "pure-ftpd \\(", "named": "named -u|/usr/sbin/named"}[name]
	if v := svcVersion(name); v != "" && strings.Contains(name, "fpm") {
		pat = "php-fpm: master process.*" + fpmConfDir(name)
	}
	if pat == "" {
		pat = name
	}
	err := exec.Command("pgrep", "-f", pat).Run()
	return err == nil
}

func pkgInstall(pkgs ...string) error {
	if osInfo.Family == "rhel" {
		// Недоступный сторонний репозиторий (например, медленный LiteSpeed по HTTP) не должен
		// срывать установку пакетов из других репозиториев
		_, err := runTimeout(30*time.Minute, "", "dnf", append([]string{"--setopt=skip_if_unavailable=True", "install", "-y"}, pkgs...)...)
		return err
	}
	_, err := runTimeout(30*time.Minute, "", "apt-get", append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)...)
	return err
}

// ---------- проверка ввода ----------

var (
	reDomain = regexp.MustCompile(`^(?i)([a-z0-9а-яё]([a-z0-9а-яё-]{0,61}[a-z0-9а-яё])?\.)+[a-zа-яё0-9-]{2,63}$`)
	reName   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	reDBName = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)
	reEmail  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

func cleanDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "http://"), "https://")
	return strings.Trim(d, "/.")
}

// safeUnder проверяет, что путь лежит внутри base (защита от ../).
func safeUnder(base, p string) (string, bool) {
	if !filepath.IsAbs(base) || strings.ContainsRune(p, 0) {
		return "", false
	}
	base = filepath.Clean(base)
	full := filepath.Clean(p)
	if !filepath.IsAbs(p) {
		full = filepath.Join(base, p)
	}
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}
