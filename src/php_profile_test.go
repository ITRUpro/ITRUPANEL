// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestPHPPoolSettingsByProfile(t *testing.T) {
	// Обычная: как было до профилей (по требованию, 10 процессов), независимо от памяти.
	for _, mem := range []int{0, 512, 4096, 65536} {
		if p := phpPoolSettings("", mem); p.Mode != "ondemand" || p.MaxChildren != 10 || p.IdleTimeout != 30 || p.MaxRequests != 500 {
			t.Fatalf("обычная при %d МБ: %+v", mem, p)
		}
	}
	// Экономная: мало процессов даже на большом сервере.
	for mem, want := range map[int]int{512: 2, 1024: 2, 2048: 3, 4096: 5, 65536: 5} {
		if p := phpPoolSettings("eco", mem); p.Mode != "ondemand" || p.MaxChildren != want || p.IdleTimeout != 10 {
			t.Fatalf("экономная при %d МБ: %+v, ждали %d процессов", mem, p, want)
		}
	}
	// Высокая нагрузка: процессы держатся наготове, число растёт с памятью, но ограничено.
	prev := 0
	for _, mem := range []int{512, 2048, 8192, 32768, 262144} {
		p := phpPoolSettings("high", mem)
		if p.Mode != "dynamic" || p.MaxChildren < 10 || p.MaxChildren > 80 || p.MaxChildren < prev {
			t.Fatalf("высокая при %d МБ: %+v", mem, p)
		}
		if p.StartServers < 2 || p.MinSpare < 1 || p.MinSpare > p.MaxSpare || p.MaxSpare > p.MaxChildren || p.StartServers > p.MaxChildren {
			t.Fatalf("высокая при %d МБ: несогласованные числа %+v", mem, p)
		}
		prev = p.MaxChildren
	}
}

func TestPHPPoolRenderingAndValidation(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.PHPMemory, s.PHPMaxExec = 512, 120
	for profile, want := range map[string][]string{
		"":     {"pm = ondemand", "pm.max_children = 10", "pm.process_idle_timeout = 30s"},
		"eco":  {"pm = ondemand", "pm.process_idle_timeout = 10s"},
		"high": {"pm = dynamic", "pm.start_servers = ", "pm.min_spare_servers = ", "pm.max_spare_servers = ", "pm.max_requests = 1000"},
	} {
		s.PHPProfile = profile
		b, err := renderPHPPool(s, u)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, w := range append(want, "php_value[memory_limit] = 512M", "php_value[max_execution_time] = 120") {
			if !strings.Contains(text, w) {
				t.Errorf("профиль %q: нет %q\n%s", profile, w, text)
			}
		}
		if profile == "high" && strings.Contains(text, "process_idle_timeout") {
			t.Error("process_idle_timeout у динамического пула")
		}
		if profile != "high" && strings.Contains(text, "pm.start_servers") {
			t.Error("start_servers у пула по требованию")
		}
	}
	s.PHPMemory, s.PHPMaxExec = 0, 0
	if b, _ := renderPHPPool(s, u); strings.Contains(string(b), "memory_limit") || strings.Contains(string(b), "max_execution_time") {
		t.Error("пустые настройки PHP попали в пул")
	}
	for name, in := range map[string]siteIn{
		"профиль":      {Mode: "php", PHPProfile: "turbo"},
		"память мало":  {Mode: "php", PHPMemory: 8},
		"память много": {Mode: "php", PHPMemory: 99999},
		"время мало":   {Mode: "php", PHPMaxExec: 1},
		"время много":  {Mode: "php", PHPMaxExec: 99999},
	} {
		if err := validatePHPSiteSettings(&in); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	ok := siteIn{Mode: "static", PHPProfile: "high", PHPMemory: 256, PHPMaxExec: 60}
	if err := validatePHPSiteSettings(&ok); err != nil || ok.PHPProfile != "" || ok.PHPMemory != 0 || ok.PHPMaxExec != 0 {
		t.Errorf("у статического сайта настройки PHP не сбрасываются: %+v %v", ok, err)
	}
}
