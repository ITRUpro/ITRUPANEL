// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Сессия импорта живёт в памяти: логины, пароли и пароли новых баз на диск не записываются
// и в журнал задач не попадают. Через три часа сессия забывается.
type impSession struct {
	ID      string
	Created time.Time
	SSH     impSSHConf
	Panel   ImpPanelConf
	Inv     *ImpInventory
	Secrets map[string]string
}

var (
	impMu       sync.Mutex
	impSessions = map[string]*impSession{}
)

const impSessionTTL = 3 * time.Hour

func impSessionGet(id string) *impSession {
	impMu.Lock()
	defer impMu.Unlock()
	for k, s := range impSessions {
		if time.Since(s.Created) > impSessionTTL {
			delete(impSessions, k)
		}
	}
	return impSessions[id]
}

type impAnalyzeIn struct {
	SSH   impSSHConf   `json:"ssh"`
	Panel ImpPanelConf `json:"panel"`
}

func (in impAnalyzeIn) hasSSH() bool {
	return strings.TrimSpace(in.SSH.Host) != "" && (in.SSH.Password != "" || strings.TrimSpace(in.SSH.Key) != "")
}

func (in impAnalyzeIn) hasPanel() bool {
	return strings.TrimSpace(in.Panel.URL) != "" && in.Panel.Login != ""
}

// impAnalyze подключается к старому серверу и собирает описание: сайты, базы, почту, DNS, cron.
// Ничего не меняется ни на старом, ни на новом сервере.
func impAnalyze(ctx context.Context, j *Job, in impAnalyzeIn) (*impSession, error) {
	if !in.hasSSH() && !in.hasPanel() {
		return nil, errf("Укажите доступ к старому серверу: адрес панели с логином и паролем или SSH")
	}
	if cfg.Dry {
		// Демо-режим: подключения нет, показывается пример старого сервера.
		sess := &impSession{ID: randToken(16), Created: time.Now(), Secrets: map[string]string{}, Inv: impDemoInventory()}
		impMu.Lock()
		impSessions[sess.ID] = sess
		impMu.Unlock()
		return sess, nil
	}
	if in.hasPanel() && strings.TrimSpace(in.SSH.Host) == "" {
		// Адрес SSH по умолчанию — тот же сервер, что и у панели.
		if base, err := itruPanelBase(in.Panel.URL); err == nil {
			if u, err := url.Parse(base); err == nil {
				in.SSH.Host = u.Hostname()
			}
		}
	}
	sess := &impSession{ID: randToken(16), Created: time.Now(), SSH: in.SSH, Panel: in.Panel, Secrets: map[string]string{}}
	inv := &ImpInventory{Source: "ssh", Services: map[string]bool{}}
	if in.hasSSH() {
		jobStage(j, "Подключение к старому серверу по SSH")
		r, err := impDialSSH(ctx, in.SSH)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		jobStage(j, "Просмотр старого сервера")
		inv, err = impScanSSH(ctx, r, in.SSH.Host)
		if err != nil {
			return nil, err
		}
		inv.Fingerprint = r.Fingerprint()
	} else {
		inv.Host = in.SSH.Host
	}
	if in.hasPanel() {
		jobStage(j, "Подключение к панели ITRUPANEL")
		c, err := itruLogin(ctx, in.Panel)
		if err != nil {
			return nil, err
		}
		jobStage(j, "Чтение данных панели")
		if inv.Host == "" {
			if u, err := url.Parse(c.base); err == nil {
				inv.Host = u.Hostname()
			}
		}
		if err := impFromITRUPANEL(ctx, c, inv); err != nil {
			return nil, err
		}
		inv.Source = "itrupanel"
	}
	sess.Inv = inv
	impMu.Lock()
	impSessions[sess.ID] = sess
	impMu.Unlock()
	return sess, nil
}

func registerImport() {
	apiOp("import.analyze", "Импорт: анализ старого сервера", func(q *Req) (any, error) {
		var in impAnalyzeIn
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !in.hasSSH() && !in.hasPanel() {
			return nil, errf("Укажите доступ к старому серверу: адрес панели с логином и паролем или SSH")
		}
		id := startJob("import:analyze", "Импорт: анализ старого сервера", q.S.User, func(j *Job) (any, error) {
			sess, err := impAnalyze(j.ctx, j, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"session": sess.ID, "inventory": sess.Inv, "info": "Старый сервер просмотрен"}, nil
		})
		return map[string]string{"job": id}, nil
	})
	api("import.needs", func(q *Req) (any, error) {
		var in struct {
			Session   string
			Selection impSelection
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		sess := impSessionGet(in.Session)
		if sess == nil {
			return nil, errf("Сессия импорта устарела: повторите анализ")
		}
		return map[string]any{"needs": impNeeds(sess.Inv, in.Selection)}, nil
	})
	apiOp("import.run", "Импорт: перенос данных", func(q *Req) (any, error) {
		var in struct {
			Session   string
			Selection impSelection
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		sess := impSessionGet(in.Session)
		if sess == nil {
			return nil, errf("Сессия импорта устарела: повторите анализ")
		}
		if !sess.Inv.CanTransfer {
			return nil, errf("Старая панель не отдаёт файлы и базы: обновите её до версии 0.1.94")
		}
		id := startJob("import:run", "Импорт: перенос данных", q.S.User, func(j *Job) (any, error) {
			res, err := impRun(j.ctx, j, sess, in.Selection)
			if res != nil && len(sess.Secrets) > 0 {
				res.Warnings = append(res.Warnings, "У части баз пароль пользователя изменён: откройте «Показать пароли новых баз»")
			}
			return res, err
		})
		return map[string]string{"job": id}, nil
	})
	// Пароли новых баз показываются один раз и из памяти удаляются.
	apiOp("import.secrets", "Импорт: пароли новых баз", func(q *Req) (any, error) {
		var in struct{ Session string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		sess := impSessionGet(in.Session)
		if sess == nil {
			return nil, errf("Сессия импорта устарела")
		}
		impMu.Lock()
		out := sess.Secrets
		sess.Secrets = map[string]string{}
		impMu.Unlock()
		return map[string]any{"passwords": out}, nil
	})
	apiOp("import.forget", "Импорт: завершение", func(q *Req) (any, error) {
		var in struct{ Session string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		impMu.Lock()
		delete(impSessions, in.Session)
		impMu.Unlock()
		return nil, nil
	})
}

// impDemoInventory — пример старого сервера ITRUPANEL для демо-режима и скриншотов.
func impDemoInventory() *ImpInventory {
	return &ImpInventory{
		Source: "itrupanel", Host: "old.example.com", OS: "Debian GNU/Linux 12 (bookworm) (x86_64)", Panel: "itrupanel",
		PanelFP: "SHA256:nmD3QXaKhMPYEuMiN3ypw5aVa5S8e6NP5HlL4yiU2lM", CanTransfer: true,
		Sites: []ImpSite{
			{Domain: "shop.example.com", Aliases: []string{"www.shop.example.com"}, Owner: "shop", Root: "/var/www/shop/data/www/shop.example.com", Mode: "php", PHP: "8.1", SSL: true, Size: 1288490188},
			{Domain: "blog.example.com", Owner: "blog", Root: "/var/www/blog/data/www/blog.example.com", Mode: "php", PHP: "7.4", Size: 356515840},
			{Domain: "landing.example.org", Owner: "promo", Root: "/var/www/promo/data/www/landing.example.org", Mode: "static", SSL: true, Size: 18874368},
			{Domain: "api.example.net", Owner: "api", Mode: "proxy", ProxyTo: "http://127.0.0.1:3000"},
		},
		DBs:         []ImpDB{{Name: "shop_db", Size: 214958080, Users: []string{"shop_db"}}, {Name: "blog_wp", Size: 48234496, Users: []string{"blog_wp"}}},
		MailDomains: []ImpMailDomain{{Name: "shop.example.com", Boxes: 6}, {Name: "blog.example.com", Boxes: 2}},
		DNS:         []ImpDNSZone{{Name: "shop.example.com"}, {Name: "blog.example.com"}},
		Cron: []ImpCron{
			{User: "shop", Schedule: "*/5 * * * *", Command: "/usr/bin/php8.1 /var/www/shop/data/www/shop.example.com/cron.php"},
			{User: "blog", Schedule: "0 3 * * *", Command: "/usr/bin/php7.4 /var/www/blog/data/www/blog.example.com/wp-cron.php"},
		},
		FTP: []ImpFTP{{Name: "shop_ftp", Owner: "shop", Home: "/var/www/shop/data/www"}}, Backup: &ImpBackup{Target: "ftp", Host: "ftp.example.com", Dir: "/backups/shop", Daily: true, Keep: 7, Hour: 3}, PHPVersions: []string{"7.4", "8.1"},
		Services: map[string]bool{"nginx": true, "mariadb": true, "postfix": true, "dovecot": true, "named": true},
	}
}
