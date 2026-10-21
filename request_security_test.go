// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecurityBrowserOrigins(t *testing.T) {
	for _, tc := range []struct {
		origin, site string
		want         bool
	}{
		{"", "", true}, {"https://panel.test:3333", "same-origin", true},
		{"https://panel.test:4444", "same-site", false}, {"https://evil.test", "", false},
		{"null", "", false}, {"https://evil.test@panel.test:3333", "", false},
		{"https://panel.test:3333/", "", false}, {"https://panel.test:3333", "cross-site", false},
	} {
		r := httptest.NewRequest("POST", "https://panel.test:3333/api/me", nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if got := browserRequestAllowed(r); got != tc.want {
			t.Errorf("origin %q site %q: %v", tc.origin, tc.site, got)
		}
	}
}
func TestSecurityCrossOriginMutations(t *testing.T) {
	mux := testPanel(t)
	for _, p := range []string{"/api/login", "/api/logout", "/api/settings.password", "/api/files/upload", "/api/shell"} {
		r := httptest.NewRequest("POST", p, strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://attacker.test")
		r.Header.Set("X-Itrupanel", "1")
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s accepted cross-origin: %d", p, w.Code)
		}
	}
	if sessions["test"] == nil {
		t.Fatal("cross-origin logout revoked session")
	}
	r := httptest.NewRequest("GET", "/api/logout", nil)
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 405 || sessions["test"] == nil {
		t.Fatal("GET logged out session")
	}
}

type brokenBody struct{ done bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		return copy(p, `{}`), nil
	}
	return 0, errors.New("connection interrupted")
}
func (b *brokenBody) Close() error { return nil }
func TestSecurityRejectsTruncatedAndOversizeBody(t *testing.T) {
	mux := testPanel(t)
	called := false
	api("audit.body", func(*Req) (any, error) { called = true; return nil, nil })
	for _, body := range []io.ReadCloser{&brokenBody{}, io.NopCloser(strings.NewReader(strings.Repeat(" ", 24<<20) + "{}"))} {
		r := httptest.NewRequest("POST", "/api/audit.body", nil)
		r.Body = body
		r.Header.Set("X-Itrupanel", "1")
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if called || (w.Code != 400 && w.Code != 413) {
			t.Fatalf("bad body executed: %d %v", w.Code, called)
		}
	}
}
func TestSecurityAtomicWriteRejectsPredictableTempAttack(t *testing.T) {
	testPanel(t)
	target := "/etc/test.conf"
	os.MkdirAll(filepath.Dir(P(target)), 0755)
	victim := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(victim, []byte("secret"), 0600)
	if e := os.Symlink(victim, P(target)+".itrupanel-tmp"); e != nil {
		t.Fatal(e)
	}
	if e := writeFile(target, []byte("config"), 0600); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(victim)
	if string(b) != "secret" {
		t.Fatal("symlink target overwritten")
	}
	fi, _ := os.Stat(P(target))
	if fi.Mode().Perm() != 0600 {
		t.Fatal(fi.Mode())
	}
}
func TestSecuritySessionLimitsAndSocketRevocation(t *testing.T) {
	testPanel(t)
	for i := 0; i < 40; i++ {
		sessions[string(rune('A'+i))] = &session{User: "alice", Expires: time.Now().Add(time.Hour)}
	}
	sessions["expired"] = &session{User: "bravo", Expires: time.Now().Add(-time.Hour)}
	sessMu.Lock()
	trimUserSessionsLocked("alice", 31)
	sessMu.Unlock()
	n := 0
	for _, s := range sessions {
		if s.User == "alice" {
			n++
		}
	}
	if n != 31 || sessions["expired"] != nil || sessions["test"] == nil {
		t.Fatal("session pruning")
	}
	r := httptest.NewRequest("GET", "/api/shell", nil)
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
	s := sessions["test"]
	if !sessionStillActive(r, s) {
		t.Fatal("live session rejected")
	}
	delete(sessions, "test")
	if sessionStillActive(r, s) {
		t.Fatal("revoked terminal remains active")
	}
}
