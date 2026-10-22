// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import "testing"

func TestPHPReleaseParsing(t *testing.T) {
	for in, want := range map[string]string{
		"PHP 8.3.14 (fpm-fcgi) (built: Nov 21 2024 15:43:54)\nCopyright (c) The PHP Group": "8.3.14",
		"PHP 8.5.0RC1 (fpm-fcgi) (built: Oct  1 2026 00:00:00)":                            "8.5.0RC1",
		"PHP 7.4.33-1+ubuntu22.04.1+deb.sury.org+1 (fpm-fcgi) (built: Aug 1 2023)":         "7.4.33-1+ubuntu22.04.1+deb.sury.org+1",
		"bash: /usr/sbin/php-fpm8.3: No such file or directory":                            "",
	} {
		got := ""
		if m := rePHPRelease.FindStringSubmatch(in); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: %q, ждали %q", in, got, want)
		}
	}
}

func TestPHPListShowsRelease(t *testing.T) {
	mux := testPanel(t)
	_, r := testAPI(t, mux, "php.list", `{}`)
	rows, _ := r["data"].([]any)
	found := map[string]string{}
	for _, x := range rows {
		m := x.(map[string]any)
		rel, _ := m["release"].(string)
		found[m["version"].(string)] = rel
	}
	if found["8.3"] != "8.3.21" || found["8.4"] != "" {
		t.Fatalf("релизы: %v", found)
	}
}

func TestWebServerVersions(t *testing.T) {
	m := reApacheVer.FindStringSubmatch("Server version: Apache/2.4.58 (Ubuntu)\nServer built:   2024-10-26T12:05:06")
	if m == nil || m[1] != "2.4.58" {
		t.Fatalf("apache: %v", m)
	}
	mux := testPanel(t)
	_, r := testAPI(t, mux, "system.info", `{}`)
	v := r["data"].(map[string]any)["versions"].(map[string]any)
	if v["apache"] != "2.4.62" || v["ols"] != "1.8.3" {
		t.Fatalf("versions: %v", v)
	}
}

func TestSitesListShowsWebServerVersion(t *testing.T) {
	mux := testPanel(t)
	mu.Lock()
	store.Sites = append(store.Sites,
		&Site{Domain: "a.example.com", Mode: "php", PHP: "8.3"},
		&Site{Domain: "b.example.com", Mode: "php", PHP: "8.3", Backend: "apache"},
		&Site{Domain: "c.example.com", Mode: "php", PHP: "8.3", Backend: "openlitespeed"},
		&Site{Domain: "d.example.com", Mode: "static", Backend: "apache"})
	mu.Unlock()
	_, r := testAPI(t, mux, "sites.list", `{}`)
	got := map[string]any{}
	for _, x := range r["data"].([]any) {
		m := x.(map[string]any)
		got[m["site"].(map[string]any)["domain"].(string)] = m["web_version"]
	}
	if got["a.example.com"] != "1.26.3" || got["b.example.com"] != "2.4.62" || got["c.example.com"] != "1.8.3" || got["d.example.com"] != "1.26.3" {
		t.Fatalf("версии: %v", got)
	}
}
