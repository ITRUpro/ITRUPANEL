// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"
)

func snapshotSiteRuntime(s *Site, oldPHP string) (func() error, error) {
	paths := []string{vhostPath(s.Domain), apacheVhostPath(s.Domain)}
	versions := dedup([]string{oldPHP, s.PHP})
	for _, v := range versions {
		if v != "" {
			paths = append(paths, phpPoolDir(v)+"/"+poolName(s.Domain)+".conf")
		}
	}
	type snapshot struct {
		path   string
		data   []byte
		exists bool
	}
	var snapshots []snapshot
	for _, path := range paths {
		data, err := os.ReadFile(P(path))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		snapshots = append(snapshots, snapshot{path, data, err == nil})
	}
	return func() error {
		var errs []error
		for _, sn := range snapshots {
			if sn.exists {
				errs = append(errs, writeFile(sn.path, sn.data, 0644))
			} else if err := os.Remove(P(sn.path)); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
		for _, v := range versions {
			if v != "" {
				errs = append(errs, service("restart", phpService(v)))
			}
		}
		if apacheInstalled() {
			errs = append(errs, service("reload", apacheService()))
		}
		errs = append(errs, nginxTestReload())
		return errors.Join(errs...)
	}, nil
}

const olsRoot = "/usr/local/lsws"
const olsPort = "8089" // 8088 is reserved for phpMyAdmin; never listen publicly.
const olsConfig = olsRoot + "/conf/httpd_config.conf"
const olsMarker = "# ITRUPANEL: managed OpenLiteSpeed\n"

const olsAccessLog = `accessLog logs/access.log {
  rollingSize 10M
  keepDays 30
  compressArchive 0
  logReferer 1
  logUserAgent 1
}
`

// Older panel releases omitted the mandatory server access log. Preserve the
// existing virtual hosts and listener maps when repairing a managed install.
func repairOLSAccessLog() error {
	config := readFile(olsConfig)
	if !strings.HasPrefix(config, olsMarker) {
		return nil
	}
	depth := 0
	accessLogLine := regexp.MustCompile(`(?i)^\s*accesslog\s+`)
	for _, line := range strings.Split(config, "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		if depth == 0 && accessLogLine.MatchString(line) {
			return nil
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
	}
	return writeFile(olsConfig, []byte(config+"\n"+olsAccessLog), 0644)
}

var tplOLS = template.Must(template.ParseFS(tplFS, "templates/ols-site.conf"))

func olsInstalled() bool                { return fileExists(olsRoot + "/bin/openlitespeed") }
func olsManaged() bool                  { return strings.HasPrefix(readFile(olsConfig), olsMarker) }
func olsVhostPath(domain string) string { return olsRoot + "/conf/itrupanel/" + domain + ".conf" }

// The vendor installs lsws.service as an alias of lshttpd.service on many
// distributions. systemctl start accepts that alias, but enable can reject it.
// Keep the legacy name for packages that provide only a real lsws unit.
func olsServiceName() string {
	for _, dir := range []string{"/etc/systemd/system", "/run/systemd/system", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system"} {
		if fileExists(dir + "/lshttpd.service") {
			return "lshttpd"
		}
	}
	return "lsws"
}

// Install only when first selected. Existing independent OLS installations are
// not adopted implicitly. A marker permits retrying our own interrupted install.
func ensureOLS() error { return ensureOLSJob(nil) }
func ensureOLSJob(job *Job) error {
	jobStage(job, "Ожидание пакетного менеджера")
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	marker := "/etc/itrupanel/openlitespeed-owned"
	if olsInstalled() && !olsManaged() && !fileExists(marker) {
		return errf("Обнаружена отдельная установка OpenLiteSpeed. Её конфигурация не изменена; сначала перенесите её сайты в панель")
	}
	if !olsInstalled() {
		if !cfg.Dry && !osInfo.Systemd {
			return errf("Автоустановка OpenLiteSpeed требует systemd")
		}
		if err := writeFile(marker, []byte("ITRUPANEL\n"), 0600); err != nil {
			return err
		}
		// Keep package post-install scripts from exposing the default listeners.
		if _, err := run("systemctl", "mask", "lsws.service", "lshttpd.service"); err != nil {
			return err
		}
		jobStage(job, "Подключение репозитория OpenLiteSpeed")
		if !olsRepoConfigured() {
			if err := liveCommand(job, 5*time.Minute, "bash", "-c", "set -euo pipefail; script=$(mktemp); trap 'rm -f \"$script\"' EXIT; curl -fsSL --connect-timeout 15 --max-time 120 --proto '=https' https://repo.litespeed.sh -o \"$script\"; bash \"$script\""); err != nil {
				// На RHEL скрипт сначала записывает файл репозитория, а затем скачивает список
				// пакетов по HTTP. Если открытый HTTP у хостера тормозит, продолжаем по HTTPS.
				if osInfo.Family == "debian" || !olsRepoConfigured() {
					return fmt.Errorf("Репозиторий OpenLiteSpeed: %w", err)
				}
				if job != nil {
					job.Write([]byte("Список пакетов по HTTP не загрузился — переключаю репозиторий LiteSpeed на HTTPS\n"))
				}
			}
		}
		// Только HTTPS: открытый HTTP у части хостеров замедляется до сотен байт в секунду
		if err := olsHTTPSRepo(); err != nil {
			return err
		}
		if osInfo.Family == "debian" {
			jobStage(job, "Обновление списка пакетов (HTTPS)")
			if err := liveCommand(job, 4*time.Minute, "apt-get", append(aptNetworkOptions(), "update")...); err != nil {
				return err
			}
		}
		jobStage(job, "Скачивание и установка OpenLiteSpeed")
		if err := installPackagesJob(job, "openlitespeed"); err != nil {
			return fmt.Errorf("Установка OpenLiteSpeed: %w", err)
		}
		if !cfg.Dry && !olsInstalled() {
			return errf("Пакет OpenLiteSpeed не содержит ожидаемый исполняемый файл")
		}
	}
	jobStage(job, "Проверка и запуск OpenLiteSpeed")
	if !olsManaged() {
		if previous := readFile(olsConfig); previous != "" && !fileExists(olsConfig+".before-itrupanel") {
			if err := writeFile(olsConfig+".before-itrupanel", []byte(previous), 0600); err != nil {
				return err
			}
		}
		if err := writeFile(olsConfig, []byte(olsGlobalConfig(nil)), 0644); err != nil {
			return err
		}
	}
	if err := repairOLSAccessLog(); err != nil {
		return err
	}
	if err := olsTest(); err != nil {
		return err
	}
	if osInfo.Systemd {
		if _, err := run("systemctl", "unmask", "lsws.service", "lshttpd.service"); err != nil {
			return err
		}
		olsRestoreAliasUnit()
		olsInstallUnit()
		if _, err := run("systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	return olsRunConfig(readFile(olsConfig))
}

// olsRunConfig запускает OpenLiteSpeed только когда в конфигурации есть сайты. Без единого
// virtualhost сервер завершается («No virtual host is available, stop server!»), а systemd
// перезапускает его каждые 5 секунд, поэтому без сайтов служба останавливается и отключается.
func olsRunConfig(conf string) error {
	if !strings.Contains(conf, "\nvirtualhost ") {
		service("stop", "lsws")
		if osInfo.Systemd {
			name := olsServiceName()
			run("systemctl", "disable", name)
			run("systemctl", "reset-failed", name)
		}
		return nil
	}
	if osInfo.Systemd {
		if _, err := run("systemctl", "enable", olsServiceName()); err != nil {
			return err
		}
	}
	return service("restart", "lsws")
}

// olsInstallUnit ставит юнит systemd из поставки OpenLiteSpeed, если пакет его не положил (Ubuntu 24.04,
// пакет 1.9.x): после этого служба называется lshttpd, а без юнита «systemctl enable» отвечает, что файла нет.
func olsInstallUnit() {
	if cfg.Dry || fileExists("/etc/systemd/system/lshttpd.service") || fileExists("/etc/systemd/system/lsws.service") {
		return
	}
	for _, dir := range []string{"/run/systemd/system", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system"} {
		if fileExists(dir+"/lshttpd.service") || fileExists(dir+"/lsws.service") {
			return
		}
	}
	if unit := readFile(olsRoot + "/admin/misc/lshttpd.service"); strings.Contains(unit, "lswsctrl") {
		writeFile("/etc/systemd/system/lshttpd.service", []byte(unit), 0644)
	}
}

// olsRestoreAliasUnit возвращает lsws.service как ссылку на lshttpd.service, если вместо
// псевдонима лежит отдельная копия юнита: две службы управляли бы одним сервером.
func olsRestoreAliasUnit() {
	const alias, unit = "/etc/systemd/system/lsws.service", "/etc/systemd/system/lshttpd.service"
	fi, err := os.Lstat(P(alias))
	if err != nil || !fi.Mode().IsRegular() || !fileExists(unit) || !strings.Contains(readFile(alias), "lswsctrl") {
		return
	}
	run("systemctl", "disable", "lsws.service")
	if os.Remove(P(alias)) == nil {
		os.Symlink("lshttpd.service", P(alias))
	}
}

// Call while holding mu. Pending replaces the stored site, or is appended when
// creating one. Suspended sites are unmapped so another vhost cannot serve them.
func olsSites(pending *Site, exclude string) []*Site {
	var list []*Site
	for _, s := range store.Sites {
		if s.Domain == exclude || pending != nil && s.Domain == pending.Domain {
			continue
		}
		if s.Mode == "php" && s.Backend == "openlitespeed" && !s.Suspended {
			list = append(list, s)
		}
	}
	if pending != nil && pending.Mode == "php" && pending.Backend == "openlitespeed" && !pending.Suspended {
		list = append(list, pending)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Domain < list[j].Domain })
	return list
}

func olsGlobalConfig(sites []*Site) string {
	var b strings.Builder
	fmt.Fprintf(&b, `%sserverName ITRUPANEL
user %s
group %s
httpdWorkers 2
autoRestart 1
mime conf/mime.properties
showVersionNumber 0
disableWebAdmin 1
useIpInProxyHeader 1
errorlog logs/error.log {
  logLevel WARN
  rollingSize 10M
}
%s
tuning {
  maxConnections 2000
  connTimeout 300
  maxKeepAliveReq 1000
  keepAliveTimeout 5
  maxReqBodySize 10240M
  maxDynRespSize 10240M
}
`, olsMarker, osInfo.WebUser, osInfo.WebUser, olsAccessLog)
	for _, s := range sites {
		fmt.Fprintf(&b, "virtualhost %s {\n  vhRoot %s/\n  configFile %s\n  allowSymbolLink 1\n  enableScript 1\n  restrained 0\n}\n", s.Domain, s.Root, olsVhostPath(s.Domain))
	}
	fmt.Fprintf(&b, "listener ITRUPANEL {\n  address 127.0.0.1:%s\n  secure 0\n", olsPort)
	for _, s := range sites {
		fmt.Fprintf(&b, "  map %s %s\n", s.Domain, strings.Join(siteNames(s), ","))
	}
	b.WriteString("}\n")
	return b.String()
}

func olsTest() error {
	out, err := run(olsRoot+"/bin/openlitespeed", "-t")
	if err != nil {
		return fmt.Errorf("Конфигурация OpenLiteSpeed: %w", err)
	}
	upper := strings.ToUpper(out)
	if strings.Contains(upper, "[ERROR]") || strings.Contains(upper, "[FATAL]") {
		return errf("Конфигурация OpenLiteSpeed: %s", out)
	}
	return nil
}

// applyOLS returns a rollback, used if the front nginx configuration fails.
// Files are restored before reloading the last working configuration.
func applyOLS(s *Site) (func() error, error) {
	noop := func() error { return nil }
	if s.Backend != "openlitespeed" && !fileExists(olsVhostPath(s.Domain)) {
		return noop, nil
	}
	if !olsManaged() {
		return noop, errf("OpenLiteSpeed ещё не настроен панелью")
	}
	path := olsVhostPath(s.Domain)
	prevMain, prevSite := readFile(olsConfig), readFile(path)
	rollback := func() error {
		if err := writeFile(olsConfig, []byte(prevMain), 0644); err != nil {
			return err
		}
		if prevSite == "" {
			os.Remove(P(path))
		} else if err := writeFile(path, []byte(prevSite), 0644); err != nil {
			return err
		}
		return olsRunConfig(prevMain)
	}
	fail := func(err error) (func() error, error) {
		if undo := rollback(); undo != nil {
			err = fmt.Errorf("%w; восстановление OpenLiteSpeed: %v", err, undo)
		}
		return noop, err
	}
	if s.Mode == "php" && s.Backend == "openlitespeed" {
		// OLS uses the existing per-site FPM pool, preserving PHP versions,
		// extensions, settings and execution under the site's system user.
		var buf bytes.Buffer
		if err := tplOLS.Execute(&buf, map[string]any{"Domain": s.Domain, "Root": s.Root, "Index": strings.Join(strings.Fields(s.Index), ","), "Sock": sockPath(s.Domain), "Log": userHome(s.User) + "/data/logs/" + s.Domain + ".ols.log"}); err != nil {
			return noop, err
		}
		if err := writeFile(path, buf.Bytes(), 0644); err != nil {
			return fail(err)
		}
	} else if err := os.Remove(P(path)); err != nil && !os.IsNotExist(err) {
		return noop, err
	}
	if err := writeFile(olsConfig, []byte(olsGlobalConfig(olsSites(s, ""))), 0644); err != nil {
		return fail(err)
	}
	if err := olsTest(); err != nil {
		return fail(err)
	}
	if err := olsRunConfig(readFile(olsConfig)); err != nil {
		return fail(err)
	}
	return rollback, nil
}

// Change only the exact vendor endpoint, retaining signed-by and signature checks.
func olsHTTPSRepo() error {
	for _, real := range olsRepoFiles() {
		b, err := os.ReadFile(real)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		s := strings.ReplaceAll(string(b), "http://rpms.litespeedtech.com/", "https://rpms.litespeedtech.com/")
		if s != string(b) {
			if err := os.WriteFile(real, []byte(s), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}
func aptNetworkOptions() []string {
	return []string{"-o", "Acquire::http::Timeout=20", "-o", "Acquire::https::Timeout=20", "-o", "Acquire::Retries=1", "-o", "DPkg::Lock::Timeout=60", "-o", "APT::Update::Error-Mode=any"}
}
func installPackagesJob(job *Job, pkgs ...string) error {
	if isBSD() {
		if err := bsdPkgCheck(pkgs...); err != nil {
			return err
		}
		return liveCommand(job, 30*time.Minute, "pkg", append([]string{"install", "-y"}, pkgs...)...)
	}
	if osInfo.Family == "rhel" {
		// Без необязательных зависимостей: меньше загрузка и память dnf на небольших серверах
		return liveCommand(job, 15*time.Minute, "dnf", append([]string{"--setopt=timeout=30", "--setopt=retries=4", "--setopt=install_weak_deps=False", "install", "-y"}, pkgs...)...)
	}
	args := append(aptNetworkOptions(), "install", "-y", "--no-install-recommends")
	return liveCommand(job, 15*time.Minute, "apt-get", append(args, pkgs...)...)
}

// olsRepoFiles — файлы источников пакетов: apt на Debian/Ubuntu, dnf на RHEL-системах.
func olsRepoFiles() []string {
	if osInfo.Family == "rhel" {
		paths, _ := filepath.Glob(P("/etc/yum.repos.d/*.repo"))
		return paths
	}
	paths, _ := filepath.Glob(P("/etc/apt/sources.list.d/*"))
	paths = append(paths, P("/etc/apt/sources.list"))
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, ".list") || strings.HasSuffix(p, ".sources") {
			out = append(out, p)
		}
	}
	return out
}

func olsRepoConfigured() bool {
	want := "rpms.litespeedtech.com/debian"
	if osInfo.Family == "rhel" {
		want = "rpms.litespeedtech.com/centos"
	}
	for _, p := range olsRepoFiles() {
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), want) {
			return true
		}
	}
	return false
}
