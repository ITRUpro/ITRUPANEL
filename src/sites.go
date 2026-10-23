// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/net/idna"
)

var (
	tplSite = template.Must(template.ParseFS(tplFS, "templates/nginx-site.conf"))
	tplPool = template.Must(template.ParseFS(tplFS, "templates/fpm-pool.conf"))
)

const (
	vhostDir  = "/etc/nginx/itrupanel/vhosts"
	customDir = "/etc/nginx/itrupanel/custom"
	acmeRoot  = "/var/www/itrupanel-acme"
	sockDir   = "/run/itrupanel-php"
)

func vhostPath(d string) string  { return vhostDir + "/" + d + ".conf" }
func customPath(d string) string { return customDir + "/" + d + ".conf" }
func poolName(d string) string   { return "itru_" + strings.NewReplacer(".", "_", "-", "_").Replace(d) }
func sockPath(d string) string   { return sockDir + "/" + d + ".sock" }
func userHome(u string) string   { return "/var/www/" + u }

// ---------- версия nginx (для синтаксиса http2) ----------

var (
	nginxOnce    sync.Once
	nginxOldHTTP bool
	nginxVer     [3]int
)

func nginxOldHTTP2() bool {
	nginxOnce.Do(func() {
		if cfg.Dry {
			return
		}
		out, _ := exec.Command("nginx", "-v").CombinedOutput()
		m := regexp.MustCompile(`nginx/(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(string(out))
		if m != nil {
			var a, b, c int
			fmt.Sscan(m[1], &a)
			fmt.Sscan(m[2], &b)
			fmt.Sscan(m[3], &c)
			nginxOldHTTP = a == 1 && (b < 25 || (b == 25 && c < 1))
			nginxVer = [3]int{a, b, c}
		}
	})
	return nginxOldHTTP
}

// nginxVersionAtLeast: неизвестная версия (демо, тесты) считается новой.
func nginxVersionAtLeast(a, b, c int) bool {
	nginxOldHTTP2()
	v := nginxVer
	if v == [3]int{} {
		return true
	}
	if v[0] != a {
		return v[0] > a
	}
	if v[1] != b {
		return v[1] > b
	}
	return v[2] >= c
}

func nginxTestReload() error {
	if _, err := run("nginx", "-t"); err != nil {
		return fmt.Errorf("nginx -t: %w", err)
	}
	return service("reload", "nginx")
}

// ---------- системные пользователи сайтов ----------

func ensureUser(name string) (*SiteUser, error) {
	if u := findUser(name); u != nil {
		return u, nil
	}
	if !reName.MatchString(name) {
		return nil, errf("Имя пользователя: латиница в нижнем регистре, цифры и _, до 32 символов, начинается с буквы")
	}
	home := userHome(name)
	if _, err := run("id", name); err != nil || cfg.Dry {
		// Подчинённые UID/GID (для rootless-контейнеров) пользователю сайта не нужны; без них
		// useradd не падает, когда диапазон в /etc/subuid уже занят (AlmaLinux 10 и др.).
		if _, err := run("useradd", "-K", "SUB_UID_COUNT=0", "-K", "SUB_GID_COUNT=0", "-m", "-d", home, "-s", "/bin/bash", "-U", name); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{"data", "data/www", "data/logs", "data/tmp", "data/backup"} {
		os.MkdirAll(P(home+"/"+d), 0750)
	}
	run("chown", "-R", name+":"+name, home)
	run("chmod", "750", home, home+"/data", home+"/data/www")
	run("chmod", "700", home+"/data/tmp", home+"/data/backup")
	// веб-сервер должен читать файлы сайтов
	run("usermod", "-aG", name, osInfo.WebUser)
	if osInfo.Family == "rhel" {
		run("restorecon", "-R", home)
	}
	u := &SiteUser{Name: name, Home: home, Created: time.Now()}
	store.Users = append(store.Users, u)
	return u, nil
}

func userFromDomain(d string) string {
	d = strings.TrimPrefix(d, "www.")
	if p := strings.Index(d, "."); p > 0 {
		d = d[:p]
	}
	if strings.HasPrefix(d, "xn--") {
		d = "site"
	}
	d = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(d, "")
	if d == "" || d[0] < 'a' || d[0] > 'z' {
		d = "u" + d
	}
	if len(d) > 16 {
		d = d[:16]
	}
	base := d
	for i := 2; ; i++ {
		taken := findUser(d) != nil
		if !taken && !cfg.Dry {
			taken = exec.Command("id", d).Run() == nil
		}
		if !taken {
			return d
		}
		d = fmt.Sprintf("%s%d", base, i)
	}
}

// ---------- генерация конфигурации сайта ----------

func toASCII(d string) (string, error) {
	d = cleanDomain(d)
	a, err := idna.Lookup.ToASCII(d)
	if err != nil || !reDomain.MatchString(d) {
		return "", errf("Неверное имя домена: %s", d)
	}
	return a, nil
}

func siteNames(s *Site) []string {
	names := []string{s.Domain}
	names = append(names, s.Aliases...)
	return names
}

// Render only nginx: editing custom rules must not reconfigure PHP or backends.
func renderSiteNginx(s *Site, u *SiteUser, custom string) ([]byte, error) {
	if err := securityCustomCheck(s.Security, custom); err != nil {
		return nil, err
	}
	pre, rules, securityLog, err := securityNginx(s)
	if err != nil {
		return nil, err
	}
	tplName, tplRules := siteTemplateRules(s)
	if tplRules != "" {
		if err := siteTemplateCompat(tplName, tplRules, s); err != nil {
			return nil, err
		}
	}
	if tplRules != "" && nginxCustomRootLocation(tplRules) && nginxCustomRootLocation(custom) {
		return nil, errf("Шаблон «%s» и свои правила сайта оба задают location /. Уберите один из них", tplName)
	}
	data := map[string]any{
		"TemplateName": tplName, "TemplateRules": tplRules,
		"GuardHTTP": guardHTTP(s), "GuardServer": guardServer(s), "CacheHTTP": cacheHTTP(s), "CacheOn": siteCacheOn(s), "CacheFastCGI": cacheDirectives(s, "fastcgi"), "CacheProxy": cacheDirectives(s, "proxy"),
		"SecurityHTTP": pre, "SecurityServer": rules, "SecurityLog": securityLog,
		"Domain": s.Domain, "Names": strings.Join(siteNames(s), " "), "Root": s.Root, "Index": s.Index,
		"CustomRootLocation": nginxCustomRootLocation(custom) || nginxCustomRootLocation(tplRules), "Custom": customPath(s.Domain), "AccessLog": u.Home + "/data/logs/" + s.Domain + ".access.log",
		"ErrorLog": u.Home + "/data/logs/" + s.Domain + ".error.log", "MaxBody": s.MaxBody,
		"Suspended": s.Suspended, "RedirectHTTPS": s.RedirectHTTPS && s.SSL != "", "WWWRedirect": s.WWWRedirect,
		"Gzip": s.Gzip, "Mode": s.Mode, "ProxyTo": s.ProxyTo, "Expires": s.StaticExpires,
		"PHP": s.Mode == "php" && s.PHP != "", "Sock": sockPath(s.Domain), "OldHTTP2": nginxOldHTTP2(),
		"Default":    store.Settings.DefaultSite == s.Domain,
		"Apache":     s.Mode == "php" && s.Backend == "apache",
		"WebBackend": s.Mode == "php" && (s.Backend == "apache" || s.Backend == "openlitespeed"), "BackendPort": apachePort,
	}
	if s.Suspended {
		page, err := tplFS.ReadFile("templates/site-disabled.html")
		if err != nil {
			return nil, err
		}
		// nginx return bodies are quoted config strings, not HTML templates.
		// Escape variable markers too so page content cannot become nginx code.
		data["SuspendedPage"] = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "\n", "\\n", "\r", "").Replace(string(page))
	}
	if s.Backend == "openlitespeed" {
		data["BackendPort"] = olsPort
	}
	if s.Mode == "php" && s.TryIndex {
		data["Fallback"] = "/index.php?$args"
	}
	if c := findCert(s.SSL); c != nil {
		data["SSL"], data["Crt"], data["Key"] = true, c.Crt, c.Key
	} else {
		data["SSL"] = false
	}
	if store.Settings.DefaultSite == s.Domain {
		names := siteNames(s)
		for i, n := range names {
			names[i] = regexp.QuoteMeta(n)
		}
		data["DefaultHosts"] = "^(" + strings.Join(names, "|") + ")$"
		data["DefaultURL"] = "http://" + s.Domain
		if data["SSL"] == true {
			data["DefaultURL"] = "https://" + s.Domain
		}
	}
	var buf bytes.Buffer
	if err := tplSite.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applySite пишет конфиги nginx и php-fpm. При ошибке nginx -t возвращает прежний конфиг. Вызывать под mu.
func applySite(s *Site, oldPHP string) (resultErr error) {
	u := findUser(s.User)
	if u == nil {
		return errf("Нет пользователя %s", s.User)
	}
	// Switching an OLS site also changes FPM/Apache. Restore these files if any
	// later validation fails, rather than leaving the previous backend broken.
	if s.Backend == "openlitespeed" || fileExists(olsVhostPath(s.Domain)) {
		rollback, err := snapshotSiteRuntime(s, oldPHP)
		if err != nil {
			return err
		}
		defer func() {
			if resultErr != nil {
				if err := rollback(); err != nil {
					resultErr = fmt.Errorf("%w; восстановление сайта: %v", resultErr, err)
				}
			}
		}()
	}
	custom, err := os.ReadFile(P(customPath(s.Domain)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	ensureSiteCache(s)
	if err := writeSiteAuth(s); err != nil {
		return err
	}
	if err := prepareSecurityLogs(s); err != nil {
		return err
	}
	rendered, err := renderSiteNginx(s, u, string(custom))
	if err != nil {
		return err
	}

	if !fileExists(customPath(s.Domain)) {
		writeFile(customPath(s.Domain), []byte("# Свои правила nginx для "+s.Domain+" (подключаются внутрь server {})\n"), 0644)
	}

	// PHP-FPM
	if oldPHP != "" && oldPHP != s.PHP {
		os.Remove(P(phpPoolDir(oldPHP) + "/" + poolName(s.Domain) + ".conf"))
		phpExtRestart(oldPHP)
	}
	if s.Mode == "php" && s.PHP != "" {
		if !phpIsInstalled(s.PHP) {
			return errf("PHP %s не установлен (раздел PHP)", s.PHP)
		}
		pb, err := renderPHPPool(s, u)
		if err != nil {
			return err
		}
		os.MkdirAll(P(sockDir), 0755)
		poolFile := phpPoolDir(s.PHP) + "/" + poolName(s.Domain) + ".conf"
		if readFile(poolFile) != string(pb) {
			if err := writeFile(poolFile, pb, 0644); err != nil {
				return err
			}
			if err := phpExtRestart(s.PHP); err != nil {
				return err
			}
		}
	} else if oldPHP != "" {
		os.Remove(P(phpPoolDir(oldPHP) + "/" + poolName(s.Domain) + ".conf"))
		phpExtRestart(oldPHP)
	}

	rollbackOLS, err := applyOLS(s)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			if err := rollbackOLS(); err != nil {
				resultErr = fmt.Errorf("%w; откат: %v", resultErr, err)
			}
		}
	}()
	if err := applyApache(s, u); err != nil {
		return err
	}
	prev := readFile(vhostPath(s.Domain))
	if err := writeFile(vhostPath(s.Domain), rendered, 0644); err != nil {
		return err
	}
	if err := nginxTestReload(); err != nil {
		if prev != "" {
			writeFile(vhostPath(s.Domain), []byte(prev), 0644)
		} else {
			os.Remove(P(vhostPath(s.Domain)))
		}
		return err
	}
	return nil
}

const placeholderHTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>%s</title>
<style>body{font:16px/1.5 system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f2f5fc;color:#1b2540}div{text-align:center}h1{font-weight:600}</style>
<div><h1>%s</h1><p>Сайт создан. Загрузите файлы в папку сайта.</p></div></html>`

type siteIn struct {
	Domain        string `json:"domain"`
	Aliases       string `json:"aliases"`
	User          string `json:"user"`
	Root          string `json:"root"`
	Mode          string `json:"mode"`
	Backend       string `json:"backend"`
	PHP           string `json:"php"`
	Index         string `json:"index"`
	RedirectHTTPS bool   `json:"redirect_https"`
	WWWRedirect   string `json:"www_redirect"`
	Gzip          bool   `json:"gzip"`
	StaticExpires string `json:"static_expires"`
	MaxBody       int    `json:"max_body"`
	ProxyTo       string `json:"proxy_to"`
	TryIndex      bool   `json:"try_index"`
	Template      string `json:"template"`
	CacheTTL      int    `json:"cache_ttl"`
	PHPProfile    string `json:"php_profile"`
	GuardHeaders  bool   `json:"guard_headers"`
	GuardHotlink  bool   `json:"guard_hotlink"`
	GuardAdminIPs string `json:"guard_admin_ips"`
	AuthUser      string `json:"auth_user"`
	AuthPassword  string `json:"auth_password"`
	PHPMemory     int    `json:"php_memory"`
	PHPMaxExec    int    `json:"php_max_exec"`
	Kind          string `json:"kind"` // тип нового сайта: см. site_kinds.go
	SSL           string `json:"ssl"`
	AddWWW        bool   `json:"add_www"`
	LE            bool   `json:"le"`
	AutoSSL       bool   `json:"auto_ssl"`
	CreateZone    bool   `json:"create_zone"`
	Email         string `json:"email"`
	Edit          bool   `json:"edit"`
}

var siteSaveMu sync.Mutex

func createOrUpdateSite(in siteIn) (*Site, error) { return createSiteJob(in, nil) }
func createSiteJob(in siteIn, job *Job) (*Site, error) {
	jobStage(job, "Ожидание других операций с сайтами")
	siteSaveMu.Lock()
	defer siteSaveMu.Unlock()
	jobStage(job, "Проверка домена и параметров сайта")
	d, err := toASCII(in.Domain)
	if err != nil {
		return nil, err
	}
	if !in.Edit {
		if err := applySiteKind(&in); err != nil {
			return nil, err
		}
	}
	var aliases []string
	for _, a := range strings.FieldsFunc(in.Aliases, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' }) {
		aa, err := toASCII(a)
		if err != nil {
			return nil, err
		}
		if aa != d {
			aliases = append(aliases, aa)
		}
	}
	// The www shortcut belongs to creation only. Older forms submitted its
	// hidden default even on edits, resurrecting aliases the user removed.
	if !in.Edit && in.AddWWW && !strings.HasPrefix(d, "www.") {
		aliases = append([]string{"www." + d}, aliases...)
	}
	aliases = dedup(aliases)
	switch in.Mode {
	case "", "php":
		in.Mode = "php"
	case "static":
		in.PHP = ""
	case "proxy":
		if !regexp.MustCompile(`^https?://[a-zA-Z0-9.\-\[\]:]+(/[^\s;{}]*)?$`).MatchString(in.ProxyTo) {
			return nil, errf("Адрес проксирования вида http://127.0.0.1:3000")
		}
	default:
		return nil, errf("Неверный режим сайта")
	}
	if in.MaxBody <= 0 || in.MaxBody > 10240 {
		in.MaxBody = 64
	}
	if in.Index == "" || !regexp.MustCompile(`^[a-zA-Z0-9_. -]+$`).MatchString(in.Index) {
		in.Index = "index.php index.html index.htm"
	}
	if in.StaticExpires != "" && !regexp.MustCompile(`^\d{1,4}[hdmy]$`).MatchString(in.StaticExpires) {
		return nil, errf("Срок кэша статики вида 30d")
	}
	if in.WWWRedirect != "" && in.WWWRedirect != "to_www" && in.WWWRedirect != "from_www" {
		in.WWWRedirect = ""
	}
	if !validCacheTTL(in.CacheTTL) || (in.Mode == "static" && in.CacheTTL != 0) {
		in.CacheTTL = 0
	}
	if err := validatePHPSiteSettings(&in); err != nil {
		return nil, err
	}
	var prevGuard SiteGuard
	mu.Lock()
	if old := findSite(d); old != nil {
		prevGuard = old.Guard
	}
	mu.Unlock()
	guard, err := guardFromInput(in, prevGuard)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	defer mu.Unlock()
	s := findSite(d)
	for _, name := range append([]string{d}, aliases...) {
		if strings.Contains(readFile(vhostDir+"/000-itrupanel-mail-"+name+".conf"), "server_name "+name+";") {
			return nil, errf("Имя %s используется для SSL почты. Выберите другое имя для сайта", name)
		}
	}
	if store.Mail.Webmail.Ready {
		for _, name := range append([]string{d}, aliases...) {
			if name == store.Mail.Webmail.Hostname {
				return nil, errf("Имя %s занято Roundcube", name)
			}
		}
	}
	if fileExists(vhostDir + "/000-itrupanel-address.conf") {
		for _, name := range append([]string{d}, aliases...) {
			if name == store.Settings.PanelDomain {
				return nil, errf("Имя %s назначено самой панели. Используйте другое имя для сайта", name)
			}
		}
	}

	if in.Edit && s == nil {
		return nil, errf("Сайт %s не найден", d)
	}
	if !in.Edit && s != nil {
		return nil, errf("Сайт %s уже есть", d)
	}
	for _, o := range store.Sites {
		if o.Domain == d {
			continue
		}
		for _, n := range siteNames(o) {
			for _, m := range append([]string{d}, aliases...) {
				if n == m {
					return nil, errf("Имя %s уже занято сайтом %s", m, o.Domain)
				}
			}
		}
	}
	if in.Mode == "php" && in.PHP == "" {
		in.PHP = defaultPHPLocked()
		if in.PHP == "" {
			return nil, errf("PHP не установлен. Установите версию в разделе PHP или выберите режим «Без PHP»")
		}
	}
	if in.SSL != "" && findCert(in.SSL) == nil {
		return nil, errf("Нет сертификата %s", in.SSL)
	}
	if in.Mode != "php" {
		in.Backend = ""
	}
	if in.Backend != "" && in.Backend != "apache" && in.Backend != "openlitespeed" {
		return nil, errf("Неизвестный веб-сервер")
	}
	if in.Backend == "openlitespeed" {
		mu.Unlock()
		err := ensureOLSJob(job)
		mu.Lock()
		if err != nil {
			return nil, err
		}
	}
	if in.Backend == "apache" && !apacheReady() {
		mu.Unlock()
		jobStage(job, "Установка и настройка Apache")
		err := ensureApache()
		mu.Lock()
		if err != nil {
			return nil, err
		}
	}
	// выбранной версии PHP нет — ставим её (несколько минут), потом создаём сайт
	if in.Mode == "php" && !phpIsInstalled(in.PHP) {
		known := false
		for _, v := range phpAll {
			known = known || v == in.PHP
		}
		if !known {
			return nil, errf("Неизвестная версия PHP %s", in.PHP)
		}
		mu.Unlock()
		jobStage(job, "Установка PHP "+in.PHP+" (может занять несколько минут)")
		err := phpInstall(in.PHP)
		mu.Lock()
		if err != nil {
			return nil, errf("Не удалось установить PHP %s: %s", in.PHP, firstLine(err.Error()))
		}
	}

	jobStage(job, "Создание каталогов и настройка сайта")
	oldPHP := ""
	isNew := s == nil
	var prev Site
	if !isNew {
		prev = *s
	}
	if isNew {
		if in.User == "" {
			in.User = userFromDomain(d)
		}
		u, err := ensureUser(in.User)
		if err != nil {
			return nil, err
		}
		root := u.Home + "/data/www/" + d
		if k, ok := siteKindByID(in.Kind); ok && k.RootSub != "" && in.Root == "" {
			root += "/" + k.RootSub
		}
		if in.Root != "" {
			r, ok := safeUnder(u.Home, in.Root)
			if !ok {
				return nil, errf("Папка сайта должна быть внутри %s", u.Home)
			}
			root = r
		}
		s = &Site{Domain: d, User: in.User, Root: root, Created: time.Now()}
		os.MkdirAll(P(root), 0750)
		if entries, _ := os.ReadDir(P(root)); len(entries) == 0 {
			title := in.Domain
			os.WriteFile(P(root+"/index.html"), []byte(fmt.Sprintf(placeholderHTML, title, title)), 0644)
		}
		run("chown", "-R", in.User+":"+in.User, root)
		// Корень в подпапке (public у Laravel): папка сайта, созданная вместе с ней, тоже принадлежит владельцу сайта,
		// иначе веб-сервер не сможет в неё войти.
		if base := u.Home + "/data/www/" + d; root != base && strings.HasPrefix(root, base+"/") {
			run("chown", in.User+":"+in.User, base)
		}
	} else {
		oldPHP = s.PHP
		if s.Mode != "php" {
			oldPHP = ""
		}
		if in.Root != "" && in.Root != s.Root {
			u := findUser(s.User)
			r, ok := safeUnder(u.Home, in.Root)
			if !ok {
				return nil, errf("Папка сайта должна быть внутри %s", u.Home)
			}
			os.MkdirAll(P(r), 0750)
			run("chown", s.User+":"+s.User, r)
			s.Root = r
		}
	}
	if in.Backend == "openlitespeed" && strings.ContainsAny(s.Root, " \t\r\n{}#\"'\\$") {
		if !isNew {
			*s = prev
		}
		return nil, errf("Путь OpenLiteSpeed не должен содержать пробелы и специальные символы конфигурации")
	}
	s.Aliases, s.Mode, s.PHP, s.Index, s.Backend = aliases, in.Mode, in.PHP, in.Index, in.Backend
	s.RedirectHTTPS, s.WWWRedirect, s.Gzip, s.StaticExpires = in.RedirectHTTPS, in.WWWRedirect, in.Gzip, in.StaticExpires
	s.MaxBody, s.ProxyTo, s.TryIndex, s.SSL, s.AutoSSL = in.MaxBody, in.ProxyTo, in.TryIndex, in.SSL, in.AutoSSL
	s.Template = in.Template
	s.CacheTTL = in.CacheTTL
	s.PHPProfile, s.PHPMemory, s.PHPMaxExec = in.PHPProfile, in.PHPMemory, in.PHPMaxExec
	s.Guard = guard
	jobStage(job, "Проверка конфигураций и перезапуск веб-сервера")
	if err := applySite(s, oldPHP); err != nil {
		if isNew {
			os.Remove(P(vhostPath(d)))
			if s.PHP != "" {
				os.Remove(P(phpPoolDir(s.PHP) + "/" + poolName(d) + ".conf"))
			}
		} else {
			*s = prev
		}
		return nil, err
	}
	if !isNew && prev.CacheTTL > 0 && s.CacheTTL == 0 {
		removeSiteCache(d)
	}
	if isNew {
		store.Sites = append(store.Sites, s)
		dnsForSite(s.Domain, s.Aliases, in.CreateZone)
	} else {
		dnsForSite(s.Domain, s.Aliases, false)
	}
	save()
	return s, nil
}

func defaultPHPLocked() string {
	mu.Unlock()
	defer mu.Lock()
	return defaultPHP()
}

func deleteSite(domain string, removeFiles bool) error {
	mu.Lock()
	defer mu.Unlock()
	s := findSite(domain)
	if s == nil {
		return errf("Сайт не найден")
	}
	removed := *s
	removed.Backend, removed.Mode = "", "static"
	if _, err := applyOLS(&removed); err != nil {
		return err
	}
	os.Remove(P(vhostPath(domain)))
	if store.Settings.DefaultSite == domain {
		// Удалённый сайт больше не может быть сайтом по умолчанию: снова страница «Сайт не найден».
		store.Settings.DefaultSite = ""
		ensureNginxDefault()
	}
	if fileExists(apacheVhostPath(domain)) {
		os.Remove(P(apacheVhostPath(domain)))
		service("reload", apacheService())
	}
	if s.PHP != "" {
		os.Remove(P(phpPoolDir(s.PHP) + "/" + poolName(domain) + ".conf"))
		phpExtRestart(s.PHP)
	}
	nginxTestReload()
	if removeFiles {
		u := findUser(s.User)
		if u != nil && strings.HasPrefix(s.Root, u.Home+"/data/www/") {
			os.RemoveAll(P(s.Root))
		}
	}
	os.Remove(P(customPath(domain)))
	os.Remove(P(siteAuthFile(domain)))
	removeSiteCache(domain)
	var keep []*Site
	for _, x := range store.Sites {
		if x != s {
			keep = append(keep, x)
		}
	}
	store.Sites = keep
	save()
	return nil
}

func dirSize(path string) int64 {
	if !cfg.Dry {
		out, err := exec.Command("du", "-sb", path).Output()
		if err == nil {
			var n int64
			fmt.Sscan(string(out), &n)
			return n
		}
	}
	var n int64
	filepath.Walk(P(path), func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

func registerSites() {
	registerSiteTemplates()
	registerSiteCache()
	apiOp("sites.start", "Сайт: запуск сохранения", func(q *Req) (any, error) {
		var in siteIn
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		d, err := toASCII(in.Domain)
		if err != nil {
			return nil, err
		}
		in.Domain = d
		id := startJob("site:"+d, "Сайт "+d, q.S.User, func(j *Job) (any, error) {
			_, err := createSiteJob(in, j)
			if err != nil {
				return nil, err
			}
			if in.Email != "" && reEmail.MatchString(in.Email) {
				mu.Lock()
				store.Settings.Email = in.Email
				save()
				mu.Unlock()
			}
			// Certificate issuance is an independent background task. Saving the site
			// must not wait for DNS or ACME, and never hides a successfully saved site.
			if in.AutoSSL {
				startJob("ssl:"+d, "SSL для "+d, j.Owner, func(sslJob *Job) (any, error) {
					sslJob.progress("Проверка DNS и выпуск сертификата")
					msg, err := autoSSLSite(d, true)
					return map[string]string{"info": msg}, err
				})
			}
			return map[string]string{"domain": d, "info": "Сайт сохранён"}, nil
		})
		return map[string]string{"job": id}, nil
	})
	api("sites.list", func(q *Req) (any, error) {
		web := webVersions()
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]any{}
		for _, s := range store.Sites {
			c := findCert(s.SSL)
			row := map[string]any{"site": siteForUI(s), "unicode": toUnicode(s.Domain)}
			if s.Mode == "php" && s.Backend != "" {
				row["web_version"] = web[s.Backend]
			} else {
				row["web_version"] = web["nginx"]
			}
			if c != nil {
				row["ssl_expires"] = c.Expires
				row["ssl_type"] = c.Type
			}
			out = append(out, row)
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["unicode"].(string) < out[j]["unicode"].(string) })
		return out, nil
	})
	api("sites.get", func(q *Req) (any, error) {
		var in struct{ Domain string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		s := findSite(in.Domain)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		return map[string]any{"site": siteForUI(s), "custom": readFile(customPath(s.Domain)), "vhost": readFile(vhostPath(s.Domain))}, nil
	})
	api("sites.form", func(q *Req) (any, error) {
		mu.Lock()
		users := []string{}
		for _, u := range store.Users {
			users = append(users, u.Name)
		}
		certs := []map[string]any{}
		for _, c := range store.Certs {
			certs = append(certs, map[string]any{"name": c.Name, "domains": c.Domains, "type": c.Type})
		}
		email := store.Settings.Email
		mu.Unlock()
		return map[string]any{"php_profiles": phpProfileChoices(), "kinds": siteKindChoices(), "templates": siteTemplateChoices(), "users": users, "php": phpInstalled(), "php_all": phpAll, "apache": apacheReady(), "openlitespeed": olsInstalled() && olsManaged(), "default_php": defaultPHP(), "certs": certs, "email": email}, nil
	})
	apiOp("sites.save", "Сайт: сохранение", func(q *Req) (any, error) {
		var in siteIn
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		s, err := createOrUpdateSite(in)
		if err != nil {
			return nil, err
		}
		if in.Email != "" && reEmail.MatchString(in.Email) {
			mu.Lock()
			store.Settings.Email = in.Email
			save()
			mu.Unlock()
		}
		if s.AutoSSL {
			msg, err := autoSSLSite(s.Domain, true)
			if err != nil {
				return map[string]any{"warning": "Сайт сохранён. Let’s Encrypt пока не выпущен: " + firstLine(err.Error()) + ". Панель повторит попытку автоматически."}, nil
			}
			if msg != "" {
				return map[string]any{"info": msg}, nil
			}
		}
		return s, nil
	})
	apiOp("sites.custom.save", "Сайт: свои правила nginx", func(q *Req) (any, error) {
		var in struct{ Domain, Text string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		s := findSite(in.Domain)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		return nil, saveNginxCustom(s, in.Text)
	})
	apiOp("sites.suspend", "Сайт: включение/выключение", func(q *Req) (any, error) {
		var in struct {
			Domain  string
			Suspend bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		s := findSite(in.Domain)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		s.Suspended = in.Suspend
		if err := applySite(s, s.PHP); err != nil {
			s.Suspended = !in.Suspend
			return nil, err
		}
		save()
		return nil, nil
	})
	apiOp("sites.delete", "Сайт: удаление", func(q *Req) (any, error) {
		var in struct {
			Domain string
			Files  bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, deleteSite(in.Domain, in.Files)
	})
	api("users.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]any{}
		for _, u := range store.Users {
			n := 0
			for _, s := range store.Sites {
				if s.User == u.Name {
					n++
				}
			}
			out = append(out, map[string]any{"name": u.Name, "home": u.Home, "sites": n, "created": u.Created})
		}
		return out, nil
	})
	apiOp("users.create", "Пользователь: создание", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if findUser(in.Name) != nil {
			return nil, errf("Пользователь уже есть")
		}
		_, err := ensureUser(in.Name)
		if err == nil {
			save()
		}
		return nil, err
	})
}

func toUnicode(d string) string {
	u, err := idna.Lookup.ToUnicode(d)
	if err != nil {
		return d
	}
	return u
}

func renderPHPPool(s *Site, u *SiteUser) ([]byte, error) {
	var pb bytes.Buffer
	data := map[string]any{
		"Domain": s.Domain, "Version": s.PHP, "Pool": poolName(s.Domain), "User": s.User, "Home": u.Home,
		"Sock": sockPath(s.Domain), "WebUser": osInfo.WebUser, "MaxBody": s.MaxBody, "Ini": phpSiteIni(s),
		"PM": phpPoolSettings(s.PHPProfile, phpMemTotalMB()),
	}
	if e := tplPool.Execute(&pb, data); e != nil {
		return nil, e
	}
	return pb.Bytes(), nil
}
