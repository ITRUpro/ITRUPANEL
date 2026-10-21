// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecurityValidation(t *testing.T) {
	p := SiteSecurity{Mode: "monitor", Rate: 10, Burst: 20, Connections: 30, Allow: []string{"192.0.2.9/24", "2001:db8::1", "192.0.2.0/24"}}
	got, e := normalizeSecurity(p)
	if e != nil || len(got.Allow) != 2 || got.Allow[0] != "192.0.2.0/24" {
		t.Fatal(got, e)
	}
	for _, v := range []string{"1.2.3.4; return 200;", "default", "0.0.0.0/33", "fe80::1%en0", "::ffff:192.0.2.1"} {
		p.Deny = []string{v}
		if _, e := normalizeSecurity(p); e == nil {
			t.Fatal("accepted", v)
		}
	}
	p.Deny = nil
	p.Rate = -1
	if _, e := normalizeSecurity(p); e == nil {
		t.Fatal("negative rate")
	}
}
func TestSecuritySaveConflictAndPersistence(t *testing.T) {
	mux := testPanel(t)
	s, u := nginxTestSite()
	store.Sites = []*Site{s}
	store.Users = []*SiteUser{u}
	p := SiteSecurity{Mode: "monitor", Rate: 10, Burst: 20, Connections: 30}
	body, _ := json.Marshal(map[string]any{"domain": s.Domain, "policy": p})
	_, r := testAPI(t, mux, "security.save", string(body))
	if r["ok"] != true {
		t.Fatal(r)
	}
	if !strings.Contains(readFile(vhostPath(s.Domain)), "limit_req_dry_run on") {
		t.Fatal("not applied")
	}
	_, r = testAPI(t, mux, "security.save", string(body))
	if r["error"] == nil {
		t.Fatal("stale update accepted")
	}
	mustLoad()
	if store.Sites[0].Security.Mode != "monitor" || store.Sites[0].Security.Revision != 1 {
		t.Fatal("not persisted")
	}
	s = store.Sites[0]
	p = s.Security
	p.Mode = "off"
	if e := saveSecurity(s, p); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(readFile(vhostPath(s.Domain)), "limit_req_zone") {
		t.Fatal("off still limits")
	}
}
func TestSecurityRollback(t *testing.T) {
	for _, failure := range []string{"nginx", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			testPanel(t)
			s, u := nginxTestSite()
			store.Sites = []*Site{s}
			store.Users = []*SiteUser{u}
			writeFile(vhostPath(s.Domain), []byte("# original"), 0644)
			if failure == "nginx" {
				bin := t.TempDir()
				os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\nexit 1\n"), 0755)
				t.Setenv("PATH", bin)
				cfg.Dry = false
				t.Cleanup(func() { cfg.Dry = true })
			} else {
				os.Mkdir(statePath()+".tmp", 0700)
			}
			e := saveSecurity(s, SiteSecurity{Mode: "enforce", Rate: 10, Connections: 30})
			if e == nil {
				t.Fatal("accepted failure")
			}
			if s.Security.Mode != "" || s.Security.Revision != 0 || readFile(vhostPath(s.Domain)) != "# original" {
				t.Fatal("rollback failed")
			}
		})
	}
}
func TestSecurityCustomCannotOverride(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.Security.Mode = "monitor"
	for _, custom := range []string{"location / { limit_req_dry_run off; }", "access_log off;", "location / { limit_req zone=other; }"} {
		if _, e := renderSiteNginx(s, u, custom); e == nil {
			t.Fatal("override accepted", custom)
		}
	}
	if _, e := renderSiteNginx(s, u, x3Custom); e != nil {
		t.Fatal(e)
	}
}
func TestSecurityLogTail(t *testing.T) {
	testPanel(t)
	s, _ := nginxTestSite()
	var b strings.Builder
	b.WriteString("invalid\n")
	for i := 0; i < 350; i++ {
		fmt.Fprintf(&b, `{"time":"2026-10-12T01:00:00+03:00","ip":"192.0.2.1","uri":"/item/%d","req":"REJECTED","mode":"enforce"}`+"\n", i)
	}
	writeFile(securityLogPath(s), []byte(b.String()), 0640)
	events, e := securityEvents(s)
	if e != nil || len(events) != 200 || events[0].URI != "/item/150" || events[199].Domain != s.Domain {
		t.Fatal(len(events), e)
	}
}

// Opt-in, isolated real nginx. Only binds loopback and uses a temporary prefix.
func TestSecurityNativeNginx(t *testing.T) {
	nginx := os.Getenv("ITRUPANEL_TEST_NGINX")
	if nginx == "" {
		t.Skip("set ITRUPANEL_TEST_NGINX to a test nginx binary")
	}
	for _, mode := range []string{"monitor", "enforce", "allow", "deny", "off", "conn"} {
		t.Run(mode, func(t *testing.T) {
			testPanel(t)
			s, u := nginxTestSite()
			s.Mode = "static"
			s.PHP = ""
			s.StaticExpires = "30d"
			s.Index = "index.html"
			s.Security = SiteSecurity{Mode: mode, Rate: 1, Burst: 0, Connections: 10}
			if mode == "allow" {
				s.Security.Mode = "enforce"
				s.Security.Allow = []string{"127.0.0.1"}
				s.Security.Deny = []string{"127.0.0.0/8"}
			}
			if mode == "deny" {
				s.Security.Mode = "enforce"
				s.Security.Deny = []string{"127.0.0.1"}
			}
			entered, release := make(chan struct{}, 1), make(chan struct{})
			if mode == "conn" {
				slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/slow" {
						entered <- struct{}{}
						<-release
					}
					w.Write([]byte("OK"))
				}))
				defer slow.Close()
				defer close(release)
				s.Mode = "proxy"
				s.ProxyTo = slow.URL
				s.Security.Mode = "enforce"
				s.Security.Rate = 1000
				s.Security.Connections = 1
			}
			dir := t.TempDir()
			os.MkdirAll(filepath.Join(dir, "logs"), 0755)
			s.Root = filepath.Join(dir, "www")
			os.MkdirAll(s.Root, 0755)
			os.WriteFile(filepath.Join(s.Root, "index.html"), []byte("OK"), 0644)
			os.WriteFile(filepath.Join(s.Root, "test.css"), []byte("OK"), 0644)
			custom := filepath.Join(dir, "custom.conf")
			os.WriteFile(custom, nil, 0644)
			acme := filepath.Join(dir, "acme/.well-known/acme-challenge")
			os.MkdirAll(acme, 0755)
			os.WriteFile(filepath.Join(acme, "proof"), []byte("OK"), 0644)
			ln, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			port := ln.Addr().(*net.TCPAddr).Port
			ln.Close()
			body, e := renderSiteNginx(s, u, "")
			if e != nil {
				t.Fatal(e)
			}
			text := string(body)
			text = strings.ReplaceAll(text, "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port))
			text = strings.ReplaceAll(text, "listen [::]:80;", "")
			text = strings.ReplaceAll(text, customPath(s.Domain), custom)
			text = strings.ReplaceAll(text, u.Home+"/data/logs/", dir+"/logs/")
			text = strings.ReplaceAll(text, securityLogPath(s), dir+"/logs/security.log")
			text = strings.ReplaceAll(text, "/var/www/itrupanel-acme", filepath.Join(dir, "acme"))
			conf := filepath.Join(dir, "nginx.conf")
			os.WriteFile(conf, []byte("pid "+dir+"/nginx.pid;\nerror_log "+dir+"/logs/error.log;\nevents {}\nhttp { map $http_upgrade $itru_connection_upgrade { default upgrade; '' close; }\n"+text+" }"), 0644)
			if out, e := exec.Command(nginx, "-p", dir+"/", "-c", conf, "-t").CombinedOutput(); e != nil {
				t.Fatalf("nginx -t: %v %s\n%s", e, out, text)
			}
			cmd := exec.Command(nginx, "-p", dir+"/", "-c", conf, "-g", "daemon off;")
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { cmd.Process.Signal(os.Interrupt); cmd.Wait() }()
			url := fmt.Sprintf("http://127.0.0.1:%d", port)
			client := &http.Client{Timeout: 2 * time.Second}
			for i := 0; i < 50; i++ {
				conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
				if e == nil {
					conn.Close()
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if mode == "conn" {
				go func() {
					resp, e := client.Get(url + "/slow")
					if e == nil {
						resp.Body.Close()
					}
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("upstream never reached")
				}
				resp, e := client.Get(url + "/fast")
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode != 429 {
					t.Fatal("connection limit failed", resp.StatusCode)
				}
				return
			}
			statuses := map[int]int{}
			for i := 0; i < 8; i++ {
				resp, e := client.Get(url + "/test.css")
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				statuses[resp.StatusCode]++
			}
			if mode == "enforce" && statuses[429] == 0 {
				t.Fatal("rate limit failed", statuses)
			}
			if mode == "deny" && statuses[403] != 8 {
				t.Fatal("deny failed", statuses)
			}
			if (mode == "monitor" || mode == "allow" || mode == "off") && statuses[200] != 8 {
				t.Fatal("unexpected block", statuses)
			}
			for i := 0; i < 3; i++ {
				resp, e := client.Get(url + "/.well-known/acme-challenge/proof")
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatal("ACME blocked", resp.StatusCode)
				}
			}
			// Reopen flushes buffered log entries without waiting for flush=5s.
			exec.Command(nginx, "-p", dir+"/", "-c", conf, "-s", "reopen").Run()
			time.Sleep(100 * time.Millisecond)
			log, _ := os.ReadFile(dir + "/logs/security.log")
			if mode == "monitor" && !strings.Contains(string(log), "REJECTED_DRY_RUN") {
				t.Fatalf("monitor/static log missing: %s", log)
			}
			if mode == "deny" && !strings.Contains(string(log), `"listed":"1"`) {
				t.Fatal("deny not logged")
			}
			if mode == "allow" && len(log) > 0 {
				t.Fatal("allowlisted logged as violation", string(log))
			}
		})
	}
}
