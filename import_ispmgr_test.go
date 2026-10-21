// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Имитация панели ISPmanager: ответы в формате XML из документации ispmanager.
func fakeISPmanager(t *testing.T) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "text/xml")
		write := func(body string) { w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><doc>` + body + `</doc>`)) }
		fn := r.Form.Get("func")
		if fn == "auth" {
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
				write(`<error type="auth"><msg>Неверное имя пользователя или пароль</msg></error>`)
				return
			}
			write(`<auth id="SESSION123"/>`)
			return
		}
		if r.Form.Get("auth") != "SESSION123" {
			write(`<error type="access"><msg>Нет доступа</msg></error>`)
			return
		}
		switch fn {
		case "webdomain":
			write(`<elem><name>shop.example.com</name><owner>shop</owner><docroot>/var/www/shop/data/www/shop.example.com</docroot><php_mode>php_mode_fcgi_nginxfpm</php_mode><php_version>8.1</php_version></elem>` +
				`<elem><name>wp.example.com</name><owner>wp</owner><docroot>/var/www/wp/data/www/wp.example.com</docroot><php_version>native</php_version></elem>`)
		case "emaildomain":
			write(`<elem><name>shop.example.com</name></elem>`)
		case "email":
			write(`<elem><name>info@shop.example.com</name></elem><elem><name>sales@shop.example.com</name></elem>`)
		case "domain":
			write(`<elem><name>shop.example.com</name></elem>`)
		case "db":
			write(`<elem><pair>shop_user->MySQL</pair><name>shop_db</name></elem>`)
		case "scheduler":
			write(`<elem><command>php /var/www/shop/data/www/shop.example.com/cron.php</command><interval>*/5 * * * *</interval><owner>shop</owner></elem>`)
		default:
			write(`<error><msg>Функция не найдена</msg></error>`)
		}
	}))
}

func TestISPmanagerAPI(t *testing.T) {
	srv := fakeISPmanager(t)
	defer srv.Close()
	ctx := context.Background()
	if _, err := ispmgrLogin(ctx, ImpPanelConf{URL: srv.URL, Login: "admin", Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "Неверное имя пользователя") {
		t.Fatalf("a wrong password must be reported: %v", err)
	}
	c, err := ispmgrLogin(ctx, ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.fp, "SHA256:") {
		t.Fatalf("certificate fingerprint: %q", c.fp)
	}
	inv := &ImpInventory{Services: map[string]bool{}, Sites: []ImpSite{{Domain: "shop.example.com", Mode: "php", Root: "/old/path"}}}
	impFromISPmanager(ctx, c, inv)
	if inv.Panel != "ispmanager" || len(inv.Sites) != 2 {
		t.Fatalf("sites: %+v", inv.Sites)
	}
	by := map[string]ImpSite{}
	for _, s := range inv.Sites {
		by[s.Domain] = s
	}
	if s := by["shop.example.com"]; s.Owner != "shop" || s.PHP != "8.1" || s.Root != "/var/www/shop/data/www/shop.example.com" {
		t.Fatalf("the panel data must refine the nginx data: %+v", s)
	}
	if s := by["wp.example.com"]; s.Owner != "wp" || s.PHP != "" {
		t.Fatalf("native php has no explicit version: %+v", s)
	}
	if len(inv.MailDomains) != 1 || inv.MailDomains[0].Boxes != 2 || len(inv.DNS) != 1 || len(inv.DBs) != 1 || inv.DBs[0].Users[0] != "shop_user" || len(inv.Cron) != 1 || inv.Cron[0].User != "shop" {
		t.Fatalf("inventory: %+v", inv)
	}
}

func TestISPmanagerBadAddress(t *testing.T) {
	for in, want := range map[string]string{"": "Укажите адрес", "ftp://x": "Неверный адрес"} {
		if _, err := impPanelBase(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", in, err)
		}
	}
	if b, _ := impPanelBase("server.example.com"); b != "https://server.example.com:1500" {
		t.Fatalf("default scheme and port: %s", b)
	}
	// Не ISPmanager: ответ не XML.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>nginx</html>x")) }))
	defer ts.Close()
	if _, err := ispmgrLogin(context.Background(), ImpPanelConf{URL: ts.URL, Login: "a", Password: "b"}); err == nil {
		t.Fatal("a non-ISPmanager server must be rejected")
	}
}
