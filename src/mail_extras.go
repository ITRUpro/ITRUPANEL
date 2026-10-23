// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"sync"
)

type SpamSettings struct {
	Installed bool `json:"installed"`
	Enabled   bool `json:"enabled"`
	Mark      int  `json:"mark"`
	Reject    int  `json:"reject"`
	// Greylisting Rspamd: письмо от неизвестного отправителя со счётом от GreylistScore сначала получает
	// временный отказ, повторная доставка через GreylistDelay минут проходит. Авторизованных и локальных не касается.
	Greylist      bool `json:"greylist,omitempty"`
	GreylistScore int  `json:"greylist_score,omitempty"`
	GreylistDelay int  `json:"greylist_delay,omitempty"` // минуты, 0 — 5
	// DNSBL: RBLOff выключает проверку целиком, RBLSkip — отдельные списки Rspamd, RBLCustom — свои.
	RBLOff    bool        `json:"rbl_off,omitempty"`
	RBLSkip   []string    `json:"rbl_skip,omitempty"`
	RBLCustom []CustomRBL `json:"rbl_custom,omitempty"`
}

type CustomRBL struct {
	Zone  string  `json:"zone"`
	Score float64 `json:"score"`
}
type WebmailSettings struct {
	Ready       bool   `json:"ready"`
	Hostname    string `json:"hostname"`
	Certificate string `json:"certificate"`
	PHP         string `json:"php"`
}

var mailSetupMu sync.Mutex

func mailQuotaRequest(q *Req) (any, error) {
	var in struct{ Address string }
	if err := q.Bind(&in); err != nil {
		return nil, err
	}
	address, err := mailAddress(in.Address)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	var box *MailBox
	for _, b := range store.Mail.Boxes {
		if b.Address == address {
			copy := b
			box = &copy
			break
		}
	}
	mu.Unlock()
	if box == nil {
		return nil, errf("Ящик не найден")
	}
	if !box.Enabled {
		return nil, errf("Включите ящик для проверки использования Dovecot")
	}
	used, err := mailboxUsage(address)
	if err != nil {
		return nil, err
	}
	return map[string]any{"used": used, "quota_mb": box.QuotaMB}, nil
}

func registerMailExtras() {
	api("mail.quota", mailQuotaRequest)
	apiOp("mail.spam.setup", "Почта: антиспам", func(q *Req) (any, error) {
		var in SpamSettings
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Mark < 1 || in.Reject <= in.Mark || in.Reject > 100 {
			return nil, errf("Порог пометки: от 1; порог отклонения должен быть больше, максимум 100")
		}
		if err := spamValidateExtras(&in); err != nil {
			return nil, err
		}
		id := startJob("mail:extras", "Настройка антиспама Rspamd", q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Антиспам настроен"}, setupSpam(in, j)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("mail.webmail.setup", "Почта: Roundcube", func(q *Req) (any, error) {
		var in struct {
			Hostname, Certificate, Email string
			LE                           bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		host, err := toASCII(in.Hostname)
		if err != nil {
			return nil, err
		}
		id := startJob("mail:extras", "Настройка Roundcube", q.S.User, func(j *Job) (any, error) {
			if in.LE {
				mu.Lock()
				ready := store.Mail.Ready
				mu.Unlock()
				if !ready {
					return nil, errf("Сначала настройте почтовый сервер")
				}
				c, err := issueMailHostLE(j, host, in.Email)
				if err != nil {
					return nil, err
				}
				in.Certificate = c
			}
			return map[string]string{"info": "Roundcube настроен: https://" + host + "/"}, setupRoundcube(host, in.Certificate, j)
		})
		return map[string]string{"job": id}, nil
	})
}

// Extra generated secrets are never returned by mail.list or written to job logs.
func mailFileMode(path string) os.FileMode {
	if path == "/etc/postfix/itrupanel/sni" {
		return 0600
	}
	if path == roundcubeConfigPath() {
		return 0640
	}
	switch path {
	case "/etc/dovecot/itrupanel-users", "/etc/rspamd/local.d/redis.conf", "/etc/itrupanel/rspamd-redis.conf", "/etc/rspamd/override.d/worker-controller.inc":
		return 0640
	}
	return 0644
}
func mailFileGroup(path string) string {
	if path == roundcubeConfigPath() {
		return "itruroundcube"
	}
	switch path {
	case "/etc/dovecot/itrupanel-users":
		return "dovecot"
	case "/etc/rspamd/local.d/redis.conf", "/etc/rspamd/override.d/worker-controller.inc":
		return "_rspamd"
	case "/etc/itrupanel/rspamd-redis.conf":
		_, user := spamRedisServer()
		return user
	}
	return ""
}
func mailServices(m MailState) []string {
	var result []string
	if m.Spam.Installed {
		result = append(result, "itrupanel-spam-redis", "rspamd")
	}
	return append(result, "opendkim", "dovecot", "postfix")
}
