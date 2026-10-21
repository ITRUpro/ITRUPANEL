// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"strings"
	"testing"
)

// По IP-адресу открывается страница «Сайт не найден», закрытое соединение или выбранный сайт;
// default_server на порту 80 всегда ровно у одного сервера.
func TestDefaultSiteSwitching(t *testing.T) {
	mux := testPanel(t)
	store.Users = []*SiteUser{{Name: "alice", Home: "/var/www/alice"}}
	a := &Site{Domain: "a.test", User: "alice", Root: "/var/www/alice/data/www/a.test", Mode: "static", Index: "index.html", MaxBody: 64}
	b := &Site{Domain: "b.test", User: "alice", Root: "/var/www/alice/data/www/b.test", Mode: "static", Index: "index.html", MaxBody: 64}
	store.Sites = []*Site{a, b}
	os.MkdirAll(P("/etc/nginx/sites-enabled"), 0755)
	os.Symlink("/etc/nginx/sites-available/default", P(debianDefaultSite))
	os.MkdirAll(P("/etc/nginx/conf.d"), 0755)
	os.WriteFile(P(manualDenyConf), []byte("server {\n listen 80 default_server;\n return 444;\n}\n"), 0644)
	defaults := func() int {
		n := strings.Count(readFile(nginxDefaultPath), "listen 80 default_server")
		for _, s := range store.Sites {
			n += strings.Count(readFile(vhostPath(s.Domain)), "listen 80 default_server")
		}
		return n
	}
	if err := setDefaultSite(""); err != nil {
		t.Fatal(err)
	}
	if conf := readFile(nginxDefaultPath); !strings.Contains(conf, "error_page 404 /index.html") || !strings.Contains(conf, "ssl_reject_handshake on") || defaults() != 1 {
		t.Fatalf("not-found page:\n%s", conf)
	}
	if !strings.Contains(readFile(defaultPageRoot+"/index.html"), "Сайт не найден") {
		t.Fatal("default page missing")
	}
	if _, err := os.Lstat(P(debianDefaultSite)); !os.IsNotExist(err) || fileExists(manualDenyConf) || !fileExists(manualDenyConf+".disabled") {
		t.Fatal("conflicting default servers must be disabled")
	}
	if code, r := testAPI(t, mux, "sites.default.set", `{"site":"a.test"}`); code != 200 || r["error"] != nil {
		t.Fatalf("set: %v", r)
	}
	if !strings.Contains(readFile(vhostPath("a.test")), "listen 80 default_server;") || strings.Contains(readFile(nginxDefaultPath), "listen 80") || defaults() != 1 {
		t.Fatalf("site a must be the default:\n%s", readFile(nginxDefaultPath))
	}
	if conf := readFile(vhostPath("a.test")); !strings.Contains(conf, `if ($host !~* "^(a\.test)$")`) || !strings.Contains(conf, "return 302 http://a.test$request_uri;") {
		t.Fatalf("IP must redirect to the default site domain:\n%s", conf)
	}
	if strings.Contains(readFile(vhostPath("b.test")), "return 302") {
		t.Fatal("only the default site redirects foreign hosts")
	}
	if err := setDefaultSite("b.test"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(vhostPath("a.test")), "default_server") || !strings.Contains(readFile(vhostPath("b.test")), "default_server") || defaults() != 1 {
		t.Fatal("switching must move default_server")
	}
	if err := setDefaultSite("-"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(nginxDefaultPath), "return 444") || defaults() != 1 {
		t.Fatal("deny mode")
	}
	if err := setDefaultSite("missing.test"); err == nil || store.Settings.DefaultSite != "-" {
		t.Fatal("unknown site accepted")
	}
	setDefaultSite("b.test")
	if err := deleteSite("b.test", false); err != nil {
		t.Fatal(err)
	}
	if store.Settings.DefaultSite != "" || !strings.Contains(readFile(nginxDefaultPath), "listen 80 default_server") {
		t.Fatal("deleting the default site must return the not-found page")
	}
}

func TestRHELDefaultServerStripped(t *testing.T) {
	testPanel(t)
	os.MkdirAll(P("/etc/nginx"), 0755)
	os.WriteFile(P(rhelNginxConf), []byte("http {\n    server {\n        listen       80 default_server;\n        listen       [::]:80 default_server;\n    }\n}\n"), 0644)
	if err := ensureNginxDefault(); err != nil {
		t.Fatal(err)
	}
	if c := readFile(rhelNginxConf); strings.Contains(c, "default_server") || !strings.Contains(c, "listen       [::]:80;") {
		t.Fatalf("nginx.conf:\n%s", c)
	}
}
