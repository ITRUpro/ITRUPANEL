// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

// Проверка Let’s Encrypt должна обходить перенаправления на уровне server, в том числе из своих правил.
func TestSiteACMEBeforeRedirects(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.RedirectHTTPS, s.WWWRedirect = true, "from_www"
	custom := "if ($host != " + s.Domain + ") { return 301 https://" + s.Domain + "$request_uri; }\n"
	writeFile(customPath(s.Domain), []byte(custom), 0644)
	b, err := renderSiteNginx(s, u, custom)
	if err != nil {
		t.Fatal(err)
	}
	conf := string(b)
	brk := strings.Index(conf, `if ($request_uri ~ "^/\.well-known/acme-challenge/") {`)
	if brk < 0 || !strings.Contains(conf[brk:], "break;") {
		t.Fatalf("ACME break missing:\n%s", conf)
	}
	for _, later := range []string{"return 301 $scheme://" + s.Domain + "$request_uri;", "include " + customPath(s.Domain)} {
		if i := strings.Index(conf, later); i < brk {
			t.Fatalf("%q must come after the ACME break:\n%s", later, conf)
		}
	}

	store.Sites, store.Users = []*Site{s}, []*SiteUser{u}
	writeFile(vhostPath(s.Domain), []byte("# ITRUPANEL: old config"), 0644)
	for _, m := range panelMigrations() {
		if m.ID != "019-site-acme-before-redirects" {
			continue
		}
		paths, err := m.Paths()
		if err != nil || len(paths) != 1 {
			t.Fatalf("snapshot: %v %v", paths, err)
		}
		if err = m.Apply(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(readFile(vhostPath(s.Domain)), "acme-challenge/\") {") {
			t.Fatal("migration did not rewrite the site config")
		}
		return
	}
	t.Fatal("migration 019 not found")
}
