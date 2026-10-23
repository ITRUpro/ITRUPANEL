// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func okLibPack(version string, rules string) map[string]any {
	return map[string]any{"schema": 1, "version": version, "released": "2026-10-23", "templates": []map[string]any{
		{"id": "demo", "name": "Демо", "name_en": "Demo", "desc": "Тест", "desc_en": "Test", "rules": rules},
	}}
}

func TestCommittedTemplateLibraryIsSignedAndRenders(t *testing.T) {
	testPanel(t)
	data, err := os.ReadFile("../templates/library.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := tplLibParse(data, releasePublicKey)
	if err != nil {
		t.Fatalf("опубликованная библиотека не проходит проверку: %v", err)
	}
	if len(p.Templates) < 5 {
		t.Fatalf("в библиотеке мало шаблонов: %d", len(p.Templates))
	}
	tplLib.Store(p)
	defer tplLib.Store(nil)
	s, u := nginxTestSite()
	for _, tpl := range p.Templates {
		if !strings.HasPrefix(tpl.ID, "lib-") || tpl.NameEn == "" || tpl.DescEn == "" {
			t.Errorf("%s: нет английского названия или описания", tpl.ID)
		}
		s.Mode, s.Backend, s.Template = "php", "", tpl.ID
		b, err := renderSiteNginx(s, u, "")
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		text := string(b)
		if !strings.Contains(text, "# Шаблон сайта: "+tpl.Name) || strings.Count(text, "location / {") != 1 || !strings.Contains(text, "fastcgi_pass") {
			t.Errorf("%s: шаблон не вписался в конфигурацию", tpl.ID)
		}
		// Шаблоны библиотеки заменяют корневой блок, поэтому режимам с сервером приложения не подходят.
		for _, m := range []struct{ mode, backend string }{{"proxy", ""}, {"php", "apache"}, {"php", "openlitespeed"}} {
			s.Mode, s.Backend, s.ProxyTo = m.mode, m.backend, "http://127.0.0.1:3000"
			if _, err := renderSiteNginx(s, u, ""); err == nil {
				t.Errorf("%s принят в режиме %s/%s", tpl.ID, m.mode, m.backend)
			}
		}
	}
}

func TestTemplateLibraryRejectsBadPacks(t *testing.T) {
	priv, pub := testKey(t)
	_, otherPub := testKey(t)
	good := signPack(t, priv, okLibPack("2026.10.23", "    location / { try_files $uri =404; }\n"))
	if _, err := tplLibParse(good, pub); err != nil {
		t.Fatalf("исправная библиотека отклонена: %v", err)
	}
	if _, err := tplLibParse(good, otherPub); err == nil {
		t.Fatal("чужая подпись принята")
	}
	for name, rules := range map[string]string{
		"include":       "    include /etc/passwd;\n",
		"root":          "    location /x { root /; }\n",
		"alias":         "    location /x { alias /etc/; }\n",
		"proxy_pass":    "    location /x { proxy_pass http://evil.example; }\n",
		"lua":           "    location /x { content_by_lua_block { os.execute('id') } }\n",
		"перл":          "    location /x { perl_set $a 'sub {1}'; }\n",
		"fastcgi чужой": "    location /x { fastcgi_pass 127.0.0.1:9000; }\n",
		"access_log":    "    access_log /etc/cron.d/x;\n",
		"скобки":        "    location / {\n",
		"неизвестная переменная": "    location / { deny %HOME%; }\n",
		"пусто": "   \n",
	} {
		if _, err := tplLibParse(signPack(t, priv, okLibPack("2026.10.23", rules)), pub); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	if _, err := tplLibParse(signPack(t, priv, okLibPack("2026.10.23", "    location /x { include fastcgi_params; fastcgi_pass unix:%SOCK%; }\n")), pub); err != nil {
		t.Errorf("include fastcgi_params отклонён: %v", err)
	}
	okSock := signPack(t, priv, okLibPack("2026.10.23", "    location /x { fastcgi_pass unix:%SOCK%; }\n"))
	if _, err := tplLibParse(okSock, pub); err != nil {
		t.Errorf("fastcgi_pass на сокет сайта отклонён: %v", err)
	}
	for name, m := range map[string]func(map[string]any){
		"схема":  func(p map[string]any) { p["schema"] = 2 },
		"версия": func(p map[string]any) { p["version"] = "latest" },
		"дата":   func(p map[string]any) { p["released"] = "вчера" },
		"поле":   func(p map[string]any) { p["exec"] = "x" },
		"пусто":  func(p map[string]any) { p["templates"] = []map[string]any{} },
		"id":     func(p map[string]any) { p["templates"].([]map[string]any)[0]["id"] = "Bad ID" },
	} {
		p := okLibPack("2026.10.23", "    location / { try_files $uri =404; }\n")
		m(p)
		if _, err := tplLibParse(signPack(t, priv, p), pub); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	if _, err := tplLibParse([]byte(strings.Repeat("x", tplLibMaxBytes+1)), pub); err == nil {
		t.Error("слишком большая библиотека принята")
	}
}

func TestTemplateLibraryUpdateFlow(t *testing.T) {
	mux := testPanel(t)
	priv, pub := testKey(t)
	oldKey, oldURLs := tplLibKey, tplLibURLs
	defer func() { tplLibKey, tplLibURLs, tplLibInsecureOK = oldKey, oldURLs, false; tplLib.Store(nil) }()
	tplLibKey, tplLibInsecureOK = pub, true
	tplLib.Store(nil)
	body := signPack(t, priv, okLibPack("2026.10.23", "    location /demo/ { return 200; }\n"))
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	tplLibURLs = []string{srv.URL + "/a", srv.URL + "/b"}
	update := func() map[string]any { _, r := testAPI(t, mux, "site.templates.library.update", `{}`); return r }
	if r := update(); r["ok"] != true || tplLib.Load() == nil || tplLib.Load().Version != "2026.10.23" {
		t.Fatalf("первое обновление: %v", r)
	}
	if _, err := os.Stat(tplLibPath()); err != nil {
		t.Fatal("библиотека не сохранена")
	}
	// После перезапуска панели сохранённая библиотека подхватывается.
	tplLib.Store(nil)
	tplLibLoad()
	if tplLib.Load() == nil {
		t.Fatal("сохранённая библиотека не прочитана")
	}
	if r := update(); r["ok"] != true || !strings.Contains(fmt.Sprint(r["data"]), "актуальна") {
		t.Fatalf("повторное обновление: %v", r)
	}
	body = signPack(t, priv, okLibPack("2026.09.01", "    location /demo/ { return 200; }\n"))
	if r := update(); r["error"] == nil || tplLib.Load().Version != "2026.10.23" {
		t.Fatalf("откат принят: %v", r)
	}
	if store.Settings.TemplateLibError == "" {
		t.Fatal("ошибка не запомнена")
	}
	body = signPack(t, priv, okLibPack("2026.11.01", "    location /demo/ { return 200; }\n"))
	if r := update(); r["ok"] != true || tplLib.Load().Version != "2026.11.01" || store.Settings.TemplateLibError != "" {
		t.Fatalf("новая версия: %v", r)
	}
	status = 500
	if r := update(); r["error"] == nil || tplLib.Load().Version != "2026.11.01" {
		t.Fatalf("сбой источника: %v", r)
	}
	// Шаблон библиотеки виден в списке и в выборе для сайта, и им можно пользоваться.
	_, list := testAPI(t, mux, "site.templates.list", `{}`)
	found := false
	for _, x := range list["data"].(map[string]any)["templates"].([]any) {
		if m := x.(map[string]any); m["id"] == "lib-demo" && m["library"] == true && m["name_en"] == "Demo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("шаблон библиотеки не показан: %v", list)
	}
	if lib := list["data"].(map[string]any)["library"].(map[string]any); lib["version"] != "2026.11.01" || lib["auto"] != true {
		t.Fatalf("сведения о библиотеке: %v", lib)
	}
	if _, r := testAPI(t, mux, "site.templates.library.settings", `{"auto":false}`); r["ok"] != true || !store.Settings.NoTemplateLibrary {
		t.Fatalf("выключение автообновления: %v", r)
	}
	if _, r := testAPI(t, mux, "site.templates.delete", `{"id":"lib-demo"}`); r["error"] == nil {
		t.Error("шаблон библиотеки удалён")
	}
	// Если шаблон пропал из библиотеки, сайт продолжает работать со стандартными правилами.
	store.Settings.DefaultPHP = "8.3"
	s, err := createOrUpdateSite(siteIn{Domain: "lib.example.test", Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, Template: "lib-demo"})
	if err != nil || s.Template != "lib-demo" || !strings.Contains(readFile(vhostPath(s.Domain)), "location /demo/") {
		t.Fatalf("сайт с шаблоном библиотеки: %v", err)
	}
	tplLib.Store(nil)
	if _, err := createOrUpdateSite(siteIn{Domain: "lib.example.test", Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, Template: "lib-demo"}); err != nil {
		t.Fatalf("сайт сломался после исчезновения шаблона: %v", err)
	}
}
