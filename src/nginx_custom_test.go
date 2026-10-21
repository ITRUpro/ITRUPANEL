// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const x3Custom = `location ^~ /x3disk/views/ { deny all; }
location ^~ /x3disk/data/ { deny all; }
location ^~ /x3disk/uploads/o/ { deny all; }
# X3 Photo Gallery
location / {
 if (!-e $request_filename) {
  rewrite (.+)\.(html|json|xml|atom|rss)$ $1/ last;
  rewrite ^/render/. /app/parsers/slir/ last;
  rewrite ^(.*)$ /index.php?$1 last;
 }
}
location ~ ^/(config|_cache)/ { deny all; }
`

func TestNginxCustomRootDetection(t *testing.T) {
	for _, tt := range []struct {
		text string
		want bool
	}{
		{x3Custom, true}, {"location ^~ / { return 200; }", true}, {"location \"/\"\n{ return 200; }", true}, {"location # comment\n / { return 200; }", true}, {"location '/' { return 200; }", true},
		{"# location / {\nlocation /images/ { return 200; }", false}, {`set $x 'location / {';`, false}, {"location = / { return 200; }", false}, {`location ~ / { return 200; }`, false}, {`location /images/ { location / { return 200; } }`, false}, {`set $x ${uri}; location / { return 200; }`, true}, {`set $x "escaped \" location / {";`, false},
	} {
		if got := nginxCustomRootLocation(tt.text); got != tt.want {
			t.Errorf("%q: %v want %v", tt.text, got, tt.want)
		}
	}
}
func nginxTestSite() (*Site, *SiteUser) {
	u := &SiteUser{Name: "test", Home: "/var/www/test"}
	s := &Site{Domain: "example.test", User: u.Name, Root: u.Home + "/data/www/example.test", Index: "index.php index.html", Mode: "php", PHP: "8.3", MaxBody: 64}
	return s, u
}
func TestNginxCustomRenderingAllModes(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	for _, mode := range []string{"php", "static", "proxy", "apache", "openlitespeed"} {
		s.Mode = mode
		s.Backend = ""
		s.ProxyTo = "http://127.0.0.1:9000"
		if mode == "apache" || mode == "openlitespeed" {
			s.Mode = "php"
			s.Backend = mode
		}
		for _, custom := range []string{"", x3Custom} {
			b, e := renderSiteNginx(s, u, custom)
			if e != nil {
				t.Fatal(e)
			}
			want := 1
			if custom != "" {
				want = 0
			}
			if strings.Count(string(b), "location / {") != want {
				t.Fatalf("%s generated duplicate/missing root: %s", mode, b)
			}
			if !strings.Contains(string(b), "/.well-known/acme-challenge/") {
				t.Fatal("ACME handler lost")
			}
			if mode == "php" && !strings.Contains(string(b), "fastcgi_pass") {
				t.Fatal("PHP handler lost")
			}
		}
	}
	s.Suspended = true
	b, e := renderSiteNginx(s, u, x3Custom)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `return 503 "`) || strings.Contains(string(b), "include "+customPath(s.Domain)) {
		t.Fatal("custom rule bypasses suspension")
	}
}
func TestNginxCustomSaveRemoveAndReload(t *testing.T) {
	mux := testPanel(t)
	s, u := nginxTestSite()
	store.Sites = append(store.Sites, s)
	store.Users = append(store.Users, u)
	for _, custom := range []string{x3Custom, ""} {
		body, _ := json.Marshal(map[string]string{"domain": s.Domain, "text": custom})
		_, result := testAPI(t, mux, "sites.custom.save", string(body))
		if result["ok"] != true {
			t.Fatal(result)
		}
		if got := readFile(customPath(s.Domain)); got != custom {
			t.Fatal("custom text did not persist")
		}
		if strings.Contains(readFile(vhostPath(s.Domain)), "location / {") != (custom == "") {
			t.Fatal("generated root not updated")
		}
		_, get := testAPI(t, mux, "sites.get", `{"domain":"example.test"}`)
		if get["data"].(map[string]any)["custom"] != custom {
			t.Fatal("reopened editor lost saved text")
		}
	}
}
func TestNginxCustomWriteFailurePreservesBothFiles(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	store.Users = append(store.Users, u)
	writeFile(customPath(s.Domain), []byte("# original\n"), 0644)
	writeFile(vhostPath(s.Domain), []byte("# old vhost\n"), 0644)
	// A non-empty directory at the destination forces an actual rename failure,
	// even under root; random staging filenames must not be predictable.
	os.Remove(P(vhostPath(s.Domain)))
	os.Mkdir(P(vhostPath(s.Domain)), 0755)
	os.WriteFile(P(vhostPath(s.Domain))+"/original", []byte("# old vhost\n"), 0644)
	if e := saveNginxCustom(s, x3Custom); e == nil {
		t.Fatal("write error hidden")
	}
	if readFile(customPath(s.Domain)) != "# original\n" || readFile(vhostPath(s.Domain)+"/original") != "# old vhost\n" {
		t.Fatal("partial save left behind")
	}
}
func TestNativeNginxCustomConfig(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("ITRUPANEL_NATIVE_NGINX") != "disposable-container" {
		t.Skip("opt-in Linux nginx validation")
	}
	if _, e := os.Stat("/.dockerenv"); e != nil {
		t.Fatal("container required")
	}
	testPanel(t)
	s, u := nginxTestSite()
	dir := t.TempDir()
	for _, mode := range []string{"php", "static", "proxy", "apache", "openlitespeed"} {
		s.Mode = mode
		s.Backend = ""
		s.ProxyTo = "http://127.0.0.1:9000"
		if mode == "apache" || mode == "openlitespeed" {
			s.Mode = "php"
			s.Backend = mode
		}
		for _, custom := range []string{x3Custom, ""} {
			b, e := renderSiteNginx(s, u, custom)
			if e != nil {
				t.Fatal(e)
			}
			c := strings.ReplaceAll(string(b), customPath(s.Domain), filepath.Join(dir, "custom.conf"))
			c = strings.ReplaceAll(c, u.Home+"/data/logs/", dir+"/")
			os.WriteFile(filepath.Join(dir, "custom.conf"), []byte(custom), 0644)
			conf := "pid " + dir + "/pid; error_log " + dir + "/error.log; events {} http { include /etc/nginx/fastcgi_params; map $http_upgrade $itru_connection_upgrade { default upgrade; '' close; }\n" + c + "\n}"
			// Site fastcgi include resolves relative to the main config directory.
			conf = strings.ReplaceAll(conf, "include fastcgi_params;", "include /etc/nginx/fastcgi_params;")
			os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0644)
			if out, e := exec.Command("nginx", "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); e != nil {
				t.Fatalf("%s: %v\n%s", mode, e, out)
			}
		}
	}
}

func TestNginxCustomValidationFailureRestoresBothFiles(t *testing.T) {
	testPanel(t)
	s, u := nginxTestSite()
	store.Users = append(store.Users, u)
	writeFile(customPath(s.Domain), []byte("# original\n"), 0644)
	writeFile(vhostPath(s.Domain), []byte("# old vhost\n"), 0644)
	bin := t.TempDir()
	if e := os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\necho 'nginx: invalid test directive' >&2\nexit 1\n"), 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin)
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	e := saveNginxCustom(s, "invalid_test_directive;")
	if e == nil || !strings.Contains(e.Error(), "invalid test directive") {
		t.Fatalf("validation error lost: %v", e)
	}
	if readFile(customPath(s.Domain)) != "# original\n" || readFile(vhostPath(s.Domain)) != "# old vhost\n" {
		t.Fatal("invalid config remained on disk")
	}
}
