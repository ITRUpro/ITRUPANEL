// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestRepairEmptyIniValues(t *testing.T) {
	in := "[PHP]\nmemory_limit = 256M\nmax_input_vars = \n;max_input_vars = 1000\ndate.timezone =\nshort_open_tag = Off\nmax_input_time = \"\"\nfoo = \n"
	out, changed := repairEmptyIniValues(in)
	if !changed {
		t.Fatal("пустые значения не найдены")
	}
	for _, bad := range []string{"\nmax_input_vars = \n", "\ndate.timezone =\n", "\nmax_input_time = \"\"\n"} {
		if strings.Contains(out, bad) {
			t.Fatalf("осталось %q:\n%s", bad, out)
		}
	}
	for _, keep := range []string{"memory_limit = 256M", ";max_input_vars = 1000", "short_open_tag = Off", "\nfoo = \n"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("изменено %q:\n%s", keep, out)
		}
	}
	if again, ch := repairEmptyIniValues(out); ch || again != out {
		t.Fatal("повторный запуск меняет файл")
	}
}

func TestWriteIniSkipsEmptyFields(t *testing.T) {
	testPanel(t)
	osInfo.Family = "debian"
	p := phpIni("8.3")
	if err := writeFile(p, []byte("[PHP]\nmemory_limit = 128M\n;max_input_vars = 1000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeIni("8.3", map[string]string{"memory_limit": "512M", "max_input_vars": "", "date.timezone": "  "}); err != nil {
		t.Fatal(err)
	}
	s := readFile(p)
	if !strings.Contains(s, "memory_limit = 512M") || strings.Contains(s, "max_input_vars = \n") || strings.Contains(s, "\nmax_input_vars =") || strings.Contains(s, "date.timezone") {
		t.Fatalf("php.ini:\n%s", s)
	}
}
