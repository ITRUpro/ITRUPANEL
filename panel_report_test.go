// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Отчёт содержит только случайный ID, версию и тип события; ID сохраняется и не меняется,
// а файл отключения статистики останавливает отправку.
func TestPanelReportPayloadAndOptOut(t *testing.T) {
	testPanel(t)
	os.MkdirAll(statDir(), 0700)
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		got = append(got, v)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	for i := 0; i < 2; i++ {
		if err := sendPanelReport(context.Background(), srv.Client(), srv.URL); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || len(got[0]) != 4 || got[0]["event"] != "active" || got[0]["version"] != Version || got[0]["install_id"] != got[1]["install_id"] || !reStatID.MatchString(got[0]["install_id"].(string)) {
		t.Fatalf("payload: %v", got)
	}
	os.WriteFile(filepath.Join(statDir(), "installation-stat-disabled"), nil, 0600)
	sendPanelReport(context.Background(), srv.Client(), srv.URL)
	if len(got) != 2 {
		t.Fatal("opt-out ignored")
	}
}
