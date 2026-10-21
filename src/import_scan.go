// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Что найдено на старом сервере. Структура общая для всех способов подключения.
type ImpSite struct {
	Domain  string   `json:"domain"`
	Aliases []string `json:"aliases"`
	Owner   string   `json:"owner"`
	Root    string   `json:"root"`
	Mode    string   `json:"mode"` // php | static | proxy
	ProxyTo string   `json:"proxy_to,omitempty"`
	PHP     string   `json:"php,omitempty"`
	SSL     bool     `json:"ssl"`
	Size    int64    `json:"size"`
	sock    string   // сокет PHP-FPM из nginx: по нему определяется версия PHP
}
type ImpDB struct {
	Name  string   `json:"name"`
	Size  int64    `json:"size"`
	Users []string `json:"users,omitempty"`
}
type ImpMailDomain struct {
	Name  string `json:"name"`
	Boxes int    `json:"boxes"`
}
type ImpDNSZone struct {
	Name string `json:"name"`
}
type ImpCron struct {
	User     string `json:"user"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}
type ImpFTP struct {
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
	Home  string `json:"home"`
}

// ImpBackup — настройки резервного копирования старой панели без пароля FTP.
type ImpBackup struct {
	Target string `json:"target"` // local | ftp | both
	Host   string `json:"host,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Daily  bool   `json:"daily"`
	Keep   int    `json:"keep"`
	Hour   int    `json:"hour"`
}

type ImpInventory struct {
	Source        string          `json:"source"`
	Host          string          `json:"host"`
	OS            string          `json:"os"`
	Panel         string          `json:"panel"`
	Fingerprint   string          `json:"fingerprint,omitempty"`
	PanelFP       string          `json:"panel_fingerprint,omitempty"` // сертификат старой панели
	Sites         []ImpSite       `json:"sites"`
	DBs           []ImpDB         `json:"dbs"`
	MailDomains   []ImpMailDomain `json:"mail_domains"`
	DNS           []ImpDNSZone    `json:"dns"`
	Cron          []ImpCron       `json:"cron"`
	FTP           []ImpFTP        `json:"ftp"`
	Backup        *ImpBackup      `json:"backup,omitempty"`
	PHPVersions   []string        `json:"php_versions"`
	Services      map[string]bool `json:"services"`
	Warnings      []string        `json:"warnings"`
	CanTransfer   bool            `json:"can_transfer"` // есть SSH-доступ для переноса файлов и баз
	DBAccessError string          `json:"db_access_error,omitempty"`
}

// ---------- разбор конфигурации nginx ----------

type nginxDirective struct {
	Name string
	Args []string
	Body []nginxDirective
	Has  bool // есть блок { }
}

var reNginxTok = regexp.MustCompile(`"[^"]*"|'[^']*'|[{};]|[^\s{};]+`)

// parseNginx разбирает вывод `nginx -T`: комментарии отбрасываются, блоки вложены.
func parseNginx(text string) []nginxDirective {
	var clean strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "#"); i >= 0 && !strings.ContainsAny(line[:i], "\"'") {
			line = line[:i]
		}
		clean.WriteString(line)
		clean.WriteByte('\n')
	}
	toks := reNginxTok.FindAllString(clean.String(), -1)
	pos := 0
	var block func() []nginxDirective
	block = func() []nginxDirective {
		var out []nginxDirective
		for pos < len(toks) {
			t := toks[pos]
			if t == "}" {
				pos++
				return out
			}
			d := nginxDirective{Name: t}
			pos++
			for pos < len(toks) && toks[pos] != ";" && toks[pos] != "{" && toks[pos] != "}" {
				d.Args = append(d.Args, strings.Trim(toks[pos], `"'`))
				pos++
			}
			if pos < len(toks) {
				switch toks[pos] {
				case ";":
					pos++
				case "{":
					pos++
					d.Has, d.Body = true, block()
				}
			}
			out = append(out, d)
		}
		return out
	}
	return block()
}

func nginxFind(ds []nginxDirective, name string) []nginxDirective {
	return nginxFindSkip(ds, name, nil)
}

// nginxFindSkip ищет директивы по всем вложенным блокам, пропуская блоки, для которых skip вернул true.
func nginxFindSkip(ds []nginxDirective, name string, skip func(nginxDirective) bool) []nginxDirective {
	var out []nginxDirective
	for _, d := range ds {
		if skip != nil && skip(d) {
			continue
		}
		if d.Name == name {
			out = append(out, d)
		}
		if d.Has {
			out = append(out, nginxFindSkip(d.Body, name, skip)...)
		}
	}
	return out
}

// Блок location для проверки Let’s Encrypt: его root — служебная папка, а не папка сайта.
func nginxIsACME(d nginxDirective) bool {
	if d.Name != "location" {
		return false
	}
	for _, a := range d.Args {
		if strings.Contains(a, ".well-known") {
			return true
		}
	}
	return false
}

type nginxServer struct {
	names    []string
	root     string
	ssl      bool
	phpSock  string
	proxyTo  string
	redirect bool
}

func nginxServers(ds []nginxDirective) []nginxServer {
	var out []nginxServer
	for _, srv := range nginxFind(ds, "server") {
		var s nginxServer
		vars := map[string]string{}
		for _, d := range nginxFind(srv.Body, "set") {
			if len(d.Args) >= 2 && strings.HasPrefix(d.Args[0], "$") {
				vars[d.Args[0]] = d.Args[1]
			}
		}
		for _, d := range srv.Body {
			switch d.Name {
			case "server_name":
				s.names = append(s.names, d.Args...)
			case "root":
				if len(d.Args) > 0 {
					s.root = d.Args[0]
				}
			case "listen":
				for _, a := range d.Args {
					if a == "ssl" || strings.HasPrefix(a, "443") || strings.HasSuffix(a, ":443") {
						s.ssl = true
					}
				}
			case "ssl_certificate":
				s.ssl = true
			case "return":
				if len(d.Args) > 1 && (d.Args[0] == "301" || d.Args[0] == "302" || d.Args[0] == "308") {
					s.redirect = true
				}
			}
		}
		for _, d := range nginxFindSkip(srv.Body, "root", nginxIsACME) {
			if s.root == "" && len(d.Args) > 0 {
				s.root = d.Args[0]
			}
		}
		// ISPmanager задаёт корень переменной: set $root_path /var/www/…; root $root_path;
		if strings.HasPrefix(s.root, "$") {
			s.root = vars["$"+strings.Trim(s.root, "${}")]
		}
		for _, d := range nginxFind(srv.Body, "fastcgi_pass") {
			if len(d.Args) > 0 && s.phpSock == "" {
				s.phpSock = d.Args[0]
			}
		}
		for _, d := range nginxFind(srv.Body, "proxy_pass") {
			if len(d.Args) > 0 && s.proxyTo == "" {
				s.proxyTo = d.Args[0]
			}
		}
		out = append(out, s)
	}
	return out
}

var rePHPVer = regexp.MustCompile(`php[-_]?(\d)\.?(\d)`)
var rePHPDir = regexp.MustCompile(`/php/(\d)\.(\d)(?:/|$)`)

// phpVersionFrom находит версию PHP в пути сокета или пула: php8.1-fpm.sock, php81-php-fpm.sock,
// /etc/php/8.3/fpm/pool.d/…
func phpVersionFrom(s string) string {
	low := strings.ToLower(s)
	if m := rePHPDir.FindStringSubmatch(low); m != nil {
		return m[1] + "." + m[2]
	}
	if m := rePHPVer.FindStringSubmatch(low); m != nil {
		return m[1] + "." + m[2]
	}
	return ""
}

var reBadHost = regexp.MustCompile(`^(_|localhost|default|\*|\.)$|^\d+\.\d+\.\d+\.\d+$|[*~$]`)

// sitesFromNginx собирает сайты: блоки http и https одного домена объединяются, редиректы пропускаются.
// Папки веб-приложений самой панели и системы (webmail, phpMyAdmin и т. п.) — не сайты клиентов.
var reImpSystemRoot = regexp.MustCompile(`^/(usr/(share|lib|local/(mgr5|ispmgr|itrupanel|vesta|hestia|fastpanel2|cpanel))|var/lib/(roundcube|itrupanel|phpmyadmin|mysql)|var/www/(itrupanel-acme|_letsencrypt|letsencrypt)|opt/(remi|phpmyadmin)|www/server/(phpmyadmin|panel)|etc)(/|$)|^/var/www/(html|roundcube|phpmyadmin)/?$`)

func sitesFromNginx(text string) []ImpSite {
	byRoot := map[string]*ImpSite{}
	var order []string
	for _, s := range nginxServers(parseNginx(text)) {
		if s.redirect && s.root == "" {
			continue
		}
		var names []string
		for _, n := range s.names {
			n = strings.ToLower(strings.TrimSpace(n))
			if n != "" && !reBadHost.MatchString(n) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			continue
		}
		key := ""
		switch {
		case s.proxyTo != "" && s.phpSock == "":
			key = "proxy:" + names[0]
		case s.root != "":
			if reImpSystemRoot.MatchString(s.root) {
				continue
			}
			key = s.root
		default:
			continue
		}
		it := byRoot[key]
		if it == nil {
			it = &ImpSite{Domain: names[0], Root: s.root, Mode: "static"}
			byRoot[key] = it
			order = append(order, key)
		}
		for _, n := range names {
			if n != it.Domain {
				it.Aliases = append(it.Aliases, n)
			}
		}
		it.SSL = it.SSL || s.ssl
		if s.phpSock != "" {
			it.Mode = "php"
			it.sock = strings.TrimPrefix(s.phpSock, "unix:")
			if v := phpVersionFrom(s.phpSock); v != "" {
				it.PHP = v
			}
		}
		if s.proxyTo != "" && s.phpSock == "" {
			it.Mode, it.ProxyTo, it.Root = "proxy", s.proxyTo, ""
		}
	}
	var out []ImpSite
	for _, k := range order {
		it := byRoot[k]
		it.Aliases = dedup(it.Aliases)
		sort.Strings(it.Aliases)
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// ---------- сканер по SSH ----------

const (
	impCmdOS      = `. /etc/os-release 2>/dev/null; echo "${PRETTY_NAME:-unknown}"; uname -m`
	impCmdNginx   = `nginx -T 2>/dev/null`
	impCmdPanel   = `for p in /usr/local/mgr5/sbin/mgrctl:ispmanager /usr/local/ispmgr/sbin/ispmgr:ispmanager4 /www/server/panel:aapanel /usr/local/hestia:hestia /usr/local/vesta:vesta /usr/local/fastpanel2:fastpanel /usr/local/cpanel:cpanel /usr/local/CyberCP:cyberpanel; do [ -e "${p%%:*}" ] && echo "${p##*:}" && break; done; true`
	impCmdPHPVers = `ls -d /usr/bin/php[0-9]* /usr/bin/php-* /opt/remi/php*/root/usr/bin/php /opt/php-* 2>/dev/null; ls -d /etc/php/*/ /etc/opt/remi/php*/ 2>/dev/null; true`
	impCmdMySQL   = `mysql -N -B -e "SELECT s.schema_name, COALESCE(SUM(t.data_length+t.index_length),0) FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema=s.schema_name WHERE s.schema_name NOT IN ('information_schema','mysql','performance_schema','sys','phpmyadmin') GROUP BY s.schema_name ORDER BY s.schema_name"`
	impCmdDBUsers = `mysql -N -B -e "SELECT db, user FROM mysql.db WHERE db NOT IN ('performance_schema','sys') AND user<>'' ORDER BY db, user"`
	impCmdSvc     = `for s in exim4 exim postfix dovecot named bind9 pure-ftpd proftpd vsftpd mysql mariadb mysqld nginx apache2 httpd docker; do systemctl list-unit-files "$s.service" 2>/dev/null | grep -q "^$s.service" && echo "$s"; done; true`
	impCmdMailDom = `(cat /etc/exim4/virtual/domains /etc/exim/domains /etc/postfix/virtual_domains /etc/postfix/vdomains 2>/dev/null; doveadm user '*' 2>/dev/null | sed 's/.*@//') | sort -u | head -500`
	impCmdMailBox = `doveadm user '*' 2>/dev/null | head -5000`
	impCmdDNS     = `grep -rhoE '^[[:space:]]*zone[[:space:]]+"[^"]+"' /etc/bind /etc/named.conf /etc/named /var/named/chroot/etc 2>/dev/null | sed -E 's/.*"([^"]+)".*/\1/' | grep -vE '^(\.|localhost|[0-9.]+\.in-addr\.arpa|[0-9a-f:.]+\.ip6\.arpa|0|127|255)$' | sort -u | head -2000`
	impCmdFTP     = `(grep -E '^[^#:]+:' /etc/pure-ftpd/pureftpd.passwd 2>/dev/null | cut -d: -f1,6; ls /etc/proftpd/ftpd.passwd 2>/dev/null) | head -500`
)

func impNumPairs(text string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			out = append(out, [2]string{f[0], f[1]})
		}
	}
	return out
}

// impScanSSH осматривает старый сервер. Все команды только читают.
func impScanSSH(ctx context.Context, r impRunner, host string) (*ImpInventory, error) {
	inv := &ImpInventory{Source: "ssh", Host: host, Services: map[string]bool{}, CanTransfer: true}
	if out, err := r.Run(ctx, impCmdOS); err == nil {
		f := strings.Split(strings.TrimSpace(out), "\n")
		inv.OS = f[0]
		if len(f) > 1 {
			inv.OS += " (" + f[1] + ")"
		}
	}
	if out, _ := r.Run(ctx, impCmdPanel); strings.TrimSpace(out) != "" {
		inv.Panel = strings.TrimSpace(out)
	}
	// Сайты: nginx — основной веб-сервер почти у всех панелей; для Apache без nginx сайты не определяются.
	nginxText, err := r.Run(ctx, impCmdNginx)
	if err != nil || strings.TrimSpace(nginxText) == "" {
		inv.Warnings = append(inv.Warnings, "nginx на старом сервере не найден или его настройки не прочитались: сайты определить не удалось")
	} else {
		inv.Sites = sitesFromNginx(nginxText)
	}
	// Владельцы и размеры папок сайтов одним запросом.
	var roots []string
	for _, s := range inv.Sites {
		if s.Root != "" {
			roots = append(roots, shQuote(s.Root))
		}
	}
	if len(roots) > 0 {
		cmd := `for d in ` + strings.Join(roots, " ") + `; do printf '%s\t%s\t%s\n' "$d" "$(stat -c %U "$d" 2>/dev/null)" "$(timeout 120 du -sb "$d" 2>/dev/null | cut -f1)"; done`
		if out, err := r.Run(ctx, cmd); err == nil {
			info := map[string][2]string{}
			for _, line := range strings.Split(out, "\n") {
				f := strings.Split(line, "\t")
				if len(f) == 3 {
					info[f[0]] = [2]string{f[1], f[2]}
				}
			}
			for i := range inv.Sites {
				if v, ok := info[inv.Sites[i].Root]; ok {
					inv.Sites[i].Owner = v[0]
					inv.Sites[i].Size, _ = strconv.ParseInt(v[1], 10, 64)
				}
			}
		}
	}
	// Версия PHP сайта, если сокет её не содержит (пул php-fpm на пользователя): ищем пул с этим сокетом.
	for i := range inv.Sites {
		st := &inv.Sites[i]
		if st.Mode != "php" || st.PHP != "" || st.sock == "" {
			continue
		}
		cmd := `grep -rlF ` + shQuote(st.sock) + ` /etc/php /etc/opt/remi /etc/php-fpm.d /usr/local/mgr5/etc/php-fpm.d /opt/php-*/etc 2>/dev/null | head -3`
		if out, err := r.Run(ctx, cmd); err == nil {
			for _, p := range strings.Fields(out) {
				if v := phpVersionFrom(p); v != "" {
					st.PHP = v
					break
				}
			}
		}
	}
	// Версии PHP на старом сервере.
	if out, _ := r.Run(ctx, impCmdPHPVers); out != "" {
		seen := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			if v := phpVersionFrom(path.Base(strings.TrimSuffix(strings.TrimSpace(line), "/"))); v != "" && !seen[v] {
				seen[v] = true
				inv.PHPVersions = append(inv.PHPVersions, v)
			} else if v := phpVersionFrom(line); v != "" && !seen[v] {
				seen[v] = true
				inv.PHPVersions = append(inv.PHPVersions, v)
			}
		}
		sort.Strings(inv.PHPVersions)
	}
	// Базы данных.
	if out, err := r.Run(ctx, impCmdMySQL); err != nil {
		inv.DBAccessError = err.Error()
		inv.Warnings = append(inv.Warnings, "базы данных MySQL/MariaDB не прочитались: "+err.Error())
	} else {
		users := map[string][]string{}
		if u, err := r.Run(ctx, impCmdDBUsers); err == nil {
			for _, p := range impNumPairs(u) {
				users[p[0]] = append(users[p[0]], p[1])
			}
		}
		for _, p := range impNumPairs(out) {
			n, _ := strconv.ParseInt(p[1], 10, 64)
			inv.DBs = append(inv.DBs, ImpDB{Name: p[0], Size: n, Users: dedup(users[p[0]])})
		}
		sort.Slice(inv.DBs, func(i, j int) bool { return inv.DBs[i].Name < inv.DBs[j].Name })
	}
	// Службы и прочие данные: почта, DNS, FTP, cron.
	if out, _ := r.Run(ctx, impCmdSvc); out != "" {
		for _, s := range strings.Fields(out) {
			inv.Services[s] = true
		}
	}
	if inv.Services["exim4"] || inv.Services["exim"] || inv.Services["postfix"] || inv.Services["dovecot"] {
		boxes := map[string]int{}
		if out, _ := r.Run(ctx, impCmdMailBox); out != "" {
			for _, u := range strings.Fields(out) {
				if i := strings.LastIndex(u, "@"); i > 0 {
					boxes[strings.ToLower(u[i+1:])]++
				}
			}
		}
		if out, _ := r.Run(ctx, impCmdMailDom); out != "" {
			for _, d := range strings.Fields(out) {
				d = strings.ToLower(d)
				if reDomain.MatchString(d) {
					if _, ok := boxes[d]; !ok {
						boxes[d] = 0
					}
				}
			}
		}
		for d, n := range boxes {
			inv.MailDomains = append(inv.MailDomains, ImpMailDomain{Name: d, Boxes: n})
		}
		sort.Slice(inv.MailDomains, func(i, j int) bool { return inv.MailDomains[i].Name < inv.MailDomains[j].Name })
	}
	if inv.Services["named"] || inv.Services["bind9"] {
		if out, _ := r.Run(ctx, impCmdDNS); out != "" {
			for _, z := range strings.Fields(out) {
				inv.DNS = append(inv.DNS, ImpDNSZone{Name: z})
			}
		}
	}
	if inv.Services["pure-ftpd"] || inv.Services["proftpd"] || inv.Services["vsftpd"] {
		if out, _ := r.Run(ctx, impCmdFTP); out != "" {
			for _, line := range strings.Split(out, "\n") {
				if f := strings.SplitN(strings.TrimSpace(line), ":", 2); len(f) == 2 && f[0] != "" {
					inv.FTP = append(inv.FTP, ImpFTP{Name: f[0], Home: f[1]})
				}
			}
		}
	}
	owners := map[string]bool{}
	for _, s := range inv.Sites {
		if s.Owner != "" && s.Owner != "root" {
			owners[s.Owner] = true
		}
	}
	for u := range owners {
		out, err := r.Run(ctx, `crontab -l -u `+shQuote(u)+` 2>/dev/null`)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.Contains(strings.SplitN(line, " ", 2)[0], "=") {
				continue
			}
			if f := strings.Fields(line); len(f) >= 6 {
				inv.Cron = append(inv.Cron, ImpCron{User: u, Schedule: strings.Join(f[:5], " "), Command: strings.Join(f[5:], " ")})
			}
		}
	}
	return inv, nil
}
