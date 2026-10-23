// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Кэш страниц сайта на стороне nginx (FastCGI-кэш для PHP-FPM, прокси-кэш для Apache, OpenLiteSpeed и приложений).
// Включается одним выбором срока в форме сайта. Панель не кэширует запросы с данными (POST, ссылки с параметрами),
// администраторов, вошедших пользователей и корзины, а ответы с Set-Cookie nginx не кэширует сам.

const siteCacheRoot = "/var/cache/nginx/itrupanel"

var siteCacheTTLs = []int{0, 1, 5, 10, 30, 60, 360, 1440}

func validCacheTTL(n int) bool {
	for _, v := range siteCacheTTLs {
		if v == n {
			return true
		}
	}
	return false
}

// siteCacheOn — кэш включён и сайту есть что кэшировать (статические сайты nginx отдаёт сам).
func siteCacheOn(s *Site) bool { return s.CacheTTL > 0 && s.Mode != "static" && !s.Suspended }

func siteCacheDir(domain string) string { return siteCacheRoot + "/" + domain }

func siteCacheZone(domain string) string {
	return fmt.Sprintf("itru_%x", sha256.Sum256([]byte(domain)))[:17]
}

// cacheHTTP — строка для области http (перед server): место и зона кэша сайта.
func cacheHTTP(s *Site) string {
	if !siteCacheOn(s) {
		return ""
	}
	kind := "fastcgi_cache_path"
	if !(s.Mode == "php" && s.Backend == "") {
		kind = "proxy_cache_path"
	}
	return fmt.Sprintf("%s %s levels=1:2 keys_zone=%s:10m max_size=512m inactive=60m use_temp_path=off;\n", kind, siteCacheDir(s.Domain), siteCacheZone(s.Domain))
}

// cacheDirectives — правила кэша внутри location: kind «fastcgi» или «proxy».
func cacheDirectives(s *Site, kind string) string {
	if !siteCacheOn(s) {
		return ""
	}
	lines := []string{
		kind + "_cache " + siteCacheZone(s.Domain) + ";",
		kind + "_cache_key \"$scheme$request_method$host$request_uri\";",
		fmt.Sprintf("%s_cache_valid 200 301 302 %dm;", kind, s.CacheTTL),
		kind + "_cache_valid 404 1m;",
		kind + "_cache_bypass $itru_skip_cache;",
		kind + "_no_cache $itru_skip_cache;",
		kind + "_cache_lock on;",
		kind + "_cache_use_stale error timeout updating http_500 http_503;",
	}
	return "        " + strings.Join(lines, "\n        ")
}

// ensureSiteCache создаёт папку кэша с правами веб-сервера (и меткой SELinux на RHEL-системах).
func ensureSiteCache(s *Site) {
	if !siteCacheOn(s) || cfg.Dry {
		return
	}
	dir := siteCacheDir(s.Domain)
	// Общие папки доступны для обхода, сама папка сайта — только веб-серверу (иначе рабочий процесс nginx
	// не сможет ни войти в неё, ни писать кэш, и страницы отдают ошибку 500).
	os.MkdirAll(P(siteCacheRoot), 0755)
	run("chmod", "755", P(filepath.Dir(siteCacheRoot)), P(siteCacheRoot))
	os.MkdirAll(P(dir), 0700)
	run("chown", osInfo.WebUser+":"+osInfo.WebUser, P(dir))
	run("chmod", "700", P(dir))
	if osInfo.Family == "rhel" && have("restorecon") {
		run("restorecon", "-R", siteCacheRoot)
	}
}

// purgeSiteCache удаляет содержимое кэша сайта и возвращает освобождённый размер. nginx перезагружать не нужно.
func purgeSiteCache(domain string) (int64, error) {
	dir := P(siteCacheDir(domain))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var size int64
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
			if err == nil && info.Mode().IsRegular() {
				size += info.Size()
			}
			return nil
		})
		if err := os.RemoveAll(p); err != nil {
			return size, err
		}
	}
	return size, nil
}

func removeSiteCache(domain string) { os.RemoveAll(P(siteCacheDir(domain))) }

func registerSiteCache() {
	apiOp("sites.cache.purge", "Сайт: очистка кэша", func(q *Req) (any, error) {
		var in struct{ Domain string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		s := findSite(in.Domain)
		on := s != nil && s.CacheTTL > 0
		mu.Unlock()
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		if !on {
			return nil, errf("У сайта выключен кэш страниц")
		}
		freed, err := purgeSiteCache(in.Domain)
		if err != nil {
			return nil, err
		}
		return map[string]any{"freed": freed}, nil
	})
}
