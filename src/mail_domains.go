// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

func mailDomainHost(m MailState, d MailDomain) string {
	if d.Hostname != "" {
		return d.Hostname
	}
	return m.Hostname
}
func mailDomainCertUsed(name string) bool {
	for _, d := range store.Mail.Domains {
		if d.Certificate == name {
			return true
		}
	}
	return false
}
func checkMailDomains(m MailState) error {
	hosts := map[string]string{}
	for _, d := range m.Domains {
		if d.MaxBoxes < 0 || d.MaxBoxes > 100000 || d.QuotaMB < 0 || d.QuotaMB > 104857600 || d.DefaultQuotaMB < 0 || d.DefaultQuotaMB > 1048576 {
			return errf("Неверные ограничения почтового домена")
		}
		host := mailDomainHost(m, d)
		if d.Hostname != "" {
			if v, e := toASCII(host); e != nil || v != host {
				return errf("Неверное имя почтового сервера")
			}
		}
		if d.Certificate != "" {
			if old, ok := hosts[host]; ok && old != d.Certificate {
				return errf("Для одного имени сервера нужен один сертификат")
			}
			hosts[host] = d.Certificate
		}
		count := 0
		allocated := int64(0)
		found := d.CatchAll == ""
		for _, b := range m.Boxes {
			if !strings.HasSuffix(b.Address, "@"+d.Name) {
				continue
			}
			count++
			allocated += b.QuotaMB
			if d.QuotaMB > 0 && b.QuotaMB == 0 {
				return errf("Для домена с общей квотой каждый ящик должен иметь ограничение")
			}
			if b.Address == d.CatchAll && b.Enabled {
				found = true
			}
		}
		if d.MaxBoxes > 0 && count > d.MaxBoxes {
			return errf("Достигнут лимит ящиков домена %s", d.Name)
		}
		if d.QuotaMB > 0 && allocated > d.QuotaMB {
			return errf("Сумма квот ящиков превышает квоту домена %s", d.Name)
		}
		if !found {
			return errf("Catch All должен указывать на включённый ящик этого домена")
		}
		for _, f := range m.Forwards {
			if f.Address == d.CatchAll {
				return errf("Для Catch All выберите ящик без пересылки")
			}
		}
	}
	return nil
}

// Postfix opens texthash SNI maps with RHS-file support: comma-separated
// PEM filenames are read as key+chain before dropping privileges.
func mailDomainTLS(m MailState) (string, string, error) {
	var smtp, imap strings.Builder
	seen := map[string]bool{}
	for _, d := range m.Domains {
		if d.Certificate == "" {
			continue
		}
		host := mailDomainHost(m, d)
		if seen[host] {
			continue
		}
		seen[host] = true
		c := findCert(d.Certificate)
		if c == nil {
			return "", "", errf("Сертификат почтового домена не найден")
		}
		if _, e := validatedPair(c.Crt, c.Key, host); e != nil {
			return "", "", e
		}
		fmt.Fprintf(&smtp, "%s %s,%s\n", host, c.Key, c.Crt)
		if m.Dovecot24 {
			fmt.Fprintf(&imap, "\nlocal_name %s {\n ssl_server_cert_file = %s\n ssl_server_key_file = %s\n}\n", host, c.Crt, c.Key)
		} else {
			fmt.Fprintf(&imap, "\nlocal_name %s {\n ssl_cert = <%s\n ssl_key = <%s\n}\n", host, c.Crt, c.Key)
		}
	}
	return smtp.String(), imap.String(), nil
}

func registerMailDomains() {
	registerMailReputation()
	registerMailUsage()
	apiOp("mail.box.bulk", "Почта: групповая операция", func(q *Req) (any, error) {
		var in struct {
			Addresses []string
			Action    string
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		if len(in.Addresses) == 0 || len(in.Addresses) > 500 {
			return nil, errf("Выберите от 1 до 500 ящиков")
		}
		if in.Action != "enable" && in.Action != "disable" && in.Action != "delete" {
			return nil, errf("Неверное действие")
		}
		chosen := map[string]bool{}
		for _, a := range in.Addresses {
			chosen[a] = true
		}
		e := changeMail(func(m *MailState) error {
			found := 0
			next := []MailBox{}
			for _, b := range m.Boxes {
				if chosen[b.Address] {
					found++
					if in.Action == "delete" {
						for _, f := range m.Forwards {
							if f.Address == b.Address {
								return errf("Сначала удалите пересылку этого ящика")
							}
						}
						continue
					}
					b.Enabled = in.Action == "enable"
				}
				next = append(next, b)
			}
			if found != len(chosen) {
				return errf("Ящик не найден")
			}
			m.Boxes = next
			return nil
		})
		if e == nil {
			for a := range chosen {
				run("doveadm", "kick", a)
			}
		}
		return nil, e
	})
	apiOp("mail.domain.save", "Почта: настройки домена", func(q *Req) (any, error) {
		var in struct {
			Name           string
			QuotaMB        int64  `json:"quota_mb"`
			DefaultQuotaMB int64  `json:"default_quota_mb"`
			MaxBoxes       int    `json:"max_boxes"`
			CatchAll       string `json:"catch_all"`
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return nil, changeMail(func(m *MailState) error {
			for i := range m.Domains {
				d := &m.Domains[i]
				if d.Name == in.Name {
					d.QuotaMB = in.QuotaMB
					d.DefaultQuotaMB = in.DefaultQuotaMB
					d.MaxBoxes = in.MaxBoxes
					d.CatchAll = strings.ToLower(strings.TrimSpace(in.CatchAll))
					return nil
				}
			}
			return errf("Почтовый домен не найден")
		})
	})
	apiOp("mail.domain.ssl", "Почта: SSL домена", func(q *Req) (any, error) {
		var in struct{ Name, Hostname, Mode, Certificate, Email string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		host, e := toASCII(in.Hostname)
		if e != nil {
			return nil, e
		}
		if in.Mode != "le" && in.Mode != "existing" {
			return nil, errf("Выберите способ установки SSL")
		}
		mu.Lock()
		exists := mailDomainExists(store.Mail, in.Name)
		mu.Unlock()
		if !exists {
			return nil, errf("Почтовый домен не найден")
		}
		id := startJob("mail:ssl:"+in.Name, "SSL почтового домена", q.S.User, func(j *Job) (any, error) {
			mailSetupMu.Lock()
			defer mailSetupMu.Unlock()
			cert := in.Certificate
			if in.Mode == "le" {
				c, e := issueMailHostLE(j, host, in.Email)
				if e != nil {
					return nil, e
				}
				cert = c
			}
			jobStage(j, "Применение сертификата SMTP и IMAP")
			e := changeMail(func(m *MailState) error {
				c := findCert(cert)
				if c == nil {
					return errf("Сертификат не найден")
				}
				if _, e := validatedPair(c.Crt, c.Key, host); e != nil {
					return e
				}
				for i := range m.Domains {
					if m.Domains[i].Name == in.Name {
						// Shared global hostname must present the same certificate to clients without SNI.
						if host == m.Hostname {
							m.Certificate = cert
							for n := range m.Domains {
								if mailDomainHost(*m, m.Domains[n]) == host && m.Domains[n].Certificate != "" {
									m.Domains[n].Certificate = cert
								}
							}
						}
						m.Domains[i].Hostname = host
						m.Domains[i].Certificate = cert
						return nil
					}
				}
				return errf("Почтовый домен не найден")
			})
			return map[string]string{"info": "SSL почтового домена настроен"}, e
		})
		return map[string]string{"job": id}, nil
	})
}

// Dedicated HTTP vhost persists for renewal. Existing site/panel/Roundcube
// vhosts already serve the shared ACME webroot and must not be duplicated.
// issueMailHostLE выпускает Let's Encrypt на имя почтового сервера или веб-почты:
// HTTP-проверка идёт через nginx на порту 80, сайт для этого имени не нужен.
func issueMailHostLE(j *Job, host, email string) (string, error) {
	siteSaveMu.Lock()
	defer siteSaveMu.Unlock()
	jobStage(j, "Подготовка HTTP-проверки почтового домена")
	if e := mailChallenge(host); e != nil {
		return "", e
	}
	jobStage(j, "Выпуск сертификата Let's Encrypt")
	digest := sha256.Sum256([]byte(host))
	c, e := issueLE(fmt.Sprintf("mail-%x", digest[:12]), []string{host}, email, false)
	if e != nil {
		return "", errf("%s. Проверьте, что A-запись %s указывает на этот сервер и порт 80 открыт, или выберите самоподписанный сертификат", e.Error(), host)
	}
	return c.Name, nil
}

func mailChallenge(host string) error {
	mu.Lock()
	defer mu.Unlock()
	shared := store.Settings.PanelDomain == host || (store.Mail.Webmail.Ready && webmailHostOn(store.Mail, host))
	for _, s := range store.Sites {
		for _, name := range siteNames(s) {
			if name == host {
				shared = true
			}
		}
	}
	if shared {
		if store.Mail.Webmail.Ready && webmailHostOn(store.Mail, host) {
			// Upgrade old Roundcube vhosts which redirected the ACME path as well.
			old, e := os.ReadFile(P(webmailVhost))
			if e != nil {
				return e
			}
			conf := string(old)
			if !strings.Contains(conf, "/.well-known/acme-challenge/") {
				conf = strings.Replace(conf, " location / { return 301", " location ^~ /.well-known/acme-challenge/ { root "+acmeRoot+"; try_files $uri =404; }\n location / { return 301", 1)
				if !strings.Contains(conf, "/.well-known/acme-challenge/") {
					return errf("Обновите настройки Roundcube перед выпуском SSL")
				}
				if e := writeFile(webmailVhost, []byte(conf), 0644); e != nil {
					return e
				}
				if e := nginxTestReload(); e != nil {
					writeFile(webmailVhost, old, 0644)
					nginxTestReload()
					return e
				}
			}
		}
		return nil
	}
	path := vhostDir + "/000-itrupanel-mail-" + host + ".conf"
	old, e := os.ReadFile(P(path))
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	conf := fmt.Sprintf("# ITRUPANEL mail ACME\nserver {\n listen 80;\n listen [::]:80;\n server_name %s;\n location ^~ /.well-known/acme-challenge/ { root %s; try_files $uri =404; }\n location / { return 404; }\n}\n", host, acmeRoot)
	if e := writeFile(path, []byte(conf), 0644); e != nil {
		return e
	}
	if err := nginxTestReload(); err != nil {
		if old == nil {
			os.Remove(P(path))
		} else {
			writeFile(path, old, 0644)
		}
		nginxTestReload()
		return err
	}
	return nil
}

func mailCertificateList() []map[string]any {
	out := []map[string]any{}
	for _, c := range store.Certs {
		out = append(out, map[string]any{"name": c.Name, "type": c.Type, "domains": c.Domains, "expires": c.Expires})
	}
	return out
}
