// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSystemPath(t *testing.T) {
	for _, current := range []string{"", "/usr/bin:/bin", "/custom/bin:/usr/sbin:/custom/bin"} {
		got := filepath.SplitList(systemPath(current))
		seen := map[string]bool{}
		for _, dir := range got {
			if seen[dir] {
				t.Fatalf("duplicate PATH entry %q", dir)
			}
			seen[dir] = true
		}
		for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/bin", "/usr/local/sbin"} {
			if !seen[dir] {
				t.Fatalf("PATH missing %s: %s", dir, got)
			}
		}
		if strings.Contains(current, "/custom/bin") && !seen["/custom/bin"] {
			t.Fatal("custom PATH was lost")
		}
	}
}

func TestNginxMissingCommandReportsCause(t *testing.T) {
	testPanel(t)
	t.Setenv("PATH", t.TempDir())
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	err := nginxTestReload()
	if err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("missing command cause was lost: %v", err)
	}
}

func TestNginxServerTokensScope(t *testing.T) {
	global, err := tplFS.ReadFile("templates/nginx-global.conf")
	if err != nil {
		t.Fatal(err)
	}
	// Debian's main http block can already define server_tokens.
	combinedHTTP := "server_tokens off;\n" + string(global)
	if strings.Count(combinedHTTP, "server_tokens ") != 1 {
		t.Fatal("panel adds a duplicate server_tokens to the http context")
	}
	var site bytes.Buffer
	err = tplSite.Execute(&site, map[string]any{
		"Domain": "example.test", "Names": "example.test", "Root": "/var/www/example",
		"Index": "index.html", "Mode": "static", "WWWRedirect": "", "Custom": "/tmp/custom.conf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(site.String(), "server {\n    server_tokens off;") {
		t.Fatal("generated site no longer hides nginx version at server scope")
	}
}

// Tests run against an isolated demo filesystem and never execute server commands.
func testPanel(t *testing.T) *http.ServeMux {
	t.Helper()
	root := t.TempDir()
	cfg = Config{Root: root, Home: filepath.Join(root, "usr/local/itrupanel"), Dry: true, Insecure: true}
	store = State{}
	apis = map[string]apiDef{}
	sessions = map[string]*session{"test": {User: "admin", IP: "127.0.0.1", Expires: time.Now().Add(time.Hour)}}
	mustLoad()
	// Most regression tests exercise an already-onboarded admin. Terms tests clear this fixture.
	store.TermsAcceptance = map[string]TermsAcceptance{"admin": {Revision: termsRevision, SHA256: termsDigest, AcceptedAt: time.Now().UTC()}}
	detectOS()
	mux := http.NewServeMux()
	registerAPI(mux)
	return mux
}

func testAPI(t *testing.T, mux *http.ServeMux, name, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/"+name, strings.NewReader(body))
	r.Header.Set("X-Itrupanel", "1")
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return w.Code, result
}

func TestSafeUnder(t *testing.T) {
	for _, tt := range []struct {
		path, want string
		ok         bool
	}{
		{"data/www/site", "/var/www/alice/data/www/site", true},
		{"/var/www/alice/data/www/site", "/var/www/alice/data/www/site", true},
		{"", "/var/www/alice", true},
		{"../bob", "", false},
		{"/var/www/alice/../bob", "", false},
		{"/var/www/alice2/data", "", false},
		{"/etc", "", false},
		{"data/\x00file", "", false},
	} {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := safeUnder("/var/www/alice", tt.path)
			if ok != tt.ok || ok && got != tt.want {
				t.Fatalf("safeUnder(%q) = %q, %v; want %q, %v", tt.path, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSiteAbsoluteRoot(t *testing.T) {
	testPanel(t)
	want := "/var/www/alice/data/www/custom"
	s, err := createOrUpdateSite(siteIn{Domain: "test.example", User: "alice", Root: want, Mode: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != want {
		t.Fatalf("root = %q, want %q", s.Root, want)
	}
	want = "/var/www/alice/data/www/edited"
	s, err = createOrUpdateSite(siteIn{Domain: s.Domain, Edit: true, Root: want, Mode: "static"})
	if err != nil || s.Root != want {
		t.Fatalf("edit = %v, %v", s, err)
	}
}

func TestFTPHomeValidation(t *testing.T) {
	mux := testPanel(t)
	store.Users = []*SiteUser{{Name: "alice", Home: "/var/www/alice"}}
	_, result := testAPI(t, mux, "ftp.create", `{"name":"alice_ftp","owner":"alice","home":"/var/www/alice/data/www","password":"Example12345"}`)
	if result["ok"] != true || len(store.FTP) != 1 || store.FTP[0].Home != "/var/www/alice/data/www" {
		t.Fatalf("valid FTP home: %v", result)
	}
	_, result = testAPI(t, mux, "ftp.create", `{"name":"other_ftp","owner":"alice","home":"/var/www/alice2/data/www","password":"Example12345"}`)
	if result["error"] == nil || len(store.FTP) != 1 {
		t.Fatalf("outside FTP home accepted: %v", result)
	}
}

func TestLoginLogout(t *testing.T) {
	mux := testPanel(t)
	if err := setAdminPassword("TestPassword123"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"user":"admin","password":"TestPassword123"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("missing session cookie: %s", w.Body.String())
	}
	for _, name := range []string{"me", "logout", "me"} {
		r = httptest.NewRequest(http.MethodPost, "/api/"+name, strings.NewReader(`{}`))
		r.Header.Set("X-Itrupanel", "1")
		r.AddCookie(cookies[0])
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if name == "logout" {
			if getSession(r) != nil {
				t.Fatal("logout kept session")
			}
		} else if getSession(r) != nil && w.Code != http.StatusOK {
			t.Fatalf("authenticated request failed: %d", w.Code)
		} else if getSession(r) == nil && w.Code != http.StatusUnauthorized {
			t.Fatalf("expired session accepted: %d", w.Code)
		}
	}
}

func TestSiteRootRollback(t *testing.T) {
	testPanel(t)
	s, err := createOrUpdateSite(siteIn{Domain: "test.example", User: "alice", Mode: "static"})
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := s.Root
	// An unwritable configuration destination forces applySite to fail.
	if err := os.Remove(P(vhostPath(s.Domain))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(P(vhostPath(s.Domain)), 0755); err != nil {
		t.Fatal(err)
	}
	_, err = createOrUpdateSite(siteIn{Domain: s.Domain, Edit: true, Mode: "static", Root: "/var/www/alice/data/www/new"})
	if err == nil {
		t.Fatal("expected configuration write error")
	}
	if s.Root != oldRoot {
		t.Fatalf("failed edit changed root to %q", s.Root)
	}
}

func TestBackupHourSurvivesReload(t *testing.T) {
	mux := testPanel(t)
	if store.Settings.BackupHour != 3 {
		t.Fatal("new installations should default to 03:00")
	}
	_, result := testAPI(t, mux, "backups.settings", `{"daily":true,"keep":7,"hour":0}`)
	if result["ok"] != true {
		t.Fatal(result)
	}
	mustLoad()
	if store.Settings.BackupHour != 0 {
		t.Fatalf("midnight became %02d:00 after reload", store.Settings.BackupHour)
	}
	if err := os.WriteFile(statePath(), []byte(`{"settings":{"backup_keep":5}}`), 0600); err != nil {
		t.Fatal(err)
	}
	mustLoad()
	if store.Settings.BackupHour != 3 {
		t.Fatal("missing hour should default to 03:00")
	}
}

func TestInvalidFileWriteDoesNotChangeFile(t *testing.T) {
	for _, body := range []string{`{"path":"/note.txt","text":123}`, `{"path":"/note.txt","text":`} {
		t.Run(body, func(t *testing.T) {
			mux := testPanel(t)
			if err := os.WriteFile(P("/note.txt"), []byte("keep me"), 0644); err != nil {
				t.Fatal(err)
			}
			_, result := testAPI(t, mux, "files.write", body)
			if result["error"] == nil {
				t.Errorf("invalid request accepted: %v", result)
			}
			if got := readFile("/note.txt"); got != "keep me" {
				t.Fatalf("file overwritten: %q", got)
			}
		})
	}
}

func TestOwnerNameConcurrent(t *testing.T) {
	fi, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uidCache = map[uint32]string{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; ownerName(fi) }()
	}
	close(start)
	wg.Wait()
}

func TestSetupReportsFailure(t *testing.T) {
	testPanel(t)
	// The nginx step cannot replace a directory with its configuration file.
	if err := os.MkdirAll(P("/etc/nginx/conf.d/itrupanel.conf"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := runSetup("none"); err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Fatalf("setup did not report nginx failure: %v", err)
	}
	if fileExists("/etc/systemd/system/itrupanel.service") {
		t.Fatal("panel service was installed despite a failed prerequisite")
	}
}

func TestStopServicesWithoutSystemd(t *testing.T) {
	testPanel(t)
	bin := t.TempDir()
	logFile := filepath.Join(bin, "commands.log")
	for _, name := range []string{"nginx", "pkill", "mariadb-admin"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"" + name + " $*\" >> \"$ITRUPANEL_TEST_COMMAND_LOG\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin) // Only inert test commands can be executed.
	t.Setenv("ITRUPANEL_TEST_COMMAND_LOG", logFile)
	cfg.Dry = false
	osInfo.Systemd = false
	t.Cleanup(func() { cfg.Dry = true })
	for _, name := range []string{"nginx", "php8.3-fpm", "mariadb", "cron", "crond", "pure-ftpd"} {
		if err := service("stop", name); err != nil {
			t.Fatalf("stop %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "nginx -s quit\npkill -f php-fpm: master process.*/etc/php/8.3/fpm\nmariadb-admin shutdown\npkill -x cron\npkill -x crond\npkill -x pure-ftpd\n"
	if string(data) != want {
		t.Fatalf("unexpected service commands:\n%s", data)
	}
}

func TestDemoReadAPIs(t *testing.T) {
	mux := testPanel(t)
	demoSeed()
	for _, name := range []string{"me", "sites.list", "sites.form", "users.list", "db.list", "dns.list", "ftp.list", "cron.list", "backups.list", "php.list", "system.info", "notices.list", "files.list"} {
		t.Run(name, func(t *testing.T) {
			status, result := testAPI(t, mux, name, `{}`)
			if status != http.StatusOK || result["ok"] != true {
				t.Fatalf("status %d: %v", status, result)
			}
		})
	}
	r := httptest.NewRequest(http.MethodPost, "/api/sites.list", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: %d", w.Code)
	}
}
