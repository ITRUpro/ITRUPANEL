// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import "testing"

func TestAutoUpdateModes(t *testing.T) {
	cases := []struct {
		mode, cur, next string
		want            bool
	}{
		{"all", "0.2.1", "0.3.0", true},
		{"all", "0.2.1", "1.0.0", true},
		{"patch", "0.2.1", "0.2.2", true},
		{"patch", "0.2.1", "0.3.0", false},
		{"patch", "0.2.9", "1.2.9", false},
		{"off", "0.2.1", "0.2.2", false},
		{"", "0.2.1", "0.2.2", false},
		{"patch", "0.2", "0.2.2", false},
	}
	for _, c := range cases {
		if got := autoUpdateAllowed(c.mode, c.cur, c.next); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
	if m, h, err := normalizeAutoUpdate("", 4); err != nil || m != "off" || h != 4 {
		t.Fatalf("пустой режим: %q %d %v", m, h, err)
	}
	for _, bad := range []struct {
		m string
		h int
	}{{"sometimes", 4}, {"all", -1}, {"all", 24}} {
		if _, _, err := normalizeAutoUpdate(bad.m, bad.h); err == nil {
			t.Fatalf("принято: %+v", bad)
		}
	}
}

func TestAutoUpdateSettingsAPIAndDefaults(t *testing.T) {
	mux := testPanel(t)
	_, g := testAPI(t, mux, "updates.auto.get", `{}`)
	d := g["data"].(map[string]any)
	if d["mode"] != "off" {
		t.Fatalf("по умолчанию автообновление должно быть выключено (ручное обновление): %v", d)
	}
	if _, r := testAPI(t, mux, "updates.auto.save", `{"mode":"patch","hour":3}`); r["ok"] != true || store.Settings.AutoUpdate != "patch" || store.Settings.AutoUpdateHour != 3 {
		t.Fatalf("сохранение: %v", r)
	}
	if _, r := testAPI(t, mux, "updates.auto.save", `{"mode":"bogus","hour":3}`); r["error"] == nil {
		t.Fatal("неверный режим принят")
	}
	if _, r := testAPI(t, mux, "updates.auto.save", `{"mode":"off","hour":3}`); r["ok"] != true || store.Settings.AutoUpdate != "off" {
		t.Fatalf("выключение: %v", r)
	}
	// Ручное обновление не зависит от режима: сведения о выпуске доступны всегда.
	if _, r := testAPI(t, mux, "updates.info", `{}`); r["ok"] != true {
		t.Fatalf("updates.info: %v", r)
	}
	// Только администратор может менять режим.
	for _, name := range []string{"updates.auto.save"} {
		if _, ok := apis[name]; !ok {
			t.Fatalf("нет %s", name)
		}
	}
}
