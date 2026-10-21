// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Температура процессора из датчиков ядра Linux. На большинстве VPS датчиков нет —
// тогда возвращается nil и дашборд температуру не показывает.

// hwmon-драйверы датчиков процессора: Intel, AMD, ARM-платы.
var cpuHwmonNames = map[string]bool{"coretemp": true, "k10temp": true, "zenpower": true, "cpu_thermal": true, "cpu-thermal": true, "soc_thermal": true}

// Подписи датчика всего процессора (а не отдельного ядра).
var cpuPackageLabels = []string{"package id 0", "tctl", "tdie", "physical id 0"}

func readMilliCelsius(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, false
	}
	c := v / 1000
	return c, c > 0 && c < 150
}

// cpuTemperature ищет датчик в sysRoot (обычно "/sys"; в тестах — подготовленная папка).
func cpuTemperature(sysRoot string) *float64 {
	best, found := 0.0, false
	hwmons, _ := filepath.Glob(filepath.Join(sysRoot, "class/hwmon/hwmon*"))
	for _, dir := range hwmons {
		name, _ := os.ReadFile(filepath.Join(dir, "name"))
		if !cpuHwmonNames[strings.TrimSpace(string(name))] {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		pkg, hasPkg, max := 0.0, false, 0.0
		for _, in := range inputs {
			c, ok := readMilliCelsius(in)
			if !ok {
				continue
			}
			label, _ := os.ReadFile(strings.TrimSuffix(in, "_input") + "_label")
			l := strings.ToLower(strings.TrimSpace(string(label)))
			for _, want := range cpuPackageLabels {
				if l == want && !hasPkg {
					pkg, hasPkg = c, true
				}
			}
			if c > max {
				max = c
			}
		}
		v := max
		if hasPkg {
			v = pkg
		}
		if v > 0 && (!found || v > best) {
			best, found = v, true
		}
	}
	if !found {
		zones, _ := filepath.Glob(filepath.Join(sysRoot, "class/thermal/thermal_zone*"))
		for _, dir := range zones {
			t, _ := os.ReadFile(filepath.Join(dir, "type"))
			typ := strings.ToLower(strings.TrimSpace(string(t)))
			if typ != "x86_pkg_temp" && !strings.Contains(typ, "cpu") && !strings.Contains(typ, "soc") {
				continue
			}
			if c, ok := readMilliCelsius(filepath.Join(dir, "temp")); ok && (!found || c > best) {
				best, found = c, true
			}
		}
	}
	if !found {
		return nil
	}
	v := round1(best)
	return &v
}
