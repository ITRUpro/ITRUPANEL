// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSys(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, v := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(v+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCPUTemperature(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  float64 // 0 — датчика нет
	}{
		{"Intel: датчик всего процессора", map[string]string{
			"class/hwmon/hwmon0/name": "acpitz", "class/hwmon/hwmon0/temp1_input": "27800",
			"class/hwmon/hwmon1/name": "coretemp", "class/hwmon/hwmon1/temp1_input": "52000", "class/hwmon/hwmon1/temp1_label": "Package id 0",
			"class/hwmon/hwmon1/temp2_input": "55000", "class/hwmon/hwmon1/temp2_label": "Core 0"}, 52},
		{"AMD: Tctl", map[string]string{"class/hwmon/hwmon2/name": "k10temp", "class/hwmon/hwmon2/temp1_input": "61250", "class/hwmon/hwmon2/temp1_label": "Tctl"}, 61.3},
		{"ARM: thermal zone", map[string]string{"class/thermal/thermal_zone0/type": "cpu-thermal", "class/thermal/thermal_zone0/temp": "47236"}, 47.2},
		{"VPS без датчиков", map[string]string{"class/thermal/thermal_zone0/type": "acpitz", "class/thermal/thermal_zone0/temp": "16800"}, 0},
		{"неверное значение", map[string]string{"class/hwmon/hwmon0/name": "coretemp", "class/hwmon/hwmon0/temp1_input": "-273000"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeSys(t, root, c.files)
			got := cpuTemperature(root)
			if c.want == 0 {
				if got != nil {
					t.Fatalf("температура %v, датчика нет", *got)
				}
				return
			}
			if got == nil || *got != c.want {
				t.Fatalf("температура %v, ожидалось %v", got, c.want)
			}
		})
	}
}
