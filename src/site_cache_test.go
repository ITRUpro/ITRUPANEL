// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteCacheRendering(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.CacheTTL = 10
	modes := []struct{ name, mode, backend, path, dir string }{
		{"php", "php", "", "fastcgi_cache_path", "fastcgi"},
		{"apache", "php", "apache", "proxy_cache_path", "proxy"},
		{"openlitespeed", "php", "openlitespeed", "proxy_cache_path", "proxy"},
		{"proxy", "proxy", "", "proxy_cache_path", "proxy"},
	}
	for _, m := range modes {
		s.Mode, s.Backend, s.ProxyTo = m.mode, m.backend, "http://127.0.0.1:3000"
		b, err := renderSiteNginx(s, u, "")
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		text := string(b)
		zone := siteCacheZone(s.Domain)
		for _, want := range []string{
			m.path + " " + siteCacheDir(s.Domain) + " levels=1:2 keys_zone=" + zone + ":10m",
			m.dir + "_cache " + zone + ";", m.dir + "_cache_valid 200 301 302 10m;", m.dir + "_cache_bypass $itru_skip_cache;", m.dir + "_no_cache $itru_skip_cache;",
			"set $itru_skip_cache 0;", "add_header X-Cache-Status $upstream_cache_status;", "wordpress_logged_in",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: нет %q\n%s", m.name, want, text)
			}
		}
		if strings.Count(text, "_cache_path ") != 1 || strings.Count(text, "add_header X-Cache-Status") != 1 {
			t.Errorf("%s: повтор строк кэша", m.name)
		}
		// Путь кэша стоит до server {, правила — внутри.
		if strings.Index(text, "_cache_path ") > strings.Index(text, "server {") {
			t.Errorf("%s: путь кэша внутри server", m.name)
		}
	}
	// Выключено, статика, выключенный сайт и ТТЛ 0 — без кэша.
	for name, mut := range map[string]func(*Site){
		"ttl 0":     func(s *Site) { s.CacheTTL = 0 },
		"static":    func(s *Site) { s.Mode, s.Backend = "static", "" },
		"suspended": func(s *Site) { s.Suspended = true },
	} {
		c := *s
		c.Mode, c.Backend = "php", ""
		mut(&c)
		b, err := renderSiteNginx(&c, u, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "_cache") || strings.Contains(string(b), "X-Cache-Status") {
			t.Errorf("%s: кэш не должен включаться", name)
		}
	}
	// Вместе с шаблоном WordPress кэш остаётся только у обработчика PHP.
	s.Mode, s.Backend, s.Template = "php", "", "wordpress"
	b, _ := renderSiteNginx(s, u, "")
	if strings.Count(string(b), "fastcgi_cache "+siteCacheZone(s.Domain)+";") != 1 {
		t.Errorf("кэш с шаблоном WordPress:\n%s", b)
	}
}

func TestSiteCacheTTLValidationAndPurge(t *testing.T) {
	mux := testPanel(t)
	store.Settings.DefaultPHP = "8.3"
	s, err := createOrUpdateSite(siteIn{Domain: "cache.example.test", Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, CacheTTL: 10})
	if err != nil {
		t.Fatal(err)
	}
	if s.CacheTTL != 10 || !strings.Contains(readFile(vhostPath(s.Domain)), "fastcgi_cache_path") {
		t.Fatalf("кэш не включён: %d", s.CacheTTL)
	}
	// Недопустимый срок и статический сайт сбрасываются в «выключено».
	if s2, err := createOrUpdateSite(siteIn{Domain: "cache.example.test", Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, CacheTTL: 7}); err != nil || s2.CacheTTL != 0 {
		t.Fatalf("срок 7 минут принят: %v %v", err, s2)
	}
	if s3, err := createOrUpdateSite(siteIn{Domain: "stat.example.test", Mode: "static", MaxBody: 64, CacheTTL: 10}); err != nil || s3.CacheTTL != 0 {
		t.Fatalf("кэш у статического сайта: %v %v", err, s3)
	}
	// Очистка: файлы удаляются, папка остаётся; без кэша — понятная ошибка.
	if _, r := testAPI(t, mux, "sites.cache.purge", `{"domain":"cache.example.test"}`); r["error"] == nil {
		t.Error("очистка при выключенном кэше разрешена")
	}
	createOrUpdateSite(siteIn{Domain: "cache.example.test", Edit: true, Mode: "php", PHP: "8.3", MaxBody: 64, TryIndex: true, CacheTTL: 30})
	dir := P(siteCacheDir("cache.example.test"))
	os.MkdirAll(filepath.Join(dir, "a/b"), 0700)
	os.WriteFile(filepath.Join(dir, "a/b/entry"), []byte(strings.Repeat("x", 1000)), 0600)
	os.WriteFile(filepath.Join(dir, "top"), []byte("yy"), 0600)
	_, r := testAPI(t, mux, "sites.cache.purge", `{"domain":"cache.example.test"}`)
	if r["error"] != nil || r["data"].(map[string]any)["freed"].(float64) != 1002 {
		t.Fatalf("очистка: %v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("после очистки остались файлы: %v", entries)
	}
	if _, r := testAPI(t, mux, "sites.cache.purge", `{"domain":"nope.example.test"}`); r["error"] == nil {
		t.Error("очистка несуществующего сайта разрешена")
	}
	// Удаление сайта убирает и папку кэша.
	os.MkdirAll(dir, 0700)
	if err := deleteSite("cache.example.test", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("папка кэша осталась после удаления сайта")
	}
}
