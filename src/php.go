// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Версии PHP: на Debian — репозиторий Sury, на Ubuntu 22.04/24.04 — PPA ondrej/php, на Ubuntu 26.04 — Sury, на RHEL — Remi.

var phpAll = []string{"5.6", "7.0", "7.1", "7.2", "7.3", "7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}

// phpAllVersions — версии PHP, доступные для установки: на FreeBSD их меньше (пакеты FreeBSD).
func phpAllVersions() []string {
	if isBSD() {
		return bsdPHPVersions
	}
	return phpAll
}

// phpInstallMu — пакетный менеджер не терпит параллельных установок.
var phpInstallMu sync.Mutex

func vv(v string) string { return strings.ReplaceAll(v, ".", "") }

func phpService(v string) string {
	if isBSD() {
		return "php_fpm"
	}
	if osInfo.Family == "rhel" {
		return "php" + vv(v) + "-php-fpm"
	}
	return "php" + v + "-fpm"
}

func phpPoolDir(v string) string {
	if isBSD() {
		return bsdPoolDir
	}
	if osInfo.Family == "rhel" {
		return "/etc/opt/remi/php" + vv(v) + "/php-fpm.d"
	}
	return "/etc/php/" + v + "/fpm/pool.d"
}

func phpIni(v string) string {
	if isBSD() {
		return bsdPHPIni
	}
	if osInfo.Family == "rhel" {
		return "/etc/opt/remi/php" + vv(v) + "/php.ini"
	}
	return "/etc/php/" + v + "/fpm/php.ini"
}

func phpCLI(v string) string {
	if isBSD() {
		return bsdPHPCLI
	}
	if osInfo.Family == "rhel" {
		return "/usr/bin/php" + vv(v)
	}
	return "/usr/bin/php" + v
}

func phpFPMBin(v string) string {
	if isBSD() {
		return bsdFPMBin
	}
	if osInfo.Family == "rhel" {
		return "/opt/remi/php" + vv(v) + "/root/usr/sbin/php-fpm"
	}
	return "/usr/sbin/php-fpm" + v
}

var reSvcVer = regexp.MustCompile(`php(\d)\.?(\d)`)

func svcVersion(name string) string {
	m := reSvcVer.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

func fpmBinary(svc string) string  { return phpFPMBin(svcVersion(svc)) }
func fpmConfDir(svc string) string { return filepath.Dir(phpPoolDir(svcVersion(svc))) }

func phpPackages(v string) []string {
	if isBSD() {
		return bsdPHPPackages(v)
	}
	ext := []string{"fpm", "cli", "mysql", "gd", "curl", "mbstring", "xml", "zip", "intl", "bcmath", "opcache", "sqlite3", "imagick", "soap"}
	var out []string
	for _, e := range ext {
		if e == "opcache" && v >= "8.5" {
			continue // в PHP 8.5 OPcache встроен в ядро, отдельного пакета нет
		}
		if osInfo.Family == "rhel" {
			switch e {
			case "fpm", "cli", "gd", "mbstring", "xml", "intl", "bcmath", "opcache", "soap", "pdo":
				out = append(out, "php"+vv(v)+"-php-"+e)
			case "mysql":
				out = append(out, "php"+vv(v)+"-php-mysqlnd")
			case "zip":
				out = append(out, "php"+vv(v)+"-php-pecl-zip")
			case "imagick":
				out = append(out, "php"+vv(v)+"-php-pecl-imagick-im7")
			case "curl", "sqlite3":
				// входят в common/pdo
				out = append(out, "php"+vv(v)+"-php-pdo")
			}
			continue
		}
		out = append(out, "php"+v+"-"+e)
	}
	return dedup(out)
}

func dedup(a []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func phpInstalled() []string {
	var out []string
	if isBSD() && !cfg.Dry {
		if v := bsdInstalledPHP(); v != "" {
			out = append(out, v)
		}
		return out
	}
	for _, v := range phpAllVersions() {
		if cfg.Dry {
			if v == "8.2" || v == "8.3" {
				out = append(out, v)
			}
			continue
		}
		if _, err := os.Stat(phpPoolDir(v)); err == nil {
			if _, err := os.Stat(phpFPMBin(v)); err == nil {
				out = append(out, v)
			}
		}
	}
	return out
}

var rePHPRelease = regexp.MustCompile(`PHP (\d+\.\d+\.\d+[^\s]*)`)

type phpReleaseCache struct {
	mtime   time.Time
	release string
}

var (
	phpReleaseMu sync.Mutex
	phpReleases  = map[string]phpReleaseCache{}
)

// phpRelease — точный выпуск установленной версии (например, 8.3.14); пусто, если узнать не удалось.
// Результат запоминается, пока бинарник не изменился (обновление пакета).
func phpRelease(v string) string {
	if cfg.Dry {
		return map[string]string{"8.2": "8.2.28", "8.3": "8.3.21"}[v]
	}
	bin := phpFPMBin(v)
	st, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	phpReleaseMu.Lock()
	c, ok := phpReleases[v]
	phpReleaseMu.Unlock()
	if ok && c.mtime.Equal(st.ModTime()) {
		return c.release
	}
	out, _ := runTimeout(10*time.Second, "", bin, "-v")
	rel := ""
	if m := rePHPRelease.FindStringSubmatch(out); m != nil {
		rel = m[1]
	}
	if rel != "" {
		phpReleaseMu.Lock()
		phpReleases[v] = phpReleaseCache{st.ModTime(), rel}
		phpReleaseMu.Unlock()
	}
	return rel
}

func phpIsInstalled(v string) bool {
	for _, x := range phpInstalled() {
		if x == v {
			return true
		}
	}
	return false
}

func defaultPHP() string {
	mu.Lock()
	d := store.Settings.DefaultPHP
	mu.Unlock()
	inst := phpInstalled()
	for _, v := range inst {
		if v == d {
			return d
		}
	}
	if len(inst) > 0 {
		return inst[len(inst)-1]
	}
	return ""
}

// Настройки php.ini, которые можно менять в панели.
var phpIniKeys = []string{"memory_limit", "upload_max_filesize", "post_max_size", "max_execution_time", "max_input_time", "max_input_vars", "display_errors", "date.timezone", "short_open_tag", "opcache.enable"}

var reIniLine = regexp.MustCompile(`(?m)^\s*;?\s*([a-z_.]+)\s*=\s*(.*)$`)

func readIni(v string) map[string]string {
	vals := map[string]string{}
	for _, m := range reIniLine.FindAllStringSubmatch(readFile(phpIni(v)), -1) {
		line := strings.TrimSpace(m[0])
		if strings.HasPrefix(line, ";") {
			continue
		}
		vals[m[1]] = strings.TrimSpace(m[2])
	}
	out := map[string]string{}
	for _, k := range phpIniKeys {
		out[k] = vals[k]
	}
	return out
}

func writeIni(v string, set map[string]string) error {
	p := phpIni(v)
	s := readFile(p)
	if s == "" && !cfg.Dry {
		return errf("Не найден %s", p)
	}
	for k, val := range set {
		ok := false
		for _, kk := range phpIniKeys {
			ok = ok || kk == k
		}
		// Пустое поле формы — «не менять»: строка «max_input_vars = » означала бы 0
		// и PHP перестал бы принимать параметры запросов (phpMyAdmin не входит)
		if !ok || strings.ContainsAny(val, "\n\r") || strings.TrimSpace(val) == "" {
			continue
		}
		re := regexp.MustCompile(`(?m)^\s*;?\s*` + regexp.QuoteMeta(k) + `\s*=.*$`)
		line := k + " = " + val
		if re.MatchString(s) {
			done := false
			s = re.ReplaceAllStringFunc(s, func(m string) string {
				if done {
					return m
				}
				done = true
				return line
			})
		} else {
			s += "\n" + line + "\n"
		}
	}
	if err := writeFile(p, []byte(s), 0644); err != nil {
		return err
	}
	return phpExtRestart(v)
}

// repairEmptyIniValues комментирует строки «ключ = » без значения для настроек панели:
// так PHP снова берёт значение по умолчанию. Их записывала форма php.ini версий до 0.1.61.
func repairEmptyIniValues(s string) (string, bool) {
	changed := false
	for _, k := range phpIniKeys {
		re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(k) + `[ \t]*=[ \t]*("")?[ \t]*$`)
		if re.MatchString(s) {
			s = re.ReplaceAllString(s, "; "+k+" = ; ITRUPANEL: пустое значение отключено")
			changed = true
		}
	}
	return s, changed
}

// phpRepoSetup подключает репозиторий с несколькими версиями PHP (один раз).
func phpRepoSetup() error {
	switch {
	case osInfo.Family == "rhel":
		if fileExists("/etc/yum.repos.d/remi.repo") {
			return nil
		}
		major := strings.Split(osInfo.Version, ".")[0]
		// EPEL нужен Remi; в части образов он установлен, но выключен (enabled=0)
		_, err := sh("(rpm -q epel-release >/dev/null || dnf install -y epel-release || dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-" + major + ".noarch.rpm); (dnf config-manager --set-enabled epel 2>/dev/null || sed -i '/^\\[epel\\]/,/^\\[/ s/^enabled=0/enabled=1/' /etc/yum.repos.d/epel.repo); dnf install -y https://rpms.remirepo.net/enterprise/remi-release-" + major + ".rpm")
		return err
	case osInfo.ID == "ubuntu":
		if ubuntuResolute() {
			return setupResolutePHPRepository()
		}
		if out, _ := sh("ls /etc/apt/sources.list.d/ | grep -i ondrej"); out != "" {
			return nil
		}
		_, err := sh("apt-get install -y software-properties-common && LC_ALL=C.UTF-8 add-apt-repository -y ppa:ondrej/php && apt-get update")
		return err
	default:
		if fileExists("/etc/apt/sources.list.d/php-sury.list") {
			return nil
		}
		_, err := sh(`apt-get install -y curl ca-certificates gnupg lsb-release && curl -fsSL https://packages.sury.org/php/apt.gpg -o /usr/share/keyrings/php-sury.gpg && echo "deb [signed-by=/usr/share/keyrings/php-sury.gpg] https://packages.sury.org/php/ $(lsb_release -sc) main" > /etc/apt/sources.list.d/php-sury.list && apt-get update`)
		return err
	}
}

var phpJobs = map[string]string{} // версия → состояние установки

func phpInstall(v string) error {
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if phpIsInstalled(v) {
		return nil
	}
	if isBSD() {
		return bsdPHPInstall(v)
	}
	if err := phpRepoSetup(); err != nil {
		return err
	}
	all := phpPackages(v)
	if err := pkgInstall(all...); err != nil {
		// не все модули есть для старых/новых версий: ставим ядро, остальное — по одному
		var core []string
		for _, p := range all {
			if strings.HasSuffix(p, "-fpm") || strings.HasSuffix(p, "-cli") {
				core = append(core, p)
			}
		}
		if err2 := pkgInstall(core...); err2 != nil {
			return err2
		}
		for _, p := range all {
			if !strings.HasSuffix(p, "-fpm") && !strings.HasSuffix(p, "-cli") {
				pkgInstall(p)
			}
		}
	}
	os.MkdirAll(P(sockDir), 0755)
	os.MkdirAll(P("/run/php"), 0755)
	return service("enable", phpService(v))
}

func registerPHP() {
	api("php.list", func(q *Req) (any, error) {
		inst := phpInstalled()
		def := defaultPHP()
		mu.Lock()
		use := map[string]int{}
		for _, s := range store.Sites {
			if s.PHP != "" {
				use[s.PHP]++
			}
		}
		jobs := map[string]string{}
		for k, v := range phpJobs {
			jobs[k] = v
		}
		mu.Unlock()
		var rows []map[string]any
		for _, v := range phpAllVersions() {
			installed := false
			for _, x := range inst {
				installed = installed || x == v
			}
			row := map[string]any{"version": v, "installed": installed, "default": v == def, "sites": use[v], "job": jobs[v]}
			if installed {
				row["active"] = serviceActive(phpService(v))
				row["release"] = phpRelease(v)
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i]["version"].(string) > rows[j]["version"].(string) })
		return rows, nil
	})
	apiOp("php.install", "Установка PHP", func(q *Req) (any, error) {
		var in struct{ Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ok := false
		for _, v := range phpAllVersions() {
			ok = ok || v == in.Version
		}
		if !ok {
			return nil, errf("Неизвестная версия PHP")
		}
		mu.Lock()
		if phpJobs[in.Version] == "running" {
			mu.Unlock()
			return nil, errf("Установка уже идёт")
		}
		phpJobs[in.Version] = "running"
		mu.Unlock()
		user, ip := q.S.User, q.S.IP
		go func() {
			start := time.Now()
			err := phpInstall(in.Version)
			e := OpEntry{Time: start, User: user, IP: ip, Action: "Установка PHP (завершение)", Object: in.Version, Status: "ok", Ms: time.Since(start).Milliseconds()}
			mu.Lock()
			if err != nil {
				phpJobs[in.Version] = "error"
				e.Status, e.Error = "error", err.Error()
				addNotice("error", "phpinst"+in.Version, fmt.Sprintf("PHP %s не установлен: %v", in.Version, firstLine(err.Error())))
			} else {
				delete(phpJobs, in.Version)
				addNotice("info", "", "PHP "+in.Version+" установлен")
			}
			save()
			mu.Unlock()
			appendJSONL("ops.jsonl", e)
		}()
		return "started", nil
	})
	apiOp("php.remove", "Удаление PHP", func(q *Req) (any, error) {
		var in struct{ Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		for _, s := range store.Sites {
			if s.PHP == in.Version {
				mu.Unlock()
				return nil, errf("PHP %s используется сайтом %s", in.Version, s.Domain)
			}
		}
		mu.Unlock()
		pk := phpPackages(in.Version)
		if isBSD() {
			return nil, bsdPHPRemove(in.Version)
		}
		if osInfo.Family == "rhel" {
			_, err := run("dnf", append([]string{"remove", "-y"}, pk...)...)
			return nil, err
		}
		_, err := run("apt-get", append([]string{"purge", "-y"}, pk...)...)
		return nil, err
	})
	api("php.ini", func(q *Req) (any, error) {
		var in struct{ Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !phpIsInstalled(in.Version) {
			return nil, errf("PHP %s не установлен", in.Version)
		}
		mods := ""
		if !cfg.Dry {
			mods, _ = run(phpCLI(in.Version), "-m")
		}
		return map[string]any{"values": readIni(in.Version), "keys": phpIniKeys, "modules": phpParseModules(mods), "ini": phpIni(in.Version), "extras": phpExtNames(), "catalog": phpExtOverview(in.Version)}, nil
	})
	apiOp("php.ext", "PHP: модуль", func(q *Req) (any, error) {
		var in struct {
			Version, Ext string
			Remove       bool
			Action       string // install | remove | enable | disable; пусто — по полю Remove
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !phpIsInstalled(in.Version) {
			return nil, errf("PHP %s не установлен", in.Version)
		}
		if phpExtFind(in.Ext) == nil {
			return nil, errf("Неизвестный модуль")
		}
		switch {
		case in.Action == "enable" || in.Action == "disable":
			phpInstallMu.Lock()
			defer phpInstallMu.Unlock()
			return nil, phpExtSetEnabled(in.Version, in.Ext, in.Action == "enable")
		case in.Remove || in.Action == "remove":
			return nil, phpExtRemove(in.Version, in.Ext)
		}
		return nil, phpExtInstall(in.Version, in.Ext)
	})
	apiOp("php.ini.save", "Настройки PHP", func(q *Req) (any, error) {
		var in struct {
			Version string
			Values  map[string]string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !phpIsInstalled(in.Version) {
			return nil, errf("PHP %s не установлен", in.Version)
		}
		return nil, writeIni(in.Version, in.Values)
	})
	apiOp("php.default", "PHP по умолчанию", func(q *Req) (any, error) {
		var in struct{ Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !phpIsInstalled(in.Version) {
			return nil, errf("PHP %s не установлен", in.Version)
		}
		mu.Lock()
		store.Settings.DefaultPHP = in.Version
		save()
		mu.Unlock()
		return nil, nil
	})
	apiOp("php.restart", "Перезапуск PHP-FPM", func(q *Req) (any, error) {
		var in struct{ Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, phpExtRestart(in.Version)
	})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
