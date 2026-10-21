// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestDBServerLowMemoryArgs(t *testing.T) {
	std := strings.Join(dbServerArgs(false), " ")
	if strings.Contains(std, "performance-schema") || !strings.Contains(std, "utf8mb4") {
		t.Fatalf("стандартные параметры: %s", std)
	}
	low := strings.Join(dbServerArgs(true), " ")
	for _, want := range []string{dbLowMemoryFlag, "--innodb-buffer-pool-size=64M", "--max-connections=50", "utf8mb4_unicode_ci"} {
		if !strings.Contains(low, want) {
			t.Fatalf("нет %s в экономных параметрах: %s", want, low)
		}
	}
}

// На серверах с памятью от 4 ГБ (и в демо) экономные файлы Rspamd пустые — настройки по умолчанию.
func TestSpamLowMemoryFiles(t *testing.T) {
	testPanel(t)
	if spamLowMemory("disable_hyperscan = true;\n") != "" {
		t.Fatal("экономный режим включён без нехватки памяти")
	}
}
