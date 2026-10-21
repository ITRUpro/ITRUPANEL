// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginGuardPersistenceExpiryAndUnban(t *testing.T) {
	testPanel(t)
	g := &loginGuardState{}
	now := time.Now()
	ip := "203.0.113.5"
	for i := 0; i < 5; i++ {
		if w, e := g.begin(ip, now); w != 0 || e != nil {
			t.Fatalf("begin %d: %v %v", i, w, e)
		}
		if e := g.finish(ip, false, now); e != nil {
			t.Fatal(e)
		}
	}
	g = &loginGuardState{}
	if w, e := g.begin(ip, now.Add(time.Minute)); e != nil || w != 14*time.Minute {
		t.Fatalf("restart lost ban: %v %v", w, e)
	}
	if w, e := g.begin(ip, now.Add(16*time.Minute)); e != nil || w != 0 {
		t.Fatalf("expired ban: %v %v", w, e)
	}
	if e := g.finish(ip, true, now.Add(16*time.Minute)); e != nil {
		t.Fatal(e)
	}
	if e := g.unban(ip); e != nil {
		t.Fatal(e)
	}
	if len(g.records) != 0 {
		t.Fatal("unban did not clear record")
	}
}
func TestLoginGuardParallelBudget(t *testing.T) {
	testPanel(t)
	g := &loginGuardState{}
	now := time.Now()
	ip := "203.0.113.5"
	for i := 0; i < 4; i++ {
		if w, e := g.begin(ip, now); w != 0 || e != nil {
			t.Fatal(w, e)
		}
	}
	if w, e := g.begin("203.0.113.6", now); w == 0 || e != nil {
		t.Fatal("global bcrypt budget bypassed", w, e)
	}
	for i := 0; i < 4; i++ {
		if e := g.finish(ip, false, now); e != nil {
			t.Fatal(e)
		}
	}
	if w, e := g.begin(ip, now); w != 0 || e != nil {
		t.Fatal(w, e)
	}
	if w, e := g.begin(ip, now); w == 0 || e != nil {
		t.Fatal("inflight budget bypassed", w, e)
	}
	if e := g.finish(ip, false, now); e != nil {
		t.Fatal(e)
	}
	if w, e := g.begin(ip, now); w < 14*time.Minute || e != nil {
		t.Fatal("ban missing", w, e)
	}
}
func TestLoginGuardHTTPAndForwardedHeaders(t *testing.T) {
	testPanel(t)
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"User":"wrong","Password":"wrong"}`))
		r.RemoteAddr = "203.0.113.88:12345"
		r.Header.Set("X-Forwarded-For", "198.51.100.9")
		w := httptest.NewRecorder()
		handleLogin(w, r)
		if i == 5 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Fatalf("ban not enforced: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"User":"`+strings.Repeat("a", 5000)+`"}`))
	handleLogin(w, r)
	if w.Code != 400 {
		t.Fatal("unbounded login payload")
	}
}
func TestFTPGuardConfig(t *testing.T) {
	conf := ftpGuardConfig(2121)
	for _, s := range []string{"port = 2121", "backend = systemd", "filter = pure-ftpd", "action = nftables-multiport", "maxretry = 5", "bantime = 900"} {
		if !strings.Contains(conf, s) {
			t.Fatal(s)
		}
	}
	out := ftpNoResolve("TLS 1\nDontResolve no\nDontResolve yes\n# another setting\n")
	if strings.Count(out, "DontResolve ") != 1 || !strings.Contains(out, "TLS 1") {
		t.Fatal(out)
	}
}

func TestLoginGuardConcurrentFailures(t *testing.T) {
	testPanel(t)
	g := &loginGuardState{}
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, e := g.begin("203.0.113.8", time.Now())
			if e != nil {
				t.Error(e)
				return
			}
			if w == 0 {
				admitted.Add(1)
				if e := g.finish("203.0.113.8", false, time.Now()); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	if admitted.Load() > 5 {
		t.Fatalf("concurrent bypass: %d", admitted.Load())
	}
}
func TestLoginGuardPersistenceFailureClosed(t *testing.T) {
	testPanel(t)
	g := &loginGuardState{}
	now := time.Now()
	ip := "203.0.113.5"
	if w, e := g.begin(ip, now); e != nil || w != 0 {
		t.Fatal(w, e)
	}
	if e := os.Mkdir(filepath.Join(cfg.Home, "var", "login-guard.json.tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := g.finish(ip, false, now); e == nil {
		t.Fatal("ignored persistence failure")
	}
	if _, e := g.begin(ip, now); e == nil {
		t.Fatal("login allowed after failed persistence")
	}
}
func TestFTPGuardRollback(t *testing.T) {
	testPanel(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	cfg.Dry = false
	osInfo.Systemd = true
	osInfo.Family = "debian"
	t.Cleanup(func() { cfg.Dry = true })
	for _, name := range []string{"pure-pw", "nft", "apt-get", "systemctl"} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(bin, "fail2ban-client"), []byte("#!/bin/sh\nif [ \"$1\" = -t ]; then exit 1; fi\nexit 0\n"), 0755); e != nil {
		t.Fatal(e)
	}
	path := "/etc/pure-ftpd/conf/DontResolve"
	if e := writeFile(path, []byte("no\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := enableFTPGuard(&Job{}, 21); e == nil {
		t.Fatal("accepted invalid configuration")
	}
	if readFile(path) != "no\n" {
		t.Fatal("FTP config not restored")
	}
	if _, e := os.Stat(P(ftpGuardPath)); !os.IsNotExist(e) {
		t.Fatal("failed jail retained", e)
	}
}

func TestFTPGuardWaitsBeforeReload(t *testing.T) {
	testPanel(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("GUARD_TEST_DIR", bin)
	cfg.Dry = false
	osInfo.Systemd = true
	osInfo.Family = "debian"
	t.Cleanup(func() { cfg.Dry = true })
	for _, name := range []string{"pure-pw", "nft", "apt-get", "systemctl"} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	script := `#!/bin/sh
case "$1" in
ping)
 n=0
 if [ -f "$GUARD_TEST_DIR/count" ]; then read n < "$GUARD_TEST_DIR/count"; fi
 n=$((n+1))
 printf '%s\n' "$n" > "$GUARD_TEST_DIR/count"
 if [ "$n" -lt 3 ]; then echo 'Could not find server'; exit 255; fi
 echo pong
 ;;
reload)
 n=0
 if [ -f "$GUARD_TEST_DIR/count" ]; then read n < "$GUARD_TEST_DIR/count"; fi
 if [ "$n" -lt 3 ]; then echo 'reload before ready'; exit 255; fi
 : > "$GUARD_TEST_DIR/reloaded"
 ;;
esac
exit 0
`
	if e := os.WriteFile(filepath.Join(bin, "fail2ban-client"), []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	if e := enableFTPGuard(&Job{}, 21); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(bin, "reloaded")); e != nil {
		t.Fatal("jail not applied", e)
	}
}

func TestFTPGuardStartupFailureAndTimeout(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			testPanel(t)
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			cfg.Dry = false
			t.Cleanup(func() { cfg.Dry = true })
			state := "activating"
			if failed {
				state = "failed"
			}
			for name, script := range map[string]string{
				"fail2ban-client": "echo 'Could not find server'; exit 255",
				"systemctl":       "echo " + state,
				"journalctl":      "echo 'Backend import failed: systemd'",
			} {
				if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0755); e != nil {
					t.Fatal(e)
				}
			}
			e := waitFTPGuard(30*time.Millisecond, time.Millisecond)
			if e == nil {
				t.Fatal("unavailable service accepted")
			}
			diagnostic := ftpGuardStartupError(e).Error()
			if !strings.Contains(diagnostic, "Backend import failed") || !strings.Contains(diagnostic, state) {
				t.Fatal("startup diagnosis missing", diagnostic)
			}
		})
	}
}

// После сбоя питания журнал защиты может оказаться пустым или оборванным: вход не должен блокироваться навсегда.
func TestLoginGuardEmptyOrCorruptFileRecovers(t *testing.T) {
	for name, content := range map[string]string{"пустой": "", "оборванный": `{"203.0.113.9":{"failures":["2026-`} {
		testPanel(t)
		p := filepath.Join(cfg.Home, "var", "login-guard.json")
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		g := &loginGuardState{}
		if w, e := g.begin("203.0.113.9", time.Now()); e != nil || w != 0 {
			t.Fatalf("%s журнал блокирует вход: %v %v", name, w, e)
		}
		if e := g.finish("203.0.113.9", false, time.Now()); e != nil {
			t.Fatalf("%s журнал: запись не удалась: %v", name, e)
		}
		if b, _ := os.ReadFile(p); len(b) == 0 {
			t.Fatalf("%s журнал остался пустым после записи", name)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if e := writeFileAtomic(p, []byte("one"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := writeFileAtomic(p, []byte("two"), 0600); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("содержимое: %q", b)
	}
	if _, e := os.Stat(p + ".tmp"); e == nil {
		t.Fatal("временный файл остался")
	}
}
