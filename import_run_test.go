// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestImpUserName(t *testing.T) {
	for _, c := range []struct{ owner, domain, want string }{
		{"shop", "shop.example.com", "shop"},
		{"Shop-User", "x.com", "shop_user"},
		{"www-data", "blog.example.org", "blog"},
		{"root", "example.net", "example"},
		{"", "my-site.ru", "mysite"},
		{"1bad", "x.com", "u1bad"},
		{strings.Repeat("a", 40), "x.com", strings.Repeat("a", 32)},
	} {
		if got := impUserName(c.owner, c.domain); got != c.want {
			t.Errorf("%q/%q: %q, want %q", c.owner, c.domain, got, c.want)
		}
	}
}

func TestImpNeeds(t *testing.T) {
	testPanel(t)
	inv := &ImpInventory{Sites: []ImpSite{
		{Domain: "a.test", Mode: "php", PHP: "8.1"},
		{Domain: "b.test", Mode: "php", PHP: "7.4"},
		{Domain: "c.test", Mode: "php"},
		{Domain: "d.test", Mode: "static"},
	}}
	by := func(sel impSelection) map[string]ImpNeed {
		m := map[string]ImpNeed{}
		for _, n := range impNeeds(inv, sel) {
			m[n.Key] = n
		}
		return m
	}
	all := by(impSelection{Sites: []string{"a.test", "b.test", "c.test", "d.test"}, DBs: []string{"x"}})
	if _, ok := all["php:8.1"]; !ok || all["php:7.4"].Title != "PHP 7.4" || all["mysql"].Key == "" {
		t.Fatalf("needs: %+v", all)
	}
	if one := by(impSelection{Sites: []string{"a.test"}}); len(one) != 1 || one["php:8.1"].Key == "" {
		t.Fatalf("only the chosen site must matter: %+v", one)
	}
	if st := by(impSelection{Sites: []string{"d.test"}}); len(st) != 0 {
		t.Fatalf("a static site needs nothing: %+v", st)
	}
	if unk := by(impSelection{Sites: []string{"c.test"}}); unk["php:default"].Key == "" {
		t.Fatalf("a site with an unknown PHP version needs the default one: %+v", unk)
	}
}

func TestImpDBPasswordHash(t *testing.T) {
	good := "*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19"
	cases := map[string]string{
		good + "\tmysql_native_password\n":       good,
		strings.ToLower(good) + "\n":             good,
		"$A$005$abcdef\tcaching_sha2_password\n": "",
		good + "\tmysql_old_password\n":          "",
		"\n":                                     "",
	}
	for out, want := range cases {
		r := fakeRun{"mysql -N -B -e ": out}
		if got := impDBPasswordHash(context.Background(), r, "shop"); got != want {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
}

func TestImpDBCharset(t *testing.T) {
	for out, want := range map[string]string{"utf8mb3\n": "utf8", "utf8mb4\n": "utf8mb4", "cp1251\n": "cp1251", "koi8r\n": "utf8mb4", "latin1\n": "latin1"} {
		if got := impDBCharset(context.Background(), fakeRun{"mysql -N -B -e ": out}, "db"); got != want {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
	if got := impDBCharset(context.Background(), fakeRun{"mysql -N -B -e ": "ERR:x"}, "db"); got != "utf8mb4" {
		t.Errorf("a failed query must fall back to utf8mb4: %q", got)
	}
}

// DEFINER удаляется из дампа: старых пользователей на новом сервере нет.
func TestImpDefinerStripped(t *testing.T) {
	in := "CREATE ALGORITHM=UNDEFINED DEFINER=`olduser`@`localhost` SQL SECURITY DEFINER VIEW `v` AS select 1;\n/*!50013 DEFINER=`root`@`%` SQL SECURITY DEFINER */\n/*!50003 CREATE*/ /*!50017 DEFINER=`a`@`b`*/ /*!50003 TRIGGER t BEFORE INSERT ON x FOR EACH ROW SET NEW.a=1 */;\nINSERT INTO t VALUES ('DEFINER=x');\n"
	cmd := exec.Command("sed", "-E", reImpDefiner)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Skip("sed недоступен: " + err.Error())
	}
	got := string(out)
	if strings.Contains(got, "`olduser`") || strings.Contains(got, "`root`@") || strings.Contains(got, "`a`@`b`") {
		t.Fatalf("DEFINER remained:\n%s", got)
	}
	if !strings.Contains(got, "INSERT INTO t VALUES ('DEFINER=x');") || !strings.Contains(got, "VIEW `v` AS select 1") {
		t.Fatalf("the dump data must stay intact:\n%s", got)
	}
}

// Кодировки, которых нет на новом сервере, заменяются при передаче дампа.
func TestImpSQLScriptCollations(t *testing.T) {
	dump := "CREATE TABLE a (x text) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_uca1400_ai_ci;\nCREATE TABLE b (x text) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;\nCREATE TABLE c (x text) DEFAULT CHARSET=utf8mb3 COLLATE=utf8mb3_general_ci;\nINSERT INTO a VALUES ('utf8mb4_uca1400_ai_ci stays in data? no: replaced');\n"
	run := func(script string) string {
		cmd := exec.Command("sed", "-E", script)
		cmd.Stdin = strings.NewReader(dump)
		out, err := cmd.Output()
		if err != nil {
			t.Skip("sed недоступен: " + err.Error())
		}
		return string(out)
	}
	none := func(string) bool { return false }
	all := func(string) bool { return true }
	old := run(impSQLScript(none))
	if strings.Contains(old, "uca1400") || strings.Contains(old, "0900") || strings.Contains(old, "utf8mb3") {
		t.Fatalf("an old server must get compatible collations:\n%s", old)
	}
	if !strings.Contains(old, "COLLATE=utf8mb4_unicode_ci") || !strings.Contains(old, "COLLATE=utf8_general_ci") {
		t.Fatalf("unexpected rewrite:\n%s", old)
	}
	if kept := run(impSQLScript(all)); kept != dump {
		t.Fatalf("a server that knows the collations must get the dump unchanged:\n%s", kept)
	}
	if got := impMySQLError("--------------\nCREATE TABLE x (y int)\n--------------\n\nERROR 1273 (HY000) at line 24: Unknown collation: 'x'\n"); !strings.HasPrefix(got, "ERROR 1273") {
		t.Fatalf("error line: %q", got)
	}
}
