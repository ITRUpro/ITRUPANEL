// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestParseGuardIPs(t *testing.T) {
	got, err := parseGuardIPs("203.0.113.5, 10.1.2.3/8\n2001:db8::1  203.0.113.5;fe80::/10")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "203.0.113.5|10.0.0.0/8|2001:db8::1|fe80::/10" {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"999.1.1.1", "1.2.3.4/40", "abc", "1.2.3.4; deny all"} {
		if _, err := parseGuardIPs(bad); err == nil {
			t.Errorf("принят %q", bad)
		}
	}
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, "10.0.0."+itoa(i))
	}
	if _, err := parseGuardIPs(strings.Join(many, " ")); err == nil {
		t.Error("больше 50 адресов принято")
	}
}

func itoa(i int) string { return string(rune('0'+i/10)) + string(rune('0'+i%10)) }

func TestGuardFromInputPassword(t *testing.T) {
	g, err := guardFromInput(siteIn{AuthUser: "client", AuthPassword: "correct-horse"}, SiteGuard{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g.AuthHash, "$2y$10$") || bcrypt.CompareHashAndPassword([]byte(strings.Replace(g.AuthHash, "$2y$", "$2a$", 1)), []byte("correct-horse")) != nil {
		t.Fatalf("хеш пароля: %q", g.AuthHash)
	}
	// Без нового пароля остаётся прежний; без логина защита паролем выключается.
	g2, err := guardFromInput(siteIn{AuthUser: "client"}, g)
	if err != nil || g2.AuthHash != g.AuthHash {
		t.Fatalf("пароль не сохранён: %v", err)
	}
	if g3, _ := guardFromInput(siteIn{}, g); g3.AuthHash != "" || g3.AuthUser != "" {
		t.Fatal("пароль не снят")
	}
	for name, in := range map[string]siteIn{
		"нет пароля":      {AuthUser: "client"},
		"короткий пароль": {AuthUser: "client", AuthPassword: "short"},
		"плохой логин":    {AuthUser: "bad user", AuthPassword: "correct-horse"},
		"плохой IP":       {GuardAdminIPs: "nope"},
	} {
		if _, err := guardFromInput(in, SiteGuard{}); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestSiteGuardRendering(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	// Без защиты в конфигурации ничего лишнего, кроме отключения пароля в проверке Let's Encrypt.
	b, _ := renderSiteNginx(s, u, "")
	for _, no := range []string{"add_header X-Frame-Options", "valid_referers", "auth_basic \"", "geo $", "itru_adm"} {
		if strings.Contains(string(b), no) {
			t.Errorf("защита выключена, но есть %q", no)
		}
	}
	if !strings.Contains(string(b), "auth_basic off;") {
		t.Error("у проверки Let's Encrypt нет auth_basic off")
	}
	s.SSL, s.RedirectHTTPS = "", false
	s.Guard = SiteGuard{Headers: true, Hotlink: true, AuthUser: "client", AuthHash: "$2y$10$abc", AdminIPs: []string{"203.0.113.5", "10.0.0.0/8"}}
	b, err := renderSiteNginx(s, u, "")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		`add_header X-Content-Type-Options "nosniff" always;`, `add_header X-Frame-Options "SAMEORIGIN" always;`, `add_header Referrer-Policy "strict-origin-when-cross-origin" always;`,
		"valid_referers none blocked server_names", `if ($itru_hot = "IR") { return 403; }`,
		"geo $" + guardZone(s.Domain) + " {", "203.0.113.5 1;", "10.0.0.0/8 1;", "127.0.0.1 1;", `if ($itru_adm = "AN") { return 403; }`, `admin-ajax\.php$`,
		"auth_basic \"Доступ ограничен\";", "auth_basic_user_file " + siteAuthFile(s.Domain) + ";",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "Strict-Transport-Security") {
		t.Error("HSTS без SSL")
	}
	if strings.Index(text, "geo $") > strings.Index(text, "server {") {
		t.Error("geo внутри server")
	}
	// HSTS — только когда есть сертификат и включено перенаправление на HTTPS.
	store.Certs = append(store.Certs, &Cert{Name: "c1", Crt: "/c.crt", Key: "/c.key", Domains: []string{s.Domain}})
	s.SSL, s.RedirectHTTPS = "c1", true
	b, _ = renderSiteNginx(s, u, "")
	if !strings.Contains(string(b), "Strict-Transport-Security") {
		t.Error("нет HSTS при SSL и перенаправлении")
	}
	// У выключенного сайта защита не нужна: остаётся заглушка.
	s.Suspended = true
	b, _ = renderSiteNginx(s, u, "")
	if strings.Contains(string(b), "itru_adm") || strings.Contains(string(b), "auth_basic \"") {
		t.Error("правила защиты у выключенного сайта")
	}
}

func TestSiteGuardCreateFileAndDelete(t *testing.T) {
	testPanel(t)
	store.Settings.DefaultPHP = "8.3"
	s, err := createOrUpdateSite(siteIn{Domain: "guard.example.test", Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, AuthUser: "client", AuthPassword: "correct-horse", GuardHeaders: true})
	if err != nil {
		t.Fatal(err)
	}
	line := readFile(siteAuthFile(s.Domain))
	if !strings.HasPrefix(line, "client:$2y$10$") {
		t.Fatalf("файл паролей: %q", line)
	}
	// Правка без нового пароля сохраняет прежний; пустой логин снимает защиту и удаляет файл.
	if _, err := createOrUpdateSite(siteIn{Domain: s.Domain, Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, AuthUser: "client"}); err != nil || readFile(siteAuthFile(s.Domain)) != line {
		t.Fatalf("пароль потерян: %v", err)
	}
	if _, err := createOrUpdateSite(siteIn{Domain: s.Domain, Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true}); err != nil || fileExists(siteAuthFile(s.Domain)) {
		t.Fatalf("защита паролем не снята: %v", err)
	}
	createOrUpdateSite(siteIn{Domain: s.Domain, Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, AuthUser: "a1", AuthPassword: "another-pass"})
	if err := deleteSite(s.Domain, false); err != nil || fileExists(siteAuthFile(s.Domain)) {
		t.Fatalf("файл паролей остался после удаления сайта: %v", err)
	}
}

func TestSiteGuardHashIsNotSentToTheBrowser(t *testing.T) {
	mux := testPanel(t)
	store.Settings.DefaultPHP = "8.3"
	if _, err := createOrUpdateSite(siteIn{Domain: "secret.example.test", Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, AuthUser: "client", AuthPassword: "correct-horse"}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"sites.list", "sites.get"} {
		_, r := testAPI(t, mux, call, `{"domain":"secret.example.test"}`)
		raw, _ := json.Marshal(r["data"])
		if strings.Contains(string(raw), "$2y$") || strings.Contains(string(raw), "auth_hash") {
			t.Fatalf("%s отдаёт хеш пароля: %s", call, raw)
		}
		if !strings.Contains(string(raw), `"auth_set":true`) || !strings.Contains(string(raw), `"auth_user":"client"`) {
			t.Fatalf("%s не показывает, что пароль задан: %s", call, raw)
		}
	}
	// Состояние на диске хеш хранит (иначе после перезапуска панели защита пропала бы).
	if findSite("secret.example.test").Guard.AuthHash == "" {
		t.Fatal("хеш пропал из состояния")
	}
}
