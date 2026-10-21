// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Автоматический SSL: раз в час для сайтов с AutoSSL проверяем, какие имена уже указывают
// на этот сервер, и выпускаем/расширяем сертификат Let's Encrypt. После неудачи — пауза 6 часов.

var (
	pubIPMu   sync.Mutex
	pubIPs    []string
	pubIPTime time.Time
	leTried   = map[string]time.Time{} // сайт → время последней неудачи
)

// serverIPs — адреса интерфейсов и внешний адрес (если сервер за NAT).
func serverIPs() map[string]bool {
	out := map[string]bool{}
	ifs, _ := net.InterfaceAddrs()
	for _, a := range ifs {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil && !ip.IsLoopback() {
			out[ip.String()] = true
		}
	}
	pubIPMu.Lock()
	defer pubIPMu.Unlock()
	if time.Since(pubIPTime) > 6*time.Hour {
		pubIPs = nil
		for _, u := range []string{"https://api.ipify.org", "https://api64.ipify.org"} {
			c := &http.Client{Timeout: 8 * time.Second}
			if r, err := c.Get(u); err == nil {
				b, _ := io.ReadAll(io.LimitReader(r.Body, 64))
				r.Body.Close()
				if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil {
					pubIPs = append(pubIPs, ip.String())
				}
			}
		}
		pubIPTime = time.Now()
	}
	for _, ip := range pubIPs {
		out[ip] = true
	}
	return out
}

// pointsHere — указывает ли DNS-имя на этот сервер.
func pointsHere(name string, mine map[string]bool) bool {
	ips, err := net.LookupHost(name)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !mine[ip] {
			return false
		}
	}
	return true
}

// autoSSLSite выпускает сертификат для сайта, если есть новые имена, указывающие сюда.
func autoSSLSite(domain string, force bool) (string, error) {
	mu.Lock()
	s := findSite(domain)
	if s == nil || !s.AutoSSL || s.Suspended {
		mu.Unlock()
		return "", nil
	}
	if t, ok := leTried[domain]; ok && !force && time.Since(t) < 6*time.Hour {
		mu.Unlock()
		return "", nil
	}
	names := siteNames(s)
	var have []string
	c := findCert(s.SSL)
	if c != nil && c.Type != "le" {
		mu.Unlock()
		return "", nil // свой или самоподписанный сертификат не трогаем
	}
	if c != nil {
		have = c.Domains
	}
	email := store.Settings.Email
	mu.Unlock()

	mine := serverIPs()
	var ready []string
	for _, n := range names {
		if pointsHere(n, mine) {
			ready = append(ready, n)
		}
	}
	if len(ready) == 0 || !contains(ready, domain) {
		return "DNS домена ещё не указывает на этот сервер", nil
	}
	missing := false
	for _, n := range ready {
		missing = missing || !contains(have, n)
	}
	if !missing && c != nil {
		return "", nil
	}
	if _, err := issueLE(domain, ready, email, true); err != nil {
		mu.Lock()
		leTried[domain] = time.Now()
		addNotice("warn", "autossl:"+domain, "Автоматический SSL для "+domain+" не выпущен: "+firstLine(err.Error()))
		save()
		mu.Unlock()
		return "", err
	}
	mu.Lock()
	delete(leTried, domain)
	addNotice("info", "", "SSL Let’s Encrypt для "+domain+" выпущен автоматически ("+strings.Join(ready, ", ")+")")
	save()
	mu.Unlock()
	return "Сертификат выпущен", nil
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func autoSSLLoop() {
	time.Sleep(time.Minute)
	for {
		if !cfg.Dry {
			mu.Lock()
			var ds []string
			for _, s := range store.Sites {
				if s.AutoSSL {
					ds = append(ds, s.Domain)
				}
			}
			mu.Unlock()
			for _, d := range ds {
				func() {
					updateGate.RLock()
					defer updateGate.RUnlock()
					if !panelUpdating() {
						autoSSLSite(d, false)
					}
				}()
			}
		}
		time.Sleep(time.Hour)
	}
}
