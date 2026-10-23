// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestSpamExtrasValidation(t *testing.T) {
	base := SpamSettings{Installed: true, Enabled: true, Mark: 6, Reject: 15}
	ok := base
	ok.Greylist, ok.RBLSkip = true, []string{"nixspam", "nixspam", "spamhaus"}
	ok.RBLCustom = []CustomRBL{{Zone: " BL.Example-DNSBL.net ", Score: 3.04}}
	if err := spamValidateExtras(&ok); err != nil {
		t.Fatal(err)
	}
	if ok.GreylistScore != 4 || ok.GreylistDelay != 5 || len(ok.RBLSkip) != 2 || ok.RBLCustom[0].Zone != "bl.example-dnsbl.net" || ok.RBLCustom[0].Score != 3 {
		t.Fatalf("значения по умолчанию или очистка: %+v", ok)
	}
	for name, mut := range map[string]func(*SpamSettings){
		"greylist выше порога пометки": func(s *SpamSettings) { s.Greylist, s.GreylistScore = true, 6 },
		"задержка 0 → 5, но 99 нельзя": func(s *SpamSettings) { s.Greylist, s.GreylistDelay = true, 99 },
		"неизвестный список":           func(s *SpamSettings) { s.RBLSkip = []string{"evil"} },
		"плохое имя зоны":              func(s *SpamSettings) { s.RBLCustom = []CustomRBL{{Zone: "bad zone;", Score: 1}} },
		"зона без точки":               func(s *SpamSettings) { s.RBLCustom = []CustomRBL{{Zone: "localhost", Score: 1}} },
		"балл 0":                       func(s *SpamSettings) { s.RBLCustom = []CustomRBL{{Zone: "a.example.net", Score: 0}} },
		"балл 50":                      func(s *SpamSettings) { s.RBLCustom = []CustomRBL{{Zone: "a.example.net", Score: 50}} },
		"повтор встроенной зоны":       func(s *SpamSettings) { s.RBLCustom = []CustomRBL{{Zone: "zen.spamhaus.org", Score: 2}} },
		"повтор своей зоны": func(s *SpamSettings) {
			s.RBLCustom = []CustomRBL{{Zone: "a.example.net", Score: 2}, {Zone: "A.example.net", Score: 3}}
		},
	} {
		s := base
		mut(&s)
		if err := spamValidateExtras(&s); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	many := base
	for i := 0; i < 11; i++ {
		many.RBLCustom = append(many.RBLCustom, CustomRBL{Zone: strings.Repeat("a", i+1) + ".example.net", Score: 1})
	}
	if spamValidateExtras(&many) == nil {
		t.Error("больше 10 списков принято")
	}
}

func TestSpamExtraFilesContent(t *testing.T) {
	testPanel(t)
	s := SpamSettings{Installed: true, Enabled: true, Mark: 6, Reject: 15}
	// Ничего не настроено и файлов нет: лишних файлов не создаём.
	if got := spamExtraFiles(s); len(got) != 0 {
		t.Fatalf("лишние файлы: %v", got)
	}
	s.Greylist, s.GreylistScore, s.GreylistDelay = true, 4, 7
	s.RBLSkip = []string{"blocklistde", "spamhaus"}
	s.RBLCustom = []CustomRBL{{Zone: "bl.example-dnsbl.net", Score: 3.5}}
	got := spamExtraFiles(s)
	if got["/etc/rspamd/local.d/greylist.conf"] != "timeout = 7min;\nexpire = 1d;\n" {
		t.Fatalf("greylist.conf: %q", got["/etc/rspamd/local.d/greylist.conf"])
	}
	rbl := got["/etc/rspamd/local.d/rbl.conf"]
	for _, want := range []string{"blocklistde { enabled = false; }", "spamhaus { enabled = false; }", `custom_1 { symbol = "RBL_CUSTOM_BL_EXAMPLE_DNSBL_NET"; rbl = "bl.example-dnsbl.net"; checks = ["from"]; ipv6 = false; }`} {
		if !strings.Contains(rbl, want) {
			t.Fatalf("rbl.conf без %q:\n%s", want, rbl)
		}
	}
	if g := got["/etc/rspamd/local.d/groups.conf"]; !strings.Contains(g, `"RBL_CUSTOM_BL_EXAMPLE_DNSBL_NET" { weight = 3.5;`) {
		t.Fatalf("groups.conf: %s", g)
	}
	if spamGreylistAction(s) != "4" || spamGreylistAction(SpamSettings{}) != "null" {
		t.Fatal("действие greylist")
	}
	// Всю проверку DNSBL можно выключить одним флагом.
	s.RBLOff = true
	off := spamExtraFiles(s)["/etc/rspamd/local.d/rbl.conf"]
	for _, r := range spamRBLCatalog {
		if !strings.Contains(off, r["id"]+" { enabled = false; }") {
			t.Fatalf("при выключении DNSBL список %s не выключен:\n%s", r["id"], off)
		}
	}
	if strings.Contains(off, "custom_1") {
		t.Fatal("свой список остался при выключенном DNSBL")
	}
	// Возврат к умолчаниям: файлы, которые уже есть, перезаписываются нейтральным содержимым, а не остаются.
	writeFile("/etc/rspamd/local.d/rbl.conf", []byte("old"), 0644)
	writeFile("/etc/rspamd/local.d/groups.conf", []byte("old"), 0644)
	writeFile("/etc/rspamd/local.d/greylist.conf", []byte("old"), 0644)
	got = spamExtraFiles(SpamSettings{Installed: true, Enabled: true, Mark: 6, Reject: 15})
	if got["/etc/rspamd/local.d/rbl.conf"] != "rbls {\n}\n" || !strings.HasPrefix(got["/etc/rspamd/local.d/groups.conf"], "#") || !strings.Contains(got["/etc/rspamd/local.d/greylist.conf"], "timeout = 5min") {
		t.Fatalf("сброс к умолчаниям: %v", got)
	}
}

func TestSpamFilesIncludeGreylistAction(t *testing.T) {
	testPanel(t)
	writeFile(spamRedisSecret, []byte(strings.Repeat("a", 64)), 0600)
	s := SpamSettings{Installed: true, Enabled: true, Mark: 6, Reject: 15, Greylist: true, GreylistScore: 4, GreylistDelay: 5}
	files, err := spamFiles(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["/etc/rspamd/local.d/actions.conf"], "greylist = 4;") {
		t.Fatal(files["/etc/rspamd/local.d/actions.conf"])
	}
	s.Greylist = false
	files, _ = spamFiles(s)
	if !strings.Contains(files["/etc/rspamd/local.d/actions.conf"], "greylist = null;") {
		t.Fatal("greylist не выключен")
	}
}
