// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// demoSeed заполняет панель примерами (только в демо-режиме, если пусто).
func demoSeed() {
	mu.Lock()
	defer mu.Unlock()
	if len(store.Sites) > 0 {
		return
	}
	now := time.Now()
	store.Users = []*SiteUser{{Name: "example", Home: "/var/www/example", Created: now}, {Name: "shop", Home: "/var/www/shop", Created: now}}
	store.Certs = []*Cert{{Name: "example.ru", Type: "le", Domains: []string{"example.ru", "www.example.ru"}, Expires: now.AddDate(0, 0, 74), Crt: "/etc/letsencrypt/live/example.ru/fullchain.pem"}}
	store.Sites = []*Site{
		{Domain: "example.ru", Aliases: []string{"www.example.ru"}, User: "example", Root: "/var/www/example/data/www/example.ru", PHP: "8.3", Mode: "php", SSL: "example.ru", RedirectHTTPS: true, Gzip: true, TryIndex: true, StaticExpires: "30d", MaxBody: 64, AutoSSL: true, Created: now},
		{Domain: "shop.example.ru", User: "shop", Root: "/var/www/shop/data/www/shop.example.ru", PHP: "8.2", Mode: "php", Gzip: true, MaxBody: 128, Created: now},
		{Domain: "api.example.ru", User: "example", Root: "/var/www/example/data/www/api.example.ru", Mode: "proxy", ProxyTo: "http://127.0.0.1:3000", Created: now},
	}
	store.DBs = []*DB{{Name: "example", User: "example", Owner: "example", Site: "example.ru", Created: now}, {Name: "shop", User: "shop", Owner: "shop", Site: "shop.example.ru", Created: now}}
	store.FTP = []*FTPUser{{Name: "shop_ftp", Owner: "shop", Home: "/var/www/shop/data/www", Created: now}}
	store.Cron = []*CronJob{{ID: "a1", User: "example", Schedule: "*/5 * * * *", Command: "/usr/bin/php8.3 /var/www/example/data/www/example.ru/cron.php", Comment: "Задачи сайта", Enabled: true}, {ID: "a2", User: "root", Schedule: "0 3 * * *", Command: "/usr/local/bin/cleanup.sh", Enabled: false}}
	store.Backups = []*Backup{{ID: "b1", Site: "example.ru", Kind: "full", Size: 184 << 20, Created: now.Add(-20 * time.Hour), Auto: true}, {ID: "b2", Site: "shop.example.ru", Kind: "full", Size: 512 << 20, Created: now.Add(-44 * time.Hour), Auto: true}}
	store.Notices = []*Notice{{ID: "n1", Time: now.Add(-3 * time.Hour), Level: "warn", Text: "Сертификат old.example.ru истекает через 10 дней"}, {ID: "n2", Time: now.Add(-26 * time.Hour), Level: "info", Text: "PHP 8.3 установлен", Read: true}}
	for _, s := range store.Sites {
		os.MkdirAll(P(s.Root+"/assets"), 0755)
		os.WriteFile(P(s.Root+"/index.php"), []byte("<?php\necho 'Привет';\n"), 0644)
	}
	// Демо для антивируса: безвредные образцы с сигнатурными строками (код не выполняется).
	os.MkdirAll(P(store.Sites[0].Root+"/wp-content/uploads/2026/10"), 0755)
	os.WriteFile(P(store.Sites[0].Root+"/wp-content/uploads/2026/10/cache.php"), []byte("<?php /* FilesMan demo sample */ ?>\n"), 0644)
	os.WriteFile(P(store.Sites[0].Root+"/wp-content/uploads/.htaccess"), []byte("AddHandler application/x-httpd-php .jpg\n"), 0644)
	os.WriteFile(P(store.Sites[1].Root+"/header.php"), []byte("<?php\n$title = 'Магазин';\n@eval(base64_decode('ZWNobyAxOw=='));\necho $title;\n"), 0644)
	os.MkdirAll(P(dockerDefaultProjects+"/wp"), 0755)
	os.WriteFile(P(dockerDefaultProjects+"/wp/docker-compose.yml"), []byte(dockerTemplates["wordpress"]), 0600)
	os.MkdirAll(P("/var/www/example/data/logs"), 0755)
	os.WriteFile(P("/var/www/example/data/logs/example.ru.access.log"), []byte("203.0.113.5 - - [05/Oct/2026:12:00:01 +0300] \"GET / HTTP/2.0\" 200 5120 \"-\" \"Mozilla/5.0\"\n203.0.113.9 - - [05/Oct/2026:12:00:04 +0300] \"GET /contacts/ HTTP/1.1\" 200 2210 \"-\" \"curl/8\"\n"), 0644)
	// Synthetic security events only in a newly seeded demo filesystem.
	for i, site := range store.Sites {
		mode := "monitor"
		if i == 1 {
			mode = "enforce"
		}
		if i == 2 {
			mode = "off"
		}
		site.Security = SiteSecurity{Mode: mode, Rate: 10, Burst: 20, Connections: 30}
		if i == 1 {
			site.Security.Deny = []string{"198.51.100.0/24"}
		}
		if mode == "off" {
			continue
		}
		var events []byte
		for n := 0; n < 24; n++ {
			event := SecurityEvent{Time: now.Add(-time.Duration(n*n+2) * time.Minute / 12).Format(time.RFC3339), IP: fmt.Sprintf("198.51.100.%d", n%7+10), Method: "GET", URI: "/catalog/", Status: 429, Req: "REJECTED", Mode: mode, Listed: "0"}
			if mode == "monitor" {
				event.Status = 200
				event.Req = "REJECTED_DRY_RUN"
			}
			if n%4 == 0 {
				event.URI = "/wp-login.php"
				event.Listed = "1"
				event.Req = ""
				if mode == "enforce" {
					event.Status = 403
				}
			}
			data, _ := json.Marshal(event)
			events = append(events, append(data, '\n')...)
		}
		writeFile(securityLogPath(site), events, 0640)
	}
	save()
}
