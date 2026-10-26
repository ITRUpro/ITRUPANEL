// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Обновление и выбор версий веб-серверов и PHP: nginx, Apache, OpenLiteSpeed, PHP-FPM каждой версии.
// Панель сама ничего не обновляет: она проверяет, есть ли в подключённых источниках более новый выпуск,
// сообщает об этом уведомлением и обновляет по кнопке (перед этим сохраняются настройки; при сбое настройки
// возвращаются). У nginx можно выбрать источник: пакеты системы, stable или mainline с nginx.org; у Apache на
// Debian и Ubuntu — пакеты системы или репозиторий Ondřej Surý.

const compBackupDir = "/var/backups/itrupanel/components"

// compInfo — строка списка компонентов.
type compInfo struct {
	ID        string `json:"id"` // nginx | apache | openlitespeed | php:8.3
	Title     string `json:"title"`
	Kind      string `json:"kind"`      // web | php
	Version   string `json:"version"`   // версия программы (например, 1.26.3)
	Package   string `json:"package"`   // версия пакета (с номером сборки)
	Candidate string `json:"candidate"` // версия пакета в источнике, если она новее установленной
	NewShown  string `json:"new"`       // то же без номера сборки, для показа
	Source    string `json:"source"`    // system | stable | mainline | ondrej | litespeed | remi
	Active    bool   `json:"active"`
	Switch    bool   `json:"switch"` // источник можно выбрать
	Job       string `json:"job,omitempty"`
}

// compChannel — источник версий компонента.
type compChannel struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version string `json:"version"` // последняя версия в источнике, если удалось узнать
	Current bool   `json:"current"`
	Note    string `json:"note,omitempty"`
}

var reUpstream = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)*)`)

// upstreamVersion — номер версии без эпохи и номера сборки: «1:1.26.3-3+deb13u1» → «1.26.3».
func upstreamVersion(pkgVer string) string {
	v := pkgVer
	if i := strings.Index(v, ":"); i >= 0 && i < 4 {
		v = v[i+1:]
	}
	if m := reUpstream.FindString(v); m != "" {
		return m
	}
	return ""
}

// numVerCmp сравнивает версии из чисел через точку: 1.30.6 > 1.30.10 неверно, 1.30.10 > 1.30.6 верно.
func numVerCmp(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---------- запросы к менеджеру пакетов ----------

func compInstalledPkg(pkg string) string {
	if cfg.Dry {
		return "1.0.0-1"
	}
	switch {
	case isBSD():
		out, err := run("pkg", "query", "%v", pkg)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(firstLine(out))
	case osInfo.Family == "rhel":
		out, err := run("rpm", "-q", "--qf", "%{VERSION}-%{RELEASE}", pkg)
		if err != nil || strings.Contains(out, "not installed") {
			return ""
		}
		return strings.TrimSpace(out)
	}
	out, err := run("dpkg-query", "-W", "-f", "${db:Status-Abbrev}|${Version}", pkg)
	if err != nil {
		return ""
	}
	st, v, _ := strings.Cut(strings.TrimSpace(out), "|")
	if !strings.HasPrefix(strings.TrimSpace(st), "ii") {
		return ""
	}
	return v
}

// compInstalledList — установленные пакеты по шаблонам имён (php8.3*, nginx*).
func compInstalledList(globs ...string) []string {
	if cfg.Dry {
		return nil
	}
	var names []string
	switch {
	case isBSD():
		out, _ := run("pkg", "query", "%n")
		for _, n := range strings.Fields(out) {
			for _, g := range globs {
				if ok, _ := filepath.Match(g, n); ok {
					names = append(names, n)
					break
				}
			}
		}
	case osInfo.Family == "rhel":
		for _, g := range globs {
			out, _ := run("rpm", "-qa", "--qf", "%{NAME}\n", g)
			for _, n := range strings.Fields(out) {
				if !strings.Contains(n, " ") {
					names = append(names, n)
				}
			}
		}
	default:
		for _, g := range globs {
			out, _ := run("dpkg-query", "-W", "-f", "${Package}|${db:Status-Abbrev}\n", g)
			for _, l := range strings.Split(out, "\n") {
				n, st, ok := strings.Cut(strings.TrimSpace(l), "|")
				if ok && strings.HasPrefix(strings.TrimSpace(st), "ii") {
					names = append(names, n)
				}
			}
		}
	}
	return dedup(names)
}

var reAptCandidate = regexp.MustCompile(`(?m)^\s*Candidate:\s*(\S+)`)

// compCandidatePkg — версия пакета, которую поставит менеджер пакетов из подключённых источников.
// Пусто, если нового нет или определить не удалось.
func compCandidatePkg(pkg, installed string) string {
	if cfg.Dry || installed == "" {
		return ""
	}
	switch {
	case isBSD():
		out, err := run("pkg", "rquery", "%v", pkg)
		cand := strings.TrimSpace(firstLine(out))
		if err != nil || cand == "" {
			return ""
		}
		if cmp, _ := run("pkg", "version", "-t", cand, installed); strings.TrimSpace(cmp) == ">" {
			return cand
		}
		return ""
	case osInfo.Family == "rhel":
		out, _ := run("dnf", "-q", "repoquery", "--upgrades", "--qf", "%{version}-%{release}\n", pkg)
		var best string
		for _, l := range strings.Fields(out) {
			if best == "" || numVerCmp(upstreamVersion(l), upstreamVersion(best)) >= 0 {
				best = l
			}
		}
		return best
	}
	out, err := run("apt-cache", "policy", pkg)
	if err != nil {
		return ""
	}
	m := reAptCandidate.FindStringSubmatch(out)
	if m == nil || m[1] == "(none)" || m[1] == installed {
		return ""
	}
	if _, err := run("dpkg", "--compare-versions", m[1], "gt", installed); err != nil {
		return ""
	}
	return m[1]
}

// aptLockWait ждёт, пока другой процесс (например, ночное обновление системы) отпустит блокировки apt:
// иначе наша команда сразу завершилась бы ошибкой «Could not get lock».
func aptLockWait(j *Job, limit time.Duration) {
	if cfg.Dry || isBSD() || osInfo.Family != "debian" {
		return
	}
	locks := []string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock", "/var/lib/apt/lists/lock", "/var/cache/apt/archives/lock"}
	busy := func() bool {
		for _, p := range locks {
			f, err := os.OpenFile(p, os.O_RDWR, 0)
			if err != nil {
				continue
			}
			l := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
			err = syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &l)
			f.Close()
			if err == nil && l.Type != syscall.F_UNLCK {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(limit)
	announced := false
	for busy() && time.Now().Before(deadline) {
		if !announced {
			jobStage(j, "Жду, пока система закончит другую операцию с пакетами")
			announced = true
		}
		time.Sleep(3 * time.Second)
	}
}

// compRefreshIndexes обновляет списки пакетов (раз в сутки или по кнопке «Проверить»).
func compRefreshIndexes(j *Job) error {
	if cfg.Dry {
		return nil
	}
	switch {
	case isBSD():
		return liveCommand(j, 10*time.Minute, "pkg", "update", "-q")
	case osInfo.Family == "rhel":
		return liveCommand(j, 10*time.Minute, "dnf", "-q", "makecache")
	}
	aptLockWait(j, 10*time.Minute)
	return liveCommand(j, 10*time.Minute, "apt-get", "update", "-q")
}

// compUpgradeCmd — команда обновления перечисленных пакетов до версии из подключённых источников.
func compUpgradeCmd(pkgs []string) (string, []string) {
	switch {
	case isBSD():
		return "pkg", append([]string{"upgrade", "-y"}, pkgs...)
	case osInfo.Family == "rhel":
		return "dnf", append([]string{"upgrade", "-y", "--best"}, pkgs...)
	}
	return "apt-get", append([]string{"install", "-y", "--only-upgrade", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef"}, pkgs...)
}

// ---------- описание компонентов ----------

type compDef struct {
	ID      string
	Title   string
	Kind    string
	Version func() string
	Main    string          // главный пакет: по нему сравниваются версии
	Pkgs    func() []string // пакеты, которые обновляются вместе
	Service string          // служба для перезапуска
	Config  []string        // каталоги настроек для резервной копии
	Test    func() error    // проверка конфигурации после обновления
	Present func() bool
	Source  func() string
	Switch  bool
}

func compNginxMain() string {
	if isBSD() && compInstalledPkg("nginx-devel") != "" {
		return "nginx-devel"
	}
	return "nginx"
}

func compDefs() []compDef {
	defs := []compDef{{
		ID: "nginx", Title: "nginx", Kind: "web", Version: nginxVersion, Main: compNginxMain(), Service: "nginx",
		Pkgs: func() []string {
			if isBSD() {
				return []string{compNginxMain()}
			}
			if osInfo.Family == "rhel" {
				return compInstalledList("nginx", "nginx-core", "nginx-filesystem", "nginx-all-modules", "nginx-mod-*")
			}
			return compInstalledList("nginx", "nginx-common", "nginx-core", "libnginx-mod-*", "nginx-module-*")
		},
		Config: []string{"/etc/nginx"},
		Test: func() error {
			_, err := run("nginx", "-t")
			return err
		},
		Present: func() bool { return have("nginx") },
		Source:  nginxSource,
		Switch:  true,
	}}
	if apacheInstalled() {
		main, svc := "apache2", apacheService()
		cfgDir := "/etc/apache2"
		switch {
		case isBSD():
			main, cfgDir = "apache24", bsdApacheDir
		case osInfo.Family == "rhel":
			main, cfgDir = "httpd", "/etc/httpd"
		}
		defs = append(defs, compDef{
			ID: "apache", Title: "Apache", Kind: "web", Version: apacheVersion, Main: main, Service: svc,
			Pkgs: func() []string {
				switch {
				case isBSD():
					return []string{"apache24"}
				case osInfo.Family == "rhel":
					return compInstalledList("httpd", "httpd-core", "httpd-tools", "httpd-filesystem", "httpd-manual", "mod_ssl", "mod_http2", "mod_proxy_html")
				}
				return compInstalledList("apache2", "apache2-bin", "apache2-data", "apache2-utils")
			},
			Config: []string{cfgDir},
			Test: func() error {
				_, err := apacheTest()
				return err
			},
			Present: func() bool { return true },
			Source:  apacheSource,
			Switch:  osInfo.Family == "debian",
		})
	}
	if olsInstalled() && !isBSD() {
		defs = append(defs, compDef{
			ID: "openlitespeed", Title: "OpenLiteSpeed", Kind: "web", Version: olsVersion, Main: "openlitespeed", Service: "lsws",
			Pkgs:    func() []string { return []string{"openlitespeed"} },
			Config:  []string{olsRoot + "/conf"},
			Test:    olsTest,
			Present: func() bool { return true },
			Source:  func() string { return "litespeed" },
		})
	}
	for _, v := range phpInstalled() {
		v := v
		pk := "php" + v + "*"
		main := "php" + v + "-fpm"
		switch {
		case isBSD():
			pk, main = "php"+vv(v)+"*", "php"+vv(v)
		case osInfo.Family == "rhel":
			pk, main = "php"+vv(v)+"-php*", "php"+vv(v)+"-php-fpm"
		}
		defs = append(defs, compDef{
			ID: "php:" + v, Title: "PHP " + v, Kind: "php", Main: main, Service: phpService(v),
			Version: func() string { return phpRelease(v) },
			Pkgs:    func() []string { return compInstalledList(pk) },
			Config:  []string{filepath.Dir(phpIni(v))},
			Test: func() error {
				_, err := run(phpFPMBin(v), "-t")
				return err
			},
			Present: func() bool { return true },
			Source:  func() string { return phpSource() },
		})
	}
	return defs
}

func compFind(id string) *compDef {
	for _, d := range compDefs() {
		if d.ID == id {
			d := d
			return &d
		}
	}
	return nil
}

func phpSource() string {
	switch {
	case osInfo.Family == "rhel":
		return "remi"
	case isBSD():
		return "system"
	}
	return "ondrej"
}

// ---------- текущий источник ----------

const (
	nginxAptList  = "/etc/apt/sources.list.d/nginx-itrupanel.list"
	nginxAptKey   = "/usr/share/keyrings/nginx-archive-keyring.asc"
	nginxAptPin   = "/etc/apt/preferences.d/99-nginx-itrupanel"
	nginxRepoFile = "/etc/yum.repos.d/nginx-itrupanel.repo"
)

func nginxSource() string {
	switch {
	case isBSD():
		if compInstalledPkg("nginx-devel") != "" {
			return "mainline"
		}
		return "stable"
	case osInfo.Family == "rhel":
		c := readFile(nginxRepoFile)
		if strings.Contains(c, "/mainline/") {
			return "mainline"
		}
		if c != "" {
			return "stable"
		}
	default:
		c := readFile(nginxAptList)
		if strings.Contains(c, "/mainline/") {
			return "mainline"
		}
		if c != "" {
			return "stable"
		}
	}
	return "system"
}

func apacheSource() string {
	if osInfo.Family != "debian" {
		return "system"
	}
	files, _ := filepath.Glob(P("/etc/apt/sources.list.d/*"))
	for _, f := range files {
		b := filepath.Base(f)
		if strings.Contains(b, "apache2") && (strings.Contains(b, "sury") || strings.Contains(b, "ondrej")) {
			return "ondrej"
		}
	}
	return "system"
}

// ---------- список ----------

var (
	compMu    sync.Mutex
	compCache []compInfo
	compAt    time.Time
)

func compList(force bool) []compInfo {
	compMu.Lock()
	if !force && compCache != nil && time.Since(compAt) < 2*time.Minute && !cfg.Dry {
		out := append([]compInfo{}, compCache...)
		compMu.Unlock()
		return out
	}
	compMu.Unlock()
	var out []compInfo
	for _, d := range compDefs() {
		if !d.Present() {
			continue
		}
		inst := compInstalledPkg(d.Main)
		row := compInfo{ID: d.ID, Title: d.Title, Kind: d.Kind, Package: inst, Source: d.Source(), Switch: d.Switch, Active: serviceActive(d.Service)}
		if d.Version != nil {
			row.Version = d.Version()
		}
		if row.Version == "" {
			row.Version = upstreamVersion(inst)
		}
		if cand := compCandidatePkg(d.Main, inst); cand != "" {
			row.Candidate, row.NewShown = cand, upstreamVersion(cand)
			// Та же версия программы, но новая сборка пакета (исправления безопасности): показываем сборку целиком
			if row.NewShown == "" || row.NewShown == upstreamVersion(inst) {
				row.NewShown = cand
				if i := strings.Index(cand, ":"); i >= 0 && i < 4 {
					row.NewShown = cand[i+1:]
				}
			}
		}
		if jobRunning("comp:" + d.ID) {
			row.Job = "running"
		}
		out = append(out, row)
	}
	compMu.Lock()
	compCache, compAt = out, time.Now()
	compMu.Unlock()
	return out
}

func compInvalidate() {
	compMu.Lock()
	compCache = nil
	compMu.Unlock()
	webVerMu.Lock()
	webVerData = nil
	webVerMu.Unlock()
	bsdPHPForget()
}

// ---------- копия настроек и откат ----------

func compBackup(id string, dirs []string) (string, error) {
	if cfg.Dry {
		return "", nil
	}
	if err := os.MkdirAll(P(compBackupDir), 0700); err != nil {
		return "", err
	}
	name := strings.NewReplacer(":", "-", "/", "-").Replace(id)
	path := fmt.Sprintf("%s/%s-%s.tgz", compBackupDir, name, time.Now().Format("20060102-150405"))
	args := []string{"-czf", path, "-C", "/"}
	for _, d := range dirs {
		if fileExists(d) {
			args = append(args, strings.TrimPrefix(d, "/"))
		}
	}
	if len(args) == 4 {
		return "", nil
	}
	if out, err := run("tar", args...); err != nil {
		return "", errf("Не удалось сохранить настройки: %s", firstLine(out))
	}
	// копии настроек хранятся не больше 10 на компонент
	old, _ := filepath.Glob(P(fmt.Sprintf("%s/%s-*.tgz", compBackupDir, name)))
	sort.Strings(old)
	for len(old) > 10 {
		os.Remove(old[0])
		old = old[1:]
	}
	return path, nil
}

func compRestore(path string) error {
	if path == "" || cfg.Dry {
		return nil
	}
	_, err := run("tar", "-xzf", path, "-C", "/")
	return err
}

// compVerify проверяет компонент после обновления: конфигурация, запуск службы, ответ nginx.
func compVerify(d *compDef) error {
	if d.Test != nil {
		if err := d.Test(); err != nil {
			return errf("Проверка конфигурации после обновления не прошла: %s", firstLine(err.Error()))
		}
	}
	if err := service("restart", d.Service); err != nil {
		return errf("Служба %s не запустилась после обновления: %s", d.Title, firstLine(err.Error()))
	}
	if !cfg.Dry {
		time.Sleep(time.Second)
		if !serviceActive(d.Service) {
			return errf("Служба %s не работает после обновления", d.Title)
		}
		if d.ID == "nginx" {
			if code, _ := run("curl", "-s", "-o", "/dev/null", "-m", "10", "-w", "%{http_code}", "http://127.0.0.1/"); strings.TrimSpace(code) == "000" || code == "" {
				return errf("nginx запущен, но не отвечает на порту 80")
			}
		}
	}
	return nil
}

// compUpdate обновляет компонент до версии из его текущего источника.
func compUpdate(j *Job, id string) error {
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	d := compFind(id)
	if d == nil {
		return errf("Компонент не найден")
	}
	pkgs := d.Pkgs()
	if len(pkgs) == 0 {
		return errf("Пакеты %s не найдены", d.Title)
	}
	if isBSD() && d.Kind == "php" {
		if err := bsdCheckBinary(bsdFPMBin, "-v"); err != nil {
			return err
		}
	}
	before := compInstalledPkg(d.Main)
	jobStage(j, "Сохранение настроек "+d.Title)
	backup, err := compBackup(d.ID, d.Config)
	if err != nil {
		return err
	}
	if err := compRefreshIndexes(j); err != nil {
		jobStage(j, "Списки пакетов обновить не удалось; используются текущие")
	}
	jobStage(j, "Обновление "+d.Title)
	name, args := compUpgradeCmd(pkgs)
	aptLockWait(j, 10*time.Minute)
	if err := liveCommand(j, 40*time.Minute, name, args...); err != nil {
		compRestore(backup)
		return err
	}
	after := compInstalledPkg(d.Main)
	if before == after {
		return errf("Новой версии %s в источниках нет", d.Title)
	}
	compPostFix(d)
	jobStage(j, "Проверка и перезапуск "+d.Title)
	if err := compVerify(d); err != nil {
		jobStage(j, "Возвращаю настройки: "+firstLine(err.Error()))
		compRestore(backup)
		compPostFix(d)
		service("restart", d.Service)
		return err
	}
	compInvalidate()
	jobStage(j, fmt.Sprintf("%s обновлён: %s → %s", d.Title, upstreamVersion(before), upstreamVersion(after)))
	return nil
}

// compPostFix приводит настройки в порядок после замены пакета.
func compPostFix(d *compDef) {
	switch d.ID {
	case "nginx":
		nginxNormalize()
	case "openlitespeed":
	}
	if d.Kind == "php" {
		bsdEnsurePHPIniIfBSD()
	}
}

func bsdEnsurePHPIniIfBSD() {
	if isBSD() {
		bsdEnsurePHPIni()
	}
}

// ---------- nginx: настройки после смены пакета ----------

var reNginxUser = regexp.MustCompile(`(?m)^[ \t]*user[ \t]+[^;]+;`)

// nginxConfSetUser задаёт пользователя рабочих процессов nginx. У пакетов nginx.org по умолчанию «user nginx»,
// а файлы сайтов и сокеты PHP-FPM принадлежат пользователю веб-сервера системы (www-data, www): без правки сайты
// отвечали бы 403.
func nginxConfSetUser(conf, user string) string {
	line := "user " + user + ";"
	if reNginxUser.MatchString(conf) {
		done := false
		return reNginxUser.ReplaceAllStringFunc(conf, func(string) string {
			if done {
				return ""
			}
			done = true
			return line
		})
	}
	return line + "\n" + conf
}

func nginxNormalize() {
	if cfg.Dry {
		return
	}
	conf := readFile("/etc/nginx/nginx.conf")
	if conf != "" {
		if fixed := nginxConfSetUser(conf, osInfo.WebUser); fixed != conf {
			writeFile("/etc/nginx/nginx.conf", []byte(fixed), 0644)
		}
	}
	// В пакете nginx.org есть conf.d/default.conf со страницей «Welcome to nginx»: сервер по умолчанию задаёт панель
	def := "/etc/nginx/conf.d/default.conf"
	if c := readFile(def); strings.Contains(c, "Welcome") || strings.Contains(c, "/usr/share/nginx/html") {
		os.Rename(P(def), P(def+".itrupanel-disabled"))
	}
	os.MkdirAll(P("/var/log/nginx"), 0755)
}

// ---------- проверка обновлений и уведомления ----------

var compLast = map[string]string{}

// compCheckNotify обновляет списки пакетов и сообщает уведомлением о каждой новой версии один раз.
func compCheckNotify(j *Job) {
	if cfg.Dry {
		return
	}
	compRefreshIndexes(j)
	rows := compList(true)
	mu.Lock()
	defer mu.Unlock()
	changed := false
	for _, r := range rows {
		if r.Candidate == "" {
			continue
		}
		key := "comp-" + r.ID + "-" + r.Candidate
		before := len(store.Notices)
		addNotice("info", key, fmt.Sprintf("Доступно обновление %s: %s → %s. Раздел «Веб-серверы».", r.Title, r.Version, r.NewShown))
		changed = changed || len(store.Notices) != before
	}
	if changed {
		save()
	}
}

var compNotifyDay string

// compAutoCheck — раз в сутки (ночью) проверяет обновления компонентов.
func compAutoCheck(now time.Time) {
	day := now.Format("2006-01-02")
	if cfg.Dry || compNotifyDay == day || now.Hour() < 4 || jobRunning("comp:check") {
		return
	}
	compNotifyDay = day
	startJob("comp:check", "Проверка обновлений веб-серверов и PHP", "admin", func(j *Job) (any, error) {
		compCheckNotify(j)
		return nil, nil
	})
}

// compChannelsFor — источники версий компонента и последние версии в них.
func compChannelsFor(id string) ([]compChannel, error) {
	d := compFind(id)
	if d == nil {
		return nil, errf("Компонент не найден")
	}
	cur := d.Source()
	switch id {
	case "nginx":
		return nginxChannels(cur), nil
	case "apache":
		return apacheChannels(cur), nil
	}
	return []compChannel{{ID: cur, Title: "Источник по умолчанию", Current: true}}, nil
}
