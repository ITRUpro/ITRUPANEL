// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// /usr/local is read-only inside distribution PHP-FPM systemd sandboxes.
// Runtime data belongs under /var/lib, outside the web root.
const pmaDataDir = "/var/lib/itrupanel/phpmyadmin"

func preparePMADirs() error {
	// PHP must be able to traverse the root-owned parent as well.
	if err := os.MkdirAll(P("/var/lib/itrupanel"), 0755); err != nil {
		return err
	}
	if err := os.Chmod(P("/var/lib/itrupanel"), 0755); err != nil {
		return err
	}
	for _, suffix := range []string{"", "/sessions", "/tmp"} {
		path := pmaDataDir + suffix
		if err := os.MkdirAll(P(path), 0700); err != nil {
			return err
		}
		if err := os.Chmod(P(path), 0700); err != nil {
			return err
		}
		if _, err := run("chown", osInfo.WebUser+":"+osInfo.WebUser, path); err != nil {
			return err
		}
	}
	return labelPMAPath(pmaDataDir)
}

func labelPMAPath(path string) error {
	if osInfo.Family != "rhel" || cfg.Dry {
		return nil
	}
	return applyPMASELinuxContext(path, run)
}

// Persist the file-context mapping, then restore the complete label. chcon -t
// only changes the type and fails on files created without any SELinux label.
func applyPMASELinuxContext(path string, command func(string, ...string) (string, error)) error {
	state, err := command("getenforce")
	if err != nil {
		return fmt.Errorf("phpMyAdmin: SELinux status: %w", err)
	}
	switch strings.TrimSpace(state) {
	case "Disabled":
		return nil
	case "Enforcing", "Permissive":
		// Both enabled modes need labels that remain valid after a relabel.
	default:
		return fmt.Errorf("phpMyAdmin: unknown SELinux status %q", state)
	}
	pattern := regexp.QuoteMeta(path) + "(/.*)?"
	if _, addErr := command("semanage", "fcontext", "-a", "-t", "httpd_sys_rw_content_t", pattern); addErr != nil {
		if _, err := command("semanage", "fcontext", "-m", "-t", "httpd_sys_rw_content_t", pattern); err != nil {
			return fmt.Errorf("phpMyAdmin: SELinux rule for %s: add: %v; modify: %w", path, addErr, err)
		}
	}
	if _, err := command("restorecon", "-RF", path); err != nil {
		return fmt.Errorf("phpMyAdmin: SELinux labels for %s: %w", path, err)
	}
	return nil
}

type pmaLink struct {
	session *session
	expires time.Time
}

// Each opening gets an independent, one-hour URL. The panel session is still
// required on EVERY request; a copied URL grants no access to another browser.
func pmaProxy() http.Handler { return newPMAProxy(time.Now, nil) }

func newPMAProxy(now func() time.Time, transport http.RoundTripper) http.Handler {
	target, _ := url.Parse("http://" + pmaPort)
	var linkMu sync.Mutex
	links := map[string]pmaLink{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		s := getSession(r)
		if s == nil {
			http.Redirect(w, r, "/#/login", http.StatusFound)
			return
		}
		if r.URL.Path == "/phpmyadmin/" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			token := randToken(32)
			linkMu.Lock()
			for k, v := range links {
				if now().After(v.expires) {
					delete(links, k)
				}
			}
			// Bound memory even if an authenticated client opens links repeatedly.
			if len(links) >= 256 {
				oldest := ""
				var expiry time.Time
				for k, v := range links {
					if oldest == "" || v.expires.Before(expiry) {
						oldest, expiry = k, v.expires
					}
				}
				delete(links, oldest)
			}
			links[token] = pmaLink{s, now().Add(time.Hour)}
			linkMu.Unlock()
			http.Redirect(w, r, "/phpmyadmin/"+token+"/", http.StatusFound)
			return
		}
		token, tail, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/phpmyadmin/"), "/")
		linkMu.Lock()
		link, found := links[token]
		linkMu.Unlock()
		if !ok || !found || link.session != s || now().After(link.expires) {
			http.Error(w, "Ссылка недействительна или истекла. Откройте phpMyAdmin из панели ещё раз.", http.StatusNotFound)
			return
		}
		prefix := "/phpmyadmin/" + token + "/"
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		publicURL := scheme + "://" + r.Host + prefix
		proxy := &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = "/phpmyadmin/" + tail
				pr.Out.URL.RawPath = ""
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
				pr.Out.Header.Set("X-Itrupanel-Pma-Url", publicURL)
				// Never give the PHP application the panel's authentication cookie.
				pr.Out.Header.Del("Cookie")
				for _, c := range pr.In.Cookies() {
					if c.Name != sessCookie {
						pr.Out.AddCookie(c)
					}
				}
			},
			ModifyResponse: func(res *http.Response) error {
				res.Header.Set("Cache-Control", "no-store")
				res.Header.Set("Referrer-Policy", "no-referrer")
				if loc := res.Header.Get("Location"); strings.HasPrefix(loc, "/phpmyadmin/") && !strings.HasPrefix(loc, prefix) {
					res.Header.Set("Location", prefix+strings.TrimPrefix(loc, "/phpmyadmin/"))
				}
				cookies := res.Cookies()
				res.Header.Del("Set-Cookie")
				for _, c := range cookies {
					if c.Name == sessCookie {
						continue
					}
					c.Path, c.Domain, c.Secure = prefix, "", r.TLS != nil
					res.Header.Add("Set-Cookie", c.String())
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, `<p>phpMyAdmin не установлен или не запущен. Установите его в разделе «Базы данных».</p>`)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}
