// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Сайт по умолчанию — что открывается при заходе на сервер по IP-адресу или по имени, не
// привязанному ни к одному сайту. Settings.DefaultSite: "" — страница «Сайт не найден»,
// "-" — соединение закрывается, иначе домен сайта (его listen 80 получает default_server).
// HTTPS по IP или чужому имени всегда отклоняется на этапе TLS (ssl_reject_handshake):
// у сервера нет сертификата на IP, а чужой сертификат показывать нельзя.

const (
	nginxDefaultPath = "/etc/nginx/conf.d/itrupanel-default.conf"
	defaultPageRoot  = "/var/www/itrupanel-default"
	defaultSiteDeny  = "-"
)

// Стандартная страница Debian/Ubuntu и мой ручной вариант из подсказки тоже объявляют
// default_server: с ними nginx не запустится, поэтому они отключаются (не удаляются).
const (
	debianDefaultSite = "/etc/nginx/sites-enabled/default"
	manualDenyConf    = "/etc/nginx/conf.d/00-default-deny.conf"
	rhelNginxConf     = "/etc/nginx/nginx.conf"
)

var reRHELDefaultListen = regexp.MustCompile(`(?m)^(\s*listen\s+(?:\[::\]:)?80)\s+default_server;`)

const defaultPageHTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta name="robots" content="noindex"><title>Сайт не найден</title>
<style>body{font:16px/1.5 system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f2f5fc;color:#1b2540}div{text-align:center;padding:24px}h1{font-weight:600;margin:0 0 8px}p{margin:0;color:#5b6787}</style>
<div><h1>Сайт не найден</h1><p>По этому адресу на сервере нет сайта.</p><p lang="en">There is no website at this address.</p></div></html>
`

func defaultSiteMode() string {
	d := store.Settings.DefaultSite
	if d != "" && d != defaultSiteDeny && findSite(d) == nil {
		return ""
	}
	return d
}

func renderNginxDefault(mode string) []byte {
	var b strings.Builder
	b.WriteString("# ITRUPANEL: сервер по умолчанию (IP-адрес и чужие имена). Файл перезаписывается панелью.\n")
	if mode == "" || mode == defaultSiteDeny {
		b.WriteString("server {\n    server_tokens off;\n    listen 80 default_server;\n    listen [::]:80 default_server;\n    server_name _;\n")
		b.WriteString("    location ^~ /.well-known/acme-challenge/ {\n        root " + acmeRoot + ";\n        default_type text/plain;\n        try_files $uri =404;\n    }\n")
		if mode == defaultSiteDeny {
			b.WriteString("    location / {\n        return 444;\n    }\n}\n")
		} else {
			b.WriteString("    root " + defaultPageRoot + ";\n    error_page 404 /index.html;\n    location = /index.html {\n        internal;\n    }\n    location / {\n        return 404;\n    }\n}\n")
		}
	}
	if nginxVersionAtLeast(1, 19, 4) {
		b.WriteString("server {\n    listen 443 ssl default_server;\n    listen [::]:443 ssl default_server;\n    server_name _;\n    ssl_reject_handshake on;\n}\n")
	}
	return []byte(b.String())
}

// disableNginxDefaultConflicts отключает чужие default_server на порту 80.
func disableNginxDefaultConflicts() {
	if fi, err := os.Lstat(P(debianDefaultSite)); err == nil && (fi.Mode()&os.ModeSymlink != 0 || strings.Contains(readFile(debianDefaultSite), "default_server")) {
		os.Remove(P(debianDefaultSite))
	}
	if c := readFile(manualDenyConf); strings.Contains(c, "return 444") && strings.Contains(c, "default_server") {
		os.Rename(P(manualDenyConf), P(manualDenyConf+".disabled"))
	}
	if c := readFile(rhelNginxConf); reRHELDefaultListen.MatchString(c) {
		writeFile(rhelNginxConf+".itrupanel-orig", []byte(c), 0644)
		writeFile(rhelNginxConf, []byte(reRHELDefaultListen.ReplaceAllString(c, "$1;")), 0644)
	}
}

// ensureNginxDefault пишет сервер по умолчанию для текущей настройки. Вызывать под mu;
// перезагрузку nginx выполняет вызывающий.
func ensureNginxDefault() error {
	if err := os.MkdirAll(P(defaultPageRoot), 0755); err != nil {
		return err
	}
	if readFile(defaultPageRoot+"/index.html") != defaultPageHTML {
		if err := writeFile(defaultPageRoot+"/index.html", []byte(defaultPageHTML), 0644); err != nil {
			return err
		}
		relabelWeb(defaultPageRoot)
	}
	disableNginxDefaultConflicts()
	return writeFile(nginxDefaultPath, renderNginxDefault(defaultSiteMode()), 0644)
}

// setDefaultSite переключает сайт по умолчанию: конфиги прежнего и нового сайта и сервер по
// умолчанию пишутся вместе и проверяются одним nginx -t, при ошибке всё возвращается.
func setDefaultSite(next string) (resultErr error) {
	mu.Lock()
	defer mu.Unlock()
	if next != "" && next != defaultSiteDeny && findSite(next) == nil {
		return errf("Сайт не найден")
	}
	old := store.Settings.DefaultSite
	files := map[string]string{nginxDefaultPath: readFile(nginxDefaultPath)}
	var sites []*Site
	for _, d := range []string{old, next} {
		if s := findSite(d); s != nil && !containsSite(sites, s) {
			sites = append(sites, s)
			files[vhostPath(s.Domain)] = readFile(vhostPath(s.Domain))
		}
	}
	store.Settings.DefaultSite = next
	defer func() {
		if resultErr == nil {
			return
		}
		store.Settings.DefaultSite = old
		for path, data := range files {
			if data == "" {
				os.Remove(P(path))
			} else {
				writeFile(path, []byte(data), 0644)
			}
		}
		resultErr = errors.Join(resultErr, nginxTestReload())
	}()
	if err := ensureNginxDefault(); err != nil {
		return err
	}
	for _, s := range sites {
		u := findUser(s.User)
		if u == nil {
			return errf("Нет пользователя %s", s.User)
		}
		b, err := renderSiteNginx(s, u, readFile(customPath(s.Domain)))
		if err != nil {
			return err
		}
		if err = writeFile(vhostPath(s.Domain), b, 0644); err != nil {
			return err
		}
	}
	if err := nginxTestReload(); err != nil {
		return err
	}
	if err := saveChecked(); err != nil {
		return fmt.Errorf("Сохранение настроек: %w", err)
	}
	return nil
}

func containsSite(list []*Site, s *Site) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func registerDefaultSite() {
	api("sites.default", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return map[string]string{"site": defaultSiteMode()}, nil
	})
	apiOp("sites.default.set", "Сайт по умолчанию", func(q *Req) (any, error) {
		var in struct{ Site string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, setDefaultSite(in.Site)
	})
}
