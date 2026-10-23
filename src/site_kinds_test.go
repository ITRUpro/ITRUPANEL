// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestSiteKindsAreConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range siteKinds {
		if seen[k.ID] || k.Name == "" || k.Desc == "" || k.NameEn == "" || k.DescEn == "" {
			t.Fatalf("тип %q описан неверно", k.ID)
		}
		seen[k.ID] = true
		if k.Template != "" {
			if _, ok := siteTemplateByID(k.Template); !ok {
				t.Errorf("%s: нет шаблона %s", k.ID, k.Template)
			}
		}
		if k.Mode != "php" && k.Mode != "static" && k.Mode != "proxy" {
			t.Errorf("%s: режим %q", k.ID, k.Mode)
		}
		// Шаблон должен подходить режиму типа сайта.
		if k.Template != "" {
			tpl, _ := siteTemplateByID(k.Template)
			s := &Site{Domain: "example.test", Mode: k.Mode}
			if err := siteTemplateCompat(tpl.Name, tpl.Rules, s); err != nil {
				t.Errorf("%s: шаблон не подходит режиму типа: %v", k.ID, err)
			}
		}
	}
	if err := applySiteKind(&siteIn{Kind: "nope"}); err == nil {
		t.Error("неизвестный тип принят")
	}
}

func TestSiteKindsCreateSites(t *testing.T) {
	testPanel(t)
	store.Settings.DefaultPHP = "8.3"
	cases := []struct {
		kind, mode, template, rootSuffix string
		proxy                            string
	}{
		{"wordpress", "php", "wordpress", "", ""},
		{"laravel", "php", "laravel", "/public", ""},
		{"joomla", "php", "joomla", "", ""},
		{"bitrix", "php", "bitrix", "", ""},
		{"spa", "static", "spa", "", ""},
		{"static", "static", "", "", ""},
		{"app", "proxy", "", "", "http://127.0.0.1:3000"},
		{"php", "php", "", "", ""},
	}
	for i, c := range cases {
		domain := c.kind + ".example.test"
		s, err := createOrUpdateSite(siteIn{Domain: domain, Kind: c.kind, ProxyTo: c.proxy, MaxBody: 64, Mode: "php"})
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if s.Mode != c.mode || s.Template != c.template || !strings.HasSuffix(s.Root, "/data/www/"+domain+c.rootSuffix) {
			t.Errorf("%d %s: режим=%s шаблон=%q корень=%s", i, c.kind, s.Mode, s.Template, s.Root)
		}
		if c.mode != "php" && s.PHP != "" {
			t.Errorf("%s: у не-PHP сайта задан PHP %s", c.kind, s.PHP)
		}
		conf := readFile(vhostPath(domain))
		if c.template != "" && !strings.Contains(conf, "# Шаблон сайта:") {
			t.Errorf("%s: шаблон не попал в конфигурацию", c.kind)
		}
		if c.mode == "proxy" && !strings.Contains(conf, "proxy_pass") {
			t.Errorf("%s: нет проксирования", c.kind)
		}
	}
	// «Обычный сайт на PHP» не навязывает веб-сервер: выбор Apache остаётся.
	if _, err := createOrUpdateSite(siteIn{Domain: "keep.example.test", Kind: "php", Mode: "php", Backend: "apache", MaxBody: 64}); err != nil && !strings.Contains(err.Error(), "Apache") {
		t.Fatalf("php + apache: %v", err)
	}
	// При редактировании тип не применяется и не сбрасывает настройки сайта.
	s, err := createOrUpdateSite(siteIn{Domain: "wordpress.example.test", Edit: true, Kind: "static", Mode: "php", PHP: "8.3", Template: "wordpress", TryIndex: true, MaxBody: 64, Root: ""})
	if err != nil || s.Mode != "php" || s.Template != "wordpress" {
		t.Fatalf("редактирование изменило тип: %v %+v", err, s)
	}
}
