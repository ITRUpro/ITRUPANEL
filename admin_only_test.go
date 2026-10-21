// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Панель принимает только сессию администратора: сессия любого другого имени отклоняется.
func TestOnlyAdministratorSession(t *testing.T) {
	mux := testPanel(t)
	sessMu.Lock()
	sessions["other"] = &session{User: "alice", Expires: time.Now().Add(time.Hour)}
	sessMu.Unlock()
	for _, name := range []string{"me", "sites.list", "db.list"} {
		r := httptest.NewRequest(http.MethodPost, "/api/"+name, strings.NewReader("{}"))
		r.Header.Set("X-Itrupanel", "1")
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: "other"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: non-admin session got %d", name, w.Code)
		}
	}
	if code, _ := testAPI(t, mux, "me", "{}"); code != 200 {
		t.Fatalf("admin session: %d", code)
	}
}
