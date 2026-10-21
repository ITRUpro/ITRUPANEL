// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOLSCreateSwitchSuspendDelete(t *testing.T) {
	testPanel(t)
	in := siteIn{Domain: "example.test", User: "alice", Mode: "php", Backend: "openlitespeed", PHP: "8.3", AddWWW: true, StaticExpires: "30d"}
	s, err := createOrUpdateSite(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"address 127.0.0.1:8089", "disableWebAdmin 1", "map example.test example.test,www.example.test"} {
		if !strings.Contains(readFile(olsConfig), want) {
			t.Error("OLS configuration missing", want)
		}
	}
	vhost := readFile(olsVhostPath(s.Domain))
	for _, want := range []string{"type fcgi", "address uds:/" + sockPath(s.Domain), "autoStart 0", "autoLoadHtaccess 1"} {
		if !strings.Contains(vhost, want) {
			t.Error("OLS vhost missing", want)
		}
	}
	nginx := readFile(vhostPath(s.Domain))
	if !strings.Contains(nginx, "proxy_pass http://127.0.0.1:8089;") || strings.Contains(nginx, "fastcgi_pass") || strings.Contains(nginx, "try_files $uri @backend") {
		t.Fatal(nginx)
	}
	if !strings.Contains(readFile(phpPoolDir("8.3")+"/"+poolName(s.Domain)+".conf"), "user = alice") {
		t.Fatal("PHP no longer runs as site owner")
	}
	// Alias changes and suspension rebuild maps without a wildcard fallback.
	s.Aliases = []string{"new.example.test"}
	s.Suspended = true
	if err := applySite(s, s.PHP); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(olsConfig), "map example.test") {
		t.Fatal("suspended site mapped")
	}
	s.Suspended = false
	if err := applySite(s, s.PHP); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(olsConfig), "www.example.test") || !strings.Contains(readFile(olsConfig), "new.example.test") {
		t.Fatal("stale aliases")
	}
	in.Edit, in.Backend = true, ""
	if _, err := createOrUpdateSite(in); err != nil {
		t.Fatal(err)
	}
	if fileExists(olsVhostPath(s.Domain)) || strings.Contains(readFile(olsConfig), "map example.test") {
		t.Fatal("switch away left old OLS routes")
	}
	if !strings.Contains(readFile(vhostPath(s.Domain)), "fastcgi_pass unix:") {
		t.Fatal("nginx PHP was not restored")
	}
	in.Backend = "openlitespeed"
	if _, err := createOrUpdateSite(in); err != nil {
		t.Fatal(err)
	}
	if err := deleteSite(s.Domain, false); err != nil {
		t.Fatal(err)
	}
	if fileExists(olsVhostPath(s.Domain)) || strings.Contains(readFile(olsConfig), "map example.test") {
		t.Fatal("deleted OLS site still mapped")
	}
}

func TestOLSRefusesIndependentInstallation(t *testing.T) {
	testPanel(t)
	writeFile(olsRoot+"/bin/openlitespeed", []byte("installed"), 0755)
	original := "# owner's configuration\nlistener production {}\n"
	writeFile(olsConfig, []byte(original), 0644)
	if err := ensureOLS(); err == nil {
		t.Fatal("unmanaged installation adopted")
	}
	if readFile(olsConfig) != original {
		t.Fatal("unmanaged configuration changed")
	}
}

func TestOLSRollbackAndInvalidBackend(t *testing.T) {
	testPanel(t)
	if _, err := createOrUpdateSite(siteIn{Domain: "bad.test", Mode: "php", Backend: "invalid", PHP: "8.3"}); err == nil {
		t.Fatal("invalid backend silently accepted")
	}
	if err := ensureOLS(); err != nil {
		t.Fatal(err)
	}
	first := &Site{Domain: "first.test", Mode: "php", Backend: "openlitespeed", Root: "/var/www/alice/data/www/first.test", User: "alice", Index: "index.php"}
	store.Sites = []*Site{first}
	if _, err := applyOLS(first); err != nil {
		t.Fatal(err)
	}
	before := readFile(olsConfig)
	other := &Site{Domain: "other.test", Mode: "php", Backend: "openlitespeed", Root: "/var/www/bob/data/www/other.test", User: "bob", Index: "index.php"}
	undo, err := applyOLS(other)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(olsConfig), "map first.test") || !strings.Contains(readFile(olsConfig), "map other.test") {
		t.Fatal("adding site lost existing mapping")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if readFile(olsConfig) != before || fileExists(olsVhostPath(other.Domain)) {
		t.Fatal("rollback did not restore exact configuration")
	}
	// A write failure must not leave a partial vhost referenced by the server.
	os.MkdirAll(P(olsVhostPath(other.Domain)), 0755)
	if _, err := applyOLS(other); err == nil {
		t.Fatal("expected vhost write error")
	}
	if readFile(olsConfig) != before {
		t.Fatal("write failure changed listener maps")
	}
}

func TestOLSSwitchRestoresPreviousPHPPoolAndApache(t *testing.T) {
	testPanel(t)
	s := &Site{Domain: "example.test", PHP: "8.3", Backend: "openlitespeed"}
	oldPool := phpPoolDir("8.2") + "/" + poolName(s.Domain) + ".conf"
	newPool := phpPoolDir("8.3") + "/" + poolName(s.Domain) + ".conf"
	writeFile(oldPool, []byte("old PHP settings"), 0644)
	writeFile(apacheVhostPath(s.Domain), []byte("previous Apache vhost"), 0644)
	writeFile(vhostPath(s.Domain), []byte("previous nginx vhost"), 0644)
	undo, err := snapshotSiteRuntime(s, "8.2")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(P(oldPool))
	os.Remove(P(apacheVhostPath(s.Domain)))
	writeFile(newPool, []byte("new PHP settings"), 0644)
	writeFile(vhostPath(s.Domain), []byte("new nginx vhost"), 0644)
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if fileExists(newPool) || readFile(oldPool) != "old PHP settings" || readFile(apacheVhostPath(s.Domain)) != "previous Apache vhost" || readFile(vhostPath(s.Domain)) != "previous nginx vhost" {
		t.Fatal("backend switch rollback lost prior configuration")
	}
}

func TestOLSRepairsLegacyAccessLog(t *testing.T) {
	testPanel(t)
	original := olsMarker + "# accessLog commented out\nvirtualhost kept.test {\n configFile /kept.conf\n}\nlistener ITRUPANEL {\n address 127.0.0.1:8089\n map kept.test kept.test\n}\n"
	if err := writeFile(olsConfig, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := repairOLSAccessLog(); err != nil {
		t.Fatal(err)
	}
	repaired := readFile(olsConfig)
	if repaired != original+"\n"+olsAccessLog {
		t.Fatal("repair changed existing routes or omitted server access log")
	}
	if err := repairOLSAccessLog(); err != nil {
		t.Fatal(err)
	}
	if readFile(olsConfig) != repaired {
		t.Fatal("repair is not idempotent")
	}
	custom := olsMarker + "accesslog /custom/access.log {\n rollingSize 20M\n}\n"
	writeFile(olsConfig, []byte(custom), 0644)
	if err := repairOLSAccessLog(); err != nil {
		t.Fatal(err)
	}
	if readFile(olsConfig) != custom {
		t.Fatal("existing access log changed")
	}
	writeFile(olsConfig, []byte("# independent server\n"), 0644)
	if err := repairOLSAccessLog(); err != nil {
		t.Fatal(err)
	}
	if readFile(olsConfig) != "# independent server\n" {
		t.Fatal("independent configuration changed")
	}
}

func TestOLSSwitchAwayKeepsServerLogAndOtherSites(t *testing.T) {
	for _, backend := range []string{"", "apache"} {
		for _, keepOther := range []bool{false, true} {
			t.Run(backend+fmt.Sprint(keepOther), func(t *testing.T) {
				testPanel(t)
				in := siteIn{Domain: "switch.test", User: "alice", Mode: "php", Backend: "openlitespeed", PHP: "8.3"}
				if _, err := createOrUpdateSite(in); err != nil {
					t.Fatal(err)
				}
				if keepOther {
					if _, err := createOrUpdateSite(siteIn{Domain: "kept.test", User: "bob", Mode: "php", Backend: "openlitespeed", PHP: "8.3"}); err != nil {
						t.Fatal(err)
					}
				}
				// Simulate the configuration left by 0.1.10 and earlier.
				legacy := strings.ReplaceAll(readFile(olsConfig), olsAccessLog, "")
				if err := writeFile(olsConfig, []byte(legacy), 0644); err != nil {
					t.Fatal(err)
				}
				in.Edit, in.Backend = true, backend
				if _, err := createOrUpdateSite(in); err != nil {
					t.Fatal(err)
				}
				got := readFile(olsConfig)
				if !strings.Contains(got, olsAccessLog) || strings.Contains(got, "switch.test") {
					t.Fatalf("invalid configuration after switch: %s", got)
				}
				if strings.Contains(got, "map kept.test") != keepOther {
					t.Fatal("other site's route changed")
				}
				if fileExists(olsVhostPath("switch.test")) {
					t.Fatal("old virtual host remains")
				}
			})
		}
	}
}

// Reproduce the vendor unit layout without touching the host systemd.
func TestOLSCanonicalServiceLifecycle(t *testing.T) {
	for _, dir := range []string{"/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "legacy"} {
		t.Run(dir, func(t *testing.T) {
			testPanel(t)
			want := "lshttpd"
			if dir == "legacy" {
				dir = "/etc/systemd/system"
				want = "lsws"
			}
			if err := writeFile(dir+"/"+want+".service", []byte("[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=multi-user.target\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if want == "lshttpd" {
				if err := os.Symlink("lshttpd.service", P(dir+"/lsws.service")); err != nil {
					t.Fatal(err)
				}
			}
			if got := olsServiceName(); got != want {
				t.Fatalf("service = %q, want %q", got, want)
			}
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			t.Setenv("OLS_TEST_LOG", log)
			t.Setenv("OLS_TEST_UNIT", want)
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$OLS_TEST_LOG"
case "$1" in
 enable) [ "$2" = --now ] && [ "$3" = "$OLS_TEST_UNIT" ] || { echo 'Refusing to operate on linked unit file lsws.service'; exit 1; };;
 is-active) [ "$2" = "$OLS_TEST_UNIT" ] || exit 3; echo active;;
 *) [ "$2" = "$OLS_TEST_UNIT" ] || exit 1;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			oldDry, oldSystemd := cfg.Dry, osInfo.Systemd
			cfg.Dry, osInfo.Systemd = false, true
			defer func() { cfg.Dry, osInfo.Systemd = oldDry, oldSystemd }()
			for _, action := range []string{"enable", "start", "restart", "reload", "stop"} {
				if err := service(action, "lsws"); err != nil {
					t.Fatalf("%s: %v", action, err)
				}
			}
			if !serviceActive("lsws") {
				t.Fatal("canonical service not monitored")
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			expected := "enable --now " + want + "\nstart " + want + "\nrestart " + want + "\nrestart " + want + "\nstop " + want + "\nis-active " + want + "\n"
			if string(calls) != expected {
				t.Fatalf("unexpected commands: %s", calls)
			}
			// A failed enable must still reach the caller; no successful fallback.
			t.Setenv("OLS_TEST_UNIT", "unavailable")
			if err := service("enable", "lsws"); err == nil {
				t.Fatal("enable error ignored")
			}
		})
	}
}

// Без сайтов OpenLiteSpeed завершается при запуске; служба должна быть остановлена, а не перезапускаться.
func TestOLSRunConfigWithoutSites(t *testing.T) {
	testPanel(t)
	writeFile("/etc/systemd/system/lshttpd.service", []byte("[Service]\nExecStart=/usr/local/lsws/bin/lswsctrl start\n"), 0644)
	// Отдельная копия вместо псевдонима, как после ручного cp --remove-destination
	writeFile("/etc/systemd/system/lsws.service", []byte("[Service]\nExecStart=/usr/local/lsws/bin/lswsctrl start\n"), 0644)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("OLS_TEST_LOG", log)
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$OLS_TEST_LOG\"\n"), 0755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldDry, oldSystemd := cfg.Dry, osInfo.Systemd
	cfg.Dry, osInfo.Systemd = false, true
	defer func() { cfg.Dry, osInfo.Systemd = oldDry, oldSystemd }()

	olsRestoreAliasUnit()
	if target, err := os.Readlink(P("/etc/systemd/system/lsws.service")); err != nil || target != "lshttpd.service" {
		t.Fatalf("lsws.service не стал ссылкой: %q %v", target, err)
	}
	if err := olsRunConfig(olsGlobalConfig(nil)); err != nil {
		t.Fatal(err)
	}
	site := &Site{Domain: "example.com", Root: "/var/www/u/data/www/example.com", Mode: "php", Backend: "openlitespeed"}
	if err := olsRunConfig(olsGlobalConfig([]*Site{site})); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	want := "disable lsws.service\nstop lshttpd\ndisable lshttpd\nreset-failed lshttpd\nenable lshttpd\nrestart lshttpd\n"
	if string(calls) != want {
		t.Fatalf("команды systemctl:\n%s\nожидалось:\n%s", calls, want)
	}
}
