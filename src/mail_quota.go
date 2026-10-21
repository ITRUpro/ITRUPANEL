// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strconv"
	"strings"
	"time"
)

func dovecotQuotaConfig(modern bool) string {
	if modern {
		return `
# ITRUPANEL quotas
mail_plugins {
 quota = yes
}
protocol imap {
 mail_plugins {
  imap_quota = yes
 }
}
mailbox_list_index = yes
quota_storage_size = 0
quota_storage_grace = 0
quota "User quota" {
 driver = count
}
`
	}
	return `
# ITRUPANEL quotas
mail_plugins = quota
protocol imap {
 mail_plugins = $mail_plugins imap_quota
}
mailbox_list_index = yes
plugin {
 quota = count:User quota
 quota_vsizes = yes
 quota_grace = 0
}
`
}

func parseMailQuota(out string) (int64, error) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		n := len(fields)
		if n >= 5 && fields[n-4] == "STORAGE" {
			value, err := strconv.ParseInt(fields[n-3], 10, 64)
			if err == nil && value >= 0 && value <= (1<<63-1)/1024 {
				return value * 1024, nil
			}
		}
	}
	return 0, errf("Dovecot не вернул использование квоты")
}
func mailboxUsage(address string) (int64, error) {
	if cfg.Dry {
		return 0, nil
	}
	out, err := runTimeout(15*time.Second, "", "doveadm", "-f", "tab", "quota", "get", "-u", address)
	if err != nil {
		return 0, err
	}
	return parseMailQuota(out)
}

// One bounded Dovecot command for the whole table, outside the state lock.
func registerMailUsage() {
	api("mail.usage", func(q *Req) (any, error) {
		mu.Lock()
		boxes := append([]MailBox{}, store.Mail.Boxes...)
		mu.Unlock()
		result := map[string]any{}
		for _, b := range boxes {
			result[b.Address] = nil
		}
		if cfg.Dry {
			for _, b := range boxes {
				result[b.Address] = int64(0)
			}
			return result, nil
		}
		out, e := runTimeout(20*time.Second, "", "doveadm", "-f", "tab", "quota", "get", "-A")
		if e != nil {
			return nil, errf("Не удалось получить использование квот: %v", e)
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if _, ok := result[fields[0]]; ok {
				if size, e := parseMailQuota(line); e == nil {
					result[fields[0]] = size
				}
			}
		}
		return result, nil
	})
}
