// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type MailDNSOptions struct {
	IPv4   string `json:"ipv4"`
	Policy string `json:"policy"`
	Report string `json:"report"`
}
type mailDNSRecord struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Priority int    `json:"priority"`
}
type mailDNSView struct {
	Domain   string          `json:"domain"`
	Hostname string          `json:"hostname"`
	Options  MailDNSOptions  `json:"options"`
	Records  []mailDNSRecord `json:"records"`
}

// Exclude private, shared, benchmark and documentation addresses (often VPN adapters).
func mailDNSPublicIPv4(ip net.IP) bool {
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func validMailDNSOptions(o *MailDNSOptions) error {
	o.IPv4 = strings.TrimSpace(o.IPv4)
	if o.IPv4 != "" {
		ip := net.ParseIP(o.IPv4)
		if !mailDNSPublicIPv4(ip) {
			return errf("Укажите публичный IPv4 почтового сервера")
		}
		o.IPv4 = ip.To4().String()
	}
	if o.Policy == "" {
		o.Policy = "none"
	}
	if o.Policy != "none" && o.Policy != "quarantine" && o.Policy != "reject" {
		return errf("Неизвестная политика DMARC")
	}
	if o.Report != "" {
		a, e := mailAddress(o.Report)
		if e != nil {
			return errf("Неверный адрес отчётов DMARC")
		}
		o.Report = a
	}
	return nil
}

// No network requests while holding the store mutex. NAT/multiple-address hosts
// require an explicit outbound mail IPv4 instead of guessing from an IP service.
func mailDNSDefaultIPv4() string {
	if cfg.Dry {
		return ""
	}
	addrs, _ := net.InterfaceAddrs()
	found := ""
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || !mailDNSPublicIPv4(ip) {
			continue
		}
		if found != "" && found != ip.String() {
			return mailDNSCachedPublicIPv4()
		}
		found = ip.String()
	}
	if found == "" {
		return mailDNSCachedPublicIPv4()
	}
	return found
}

// Caller holds mu. Only public DKIM material is returned.
func mailDNSViewFor(name string) (mailDNSView, error) {
	for _, d := range store.Mail.Domains {
		if d.Name == name {
			o := d.DNS
			if o.IPv4 == "" {
				o.IPv4 = mailDNSDefaultIPv4()
			}
			if o.Policy == "" {
				o.Policy = "none"
			}
			host := mailDomainHost(store.Mail, d)
			dm := "v=DMARC1; p=" + o.Policy
			if o.Report != "" {
				dm += "; rua=mailto:" + o.Report
			}
			records := []mailDNSRecord{
				{"MX", d.Name, "MX", host + ".", 10},
				{"A", host, "A", o.IPv4, 0},
				{"SPF", d.Name, "TXT", "v=spf1 mx -all", 0},
				{"DKIM", "itru._domainkey." + d.Name, "TXT", "", 0},
				{"DMARC", "_dmarc." + d.Name, "TXT", dm, 0},
				{"PTR", o.IPv4, "PTR", store.Mail.Hostname + ".", 0},
			}
			if d.PublicKey != "" {
				records[3].Value = "v=DKIM1; k=rsa; p=" + d.PublicKey
			}
			return mailDNSView{d.Name, host, o, records}, nil
		}
	}
	return mailDNSView{}, errf("Почтовый домен не найден")
}

type mailDNSResolver interface {
	LookupMX(context.Context, string) ([]*net.MX, error)
	LookupIP(context.Context, string, string) ([]net.IP, error)
	LookupTXT(context.Context, string) ([]string, error)
	LookupAddr(context.Context, string) ([]string, error)
}
type mailDNSCheck struct {
	Kind    string   `json:"kind"`
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Actual  []string `json:"actual"`
}

func dnsHost(s string) string { return strings.ToLower(strings.TrimSuffix(s, ".")) }
func dnsTags(s string) (map[string]string, bool) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || k == "" {
			return nil, false
		}
		if _, exists := out[k]; exists {
			return nil, false
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, true
}
func mailDNSCheckRecord(ctx context.Context, r mailDNSResolver, v mailDNSView, rec mailDNSRecord) mailDNSCheck {
	c := mailDNSCheck{Kind: rec.Kind, Status: "error", Actual: []string{}}
	failLookup := func(e error) mailDNSCheck {
		var d *net.DNSError
		if errors.As(e, &d) && d.IsNotFound {
			c.Message = "Запись не найдена"
		} else {
			c.Status = "unknown"
			c.Message = "DNS не ответил: " + e.Error()
		}
		return c
	}
	ok := func(msg string) mailDNSCheck { c.Status = "ok"; c.Message = msg; return c }
	warn := func(msg string) mailDNSCheck { c.Status = "warn"; c.Message = msg; return c }
	switch rec.Kind {
	case "MX":
		rows, e := r.LookupMX(ctx, rec.Name)
		if e != nil {
			return failLookup(e)
		}
		match := false
		other := false
		for _, x := range rows {
			c.Actual = append(c.Actual, fmt.Sprintf("%d %s", x.Pref, x.Host))
			if dnsHost(x.Host) == dnsHost(v.Hostname) {
				match = true
			} else {
				other = true
			}
		}
		if match {
			if other {
				return warn("Есть MX этого сервера и другие MX; проверьте схему доставки")
			}
			return ok("MX указывает на почтовый сервер")
		}
		c.Message = "MX не указывает на " + v.Hostname
	case "A":
		rows, e := r.LookupIP(ctx, "ip4", rec.Name)
		if e != nil {
			c = failLookup(e)
			if c.Status == "error" {
				// Почтовые программы и другие серверы ищут именно имя почтового сервера, а не @ домена.
				c.Message = "Нет A-записи для " + rec.Name + " — имени почтового сервера (запись @ домена не подходит)"
			}
			return c
		}
		match := false
		for _, x := range rows {
			c.Actual = append(c.Actual, x.String())
			if x.String() == rec.Value {
				match = true
			}
		}
		if rec.Value == "" {
			return warn("Имя " + rec.Name + " найдено; укажите внешний IPv4 сервера в параметрах записей для сравнения")
		}
		if match {
			if len(rows) > 1 {
				return warn("Ожидаемый IP найден, но есть дополнительные A-записи")
			}
			return ok("Имя " + rec.Name + " указывает на " + rec.Value)
		}
		c.Message = "Имя " + rec.Name + " указывает не на этот сервер (" + rec.Value + ")"
	case "PTR":
		if rec.Name == "" {
			return warn("Укажите внешний IPv4 для обратной проверки")
		}
		rows, e := r.LookupAddr(ctx, rec.Name)
		if e != nil {
			return failLookup(e)
		}
		c.Actual = rows
		for _, x := range rows {
			if dnsHost(x) == dnsHost(rec.Value) {
				ips, e := r.LookupIP(ctx, "ip4", dnsHost(rec.Value))
				if e != nil {
					return failLookup(e)
				}
				for _, ip := range ips {
					if ip.String() == rec.Name {
						return ok("PTR и прямая A-запись согласованы")
					}
				}
				c.Message = "PTR найден, но A не возвращает исходный IP"
				return c
			}
		}
		c.Message = "PTR должен указывать на " + dnsHost(rec.Value) + "; измените его у провайдера IP"
	default:
		rows, e := r.LookupTXT(ctx, rec.Name)
		if e != nil {
			return failLookup(e)
		}
		for _, s := range rows {
			lower := strings.ToLower(strings.TrimSpace(s))
			if (rec.Kind == "SPF" && (lower == "v=spf1" || strings.HasPrefix(lower, "v=spf1 "))) || (rec.Kind == "DMARC" && strings.HasPrefix(lower, "v=dmarc1")) || rec.Kind == "DKIM" {
				c.Actual = append(c.Actual, s)
			}
		}
		if len(c.Actual) == 0 {
			c.Message = "Нужная TXT-запись не найдена на этом имени"
			return c
		}
		if len(c.Actual) > 1 {
			c.Message = "Найдено несколько записей: устраните конфликт"
			return c
		}
		actual := strings.TrimSpace(c.Actual[0])
		switch rec.Kind {
		case "SPF":
			return mailSPFVerdict(c, actual, v)
		case "DKIM":
			tags, valid := dnsTags(actual)
			want, _ := dnsTags(rec.Value)
			if rec.Value == "" {
				return warn("В панели нет публичного ключа DKIM")
			}
			key := strings.Join(strings.Fields(tags["p"]), "")
			if valid && (tags["v"] == "" || tags["v"] == "DKIM1") && (tags["k"] == "" || tags["k"] == "rsa") && key != "" && key == want["p"] {
				if tags["t"] != "" || tags["h"] != "" || tags["s"] != "" {
					return warn("Ключ совпадает, но у DKIM есть дополнительные ограничения; проверьте их")
				}
				return ok("Опубликован действующий ключ панели, селектор itru")
			}
			c.Message = "Публичный ключ или параметры DKIM не совпадают с панелью"
		case "DMARC":
			tags, valid := dnsTags(actual)
			if !valid || tags["v"] != "DMARC1" || (tags["p"] != "none" && tags["p"] != "quarantine" && tags["p"] != "reject") {
				c.Message = "Проверьте формат и политику DMARC"
				return c
			}
			// Любая корректная политика подходит; отличие от шаблона панели ошибкой не считается.
			for _, rua := range strings.Split(tags["rua"], ",") {
				addr := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(rua)), "mailto:")
				if addr != "" && !strings.HasSuffix(addr, "@"+v.Domain) && !strings.HasSuffix(addr, "."+v.Domain) {
					return warn("DMARC опубликован (p=" + tags["p"] + "); внешний получатель отчётов " + addr + " должен отдельно разрешить приём")
				}
			}
			return ok("DMARC опубликован: p=" + tags["p"])
		}
	}
	return c
}
func checkMailDNS(ctx context.Context, r mailDNSResolver, v mailDNSView) []mailDNSCheck {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	result := make([]mailDNSCheck, len(v.Records))
	var wg sync.WaitGroup
	for i, rec := range v.Records {
		wg.Add(1)
		go func(i int, rec mailDNSRecord) { defer wg.Done(); result[i] = mailDNSCheckRecord(ctx, r, v, rec) }(i, rec)
	}
	wg.Wait()
	return result
}

type mailDNSChange struct {
	Kind    string        `json:"kind"`
	Zone    string        `json:"zone"`
	Record  mailDNSRecord `json:"record"`
	Before  []DNSRecord   `json:"before"`
	Blocked string        `json:"blocked"`
}
type mailDNSPlan struct {
	Changes  []mailDNSChange `json:"changes"`
	Token    string          `json:"token"`
	CanApply bool            `json:"can_apply"`
}

func mailDNSZone(owner string) *DNSZone {
	var best *DNSZone
	for _, z := range store.DNSZones {
		if owner == z.Name || strings.HasSuffix(owner, "."+z.Name) {
			if best == nil || len(z.Name) > len(best.Name) {
				best = z
			}
		}
	}
	return best
}
func mailDNSMatches(old *DNSRecord, rec mailDNSRecord, zone string) bool {
	if dnsHost(fqdn(old.Name, zone)) != dnsHost(rec.Name) || old.Type != rec.Type {
		return false
	}
	if rec.Kind == "SPF" {
		s := strings.ToLower(strings.Trim(strings.TrimSpace(old.Value), "\""))
		return s == "v=spf1" || strings.HasPrefix(s, "v=spf1 ") || strings.HasPrefix(s, "v=spf1\"")
	}
	return true
}
func planMailDNS(v mailDNSView) mailDNSPlan {
	p := mailDNSPlan{Changes: []mailDNSChange{}, CanApply: true}
	for _, rec := range v.Records {
		if rec.Kind == "PTR" {
			continue
		}
		c := mailDNSChange{Kind: rec.Kind, Record: rec, Before: []DNSRecord{}}
		z := mailDNSZone(rec.Name)
		if z == nil {
			c.Blocked = "Нет управляемой DNS-зоны для этого имени"
		} else {
			c.Zone = z.Name
			for _, old := range z.Records {
				if dnsHost(fqdn(old.Name, z.Name)) == dnsHost(rec.Name) && old.Type == "CNAME" {
					c.Blocked = "На этом имени есть CNAME. Сначала устраните конфликт в разделе DNS"
				}
				if mailDNSMatches(old, rec, z.Name) {
					c.Before = append(c.Before, *old)
				}
			}
		}
		if rec.Value == "" {
			c.Blocked = "Не задан IP или публичный ключ"
		}
		if c.Blocked != "" {
			p.CanApply = false
		}
		p.Changes = append(p.Changes, c)
	}
	// Include the complete zones and current settings: concurrent edits invalidate approval.
	b, _ := json.Marshal(struct {
		V mailDNSView
		Z []*DNSZone
	}{v, store.DNSZones})
	sum := sha256.Sum256(b)
	p.Token = hex.EncodeToString(sum[:])
	return p
}

// Existing zones only. Preserve unrelated records, validate all files before reloading.
func applyMailDNS(v mailDNSView, token string) (err error) {
	p := planMailDNS(v)
	if p.Token != token {
		return errf("DNS или настройки изменились. Откройте предварительный просмотр ещё раз")
	}
	if !p.CanApply {
		return errf("Не все записи можно применить: проверьте зоны и параметры")
	}
	if !cfg.Dry && !dnsInstalled() {
		return errf("DNS-сервер BIND не установлен")
	}
	oldJSON, _ := json.Marshal(store.DNSZones)
	type backup struct {
		data   []byte
		mode   os.FileMode
		exists bool
	}
	backups := map[string]backup{}
	zones := map[string]*DNSZone{}
	for _, c := range p.Changes {
		zones[c.Zone] = findZone(c.Zone)
	}
	names := []string{}
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		path := dnsDir() + "/" + n + ".db"
		b, e := os.ReadFile(P(path))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		mode := os.FileMode(0644)
		if st, se := os.Stat(P(path)); se == nil {
			mode = st.Mode().Perm()
		}
		backups[path] = backup{b, mode, e == nil}
	}
	reload := func() error {
		if isBSD() {
			return nsdReload()
		}
		if _, e := runTimeout(15*time.Second, "", "rndc", "reload"); e == nil {
			return nil
		} else if !osInfo.Systemd {
			return e
		}
		_, e := runTimeout(15*time.Second, "", "systemctl", "reload", "named")
		return e
	}
	defer func() {
		if err == nil {
			return
		}
		json.Unmarshal(oldJSON, &store.DNSZones)
		var failures []string
		for path, b := range backups {
			var e error
			if b.exists {
				e = writeFile(path, b.data, b.mode)
			} else {
				e = os.Remove(P(path))
				if os.IsNotExist(e) {
					e = nil
				}
			}
			if e != nil {
				failures = append(failures, e.Error())
			}
		}
		if e := reload(); e != nil {
			failures = append(failures, e.Error())
		}
		if len(failures) > 0 {
			err = fmt.Errorf("%w; откат DNS: %s", err, strings.Join(failures, "; "))
		}
	}()
	for _, c := range p.Changes {
		z := zones[c.Zone]
		keep := []*DNSRecord{}
		id := randToken(8)
		ttl := 3600
		for _, old := range z.Records {
			if !mailDNSMatches(old, c.Record, z.Name) {
				keep = append(keep, old)
			} else {
				id = old.ID
				ttl = old.TTL
			}
		}
		name := "@"
		if c.Record.Name != z.Name {
			name = strings.TrimSuffix(c.Record.Name, "."+z.Name)
		}
		rec := &DNSRecord{ID: id, Name: name, Type: c.Record.Type, TTL: ttl, Prio: c.Record.Priority, Value: c.Record.Value}
		if e := validateRecord(rec); e != nil {
			return e
		}
		z.Records = append(keep, rec)
	}
	for _, n := range names {
		z := zones[n]
		z.Serial = nextSerial(z.Serial)
		path := dnsDir() + "/" + n + ".db"
		if e := writeFile(path, []byte(renderZone(z)), 0644); e != nil {
			return e
		}
		if _, e := dnsCheckZone(n, P(path)); e != nil {
			return e
		}
	}
	if !isBSD() {
		if _, e := runTimeout(15*time.Second, "", "named-checkconf"); e != nil {
			return e
		}
	}
	if e := reload(); e != nil {
		return e
	}
	return saveChecked()
}
func registerMailDNS() {
	api("mail.dns", func(q *Req) (any, error) {
		var in struct{ Name string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		defer mu.Unlock()
		return mailDNSViewFor(in.Name)
	})
	apiOp("mail.dns.settings", "Почта: параметры DNS", func(q *Req) (any, error) {
		var in struct {
			Name    string
			Options MailDNSOptions
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		if e := validMailDNSOptions(&in.Options); e != nil {
			return nil, e
		}
		mu.Lock()
		defer mu.Unlock()
		for i := range store.Mail.Domains {
			d := &store.Mail.Domains[i]
			if d.Name == in.Name {
				old := d.DNS
				d.DNS = in.Options
				if e := saveChecked(); e != nil {
					d.DNS = old
					return nil, e
				}
				return mailDNSViewFor(in.Name)
			}
		}
		return nil, errf("Домен не найден")
	})
	api("mail.dns.check", func(q *Req) (any, error) {
		var in struct{ Name string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		if !cfg.Dry {
			serverIPs() // внешний IPv4 для A и PTR, если на интерфейсах его нет (NAT)
		}
		mu.Lock()
		v, e := mailDNSViewFor(in.Name)
		dry := cfg.Dry
		mu.Unlock()
		if e != nil {
			return nil, e
		}
		if dry {
			rows := []mailDNSCheck{}
			for _, rec := range v.Records {
				rows = append(rows, mailDNSCheck{rec.Kind, "unknown", "Демо: внешние DNS-запросы не выполняются", []string{}})
			}
			return map[string]any{"checks": rows, "checked": time.Now()}, nil
		}
		ctx, cancel := context.WithTimeout(q.R.Context(), 6*time.Second)
		r := mailDNSAuthority(ctx, v.Domain)
		ns := mailDNSNameServers(ctx, r.zone)
		cancel()
		return map[string]any{"checks": checkMailDNS(q.R.Context(), r, v), "checked": time.Now(), "nameservers": ns, "zone": r.zone, "ipv4": v.Options.IPv4}, nil
	})
	api("mail.dns.preview", func(q *Req) (any, error) {
		var in struct{ Name string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		defer mu.Unlock()
		v, e := mailDNSViewFor(in.Name)
		if e != nil {
			return nil, e
		}
		return planMailDNS(v), nil
	})
	apiOp("mail.dns.apply", "Почта: применение DNS", func(q *Req) (any, error) {
		var in struct{ Name, Token string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		defer mu.Unlock()
		v, e := mailDNSViewFor(in.Name)
		if e != nil {
			return nil, e
		}
		return nil, applyMailDNS(v, in.Token)
	})
}

// mailSPFVerdict проверяет SPF по смыслу: разрешён ли этот сервер (ip4, mx или a домена) и
// ограничены ли остальные отправители (-all или ~all). include/redirect не вычисляются.
func mailSPFVerdict(c mailDNSCheck, spf string, v mailDNSView) mailDNSCheck {
	ip := net.ParseIP(v.Options.IPv4)
	allowed, via, all, external := false, "", "", false
	for _, term := range strings.Fields(strings.ToLower(spf))[1:] {
		qualifier := "+"
		if strings.ContainsAny(term[:1], "+-~?") {
			qualifier, term = term[:1], term[1:]
		}
		switch {
		case term == "all":
			all = qualifier
		case qualifier != "+":
		case strings.HasPrefix(term, "ip4:"):
			value := strings.TrimPrefix(term, "ip4:")
			if !strings.Contains(value, "/") {
				value += "/32"
			}
			if _, network, err := net.ParseCIDR(value); err == nil && ip != nil && network.Contains(ip) {
				allowed, via = true, term
			}
		case term == "mx", term == "mx:"+v.Domain:
			allowed, via = true, "mx"
		case term == "a", term == "a:"+v.Domain, term == "a:"+v.Hostname:
			allowed, via = true, term
		case strings.HasPrefix(term, "include:"), strings.HasPrefix(term, "redirect="):
			external = true
		}
	}
	switch {
	case !allowed && external:
		c.Status, c.Message = "warn", "SPF ссылается на внешние правила (include/redirect); убедитесь, что в них разрешён IP "+v.Options.IPv4
	case !allowed:
		c.Status, c.Message = "error", "SPF не разрешает отправку с этого сервера: добавьте mx или ip4:"+v.Options.IPv4
	case all != "-" && all != "~":
		c.Status, c.Message = "warn", "SPF разрешает этот сервер ("+via+"), но не ограничивает остальных отправителей: добавьте ~all или -all"
	case all == "~":
		c.Status, c.Message = "ok", "SPF разрешает этот сервер ("+via+"); остальные отправители помечаются (~all)"
	default:
		c.Status, c.Message = "ok", "SPF разрешает этот сервер ("+via+") и запрещает остальных"
	}
	return c
}
