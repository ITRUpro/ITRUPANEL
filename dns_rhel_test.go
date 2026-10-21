// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestRHELNamedConfIdempotent(t *testing.T) {
	orig := "options {\n\tlisten-on port 53 { 127.0.0.1; };\n\tlisten-on-v6 port 53 { ::1; };\n\tallow-query     { localhost; };\n\trecursion yes;\n};\n"
	once := rhelNamedConf(orig)
	twice := rhelNamedConf(rhelNamedConf(once))
	if once != twice {
		t.Fatalf("повторная настройка изменила файл:\n%s\n---\n%s", once, twice)
	}
	if strings.Count(once, "allow-recursion") != 1 || !strings.Contains(once, "allow-query     { any; };") || !strings.Contains(once, "listen-on port 53 { any; };") {
		t.Fatalf("named.conf:\n%s", once)
	}
	broken := strings.Replace(once, "\tallow-recursion { localhost; };\n", "\tallow-recursion { localhost; };\n\tallow-recursion { localhost; };\n\tallow-recursion { localhost; };\n", 1)
	if fixed := rhelNamedConf(broken); strings.Count(fixed, "allow-recursion") != 1 {
		t.Fatalf("дубли не убраны:\n%s", fixed)
	}
}
