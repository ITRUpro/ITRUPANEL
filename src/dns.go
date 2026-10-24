// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DNS-сервер BIND: панель хранит зоны в state.json и генерирует zone-файлы.
// Debian/Ubuntu: /etc/bind/itrupanel, RHEL: /var/named/itrupanel. Служба — named.

type DNSRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"` // @, www, mail, _sip._tcp …
	Type  string `json:"type"`
	TTL   int    `json:"ttl"`
	Prio  int    `json:"prio"` // MX, SRV
	Value string `json:"value"`
}

type DNSZone struct {
	Name    string       `json:"name"`
	Serial  uint32       `json:"serial"`
	Email   string       `json:"email"` // hostmaster@…
	TTL     int          `json:"ttl"`
	Records []*DNSRecord `json:"records"`
	Created time.Time    `json:"created"`
}

var dnsTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}

func dnsDir() string {
	if isBSD() {
		return bsdNSDDir + "/itrupanel"
	}
	if osInfo.Family == "rhel" {
		return "/var/named/itrupanel"
	}
	return "/etc/bind/itrupanel"
}

func dnsZonesConf() string {
	if isBSD() {
		return bsdNSDDir + "/itrupanel-zones.conf"
	}
	if osInfo.Family == "rhel" {
		return "/etc/named/itrupanel-zones.conf"
	}
	return "/etc/bind/itrupanel-zones.conf"
}

func dnsInstalled() bool {
	if isBSD() {
		return fileExists(bsdNSDBin)
	}
	return fileExists("/usr/sbin/named")
}

func findZone(name string) *DNSZone {
	for _, z := range store.DNSZones {
		if z.Name == name {
			return z
		}
	}
	return nil
}

func nextSerial(old uint32) uint32 {
	day, _ := strconv.ParseUint(time.Now().Format("20060102")+"00", 10, 32)
	if uint32(day) > old {
		return uint32(day)
	}
	return old + 1
}

// defaultIP — основной внешний IPv4 сервера.
func defaultIP() string {
	ips := serverIPs()
	best := ""
	for ip := range ips {
		p := net.ParseIP(ip)
		if p == nil || p.To4() == nil {
			continue
		}
		if !p.IsPrivate() {
			return ip
		}
		best = ip
	}
	return best
}

func fqdn(name, zone string) string {
	if name == "@" || name == "" {
		return zone + "."
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "." + zone + "."
}

func txtQuote(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		return v
	}
	// TXT длиннее 255 символов (DKIM) режем на куски
	var parts []string
	for len(v) > 250 {
		parts = append(parts, v[:250])
		v = v[250:]
	}
	parts = append(parts, v)
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(p, `\`, `\\`), `"`, `\"`) + `"`
	}
	return strings.Join(parts, " ")
}

func renderZone(z *DNSZone) string {
	var b strings.Builder
	ns := ""
	for _, r := range z.Records {
		if r.Type == "NS" && (r.Name == "@" || r.Name == "") {
			ns = fqdn(r.Value, z.Name)
			break
		}
	}
	if ns == "" {
		ns = "ns1." + z.Name + "."
	}
	email := strings.Replace(z.Email, "@", ".", 1)
	if email == "" {
		email = "hostmaster." + z.Name
	}
	fmt.Fprintf(&b, "; ITRUPANEL: зона %s. Файл перезаписывается панелью.\n$TTL %d\n", z.Name, z.TTL)
	fmt.Fprintf(&b, "@\tIN\tSOA\t%s %s. ( %d 3600 900 1209600 300 )\n", ns, strings.TrimSuffix(email, "."), z.Serial)
	recs := append([]*DNSRecord{}, z.Records...)
	order := map[string]int{"NS": 0, "A": 1, "AAAA": 2, "CNAME": 3, "MX": 4, "TXT": 5, "SRV": 6, "CAA": 7}
	sort.SliceStable(recs, func(i, j int) bool { return order[recs[i].Type] < order[recs[j].Type] })
	var rrsetTTL map[string]int
	if isBSD() {
		rrsetTTL = nsdRRsetTTL(z, recs)
	}
	for _, r := range recs {
		name := r.Name
		if name == "" {
			name = "@"
		}
		ttl := ""
		if r.TTL > 0 {
			ttl = strconv.Itoa(r.TTL)
		}
		if t, ok := rrsetTTL[name+"/"+r.Type]; ok {
			ttl = strconv.Itoa(t)
		}
		val := r.Value
		switch r.Type {
		case "MX":
			val = fmt.Sprintf("%d %s", r.Prio, fqdn(r.Value, z.Name))
		case "CNAME", "NS":
			val = fqdn(r.Value, z.Name)
		case "SRV":
			// значение: "вес порт цель"
			f := strings.Fields(r.Value)
			if len(f) == 3 {
				val = fmt.Sprintf("%d %s %s %s", r.Prio, f[0], f[1], fqdn(f[2], z.Name))
			}
		case "TXT":
			val = txtQuote(r.Value)
		}
		fmt.Fprintf(&b, "%s\t%s\tIN\t%s\t%s\n", name, ttl, r.Type, val)
	}
	return b.String()
}

// applyZones пишет зону (или все) и перечитывает BIND. Под mu.
func applyZone(z *DNSZone) error {
	if !dnsInstalled() && !cfg.Dry {
		return errf("DNS-сервер BIND не установлен")
	}
	z.Serial = nextSerial(z.Serial)
	path := dnsDir() + "/" + z.Name + ".db"
	prev := readFile(path)
	os.MkdirAll(P(dnsDir()), 0755)
	if err := writeFile(path, []byte(renderZone(z)), 0644); err != nil {
		return err
	}
	if out, err := dnsCheckZone(z.Name, P(path)); err != nil {
		if prev != "" {
			writeFile(path, []byte(prev), 0644)
		} else {
			os.Remove(P(path))
		}
		return errf("Ошибка в зоне: %s", lastLines(out, 3))
	}
	if isBSD() {
		run("chown", "root:nsd", path)
	} else if osInfo.Family == "rhel" {
		run("chown", "root:named", path)
		run("restorecon", path)
	} else {
		run("chown", "root:bind", path)
	}
	return writeZonesConf()
}

func writeZonesConf() error {
	var b strings.Builder
	if isBSD() {
		b.WriteString("# ITRUPANEL: список зон. Файл перезаписывается панелью.\n") // комментарии nsd.conf начинаются с #
	} else {
		b.WriteString("// ITRUPANEL: список зон. Файл перезаписывается панелью.\n")
	}
	names := []string{}
	for _, z := range store.DNSZones {
		names = append(names, z.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		if isBSD() {
			fmt.Fprintf(&b, "zone:\n    name: \"%s\"\n    zonefile: \"%s/%s.db\"\n", n, dnsDir(), n)
			continue
		}
		fmt.Fprintf(&b, "zone \"%s\" {\n    type master;\n    file \"%s/%s.db\";\n    allow-transfer { none; };\n};\n", n, dnsDir(), n)
	}
	if err := writeFile(dnsZonesConf(), []byte(b.String()), 0644); err != nil {
		return err
	}
	if isBSD() {
		return nsdReload()
	}
	if out, err := run("named-checkconf"); err != nil {
		return errf("named-checkconf: %s", lastLines(out, 3))
	}
	if osInfo.Systemd {
		if _, err := run("rndc", "reconfig"); err == nil {
			run("rndc", "reload")
			return nil
		}
	}
	return service("reload", "named")
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " / ")
}

var (
	reDNSName = regexp.MustCompile(`^(@|\*|(\*\.)?[a-zA-Z0-9_]([a-zA-Z0-9_-]{0,62})(\.[a-zA-Z0-9_]([a-zA-Z0-9_-]{0,62}))*\.?)$`)
	reHost    = regexp.MustCompile(`^(@|[a-zA-Z0-9_]([a-zA-Z0-9_-]{0,62})(\.[a-zA-Z0-9_-]{1,63})*\.?)$`)
)

func validateRecord(r *DNSRecord) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Value = strings.TrimSpace(r.Value)
	r.Type = strings.ToUpper(r.Type)
	if r.Name == "" {
		r.Name = "@"
	}
	if !reDNSName.MatchString(r.Name) {
		return errf("Неверное имя записи: %s", r.Name)
	}
	if !contains(dnsTypes, r.Type) {
		return errf("Неподдерживаемый тип записи")
	}
	if r.TTL != 0 && (r.TTL < 60 || r.TTL > 604800) {
		return errf("TTL от 60 до 604800 секунд")
	}
	if strings.ContainsAny(r.Value, "\n\r;") && r.Type != "TXT" {
		return errf("Недопустимые символы в значении")
	}
	if strings.ContainsAny(r.Value, "\n\r") {
		return errf("Значение — одна строка")
	}
	switch r.Type {
	case "A":
		if ip := net.ParseIP(r.Value); ip == nil || ip.To4() == nil {
			return errf("A-запись: нужен IPv4-адрес")
		}
	case "AAAA":
		if ip := net.ParseIP(r.Value); ip == nil || ip.To4() != nil {
			return errf("AAAA-запись: нужен IPv6-адрес")
		}
	case "CNAME", "NS", "MX":
		if !reHost.MatchString(r.Value) {
			return errf("%s: укажите имя хоста", r.Type)
		}
		if r.Type == "MX" && (r.Prio < 0 || r.Prio > 65535) {
			return errf("Приоритет MX от 0 до 65535")
		}
	case "SRV":
		f := strings.Fields(r.Value)
		if len(f) != 3 {
			return errf("SRV: значение в виде «вес порт хост», например: 5 5060 sip.example.ru")
		}
	case "CAA":
		if !regexp.MustCompile(`^\d{1,3} (issue|issuewild|iodef) "[^"]+"$`).MatchString(r.Value) {
			return errf(`CAA: например 0 issue "letsencrypt.org"`)
		}
	case "TXT":
		if r.Value == "" {
			return errf("TXT: пустое значение")
		}
	}
	return nil
}

func newZone(name, ip string, ns []string, mail bool) (*DNSZone, error) {
	d, err := toASCII(name)
	if err != nil {
		return nil, err
	}
	if ip == "" {
		ip = defaultIP()
	}
	if net.ParseIP(ip) == nil {
		return nil, errf("Укажите IP-адрес сервера")
	}
	typ := "A"
	if net.ParseIP(ip).To4() == nil {
		typ = "AAAA"
	}
	if findZone(d) != nil {
		return nil, errf("Зона %s уже есть", d)
	}
	z := &DNSZone{Name: d, TTL: 3600, Email: "hostmaster@" + d, Created: time.Now()}
	add := func(n, t, v string, prio int) {
		z.Records = append(z.Records, &DNSRecord{ID: randToken(5), Name: n, Type: t, Value: v, Prio: prio})
	}
	if len(ns) == 0 {
		ns = []string{"ns1." + d + ".", "ns2." + d + "."}
		add("ns1", typ, ip, 0)
		add("ns2", typ, ip, 0)
	}
	for _, n := range ns {
		if !strings.HasSuffix(n, ".") {
			n += "."
		}
		add("@", "NS", n, 0)
	}
	add("@", typ, ip, 0)
	add("www", typ, ip, 0)
	if mail {
		add("mail", typ, ip, 0)
		add("@", "MX", "mail", 10)
		add("@", "TXT", "v=spf1 a mx ~all", 0)
	}
	add("@", "CAA", `0 issue "letsencrypt.org"`, 0)
	return z, nil
}

// dnsForSite: при создании сайта — добавить A-записи в существующую зону (для поддомена)
// или создать зону, если попросили. Под mu.
func dnsForSite(domain string, aliases []string, createZone bool) string {
	if !dnsInstalled() && !cfg.Dry {
		return ""
	}
	ip := defaultIP()
	if ip == "" {
		return ""
	}
	typ := "A"
	if net.ParseIP(ip).To4() == nil {
		typ = "AAAA"
	}
	if createZone && findZone(domain) == nil {
		z, err := newZone(domain, ip, store.Settings.DNSNameservers, store.Settings.DNSMail)
		if err != nil {
			return err.Error()
		}
		store.DNSZones = append(store.DNSZones, z)
		if err := applyZone(z); err != nil {
			return err.Error()
		}
		return "DNS-зона создана"
	}
	changed := map[*DNSZone]bool{}
	for _, n := range append([]string{domain}, aliases...) {
		for _, z := range store.DNSZones {
			if n == z.Name || !strings.HasSuffix(n, "."+z.Name) {
				continue
			}
			rel := strings.TrimSuffix(n, "."+z.Name)
			exists := false
			for _, r := range z.Records {
				exists = exists || (r.Name == rel && (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME"))
			}
			if !exists {
				z.Records = append(z.Records, &DNSRecord{ID: randToken(5), Name: rel, Type: typ, Value: ip})
				changed[z] = true
			}
		}
	}
	for z := range changed {
		applyZone(z)
	}
	if len(changed) > 0 {
		return "A-записи добавлены в DNS"
	}
	return ""
}

func registerDNS() {
	api("dns.list", func(q *Req) (any, error) {
		mu.Lock()
		zones := append([]*DNSZone{}, store.DNSZones...)
		set := map[string]any{"nameservers": store.Settings.DNSNameservers, "mail": store.Settings.DNSMail}
		mu.Unlock()
		rows := []map[string]any{}
		for _, z := range zones {
			var ns []string
			for _, r := range z.Records {
				if r.Type == "NS" && r.Name == "@" {
					ns = append(ns, strings.TrimSuffix(fqdn(r.Value, z.Name), "."))
				}
			}
			rows = append(rows, map[string]any{"name": z.Name, "unicode": toUnicode(z.Name), "records": len(z.Records), "serial": z.Serial, "ns": ns, "created": z.Created})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i]["unicode"].(string) < rows[j]["unicode"].(string) })
		return map[string]any{"rows": rows, "installed": dnsInstalled(), "active": serviceActive("named"), "ip": defaultIP(), "settings": set}, nil
	})
	api("dns.delegation", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		z := findZone(in.Name)
		var want []string
		if z != nil {
			for _, r := range z.Records {
				if r.Type == "NS" && r.Name == "@" {
					want = append(want, strings.ToLower(strings.TrimSuffix(fqdn(r.Value, z.Name), ".")))
				}
			}
		}
		mu.Unlock()
		if z == nil {
			return nil, errf("Зона не найдена")
		}
		var got []string
		nss, err := net.LookupNS(in.Name)
		for _, n := range nss {
			got = append(got, strings.ToLower(strings.TrimSuffix(n.Host, ".")))
		}
		sort.Strings(got)
		ok := err == nil && len(got) > 0
		for _, w := range want {
			ok = ok && contains(got, w)
		}
		return map[string]any{"ok": ok, "got": got, "want": want}, nil
	})
	apiOp("dns.zone.create", "DNS: создание зоны", func(q *Req) (any, error) {
		var in struct {
			Name string
			IP   string
			NS   string
			Mail bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ns := strings.Fields(strings.ReplaceAll(in.NS, ",", " "))
		for _, n := range ns {
			if !reHost.MatchString(n) || !strings.Contains(strings.TrimSuffix(n, "."), ".") {
				return nil, errf("Неверный NS: %s", n)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if len(ns) == 0 {
			ns = store.Settings.DNSNameservers
		}
		z, err := newZone(in.Name, in.IP, ns, in.Mail)
		if err != nil {
			return nil, err
		}
		store.DNSZones = append(store.DNSZones, z)
		if err := applyZone(z); err != nil {
			store.DNSZones = store.DNSZones[:len(store.DNSZones)-1]
			return nil, err
		}
		save()
		return z, nil
	})
	apiOp("dns.zone.delete", "DNS: удаление зоны", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		var keep []*DNSZone
		found := false
		for _, z := range store.DNSZones {
			if z.Name == in.Name {
				found = true
				continue
			}
			keep = append(keep, z)
		}
		if !found {
			return nil, errf("Зона не найдена")
		}
		store.DNSZones = keep
		os.Remove(P(dnsDir() + "/" + in.Name + ".db"))
		save()
		return nil, writeZonesConf()
	})
	api("dns.zone.get", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		z := findZone(in.Name)
		if z == nil {
			return nil, errf("Зона не найдена")
		}
		return map[string]any{"zone": z, "text": renderZone(z), "types": dnsTypes}, nil
	})
	apiOp("dns.record.save", "DNS: запись", func(q *Req) (any, error) {
		var in struct {
			Zone   string
			Record DNSRecord
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		r := in.Record
		if err := validateRecord(&r); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		z := findZone(in.Zone)
		if z == nil {
			return nil, errf("Зона не найдена")
		}
		if r.Type == "CNAME" {
			for _, o := range z.Records {
				if o.Name == r.Name && o.ID != r.ID {
					return nil, errf("CNAME нельзя совмещать с другими записями того же имени (%s %s)", o.Name, o.Type)
				}
			}
		}
		backup := make([]*DNSRecord, len(z.Records))
		copy(backup, z.Records)
		if r.ID == "" {
			r.ID = randToken(5)
			z.Records = append(z.Records, &r)
		} else {
			found := false
			for i, o := range z.Records {
				if o.ID == r.ID {
					z.Records[i] = &r
					found = true
				}
			}
			if !found {
				return nil, errf("Запись не найдена")
			}
		}
		if err := applyZone(z); err != nil {
			z.Records = backup
			return nil, err
		}
		save()
		return r, nil
	})
	apiOp("dns.record.delete", "DNS: удаление записи", func(q *Req) (any, error) {
		var in struct {
			Zone string
			IDs  []string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		z := findZone(in.Zone)
		if z == nil {
			return nil, errf("Зона не найдена")
		}
		backup := z.Records
		var keep []*DNSRecord
		for _, r := range z.Records {
			if !contains(in.IDs, r.ID) {
				keep = append(keep, r)
			}
		}
		z.Records = keep
		if err := applyZone(z); err != nil {
			z.Records = backup
			return nil, err
		}
		save()
		return nil, nil
	})
	apiOp("dns.zone.settings", "DNS: параметры зоны", func(q *Req) (any, error) {
		var in struct {
			Name  string
			Email string
			TTL   int
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		z := findZone(in.Name)
		if z == nil {
			return nil, errf("Зона не найдена")
		}
		if in.Email != "" && !reEmail.MatchString(in.Email) {
			return nil, errf("Неверный e-mail")
		}
		if in.TTL < 60 || in.TTL > 604800 {
			return nil, errf("TTL от 60 до 604800")
		}
		z.Email, z.TTL = in.Email, in.TTL
		if err := applyZone(z); err != nil {
			return nil, err
		}
		save()
		return nil, nil
	})
	apiOp("dns.settings", "DNS: настройки", func(q *Req) (any, error) {
		var in struct {
			Nameservers string
			Mail        bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ns := strings.Fields(strings.ReplaceAll(in.Nameservers, ",", " "))
		for i, n := range ns {
			if !reHost.MatchString(n) || !strings.Contains(strings.TrimSuffix(n, "."), ".") {
				return nil, errf("Неверный NS: %s", n)
			}
			ns[i] = strings.TrimSuffix(n, ".") + "."
		}
		mu.Lock()
		store.Settings.DNSNameservers, store.Settings.DNSMail = ns, in.Mail
		save()
		mu.Unlock()
		return nil, nil
	})
	apiOp("dns.install", "DNS: установка BIND", func(q *Req) (any, error) {
		return nil, setupBind()
	})
}

// setupBind ставит и настраивает BIND как авторитетный сервер.
// rhelNamedConf настраивает /etc/named.conf RHEL-систем: приём запросов на всех адресах,
// рекурсия только для localhost и подключение зон панели. Повторный вызов ничего не дублирует:
// с двумя строками allow-recursion BIND не запускается.
func rhelNamedConf(c string) string {
	c = regexp.MustCompile(`listen-on port 53 \{[^}]*\};`).ReplaceAllString(c, "listen-on port 53 { any; };")
	c = regexp.MustCompile(`listen-on-v6 port 53 \{[^}]*\};`).ReplaceAllString(c, "listen-on-v6 port 53 { any; };")
	c = regexp.MustCompile(`(?m)^[ \t]*allow-recursion\s*\{[^}]*\};[ \t]*\n?`).ReplaceAllString(c, "")
	c = regexp.MustCompile(`allow-query\s*\{[^}]*\};`).ReplaceAllString(c, "allow-query     { any; };\n\tallow-recursion { localhost; };")
	if !strings.Contains(c, dnsZonesConf()) {
		c += "\ninclude \"" + dnsZonesConf() + "\";\n"
	}
	return c
}

func setupBind() error {
	if isBSD() {
		return bsdSetupBind()
	}
	if !dnsInstalled() {
		pk := []string{"bind9", "bind9-utils"}
		if osInfo.Family == "rhel" {
			pk = []string{"bind", "bind-utils"}
		}
		if err := pkgInstall(pk...); err != nil {
			return errf("Не удалось установить BIND: %s", firstLine(err.Error()))
		}
	}
	os.MkdirAll(P(dnsDir()), 0755)
	if osInfo.Family == "rhel" {
		os.MkdirAll(P("/etc/named"), 0755)
		c := rhelNamedConf(readFile("/etc/named.conf"))
		writeFile("/etc/named.conf", []byte(c), 0640)
		run("chown", "root:named", "/etc/named.conf")
		run("chown", "root:named", dnsDir())
		run("restorecon", "-R", "/var/named")
	} else {
		local := readFile("/etc/bind/named.conf.local")
		if !strings.Contains(local, dnsZonesConf()) {
			writeFile("/etc/bind/named.conf.local", []byte(local+"\ninclude \""+dnsZonesConf()+"\";\n"), 0644)
		}
		// авторитетный сервер: рекурсия только для самого сервера
		writeFile("/etc/bind/named.conf.options", []byte(`// ITRUPANEL
options {
	directory "/var/cache/bind";
	dnssec-validation auto;
	listen-on { any; };
	listen-on-v6 { any; };
	allow-query { any; };
	allow-recursion { localhost; };
	allow-transfer { none; };
	version "unknown";
};
`), 0644)
	}
	mu.Lock()
	err := writeZonesConf()
	mu.Unlock()
	if err != nil && !strings.Contains(err.Error(), "rndc") {
		// служба могла ещё не работать — запустим ниже
	}
	return service("enable", "named")
}
