// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var activePanelCert atomic.Pointer[tls.Certificate]

// Открытие панели через nginx без порта (https://домен). Состояние копируется из настроек,
// чтобы проверка каждого запроса не брала общий mu.
type panelProxyInfo struct {
	Proxy, Closed  bool
	Domain, Secret string
}

var panelProxy atomic.Pointer[panelProxyInfo]

const panelProxyHeader = "X-Itrupanel-Proxy"

var panelCloseDelay = 30 * time.Second

// refreshPanelProxyLocked вызывается под mu (или при запуске) после изменения настроек.
func refreshPanelProxyLocked() {
	s := store.Settings
	panelProxy.Store(&panelProxyInfo{Proxy: s.PanelProxy && s.PanelDomain != "", Closed: s.PanelProxy && s.PanelDirectClosed && s.PanelDomain != "", Domain: s.PanelDomain, Secret: s.PanelProxySecret})
}

// fromPanelProxy — запрос пришёл от nginx панели: локальное подключение с секретным заголовком.
func fromPanelProxy(r *http.Request) bool {
	p := panelProxy.Load()
	if p == nil || !p.Proxy || p.Secret == "" {
		return false
	}
	got := r.Header.Get(panelProxyHeader)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(p.Secret)) == 1
}

func loopbackPeer(r *http.Request) bool {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.Unmap().IsLoopback()
}

// panelDirectGuard закрывает прямой доступ к порту панели из интернета, если это выбрано.
// С самого сервера (в том числе через SSH-туннель) порт остаётся доступен — так можно
// вернуть доступ, если адрес через nginx перестал работать.
func panelDirectGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := panelProxy.Load(); p != nil && p.Closed && !loopbackPeer(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "ITRUPANEL: прямой доступ к порту панели закрыт. Откройте https://%s/\nITRUPANEL: direct access to the panel port is closed. Open https://%s/\n", p.Domain, p.Domain)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// panelUpstream — адрес панели для nginx. Панель должна слушать все адреса или loopback.
func panelUpstream() (string, error) {
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" {
		return "", errf("Не удалось определить порт панели")
	}
	if host != "" {
		a, e := netip.ParseAddr(host)
		if e != nil || (!a.IsUnspecified() && !a.IsLoopback()) {
			return "", errf("Панель слушает только адрес %s — открыть её через nginx нельзя", host)
		}
	}
	scheme := "https"
	if cfg.Insecure {
		scheme = "http"
	}
	return scheme + "://127.0.0.1:" + port, nil
}

// panelNginxConf — сервер nginx для адреса панели: порт 80 (проверка Let’s Encrypt и переход
// на панель) и, если включено открытие без порта, 443 с проксированием на порт панели.
func panelNginxConf(domain, redirect string, c *Cert, upstream, secret string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ITRUPANEL: panel address\nserver {\n listen 80;\n listen [::]:80;\n server_name %s;\n location ^~ /.well-known/acme-challenge/ { root %s; try_files $uri =404; }\n location / { return 302 %s$request_uri; }\n}\n", domain, acmeRoot, redirect)
	if c == nil {
		return b.String()
	}
	h2, listen := "\n http2 on;", " ssl"
	if nginxOldHTTP2() {
		h2, listen = "", " ssl http2"
	}
	fmt.Fprintf(&b, "server {\n server_tokens off;\n listen 443%s;\n listen [::]:443%s;%s\n server_name %s;\n ssl_certificate %s;\n ssl_certificate_key %s;\n ssl_protocols TLSv1.2 TLSv1.3;\n client_max_body_size 0;\n location / {\n  proxy_pass %s;\n  proxy_http_version 1.1;\n  proxy_set_header Host $host;\n  proxy_set_header X-Real-IP $remote_addr;\n  proxy_set_header X-Forwarded-For $remote_addr;\n  proxy_set_header X-Forwarded-Proto https;\n  proxy_set_header %s %s;\n  proxy_set_header Upgrade $http_upgrade;\n  proxy_set_header Connection $itru_connection_upgrade;\n  proxy_read_timeout 3600s;\n  proxy_send_timeout 3600s;\n  proxy_buffering off;\n  proxy_request_buffering off;\n }\n}\n",
		listen, listen, h2, domain, c.Crt, c.Key, upstream, panelProxyHeader, secret)
	return b.String()
}

// panelDomainSharedLocked — имя панели используется сайтом (тогда порт 80 обслуживает сайт).
func panelDomainSharedLocked(domain string) bool {
	for _, s := range store.Sites {
		for _, d := range siteNames(s) {
			if d == domain {
				return true
			}
		}
	}
	return false
}

// writePanelAddressLocked записывает конфигурацию адреса панели по текущим настройкам (под mu).
// Файл содержит секрет заголовка, поэтому доступен только root.
func writePanelAddressLocked() error {
	s := store.Settings
	path := vhostDir + "/000-itrupanel-address.conf"
	if s.PanelDomain == "" || panelDomainSharedLocked(s.PanelDomain) {
		return nil
	}
	var c *Cert
	upstream := ""
	if s.PanelProxy {
		if c = findCert(s.PanelCert); c == nil {
			return errf("Для открытия без порта нужен сертификат панели")
		}
		var err error
		if upstream, err = panelUpstream(); err != nil {
			return err
		}
	}
	if err := writeFile(path, []byte(panelNginxConf(s.PanelDomain, panelURL(s.PanelDomain), c, upstream, s.PanelProxySecret)), 0600); err != nil {
		return err
	}
	return nginxTestReload()
}

func validatedPair(crt, key, domain string) (*tls.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(P(crt), P(key))
	if err != nil {
		return nil, fmt.Errorf("Сертификат/ключ: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(domain); err != nil {
		return nil, errf("Сертификат не покрывает имя %s", domain)
	}
	if time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
		return nil, errf("Срок действия сертификата ещё не начался или уже истёк")
	}
	pair.Leaf = leaf
	return &pair, nil
}

// Called under mu, only changes the live TLS certificate after full validation.
func syncPanelTLSLocked() error {
	if store.Settings.PanelCert == "" {
		return nil
	}
	c := findCert(store.Settings.PanelCert)
	if c == nil {
		return errf("Сертификат панели не найден")
	}
	pair, err := validatedPair(c.Crt, c.Key, store.Settings.PanelDomain)
	if err != nil {
		return err
	}
	activePanelCert.Store(pair)
	return nil
}
func panelURL(domain string) string {
	if p := panelProxy.Load(); p != nil && p.Proxy && p.Domain == domain {
		return "https://" + domain
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" {
		port = "3333"
	}
	return "https://" + net.JoinHostPort(domain, port)
}
func panelPort() string {
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" {
		return "3333"
	}
	return port
}
func panelChallenge(domain string) (func(), error) {
	mu.Lock()
	defer mu.Unlock()
	if store.Mail.Webmail.Ready && domain == store.Mail.Webmail.Hostname {
		return nil, errf("Имя занято Roundcube. Для панели нужен отдельный адрес")
	}
	if strings.Contains(readFile(vhostDir+"/000-itrupanel-mail-"+domain+".conf"), "server_name "+domain+";") {
		return nil, errf("Имя используется для SSL почты. Для панели нужен отдельный адрес")
	}
	path := vhostDir + "/000-itrupanel-address.conf"
	prev, readErr := os.ReadFile(P(path))
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	undo := func() {
		if os.IsNotExist(readErr) {
			os.Remove(P(path))
		} else {
			writeFile(path, prev, 0644)
		}
		nginxTestReload()
	}
	if panelDomainSharedLocked(domain) {
		if err := os.Remove(P(path)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		// До выпуска сертификата — только порт 80; 443 добавляется после применения настроек
		if err := writeFile(path, []byte(panelNginxConf(domain, panelURL(domain), nil, "", "")), 0600); err != nil {
			return nil, err
		}
	}
	if err := nginxTestReload(); err != nil {
		undo()
		return nil, err
	}
	return undo, nil
}
func registerPanelSettings() {
	api("panel.settings", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		_, upErr := panelUpstream()
		return map[string]any{"domain": store.Settings.PanelDomain, "certificate": store.Settings.PanelCert, "url": panelURL(store.Settings.PanelDomain), "certs": append([]*Cert{}, store.Certs...),
			"proxy": store.Settings.PanelProxy, "close_direct": store.Settings.PanelDirectClosed, "proxy_supported": upErr == nil, "port": panelPort()}, nil
	})
	apiOp("panel.configure", "Настройка адреса и SSL панели", func(q *Req) (any, error) {
		var in struct {
			Domain, Mode, Certificate, Email string
			Proxy                            bool `json:"proxy"`
			CloseDirect                      bool `json:"close_direct"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		domain, err := toASCII(in.Domain)
		if err != nil {
			return nil, err
		}
		if in.CloseDirect && !in.Proxy {
			return nil, errf("Закрыть порт панели можно только при открытии панели без порта")
		}
		if in.Proxy {
			if in.Mode == "self" {
				return nil, errf("Для открытия без порта нужен Let’s Encrypt или установленный сертификат: с самоподписанным браузер не откроет адрес без предупреждения")
			}
			if _, err := panelUpstream(); err != nil {
				return nil, err
			}
			mu.Lock()
			shared := panelDomainSharedLocked(domain)
			mu.Unlock()
			if shared {
				return nil, errf("Имя %s используется сайтом. Для панели без порта нужен отдельный адрес", domain)
			}
		}
		if in.Mode != "le" && in.Mode != "self" && in.Mode != "existing" {
			return nil, errf("Выберите тип сертификата")
		}
		if in.Mode == "existing" {
			mu.Lock()
			c := findCert(in.Certificate)
			if c == nil {
				mu.Unlock()
				return nil, errf("Выберите сертификат")
			}
			_, err = validatedPair(c.Crt, c.Key, domain)
			mu.Unlock()
			if err != nil {
				return nil, err
			}
		}
		id := startJob("panel:configure", "Адрес и SSL панели", q.S.User, func(j *Job) (any, error) {
			j.progress("Настройка адреса панели и HTTP-проверки домена")
			siteSaveMu.Lock()
			defer siteSaveMu.Unlock()
			undo, err := panelChallenge(domain)
			if err != nil {
				return nil, err
			}
			applied := false
			defer func() {
				if !applied {
					undo()
				}
			}()
			name := in.Certificate
			switch in.Mode {
			case "le":
				j.progress("Выпуск Let’s Encrypt: DNS должен указывать на сервер, порт 80 должен быть доступен извне")
				c, err := issueLE("itrupanel-"+strings.ReplaceAll(domain, ".", "-"), []string{domain}, in.Email, false)
				if err != nil {
					return nil, err
				}
				name = c.Name
			case "self":
				j.progress("Создание сертификата для " + domain)
				cr, k, err := selfSigned([]string{domain}, 365)
				if err != nil {
					return nil, err
				}
				c, err := saveCert("panel-"+strings.ReplaceAll(domain, ".", "-"), "self", cr, k, "")
				if err != nil {
					return nil, err
				}
				name = c.Name
			}
			j.progress("Применение сертификата без перезапуска панели")
			mu.Lock()
			defer mu.Unlock()
			oldPair := activePanelCert.Load()
			old := store.Settings
			restore := func() {
				store.Settings.PanelDomain, store.Settings.PanelCert = old.PanelDomain, old.PanelCert
				store.Settings.PanelProxy, store.Settings.PanelDirectClosed, store.Settings.PanelProxySecret = old.PanelProxy, old.PanelDirectClosed, old.PanelProxySecret
				activePanelCert.Store(oldPair)
				refreshPanelProxyLocked()
			}
			store.Settings.PanelDomain, store.Settings.PanelCert = domain, name
			store.Settings.PanelProxy, store.Settings.PanelDirectClosed = in.Proxy, in.Proxy && in.CloseDirect
			if store.Settings.PanelProxySecret == "" {
				store.Settings.PanelProxySecret = randToken(24)
			}
			if err := syncPanelTLSLocked(); err != nil {
				restore()
				return nil, err
			}
			refreshPanelProxyLocked()
			if store.Settings.PanelDirectClosed && !old.PanelDirectClosed {
				// Порт закрывается через полминуты: текущая вкладка успеет показать ссылку на новый адрес
				p := *panelProxy.Load()
				p.Closed = false
				panelProxy.Store(&p)
				time.AfterFunc(panelCloseDelay, func() {
					mu.Lock()
					refreshPanelProxyLocked()
					mu.Unlock()
				})
			}
			if in.Proxy {
				j.progress("Настройка nginx: панель на https://" + domain + " без порта")
			}
			if err := writePanelAddressLocked(); err != nil {
				restore()
				return nil, err
			}
			if err := saveChecked(); err != nil {
				restore()
				return nil, err
			}
			applied = true
			info := "Адрес и сертификат сохранены"
			if store.Settings.PanelDirectClosed && !old.PanelDirectClosed {
				info += fmt.Sprintf(". Через 30 секунд порт %s закроется для доступа из интернета — откройте панель по новому адресу", panelPort())
			}
			return map[string]string{"info": info, "url": panelURL(domain)}, nil
		})
		return map[string]string{"job": id}, nil
	})
}
