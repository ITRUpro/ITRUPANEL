// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSuspendedSiteMigration(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	s.Suspended = true
	active := *s
	active.Domain = "active.test"
	active.Suspended = false
	store.Sites = []*Site{s, &active}
	store.Users = []*SiteUser{u}
	writeFile(vhostPath(s.Domain), []byte("# ITRUPANEL: old suspended config"), 0644)
	writeFile(vhostPath(active.Domain), []byte("# active unchanged"), 0644)
	var migration updateMigration
	for _, m := range panelMigrations() {
		if m.ID == "006-suspended-site-page" {
			migration = m
		}
	}
	paths, err := migration.Paths()
	if err != nil || len(paths) != 1 || paths[0] != P(vhostPath(s.Domain)) {
		t.Fatalf("snapshot: %v %v", paths, err)
	}
	if err = migration.Apply(); err != nil {
		t.Fatal(err)
	}
	body := readFile(vhostPath(s.Domain))
	if !strings.Contains(body, "Сайт отключён") || strings.Contains(body, "fastcgi_pass") || strings.Contains(body, "proxy_pass") {
		t.Fatal("disabled page missing or backend exposed")
	}
	if readFile(vhostPath(active.Domain)) != "# active unchanged" {
		t.Fatal("active site modified")
	}
	if err = migration.Apply(); err != nil || readFile(vhostPath(s.Domain)) != body {
		t.Fatal("not idempotent", err)
	}
}

func TestNativeSuspendedSiteResponse(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("ITRUPANEL_NATIVE_NGINX") != "disposable-container" {
		t.Skip("opt-in Linux nginx validation")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("container required")
	}
	testPanel(t)
	s, u := nginxTestSite()
	s.Suspended = true
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "challenge"), []byte("acme-proof"), 0644)
	page, _ := tplFS.ReadFile("templates/site-disabled.html")
	for _, mode := range []string{"php", "static", "proxy", "apache", "openlitespeed"} {
		t.Run(mode, func(t *testing.T) {
			s.Mode = mode
			s.Backend = ""
			s.ProxyTo = "http://127.0.0.1:1"
			if mode == "apache" || mode == "openlitespeed" {
				s.Mode = "php"
				s.Backend = mode
			}
			b, err := renderSiteNginx(s, u, x3Custom)
			if err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := ln.Addr().(*net.TCPAddr).Port
			ln.Close()
			config := strings.ReplaceAll(string(b), "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port))
			config = strings.ReplaceAll(config, "listen [::]:80;", "")
			config = strings.ReplaceAll(config, u.Home+"/data/logs/", dir+"/")
			config = strings.ReplaceAll(config, "root /var/www/itrupanel-acme;", "root "+dir+";")
			challengeDir := filepath.Join(dir, ".well-known/acme-challenge")
			os.MkdirAll(challengeDir, 0755)
			os.WriteFile(filepath.Join(challengeDir, "proof"), []byte("acme-proof"), 0644)
			// nginx workers need to traverse the test temp hierarchy.
			config = "user root; daemon off; pid " + dir + "/nginx.pid; error_log " + dir + "/error.log; events {} http { include /etc/nginx/mime.types; " + config + " }"
			conf := filepath.Join(dir, "nginx.conf")
			os.WriteFile(conf, []byte(config), 0644)
			if out, err := exec.Command("nginx", "-t", "-p", dir, "-c", conf).CombinedOutput(); err != nil {
				t.Fatalf("nginx -t: %v %s", err, out)
			}
			cmd := exec.Command("nginx", "-p", dir, "-c", conf)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Signal(os.Interrupt); cmd.Wait() }()
			client := &http.Client{Timeout: time.Second}
			base := fmt.Sprintf("http://127.0.0.1:%d", port)
			var res *http.Response
			for i := 0; i < 50; i++ {
				res, err = client.Get(base + "/")
				if err == nil {
					res.Body.Close()
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/", "/index.php", "/image.css", "/.env"} {
				res, err = client.Get(base + path)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode != 503 || !bytes.Equal(body, page) || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") || res.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("%s: status=%d type=%q body=%s", path, res.StatusCode, res.Header.Get("Content-Type"), body)
				}
			}
			res, err = client.Get(base + "/.well-known/acme-challenge/proof")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != 200 || string(body) != "acme-proof" {
				t.Fatalf("ACME blocked: %d %s", res.StatusCode, body)
			}
		})
	}
}
