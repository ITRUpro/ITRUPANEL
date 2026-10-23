// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"strconv"
)

// Профиль нагрузки PHP: вместо ручной правки пула PHP-FPM — три понятных варианта, число процессов панель считает по памяти
// сервера. Рядом — простые настройки PHP для сайта (память и время выполнения).

type phpPM struct {
	Mode                                          string
	MaxChildren, StartServers, MinSpare, MaxSpare int
	IdleTimeout, MaxRequests                      int
}

var phpProfiles = []map[string]string{
	{"id": "", "name": "Обычная", "name_en": "Normal", "desc": "Процессы PHP запускаются по требованию, до 10 одновременно. Подходит большинству сайтов.", "desc_en": "PHP processes start on demand, up to 10 at a time. Suits most websites."},
	{"id": "eco", "name": "Экономная", "name_en": "Economical", "desc": "Мало процессов и быстрое освобождение памяти: для слабых серверов и сайтов с редкими посещениями.", "desc_en": "Few processes and quick release of memory: for weak servers and rarely visited sites."},
	{"id": "high", "name": "Высокая нагрузка", "name_en": "High load", "desc": "Процессы всегда наготове, их число считается по памяти сервера: для посещаемых сайтов и интернет-магазинов.", "desc_en": "Processes are always ready, their number is calculated from the server memory: for busy sites and online shops."},
}

func phpMemTotalMB() int { return int(avMemTotal() >> 20) }

func phpProfileKnown(id string) bool {
	for _, p := range phpProfiles {
		if p["id"] == id {
			return true
		}
	}
	return false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// phpPoolSettings возвращает настройки пула PHP-FPM для профиля. memMB — память сервера в мегабайтах
// (0 — неизвестна, берутся осторожные значения). Расчёт исходит из 60 МБ на процесс PHP.
func phpPoolSettings(profile string, memMB int) phpPM {
	if memMB <= 0 {
		memMB = 2048
	}
	switch profile {
	case "eco":
		return phpPM{Mode: "ondemand", MaxChildren: clampInt(memMB/10/60, 2, 5), IdleTimeout: 10, MaxRequests: 500}
	case "high":
		max := clampInt(memMB*4/10/60, 10, 80)
		start := clampInt(max/4, 2, 20)
		return phpPM{Mode: "dynamic", MaxChildren: max, StartServers: start, MinSpare: clampInt(start/2, 1, 10), MaxSpare: clampInt(start, 2, 20), MaxRequests: 1000}
	}
	return phpPM{Mode: "ondemand", MaxChildren: 10, IdleTimeout: 30, MaxRequests: 500}
}

// phpSiteIni — настройки PHP сайта для пула.
func phpSiteIni(s *Site) map[string]string {
	ini := map[string]string{}
	if s.PHPMemory > 0 {
		ini["memory_limit"] = fmt.Sprintf("%dM", s.PHPMemory)
	}
	if s.PHPMaxExec > 0 {
		ini["max_execution_time"] = strconv.Itoa(s.PHPMaxExec)
	}
	return ini
}

func validatePHPSiteSettings(in *siteIn) error {
	if !phpProfileKnown(in.PHPProfile) {
		return errf("Неизвестный профиль нагрузки PHP")
	}
	if in.PHPMemory != 0 && (in.PHPMemory < 32 || in.PHPMemory > 8192) {
		return errf("Память PHP: от 32 до 8192 МБ или 0 — по умолчанию")
	}
	if in.PHPMaxExec != 0 && (in.PHPMaxExec < 5 || in.PHPMaxExec > 3600) {
		return errf("Время выполнения PHP: от 5 до 3600 секунд или 0 — по умолчанию")
	}
	if in.Mode != "php" {
		in.PHPProfile, in.PHPMemory, in.PHPMaxExec = "", 0, 0
	}
	return nil
}

// phpPoolSummary — краткое описание пула для подсказки в форме.
func phpPoolSummary(profile string) string {
	pm := phpPoolSettings(profile, phpMemTotalMB())
	if pm.Mode == "dynamic" {
		return fmt.Sprintf("до %d процессов, %d всегда запущено", pm.MaxChildren, pm.StartServers)
	}
	return fmt.Sprintf("до %d процессов по требованию", pm.MaxChildren)
}

func phpProfileChoices() []map[string]string {
	var out []map[string]string
	for _, p := range phpProfiles {
		c := map[string]string{}
		for k, v := range p {
			c[k] = v
		}
		c["summary"] = phpPoolSummary(p["id"])
		out = append(out, c)
	}
	return out
}
