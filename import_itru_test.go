// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeITRU имитирует API старой ITRUPANEL: вход по cookie, заголовок X-Itrupanel, ответы {ok,data}.
func fakeITRU(t *testing.T, terms bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if r.Method != "POST" || r.Header.Get("X-Itrupanel") != "1" {
			write(400, map[string]any{"error": "bad request"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		if name == "login" {
			var in struct{ User, Password string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.User != "admin" || in.Password != "secret" {
				write(200, map[string]any{"error": "Неверный логин или пароль"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
			write(200, map[string]any{"ok": true})
			return
		}
		if c, err := r.Cookie("sid"); err != nil || c.Value != "abc" {
			write(401, map[string]any{"error": "auth"})
			return
		}
		if terms {
			write(403, map[string]any{"error": "Для работы с панелью необходимо принять условия использования", "code": "terms_required"})
			return
		}
		data := map[string]any{
			"sites.list": []map[string]any{
				{"site": map[string]any{"domain": "shop.example.com", "aliases": []string{"www.shop.example.com"}, "user": "shop", "root": "/var/www/shop/data/www/shop.example.com", "php": "8.2", "mode": "php", "ssl": "le-shop"}},
				{"site": map[string]any{"domain": "static.example.com", "user": "st", "root": "/var/www/st/data/www/static.example.com", "mode": "static", "backend": "apache"}},
			},
			"db.list": map[string]any{"rows": []map[string]any{
				{"db": map[string]any{"name": "shop_db", "user": "shop_db", "server": ""}, "size": 2048},
				{"db": map[string]any{"name": "far_db", "user": "far", "server": "mysql8"}, "size": 1},
			}},
			"cron.list": map[string]any{"rows": []map[string]any{
				{"user": "shop", "schedule": "*/5 * * * *", "command": "/usr/bin/php cron.php", "enabled": true},
				{"user": "shop", "schedule": "0 0 * * *", "command": "off", "enabled": false},
			}},
			"me":            map[string]any{"version": "0.1.94", "os": "Debian GNU/Linux 12"},
			"transfer.info": map[string]any{"os": "Debian GNU/Linux 12 (bookworm)", "arch": "amd64", "php_versions": []string{"8.2"}, "sites": []map[string]any{{"domain": "shop.example.com", "size": 4096}}, "dbs": []map[string]any{{"name": "shop_db", "user": "shop_db", "charset": "utf8", "hash": "*0F4A090000000000000000000000000000000000"}}},
			"mail.list":     map[string]any{"boxes": []map[string]any{{"address": "a@shop.example.com"}, {"address": "b@shop.example.com"}}},
			"dns.list":      map[string]any{"rows": []map[string]any{{"name": "shop.example.com"}}},
			"ftp.list":      map[string]any{"rows": []map[string]any{{"name": "shopftp", "home": "/var/www/shop"}}},
		}[name]
		if data == nil {
			write(200, map[string]any{"error": "нет метода"})
			return
		}
		write(200, map[string]any{"ok": true, "data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestITRULogin(t *testing.T) {
	srv := fakeITRU(t, false)
	ctx := context.Background()
	if _, err := itruLogin(ctx, ImpPanelConf{URL: srv.URL, Login: "admin", Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "Неверный логин") {
		t.Fatalf("неверный пароль: %v", err)
	}
	c, err := itruLogin(ctx, ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret"})
	if err != nil || !strings.HasPrefix(c.fp, "SHA256:") {
		t.Fatalf("вход: %v fp=%q", err, c.fp)
	}
	if b, _ := itruPanelBase("server.example.com"); b != "https://server.example.com:3333" {
		t.Fatalf("адрес по умолчанию: %s", b)
	}
	for in, want := range map[string]string{"": "Укажите адрес", "ftp://x": "Неверный адрес"} {
		if _, err := itruPanelBase(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: %v", in, err)
		}
	}
}

func TestImpFromITRUPANEL(t *testing.T) {
	srv := fakeITRU(t, false)
	c, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	// Размер, найденный по SSH, сохраняется; сайт, которого не было в nginx, добавляется.
	inv := &ImpInventory{Services: map[string]bool{}, Sites: []ImpSite{{Domain: "shop.example.com", Size: 777}}, DBs: []ImpDB{{Name: "shop_db"}}}
	if err := impFromITRUPANEL(context.Background(), c, inv); err != nil {
		t.Fatal(err)
	}
	if inv.Panel != "itrupanel" || len(inv.Sites) != 2 {
		t.Fatalf("сайты: %+v", inv.Sites)
	}
	s := inv.Sites[0]
	if s.Size != 4096 || s.Owner != "shop" || s.PHP != "8.2" || s.Root != "/var/www/shop/data/www/shop.example.com" || !s.SSL || len(s.Aliases) != 1 {
		t.Fatalf("shop: %+v", s)
	}
	if !inv.CanTransfer || inv.PHPVersions[0] != "8.2" || inv.OS != "Debian GNU/Linux 12 (bookworm) (amd64)" || s.Size != 4096 || !strings.HasPrefix(inv.PanelFP, "SHA256:") {
		t.Fatalf("сведения для переноса: %+v", inv)
	}
	if inv.Sites[1].Mode != "static" || len(inv.Warnings) != 2 {
		t.Fatalf("static/предупреждения: %+v %v", inv.Sites[1], inv.Warnings)
	}
	if len(inv.DBs) != 1 || inv.DBs[0].Size != 2048 || inv.DBs[0].Users[0] != "shop_db" {
		t.Fatalf("базы: %+v", inv.DBs)
	}
	if len(inv.Cron) != 1 || inv.Cron[0].Schedule != "*/5 * * * *" {
		t.Fatalf("cron: %+v", inv.Cron)
	}
	if len(inv.MailDomains) != 1 || inv.MailDomains[0].Boxes != 2 || len(inv.DNS) != 1 || len(inv.FTP) != 1 {
		t.Fatalf("почта/DNS/FTP: %+v %+v %+v", inv.MailDomains, inv.DNS, inv.FTP)
	}
}

func TestITRUTermsNotAccepted(t *testing.T) {
	srv := fakeITRU(t, true)
	c, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := impFromITRUPANEL(context.Background(), c, &ImpInventory{}); err == nil || !strings.Contains(err.Error(), "не приняты условия") {
		t.Fatalf("ожидалась подсказка про условия: %v", err)
	}
}

func TestITRUNotAPanel(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>hi</html>")) }))
	defer srv.Close()
	if _, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "a", Password: "b"}); err == nil || !strings.Contains(err.Error(), "не похож на ITRUPANEL") {
		t.Fatalf("чужой сервер: %v", err)
	}
}

// Старая панель без выгрузки (до 0.1.94): просмотр возможен, перенос — нет, и сказано почему.
func TestImpFromITRUOldPanel(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/api/") {
		case "login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "sites.list":
			_, _ = w.Write([]byte(`{"ok":true,"data":[{"site":{"domain":"a.example.com","user":"a","root":"/var/www/a/data/www/a.example.com","mode":"static"}}]}`))
		case "me":
			_, _ = w.Write([]byte(`{"ok":true,"data":{"version":"0.1.80","os":"Debian GNU/Linux 12"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	inv := &ImpInventory{}
	if err := impFromITRUPANEL(context.Background(), c, inv); err != nil {
		t.Fatal(err)
	}
	if inv.CanTransfer || len(inv.Sites) != 1 || len(inv.Warnings) == 0 || !strings.Contains(inv.Warnings[len(inv.Warnings)-1], "0.1.80") {
		t.Fatalf("старая панель: %+v", inv)
	}
}

// Сертификат старой панели, запомненный при проверке, должен совпасть при переносе.
func TestITRUFingerprintPinned(t *testing.T) {
	srv := fakeITRU(t, false)
	if _, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret", Fingerprint: "SHA256:wrong"}); err == nil {
		t.Fatal("отпечаток не совпадает, а вход прошёл")
	}
	c, _ := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret"})
	if _, err := itruLogin(context.Background(), ImpPanelConf{URL: srv.URL, Login: "admin", Password: "secret", Fingerprint: c.fp}); err != nil {
		t.Fatalf("верный отпечаток отклонён: %v", err)
	}
}
