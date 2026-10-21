// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
	"time"
)

func TestAdminLoginChange(t *testing.T) {
	mux := testPanel(t)
	if err := setAdminPassword("TestPassword123"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "ab", "-abc", "имя", "a b c", strings.Repeat("x", 33)} {
		if err := validAdminLogin(bad); err == nil {
			t.Fatalf("логин %q принят", bad)
		}
	}
	if l := randomLogin(); len(l) != 8 || l[0] < 'a' || l[0] > 'z' || validAdminLogin(l) != nil {
		t.Fatalf("случайный логин: %q", l)
	}
	// Неверный текущий пароль и пустая смена отклоняются.
	if _, r := testAPI(t, mux, "settings.password", `{"old":"wrong","login":"newadmin"}`); r["error"] == nil {
		t.Fatal("смена без пароля")
	}
	if _, r := testAPI(t, mux, "settings.password", `{"old":"TestPassword123"}`); r["error"] == nil {
		t.Fatal("пустая смена принята")
	}
	// Смена только логина: условия и сессия переходят на новое имя.
	if _, r := testAPI(t, mux, "settings.password", `{"old":"TestPassword123","login":"Boss_01"}`); r["ok"] != true {
		t.Fatalf("смена логина: %v", r)
	}
	if store.Settings.AdminUser != "Boss_01" || !termsAccepted("Boss_01") || termsAccepted("admin") {
		t.Fatalf("после смены: %q terms=%v/%v", store.Settings.AdminUser, termsAccepted("Boss_01"), termsAccepted("admin"))
	}
	if s := sessions["test"]; s == nil || s.User != "Boss_01" {
		t.Fatalf("сессия не переехала: %+v", sessions["test"])
	}
	if _, r := testAPI(t, mux, "me", `{}`); r["ok"] != true {
		t.Fatalf("после смены логина запросы не проходят: %v", r)
	}
	// Логин и пароль вместе.
	if _, r := testAPI(t, mux, "settings.password", `{"old":"TestPassword123","login":"root2","new":"AnotherPass456"}`); r["ok"] != true {
		t.Fatalf("смена обоих: %v", r)
	}
	if store.Settings.AdminUser != "root2" {
		t.Fatal("логин не сменился")
	}
	// Короткий пароль и неверный логин не меняют ничего.
	if _, r := testAPI(t, mux, "settings.password", `{"old":"AnotherPass456","login":"ok_name","new":"short"}`); r["error"] == nil || store.Settings.AdminUser != "root2" {
		t.Fatalf("короткий пароль: %v", r)
	}
	_ = time.Now
}
