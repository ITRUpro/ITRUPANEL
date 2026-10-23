// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinSiteTemplatesAreValidAndRender(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	seen := map[string]bool{}
	for _, tpl := range siteTemplateBuiltins {
		if seen[tpl.ID] || !reTemplateID.MatchString(tpl.ID) || tpl.Name == "" || tpl.Desc == "" {
			t.Fatalf("шаблон %q описан неверно", tpl.ID)
		}
		seen[tpl.ID] = true
		if err := validateSiteTemplateRules(tpl.Rules); err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		for _, mode := range []string{"php", "static"} {
			s.Mode, s.Backend, s.Template = mode, "", tpl.ID
			b, err := renderSiteNginx(s, u, "")
			if err != nil {
				// Шаблоны для PHP не подходят статическому сайту (см. TestSiteTemplatesMatchSiteModes).
				if mode == "static" && strings.Contains(err.Error(), "nginx + PHP-FPM") {
					continue
				}
				t.Fatalf("%s/%s: %v", tpl.ID, mode, err)
			}
			text := string(b)
			if strings.Contains(text, "%SOCK%") || strings.Contains(text, "%DOMAIN%") || strings.Contains(text, "%ROOT%") {
				t.Fatalf("%s: остались неподставленные переменные", tpl.ID)
			}
			if tpl.Rules != "" && !strings.Contains(text, "# Шаблон сайта: "+tpl.Name) {
				t.Fatalf("%s: правила шаблона не попали в конфигурацию", tpl.ID)
			}
			// Основа панели не теряется: проверка Let's Encrypt, закрытие служебных файлов, PHP-обработчик.
			if !strings.Contains(text, "/.well-known/acme-challenge/") || !strings.Contains(text, `location ~ /\.(?!well-known)`) {
				t.Fatalf("%s: потеряна основа панели", tpl.ID)
			}
			if mode == "php" && !strings.Contains(text, "fastcgi_pass") {
				t.Fatalf("%s: потерян обработчик PHP", tpl.ID)
			}
			// Ровно один location / — шаблон заменяет его, а не дублирует.
			if n := strings.Count(text, "location / {"); n != 1 {
				t.Fatalf("%s/%s: location / встречается %d раз", tpl.ID, mode, n)
			}
		}
	}
	for _, id := range []string{"plain", "hardened", "wordpress", "laravel", "joomla", "bitrix", "spa"} {
		if !seen[id] {
			t.Errorf("нет шаблона %s", id)
		}
	}
}

func TestSiteTemplateVariablesAndSuspend(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	store.Settings.SiteTemplates = []SiteTemplate{{ID: "u-mine", Name: "Мой", Rules: "    location /api/ { fastcgi_pass unix:%SOCK%; }\n    # %DOMAIN% %USER% %PHP% %ROOT%\n"}}
	s.Template = "u-mine"
	b, err := renderSiteNginx(s, u, "")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"fastcgi_pass unix:" + sockPath(s.Domain) + ";", "# example.test test 8.3 " + s.Root} {
		if !strings.Contains(text, want) {
			t.Fatalf("нет %q:\n%s", want, text)
		}
	}
	// Выключенный сайт: правила шаблона не должны обходить заглушку.
	s.Suspended = true
	b, _ = renderSiteNginx(s, u, "")
	if strings.Contains(string(b), "location /api/") || !strings.Contains(string(b), `return 503 "`) {
		t.Fatal("шаблон обходит выключение сайта")
	}
	// Неизвестный шаблон у сайта безопасно считается стандартным.
	s.Suspended, s.Template = false, "u-deleted"
	if _, err := renderSiteNginx(s, u, ""); err != nil {
		t.Fatal(err)
	}
}

func TestSiteTemplateConflictsAndValidation(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.Template = "wordpress"
	if _, err := renderSiteNginx(s, u, x3Custom); err == nil || !strings.Contains(err.Error(), "location /") {
		t.Fatalf("шаблон и свои правила с location / должны конфликтовать: %v", err)
	}
	for _, bad := range []string{"location / {", "location / } {", `set $x "unterminated;`, "location / { deny %OTHER%; }"} {
		if validateSiteTemplateRules(bad) == nil {
			t.Errorf("принят %q", bad)
		}
	}
	for _, ok := range []string{"", "location /a { return 200 \"}\"; } # {", "location ~ \\.php$ { deny all; }"} {
		if err := validateSiteTemplateRules(ok); err != nil {
			t.Errorf("отклонён %q: %v", ok, err)
		}
	}
}

func TestSiteTemplateAPIFlow(t *testing.T) {
	mux := testPanel(t)
	s, u := nginxTestSite()
	store.Sites = append(store.Sites, s)
	store.Users = append(store.Users, u)
	_, r := testAPI(t, mux, "site.templates.list", `{}`)
	rows := r["data"].(map[string]any)["templates"].([]any)
	if len(rows) < 7 {
		t.Fatalf("список: %d", len(rows))
	}
	save := func(m map[string]string) map[string]any {
		b, _ := json.Marshal(m)
		_, r := testAPI(t, mux, "site.templates.save", string(b))
		return r
	}
	if r := save(map[string]string{"name": "Мой шаблон", "rules": "    location /x/ { return 200; }\n"}); r["error"] != nil {
		t.Fatal(r)
	}
	if len(store.Settings.SiteTemplates) != 1 {
		t.Fatal("шаблон не сохранён")
	}
	id := store.Settings.SiteTemplates[0].ID
	if r := save(map[string]string{"name": "Мой шаблон", "rules": ""}); r["error"] == nil {
		t.Error("повторное название принято")
	}
	if r := save(map[string]string{"id": "wordpress", "name": "WP2", "rules": ""}); r["error"] == nil {
		t.Error("встроенный шаблон изменён")
	}
	if r := save(map[string]string{"name": "Плохой", "rules": "location / {"}); r["error"] == nil {
		t.Error("непарная скобка принята")
	}
	// Сайт использует шаблон: удалить нельзя, изменение попадает в конфигурацию сайта.
	s.Template = id
	if r := save(map[string]string{"id": id, "name": "Мой шаблон", "rules": "    location /y/ { return 200; }\n"}); r["error"] != nil {
		t.Fatal(r)
	}
	if got := readFile(vhostPath(s.Domain)); !strings.Contains(got, "location /y/") {
		t.Fatalf("новая версия шаблона не применена к сайту:\n%s", got)
	}
	if _, r := testAPI(t, mux, "site.templates.delete", `{"id":"`+id+`"}`); r["error"] == nil {
		t.Error("шаблон удалён, хотя его использует сайт")
	}
	_, pv := testAPI(t, mux, "site.templates.preview", `{"domain":"`+s.Domain+`","id":"wordpress"}`)
	if cfgText, _ := pv["data"].(map[string]any)["config"].(string); !strings.Contains(cfgText, "wp-config") || strings.Contains(cfgText, "location /y/") {
		t.Fatalf("просмотр: %v", pv)
	}
	if siteTemplatePreview != nil || s.Template != id {
		t.Fatal("просмотр изменил состояние")
	}
	s.Template = ""
	if _, r := testAPI(t, mux, "site.templates.delete", `{"id":"`+id+`"}`); r["error"] != nil {
		t.Fatal(r)
	}
	if len(store.Settings.SiteTemplates) != 0 {
		t.Fatal("шаблон не удалён")
	}
}

// Шаблон не должен ломать сайт: в режимах «прокси», Apache и OpenLiteSpeed запросы идут серверу приложения,
// а шаблоны для PHP работают только в режиме «nginx + PHP-FPM».
func TestSiteTemplatesMatchSiteModes(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	modes := []struct{ name, mode, backend string }{
		{"php", "php", ""}, {"static", "static", ""}, {"proxy", "proxy", ""}, {"apache", "php", "apache"}, {"openlitespeed", "php", "openlitespeed"},
	}
	want := map[string]map[string]bool{
		"plain":     {"php": true, "static": true, "proxy": true, "apache": true, "openlitespeed": true},
		"hardened":  {"php": true, "static": true, "proxy": true, "apache": true, "openlitespeed": true},
		"wordpress": {"php": true},
		"laravel":   {"php": true},
		"joomla":    {"php": true},
		"bitrix":    {"php": true},
		"spa":       {"php": true, "static": true},
	}
	for id, ok := range want {
		for _, m := range modes {
			s.Mode, s.Backend, s.Template, s.ProxyTo = m.mode, m.backend, id, "http://127.0.0.1:3000"
			b, err := renderSiteNginx(s, u, "")
			if ok[m.name] && err != nil {
				t.Errorf("%s в режиме %s должен подходить: %v", id, m.name, err)
			}
			if !ok[m.name] && err == nil {
				t.Errorf("%s в режиме %s принят, хотя сломал бы сайт", id, m.name)
			}
			// В режимах с сервером приложения проксирование остаётся на месте.
			if err == nil && (m.name == "proxy" || m.name == "apache" || m.name == "openlitespeed") && !strings.Contains(string(b), "proxy_pass") {
				t.Errorf("%s в режиме %s: пропало проксирование", id, m.name)
			}
		}
	}
	// Свой шаблон проверяется по содержимому, а не по названию.
	store.Settings.SiteTemplates = []SiteTemplate{
		{ID: "u-deny", Name: "Только запреты", Rules: "    location ~* \\.bak$ { deny all; }\n"},
		{ID: "u-root", Name: "Свой корень", Rules: "    location / { try_files $uri =404; }\n"},
	}
	s.Mode, s.Backend = "php", "apache"
	s.Template = "u-deny"
	if _, err := renderSiteNginx(s, u, ""); err != nil {
		t.Errorf("шаблон только с запретами должен подходить любому режиму: %v", err)
	}
	s.Template = "u-root"
	if _, err := renderSiteNginx(s, u, ""); err == nil {
		t.Error("свой шаблон с location / принят для Apache")
	}
}
