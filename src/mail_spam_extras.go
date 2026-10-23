// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Greylisting и DNSBL (RBL) в Rspamd: настройки хранятся в SpamSettings и записываются в local.d вместе
// с остальной почтовой конфигурацией. Проверка configtest и откат при ошибке — в applyMail.

// Списки DNSBL, которые Rspamd проверяет по умолчанию; их можно выключить по одному.
var spamRBLCatalog = []map[string]string{
	{"id": "spamhaus", "zone": "zen.spamhaus.org", "title": "Spamhaus ZEN", "title_en": "Spamhaus ZEN", "desc": "самый известный чёрный список: спам-рассылки, взломанные машины, динамические адреса. Не отвечает публичным резолверам (8.8.8.8, 1.1.1.1)", "desc_en": "the best-known blocklist: spam sources, hijacked machines, dynamic addresses. Does not answer public resolvers (8.8.8.8, 1.1.1.1)"},
	{"id": "nixspam", "zone": "ix.dnsbl.manitu.net", "title": "NiX Spam (manitu.net)", "title_en": "NiX Spam (manitu.net)", "desc": "немецкий список недавних источников спама", "desc_en": "a German list of recent spam sources"},
	{"id": "blocklistde", "zone": "bl.blocklist.de", "title": "Blocklist.de", "title_en": "Blocklist.de", "desc": "адреса, замеченные в атаках на серверы (подбор паролей, сканирование)", "desc_en": "addresses seen attacking servers (password guessing, scanning)"},
	{"id": "virusfree", "zone": "bip.virusfree.cz", "title": "Virusfree", "title_en": "Virusfree", "desc": "заражённые компьютеры и ботнеты", "desc_en": "infected computers and botnets"},
	{"id": "senderscore", "zone": "bl.score.senderscore.com", "title": "SenderScore", "title_en": "SenderScore", "desc": "репутация отправителя: плохая повышает балл, хорошая снижает", "desc_en": "sender reputation: bad raises the score, good lowers it"},
	{"id": "mailspike", "zone": "rep.mailspike.net", "title": "Mailspike", "title_en": "Mailspike", "desc": "репутация адреса: хорошие отправители получают скидку по баллу", "desc_en": "address reputation: good senders get a score discount"},
	{"id": "dnswl", "zone": "list.dnswl.org", "title": "DNSWL (белый список)", "title_en": "DNSWL (whitelist)", "desc": "проверенные отправители: балл снижается", "desc_en": "verified senders: the score is lowered"},
}

var reRBLZone = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func spamRBLKnown(id string) bool {
	for _, r := range spamRBLCatalog {
		if r["id"] == id {
			return true
		}
	}
	return false
}

// spamValidateExtras приводит настройки greylisting и DNSBL к допустимому виду или возвращает понятную ошибку.
func spamValidateExtras(s *SpamSettings) error {
	if s.GreylistDelay == 0 {
		s.GreylistDelay = 5
	}
	if s.GreylistScore == 0 {
		s.GreylistScore = 4
	}
	if s.Greylist {
		if s.GreylistDelay < 1 || s.GreylistDelay > 60 {
			return errf("Задержка greylisting: от 1 до 60 минут")
		}
		if s.GreylistScore < 1 || s.GreylistScore >= s.Mark {
			return errf("Greylisting включается с балла от 1 и ниже порога пометки (%d)", s.Mark)
		}
	}
	seen := map[string]bool{}
	var skip []string
	for _, id := range s.RBLSkip {
		if !spamRBLKnown(id) {
			return errf("Неизвестный список DNSBL: %s", id)
		}
		if !seen[id] {
			seen[id] = true
			skip = append(skip, id)
		}
	}
	sort.Strings(skip)
	s.RBLSkip = skip
	if len(s.RBLCustom) > 10 {
		return errf("Не больше 10 своих списков DNSBL")
	}
	zones := map[string]bool{}
	for _, r := range spamRBLCatalog {
		zones[r["zone"]] = true
	}
	for i := range s.RBLCustom {
		c := &s.RBLCustom[i]
		c.Zone = strings.ToLower(strings.TrimSpace(c.Zone))
		if len(c.Zone) > 200 || !reRBLZone.MatchString(c.Zone) {
			return errf("Неверное имя списка DNSBL: %s", c.Zone)
		}
		if zones[c.Zone] {
			return errf("Список %s уже проверяется или добавлен", c.Zone)
		}
		zones[c.Zone] = true
		if c.Score < 0.1 || c.Score > 20 {
			return errf("Балл списка %s: от 0,1 до 20", c.Zone)
		}
		c.Score = float64(int(c.Score*10+0.5)) / 10
	}
	return nil
}

func spamGreylistAction(s SpamSettings) string {
	if !s.Greylist {
		return "null"
	}
	g := s.GreylistScore
	if g < 1 {
		g = 4
	}
	return fmt.Sprint(g)
}

func spamRBLSymbol(zone string) string {
	name := strings.ToUpper(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(zone, "_"))
	return "RBL_CUSTOM_" + strings.Trim(name, "_")
}

// spamExtraFiles — файлы Rspamd для greylisting и DNSBL. Если ничего не настроено и файлов ещё нет,
// их не создают: у существующих установок конфигурация и перезапуск Rspamd не меняются зря.
func spamExtraFiles(s SpamSettings) map[string]string {
	out := map[string]string{}
	add := func(path, content string, custom bool) {
		if custom || fileExists(path) {
			out[path] = content
		}
	}
	delay := s.GreylistDelay
	if delay < 1 {
		delay = 5
	}
	add("/etc/rspamd/local.d/greylist.conf", fmt.Sprintf("timeout = %dmin;\nexpire = 1d;\n", delay), s.Greylist)

	var rbl strings.Builder
	// «Выключить DNSBL» гасит списки адресов отправителей; проверка ссылок в письмах (SURBL, DBL) остаётся.
	skip := s.RBLSkip
	if s.RBLOff {
		skip = nil
		for _, r := range spamRBLCatalog {
			skip = append(skip, r["id"])
		}
	}
	rbl.WriteString("rbls {\n")
	for _, id := range skip {
		fmt.Fprintf(&rbl, "  %s { enabled = false; }\n", id)
	}
	if !s.RBLOff {
		for i, c := range s.RBLCustom {
			fmt.Fprintf(&rbl, "  custom_%d { symbol = \"%s\"; rbl = \"%s\"; checks = [\"from\"]; ipv6 = false; }\n", i+1, spamRBLSymbol(c.Zone), c.Zone)
		}
	}
	rbl.WriteString("}\n")
	add("/etc/rspamd/local.d/rbl.conf", rbl.String(), s.RBLOff || len(s.RBLSkip) > 0 || len(s.RBLCustom) > 0)

	var groups strings.Builder
	if len(s.RBLCustom) == 0 {
		groups.WriteString("# ITRUPANEL: своих списков DNSBL нет\n")
	} else {
		groups.WriteString("group \"itrupanel_rbl\" {\n  max_score = 30.0;\n  symbols = {\n")
		for _, c := range s.RBLCustom {
			fmt.Fprintf(&groups, "    \"%s\" { weight = %.1f; description = \"DNSBL %s\"; }\n", spamRBLSymbol(c.Zone), c.Score, c.Zone)
		}
		groups.WriteString("  }\n}\n")
	}
	add("/etc/rspamd/local.d/groups.conf", groups.String(), len(s.RBLCustom) > 0 && !s.RBLOff)
	return out
}
