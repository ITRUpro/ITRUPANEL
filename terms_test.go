// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTermsGateAndPersistence(t *testing.T) {
	mux := testPanel(t)
	store.TermsAcceptance = nil
	_, me := testAPI(t, mux, "me", `{}`)
	if !me["data"].(map[string]any)["terms_required"].(bool) {
		t.Fatal("new login not gated")
	}
	for _, name := range []string{"sites.list", "sites.save", "system.info", "files.list", "updates.info", "feedback.send"} {
		code, res := testAPI(t, mux, name, `{}`)
		if code != 403 || res["code"] != "terms_required" {
			t.Fatalf("%s bypass: %d %v", name, code, res)
		}
	}
	for _, path := range []string{"/api/shell", "/api/files/download", "/api/files/upload", "/api/backups/download"} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("special route bypass: %s %d", path, w.Code)
		}
	}
	hit := false
	proxy := requireAuthPage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	r := httptest.NewRequest("GET", "/phpmyadmin/", nil)
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	if hit || w.Code != 302 {
		t.Fatal("phpMyAdmin bypass")
	}
	_, res := testAPI(t, mux, "terms.get", `{}`)
	doc := res["data"].(map[string]any)
	if doc["sha256"] != termsDigest || !strings.Contains(doc["text"].(string), "Гарантии и ответственность") {
		t.Fatal("wrong document")
	}
	for _, body := range []string{`{}`, `{"agree":false}`, `{"agree":true,"revision":"old","sha256":"old"}`, `{"agree":"true"}`} {
		_, res = testAPI(t, mux, "terms.accept", body)
		if res["error"] == nil || termsAccepted("admin") {
			t.Fatalf("invalid consent: %s %v", body, res)
		}
	}
	body := fmt.Sprintf(`{"agree":true,"revision":%q,"sha256":%q}`, termsRevision, termsDigest)
	_, res = testAPI(t, mux, "terms.accept", body)
	if res["error"] != nil || !termsAccepted("admin") {
		t.Fatalf("accept: %v", res)
	}
	first := store.TermsAcceptance["admin"]
	if first.PanelVersion != Version || first.Language != "ru" {
		t.Fatalf("incomplete receipt: %+v", first)
	}
	_, _ = testAPI(t, mux, "terms.accept", body)
	if store.TermsAcceptance["admin"] != first {
		t.Fatal("repeat changed receipt")
	}
	mustLoad()
	if !termsAccepted("admin") {
		t.Fatal("acceptance lost on restart")
	}
	if termsAccepted("other") {
		t.Fatal("acceptance crossed logins")
	}
	code, res := testAPI(t, mux, "sites.list", `{}`)
	if code != 200 || res["error"] != nil {
		t.Fatalf("accepted user blocked: %v", res)
	}
	oldVersion := Version
	Version = "0.1.999"
	defer func() { Version = oldVersion }()
	if !termsAccepted("admin") {
		t.Fatal("normal release requires reacceptance")
	}
	store.TermsAcceptance["admin"] = TermsAcceptance{Revision: termsRevision, SHA256: "old", AcceptedAt: time.Now()}
	if termsAccepted("admin") {
		t.Fatal("changed terms not gated")
	}
}

func TestTermsRejectsUnauthenticatedAndCSRF(t *testing.T) {
	mux := testPanel(t)
	store.TermsAcceptance = nil
	body := fmt.Sprintf(`{"agree":true,"revision":%q,"sha256":%q}`, termsRevision, termsDigest)
	for _, tc := range []struct {
		method, session, header string
		status                  int
	}{{"POST", "", "1", 401}, {"GET", "test", "1", 400}, {"POST", "test", "", 400}} {
		r := httptest.NewRequest(tc.method, "/api/terms.accept", strings.NewReader(body))
		r.Header.Set("X-Itrupanel", tc.header)
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: tc.session})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || termsAccepted("admin") {
			t.Fatalf("request bypass: %d", w.Code)
		}
	}
}
func TestTermsFailedSaveDoesNotUnlock(t *testing.T) {
	mux := testPanel(t)
	store.TermsAcceptance = nil
	// A directory at the atomic-write destination forces failure even under root.
	if err := os.MkdirAll(statePath()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"agree":true,"revision":%q,"sha256":%q}`, termsRevision, termsDigest)
	_, res := testAPI(t, mux, "terms.accept", body)
	if res["error"] == nil || termsAccepted("admin") {
		t.Fatal("failed save unlocked login")
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, "etc", "state.json")); err == nil {
		t.Fatal("failed write persisted")
	}
}
func TestTermsEnglishDocument(t *testing.T) {
	mux := testPanel(t)
	store.TermsAcceptance = nil
	r := httptest.NewRequest("POST", "/api/terms.get", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
	r.AddCookie(&http.Cookie{Name: "itrupanel_lang", Value: "en"})
	r.Header.Set("X-Itrupanel", "1")
	w := httptest.NewRecorder()
	languageMiddleware(mux).ServeHTTP(w, r)
	var res struct {
		Data struct{ Text, Language, SHA256 string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Data.Language != "en" || !strings.Contains(res.Data.Text, "Warranty and liability") || res.Data.SHA256 != termsDigest {
		t.Fatal("English terms unavailable")
	}
}

func TestEarlierFreeUseConsentSurvivesGPLSwitch(t *testing.T) {
	testPanel(t)
	for _, digest := range []string{"cf8e463d91e56c86ace48bba7e4378286c8f7b4c24306442c7c9023a0d964a78", "7d4220e0a0301f7b9a94ad9df79862884334d536739e9a380b3b82e4131b5245"} {
		store.TermsAcceptance["admin"] = TermsAcceptance{Revision: "1.0", SHA256: digest, AcceptedAt: time.Now()}
		if !termsAccepted("admin") {
			t.Fatal("GPL switch forced re-consent")
		}
	}
	store.TermsAcceptance["admin"] = TermsAcceptance{Revision: "1.0", SHA256: strings.Repeat("a", 64), AcceptedAt: time.Now()}
	if termsAccepted("admin") {
		t.Fatal("unknown earlier document accepted")
	}
	old := termsDigest
	defer func() { termsDigest = old }()
	store.TermsAcceptance["admin"] = TermsAcceptance{Revision: termsRevision, SHA256: termsDigest, AcceptedAt: time.Now()}
	if !termsAccepted("admin") {
		t.Fatal("current consent rejected")
	}
	termsDigest = strings.Repeat("b", 64)
	if termsAccepted("admin") {
		t.Fatal("other changes bypassed consent")
	}
}

func TestRoundcubeSupportDomainMigration(t *testing.T) {
	testPanel(t)
	store.Mail.Webmail.Ready = true
	if err := writeFile(webmailOwned, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/etc/roundcube/config.inc.php"
	input := "<?php\n// ITRUPANEL: managed Roundcube\n$config['support_url'] = 'https://old.example/forum/';\n$config['des_key'] = 'keep-this-secret';\n"
	if err := writeFile(path, []byte(input), 0640); err != nil {
		t.Fatal(err)
	}
	for _, m := range panelMigrations() {
		if m.ID == "005-roundcube-support-domain" {
			if err := m.Apply(); err != nil {
				t.Fatal(err)
			}
			got := readFile(path)
			if !strings.Contains(got, "https://itrupanel.com/forum/") || !strings.Contains(got, "keep-this-secret") {
				t.Fatal("config not migrated safely")
			}
			if err := m.Apply(); err != nil {
				t.Fatal(err)
			}
			if readFile(path) != got {
				t.Fatal("not idempotent")
			}
			return
		}
	}
	t.Fatal("migration missing")
}
