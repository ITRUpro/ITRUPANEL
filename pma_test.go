// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPMARuntimeOutsideReadOnlyApplication(t *testing.T) {
	testPanel(t)
	if err := installPMA(); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(P("/var/lib/itrupanel"))
	if err != nil || parent.Mode().Perm() != 0755 {
		t.Fatal("PHP cannot traverse runtime parent", err)
	}
	pool := readFile(phpPoolDir(defaultPHP()) + "/itru_pma.conf")
	if !strings.Contains(pool, "session.save_path] = "+pmaDataDir+"/sessions") || strings.Contains(pool, cfg.Home+"/www") {
		t.Fatal(pool)
	}
	config := readFile(cfg.Home + "/www/phpmyadmin/config.inc.php")
	for _, want := range []string{"$cfg['TempDir'] = '" + pmaDataDir + "/tmp'", "$cfg['SessionSavePath'] = '" + pmaDataDir + "/sessions'", "HTTP_X_ITRUPANEL_PMA_URL"} {
		if !strings.Contains(config, want) {
			t.Errorf("missing %q in config", want)
		}
	}
	for _, suffix := range []string{"", "/tmp", "/sessions"} {
		st, err := os.Stat(P(pmaDataDir + suffix))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0700 {
			t.Fatalf("runtime permissions: %v", st.Mode())
		}
	}
	ng := readFile(vhostDir + "/000-itrupanel-pma.conf")
	if !strings.Contains(ng, "listen 127.0.0.1:8088;") {
		t.Fatal("PMA exposed publicly")
	}
}

func TestPMALinksBoundToSessionExpireAndProxy(t *testing.T) {
	testPanel(t)
	now := time.Now()
	var upstream *http.Request
	h := newPMAProxy(func() time.Time { return now }, transportFunc(func(r *http.Request) (*http.Response, error) {
		upstream = r
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{
			"Set-Cookie": {"phpMyAdmin=db-session; Path=/phpmyadmin/; HttpOnly", sessCookie + "=forged; Path=/"},
			"Location":   {"/phpmyadmin/index.php?route=/"},
		}, Request: r}, nil
	}))
	request := func(path, sid string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://panel.test:3333"+path, nil)
		if sid != "" {
			r.AddCookie(&http.Cookie{Name: sessCookie, Value: sid})
		}
		r.AddCookie(&http.Cookie{Name: "phpMyAdmin", Value: "db-session"})
		r.Header.Set("X-Itrupanel-Pma-Url", "https://attacker.invalid/")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("/phpmyadmin/", ""); w.Code != 302 || w.Header().Get("Location") != "/#/login" {
		t.Fatal(w)
	}
	first := request("/phpmyadmin/", "test").Header().Get("Location")
	second := request("/phpmyadmin/", "test").Header().Get("Location")
	if first == second || len(first) != len("/phpmyadmin/")+65 {
		t.Fatal("links not independently random", first, second)
	}
	if w := request("/phpmyadmin/index.php", "test"); w.Code != 404 {
		t.Fatal("plain route accepted")
	}
	if w := request("/phpmyadmin/"+strings.Repeat("0", 64)+"/", "test"); w.Code != 404 {
		t.Fatal("unknown token accepted")
	}
	sessions["other"] = &session{User: "admin", Expires: time.Now().Add(time.Hour)}
	if w := request(first, "other"); w.Code != 404 {
		t.Fatal("token grants another login access")
	}
	w := request(first+"index.php?route=/sql", "test")
	if w.Code != 200 || upstream.URL.Path != "/phpmyadmin/index.php" || upstream.URL.RawQuery != "route=/sql" {
		t.Fatal("upstream URL", upstream)
	}
	if _, err := upstream.Cookie(sessCookie); err == nil {
		t.Fatal("panel cookie leaked to PHP")
	}
	if upstream.Header.Get("X-Itrupanel-Pma-Url") != "https://panel.test:3333"+first {
		t.Fatal("public URL was spoofed")
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Path != first || !cs[0].Secure || !cs[0].HttpOnly {
		t.Fatal("cookie isolation", cs)
	}
	if w.Header().Get("Location") != first+"index.php?route=/" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Header())
	}
	if request(second, "test").Code != 200 {
		t.Fatal("opening a second tab invalidated the first")
	}
	now = now.Add(time.Hour + time.Second)
	if request(first, "test").Code != 404 {
		t.Fatal("expired link accepted")
	}
	now = now.Add(-time.Hour - time.Second)
	delete(sessions, "test")
	if request(first, "test").Code != 302 {
		t.Fatal("logout did not revoke link")
	}
}
