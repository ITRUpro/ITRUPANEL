// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import "testing"

// В демо сервер не перезагружается; при выполняющихся задачах перезагрузка не начинается.
func TestRebootGuards(t *testing.T) {
	mux := testPanel(t)
	if _, r := testAPI(t, mux, "system.reboot", `{}`); r["error"] == nil {
		t.Fatal("demo mode must not reboot")
	}
	_, r := testAPI(t, mux, "system.info", `{}`)
	if r["data"].(map[string]any)["reboot_required"] != false {
		t.Fatalf("reboot flag: %v", r["data"])
	}
}
