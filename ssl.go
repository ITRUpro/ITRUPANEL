// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const sslDir = "/etc/nginx/itrupanel/ssl"

// selfSigned создаёт ключ ECDSA и самоподписанный сертификат.
func selfSigned(domains []string, days int) (crtPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0], Organization: []string{"ITRUPANEL self-signed"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, d := range domains {
		if ip := net.ParseIP(d); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, d)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}

func parseCertFile(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(P(path))
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errf("Не PEM-сертификат")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func panelCert() (string, string, error) {
	crt := filepath.Join(cfg.Home, "etc", "panel.crt")
	key := filepath.Join(cfg.Home, "etc", "panel.key")
	if _, err := os.Stat(crt); err == nil {
		return crt, key, nil
	}
	host, _ := os.Hostname()
	c, k, err := selfSigned([]string{host, "localhost", "127.0.0.1"}, 3650)
	if err != nil {
		return "", "", err
	}
	os.WriteFile(key, k, 0600)
	os.WriteFile(crt, c, 0644)
	return crt, key, nil
}

var reCertName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$`)

// attachCert подключает сертификат к сайтам, чьи имена он покрывает (под mu).
func attachCert(c *Cert, onlySite string) {
	for _, s := range store.Sites {
		if onlySite != "" && s.Domain != onlySite {
			continue
		}
		if s.SSL != "" && s.SSL != c.Name && onlySite == "" {
			continue
		}
		covered := false
		for _, d := range c.Domains {
			covered = covered || d == s.Domain || (strings.HasPrefix(d, "*.") && strings.HasSuffix(s.Domain, d[1:]))
		}
		if !covered && onlySite == "" {
			continue
		}
		prev, prevRedir := s.SSL, s.RedirectHTTPS
		s.SSL = c.Name
		if prev == "" {
			s.RedirectHTTPS = true
		}
		if err := applySite(s, s.PHP); err != nil {
			s.SSL, s.RedirectHTTPS = prev, prevRedir
			c.LastErr = err.Error()
		}
	}
}

// acmeReachable проверяет имена так же, как Let’s Encrypt: кладёт временный файл в каталог
// HTTP-проверки и запрашивает его по http://имя/ с переходом по перенаправлениям.
var acmeProbeClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

func acmeReachable(names []string) (ok, bad []string) {
	if cfg.Dry || len(names) < 2 {
		return names, nil
	}
	token := "itrupanel-" + randToken(16)
	path := acmeRoot + "/.well-known/acme-challenge/" + token
	if err := writeFile(path, []byte(token), 0644); err != nil {
		return names, nil
	}
	defer os.Remove(P(path))
	relabelWeb(acmeRoot + "/.well-known")
	res := make([]bool, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			r, err := acmeProbeClient.Get("http://" + n + "/.well-known/acme-challenge/" + token)
			if err != nil {
				return
			}
			defer r.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(r.Body, 256))
			res[i] = r.StatusCode == 200 && strings.TrimSpace(string(b)) == token
		}(i, n)
	}
	wg.Wait()
	for i, n := range names {
		if res[i] {
			ok = append(ok, n)
		} else {
			bad = append(bad, n)
		}
	}
	if len(ok) == 0 {
		// Сервер может не видеть сам себя по внешнему адресу (NAT) — тогда решает Let’s Encrypt.
		return names, nil
	}
	return ok, bad
}

func issueLE(name string, domains []string, email string, attach bool) (*Cert, error) {
	if !reCertName.MatchString(name) {
		return nil, errf("Неверное имя сертификата")
	}
	for i, d := range domains {
		a, err := toASCII(d)
		if err != nil {
			return nil, err
		}
		domains[i] = a
	}
	if !have("certbot") {
		if err := pkgInstall("certbot"); err != nil {
			return nil, errf("Не удалось установить certbot: %v", err)
		}
	}
	os.MkdirAll(P(acmeRoot+"/.well-known/acme-challenge"), 0755)
	args := []string{"certonly", "--webroot", "-w", acmeRoot, "--cert-name", name, "--non-interactive", "--agree-tos", "--keep-until-expiring", "--expand"}
	if email != "" && reEmail.MatchString(email) {
		args = append(args, "-m", email)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	hook := "systemctl reload nginx"
	if !osInfo.Systemd {
		hook = "nginx -s reload"
	}
	args = append(args, "--deploy-hook", hook)
	for _, d := range domains {
		args = append(args, "-d", d)
	}
	if out, err := runCertbot(5*time.Minute, args...); err != nil {
		msg := out
		if i := strings.Index(out, "Detail:"); i >= 0 {
			msg = out[i:]
		}
		if len(msg) > 600 {
			msg = msg[:600]
		}
		return nil, errf("certbot: %s", msg)
	}
	c := &Cert{Name: name, Type: "le", Domains: domains, Email: email, Created: time.Now(),
		Crt: "/etc/letsencrypt/live/" + name + "/fullchain.pem", Key: "/etc/letsencrypt/live/" + name + "/privkey.pem"}
	if cfg.Dry {
		cr, k, _ := selfSigned(domains, 90)
		writeFile(c.Crt, cr, 0644)
		writeFile(c.Key, k, 0600)
	}
	if x, err := parseCertFile(c.Crt); err == nil {
		c.Expires, c.Issuer = x.NotAfter, x.Issuer.CommonName
	}
	mu.Lock()
	defer mu.Unlock()
	if old := findCert(name); old != nil {
		*old = *c
		c = old
	} else {
		store.Certs = append(store.Certs, c)
	}
	if attach {
		attachCert(c, "")
	}
	if email != "" && store.Settings.Email == "" {
		store.Settings.Email = email
	}
	save()
	return c, nil
}

// certbot не допускает параллельных запусков: выпуск, продление и автоSSL панели идут по очереди,
// а занятость чужим certbot (таймер продления ОС, ручной запуск) пережидается.
var (
	certbotMu         sync.Mutex
	certbotExec       = func(t time.Duration, args ...string) (string, error) { return runTimeout(t, "", "certbot", args...) }
	certbotRetryDelay = 10 * time.Second
	certbotRetries    = 18
)

const certbotBusy = "Another instance of Certbot is already running"

func runCertbot(t time.Duration, args ...string) (string, error) {
	certbotMu.Lock()
	defer certbotMu.Unlock()
	for i := 0; ; i++ {
		out, err := certbotExec(t, args...)
		if err == nil || !strings.Contains(out, certbotBusy) {
			return out, err
		}
		if i >= certbotRetries {
			return "Другой процесс certbot работает дольше 3 минут. Повторите позже или проверьте на сервере: pgrep -a certbot", err
		}
		time.Sleep(certbotRetryDelay)
	}
}

// renewCerts — ежедневно: certbot renew и обновление сроков.
func renewCerts() {
	mu.Lock()
	hasLE := false
	for _, c := range store.Certs {
		hasLE = hasLE || c.Type == "le"
	}
	mu.Unlock()
	if hasLE {
		runCertbot(10*time.Minute, "renew", "--quiet")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range store.Certs {
		if x, err := parseCertFile(c.Crt); err == nil {
			c.Expires = x.NotAfter
		}
		left := time.Until(c.Expires)
		if c.Type != "self" && left < 14*24*time.Hour {
			addNotice("warn", "certexp:"+c.Name, "Сертификат "+c.Name+" истекает "+c.Expires.Format("02.01.2006"))
		}
	}
	if err := syncPanelTLSLocked(); err != nil {
		addNotice("warn", "panelcert", err.Error())
	}
	if store.Mail.Webmail.Ready {
		if files, e := roundcubeMailFiles(store.Mail); e != nil {
			addNotice("warn", "webmailcert", e.Error())
		} else {
			for p, s := range files {
				if e = writeFile(p, []byte(s), mailFileMode(p)); e != nil {
					addNotice("warn", "webmailcert", e.Error())
					break
				}
				if g := mailFileGroup(p); g != "" {
					run("chown", "root:"+g, p)
				}
			}
		}
		nginxTestReload()
	}
	if store.Mail.Ready {
		if _, err := applyMail(store.Mail); err != nil {
			addNotice("warn", "mailcert", err.Error())
		}
	}
	save()
}

func registerSSL() {
	api("ssl.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]any{}
		for _, c := range store.Certs {
			var sites []string
			for _, s := range store.Sites {
				if s.SSL == c.Name {
					sites = append(sites, s.Domain)
				}
			}
			out = append(out, map[string]any{"cert": c, "sites": sites, "days": int(time.Until(c.Expires).Hours() / 24)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["cert"].(*Cert).Name < out[j]["cert"].(*Cert).Name })
		return out, nil
	})
	apiOp("ssl.le", "SSL: Let’s Encrypt", func(q *Req) (any, error) {
		var in struct {
			Site    string
			WWW     bool
			Aliases bool
			Email   string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		s := findSite(in.Site)
		var domains []string
		if s != nil {
			domains = []string{s.Domain}
			for _, a := range s.Aliases {
				if (a == "www."+s.Domain && in.WWW) || (a != "www."+s.Domain && in.Aliases) {
					domains = append(domains, a)
				}
			}
		}
		mu.Unlock()
		if s == nil {
			return nil, errf("Выберите сайт")
		}
		// Имена, которые ещё ведут на другой сервер, не должны срывать выпуск для остальных.
		ok, skipped := acmeReachable(domains)
		if len(skipped) > 0 && contains(ok, s.Domain) {
			domains = ok
		} else {
			skipped = nil
		}
		c, err := issueLE(s.Domain, domains, in.Email, true)
		if err != nil {
			return nil, err
		}
		if len(skipped) > 0 {
			mu.Lock()
			addNotice("warn", "le-skipped:"+s.Domain, "SSL для "+s.Domain+" выпущен без имён "+strings.Join(skipped, ", ")+": они не открывают этот сервер по HTTP (DNS указывает на другой сервер или порт 80 закрыт). Исправьте DNS и выпустите сертификат снова.")
			save()
			mu.Unlock()
		}
		return map[string]any{"name": c.Name, "domains": c.Domains, "skipped": skipped}, nil
	})
	apiOp("ssl.self", "SSL: самоподписанный", func(q *Req) (any, error) {
		var in struct {
			Name    string
			Domains string
			Days    int
			Site    string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		var domains []string
		for _, d := range strings.Fields(strings.ReplaceAll(in.Domains, ",", " ")) {
			if net.ParseIP(d) == nil {
				a, err := toASCII(strings.TrimPrefix(d, "*."))
				if err != nil {
					return nil, err
				}
				if strings.HasPrefix(d, "*.") {
					a = "*." + a
				}
				d = a
			}
			domains = append(domains, d)
		}
		if len(domains) == 0 {
			return nil, errf("Укажите домены")
		}
		if in.Name == "" {
			in.Name = strings.TrimPrefix(domains[0], "*.") + "-self"
		}
		if !reCertName.MatchString(in.Name) {
			return nil, errf("Имя сертификата: латиница, цифры, точка, дефис")
		}
		if in.Days <= 0 || in.Days > 3650 {
			in.Days = 365
		}
		crt, key, err := selfSigned(domains, in.Days)
		if err != nil {
			return nil, err
		}
		return saveCert(in.Name, "self", crt, key, in.Site)
	})
	apiOp("ssl.import", "SSL: добавление существующего", func(q *Req) (any, error) {
		var in struct{ Name, Crt, Chain, Key, Site string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		full := strings.TrimSpace(in.Crt) + "\n"
		if strings.TrimSpace(in.Chain) != "" {
			full += strings.TrimSpace(in.Chain) + "\n"
		}
		if _, err := tls.X509KeyPair([]byte(full), []byte(in.Key)); err != nil {
			return nil, errf("Сертификат и ключ не подходят друг к другу: %v", err)
		}
		if in.Name == "" {
			blk, _ := pem.Decode([]byte(full))
			if x, err := x509.ParseCertificate(blk.Bytes); err == nil {
				in.Name = strings.TrimPrefix(x.Subject.CommonName, "*.") + "-custom"
			}
		}
		if !reCertName.MatchString(in.Name) {
			return nil, errf("Имя сертификата: латиница, цифры, точка, дефис")
		}
		return saveCert(in.Name, "custom", []byte(full), []byte(strings.TrimSpace(in.Key)+"\n"), in.Site)
	})
	apiOp("ssl.delete", "SSL: удаление", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		c := findCert(in.Name)
		if c == nil {
			return nil, errf("Нет сертификата")
		}
		if store.Settings.PanelCert == c.Name || store.Mail.Certificate == c.Name || store.Mail.Webmail.Certificate == c.Name || mailDomainCertUsed(c.Name) {
			return nil, errf("Сертификат используется панелью или почтой. Сначала выберите другой")
		}
		for _, s := range store.Sites {
			if s.SSL == c.Name {
				s.SSL, s.RedirectHTTPS = "", false
				applySite(s, s.PHP)
			}
		}
		if c.Type == "le" {
			run("certbot", "delete", "--cert-name", c.Name, "--non-interactive")
		} else {
			os.Remove(P(c.Crt))
			os.Remove(P(c.Key))
		}
		var keep []*Cert
		for _, x := range store.Certs {
			if x != c {
				keep = append(keep, x)
			}
		}
		store.Certs = keep
		save()
		return nil, nil
	})
	apiOp("ssl.renew", "SSL: продление", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		c := findCert(in.Name)
		mu.Unlock()
		if c == nil || c.Type != "le" {
			return nil, errf("Продлевать можно только Let’s Encrypt")
		}
		if out, err := runCertbot(5*time.Minute, "renew", "--cert-name", c.Name, "--force-renewal", "--non-interactive"); err != nil {
			return nil, errf("certbot: %s", out)
		}
		renewCerts()
		return nil, nil
	})
}

func saveCert(name, typ string, crt, key []byte, site string) (*Cert, error) {
	mu.Lock()
	defer mu.Unlock()
	if store.Settings.PanelCert == name || store.Mail.Certificate == name || store.Mail.Webmail.Certificate == name || mailDomainCertUsed(name) {
		return nil, errf("Сертификат используется панелью или почтой. Добавьте новый под другим именем и выберите его в настройках")
	}
	if old := findCert(name); old != nil && old.Type == "le" {
		return nil, errf("Имя %s занято сертификатом Let’s Encrypt", name)
	}
	c := &Cert{Name: name, Type: typ, Crt: sslDir + "/" + name + ".crt", Key: sslDir + "/" + name + ".key", Created: time.Now()}
	if err := writeFile(c.Key, key, 0600); err != nil {
		return nil, err
	}
	if err := writeFile(c.Crt, crt, 0644); err != nil {
		return nil, err
	}
	if x, err := parseCertFile(c.Crt); err == nil {
		c.Expires, c.Issuer = x.NotAfter, x.Issuer.CommonName
		c.Domains = x.DNSNames
		for _, ip := range x.IPAddresses {
			c.Domains = append(c.Domains, ip.String())
		}
		if len(c.Domains) == 0 {
			c.Domains = []string{x.Subject.CommonName}
		}
	}
	if old := findCert(name); old != nil {
		*old = *c
		c = old
	} else {
		store.Certs = append(store.Certs, c)
	}
	if site != "" {
		attachCert(c, site)
	} else {
		// обновить сайты, уже использующие этот сертификат
		for _, s := range store.Sites {
			if s.SSL == c.Name {
				applySite(s, s.PHP)
			}
		}
	}
	save()
	return c, nil
}
