// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolutePPASources(t *testing.T) {
	ppa := "https://ppa.launchpadcontent.net/ondrej/php/ubuntu/"
	for _, tc := range []struct {
		name, in string
		deb822   bool
		want     string
	}{
		{"list", "deb [signed-by=/key.gpg] " + ppa + " resolute main\n", false, "# ITRUPANEL: obsolete Resolute PHP PPA: deb [signed-by=/key.gpg] " + ppa + " resolute main\n"},
		{"other suite", "deb " + ppa + " noble main\n", false, "deb " + ppa + " noble main\n"},
		{"comment", "deb https://archive.ubuntu.com/ubuntu resolute main # " + ppa + " resolute\n", false, "deb https://archive.ubuntu.com/ubuntu resolute main # " + ppa + " resolute\n"},
		{"lookalike", "deb https://ppa.launchpadcontent.net.evil.test/ondrej/php/ubuntu resolute main\n", false, "deb https://ppa.launchpadcontent.net.evil.test/ondrej/php/ubuntu resolute main\n"},
		{"deb822", "Types: deb\nURIs: " + ppa + "\nSuites: resolute\nComponents: main\n", true, "Types: deb\nURIs: " + ppa + "\nSuites: resolute\nComponents: main\nEnabled: no"},
		{"multi suite", "Types: deb\nURIs: " + ppa + "\nSuites: noble resolute\nComponents: main\n", true, "Types: deb\nURIs: " + ppa + "\nSuites: noble\nComponents: main\n"},
		{"unrelated whitespace", "Types: deb\nURIs: https://archive.ubuntu.com/ubuntu\nSuites: resolute\n\n \n# Keep\n", true, "Types: deb\nURIs: https://archive.ubuntu.com/ubuntu\nSuites: resolute\n\n \n# Keep\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := disableResolutePPA(tc.in, tc.deb822)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if twice := disableResolutePPA(got, tc.deb822); twice != got {
				t.Fatal("not idempotent")
			}
		})
	}
	mixed := "Types: deb\nURIs: " + ppa + "\n https://archive.ubuntu.com/ubuntu\nSuites: noble resolute\nComponents: main\n"
	got := disableResolutePPA(mixed, true)
	parts := strings.Split(got, "\n\n")
	if len(parts) != 2 {
		t.Fatal(got)
	}
	a, b := sourceFields(parts[0]), sourceFields(parts[1])
	if a["uris"] != "https://archive.ubuntu.com/ubuntu" || a["suites"] != "noble resolute" || b["uris"] != ppa || b["suites"] != "noble" {
		t.Fatal(got)
	}
}
func TestResolutePrepBackupAndOtherOS(t *testing.T) {
	for _, tc := range []struct{ id, version, family string }{
		{"ubuntu", "22.04", "debian"}, {"ubuntu", "24.04", "debian"}, {"ubuntu", "26.04", "debian"}, {"debian", "11", "debian"}, {"debian", "12", "debian"}, {"debian", "13", "debian"}, {"almalinux", "8.10", "rhel"}, {"rocky", "9.6", "rhel"}, {"rhel", "10.0", "rhel"}, {"centos", "9", "rhel"},
	} {
		t.Run(tc.id+tc.version, func(t *testing.T) {
			testPanel(t)
			osInfo.ID = tc.id
			osInfo.Version = tc.version
			osInfo.Family = tc.family
			path := "/etc/apt/sources.list.d/ondrej.sources"
			before := "Types: deb\nURIs: https://ppa.launchpadcontent.net/ondrej/php/ubuntu/\nSuites: resolute\nComponents: main\n"
			if e := writeFile(path, []byte(before), 0644); e != nil {
				t.Fatal(e)
			}
			if e := preparePHPRepositories(); e != nil {
				t.Fatal(e)
			}
			if ubuntuResolute() {
				if !strings.Contains(readFile(path), "Enabled: no") {
					t.Fatal("PPA still enabled")
				}
				if readFile(path+".itrupanel-before-resolute") != before {
					t.Fatal("backup mismatch")
				}
				if e := preparePHPRepositories(); e != nil {
					t.Fatal(e)
				}
				if readFile(path+".itrupanel-before-resolute") != before {
					t.Fatal("backup overwritten")
				}
			} else {
				if readFile(path) != before {
					t.Fatal("unrelated OS changed")
				}
			}
		})
	}
}
func TestPHPRepoRoutingAndResoluteRetry(t *testing.T) {
	for _, tc := range []struct{ id, version, family, expected string }{
		{"ubuntu", "22.04", "debian", "ppa:ondrej/php"}, {"ubuntu", "24.04", "debian", "ppa:ondrej/php"}, {"ubuntu", "26.04", "debian", "debsuryorg-archive-keyring.deb"}, {"debian", "11", "debian", "packages.sury.org/php"}, {"debian", "12", "debian", "packages.sury.org/php"}, {"debian", "13", "debian", "packages.sury.org/php"}, {"almalinux", "8.10", "rhel", "remi-release-8.rpm"}, {"rocky", "9.6", "rhel", "remi-release-9.rpm"}, {"rhel", "10.0", "rhel", "remi-release-10.rpm"}, {"centos", "9", "rhel", "remi-release-9.rpm"},
	} {
		t.Run(tc.id+tc.version, func(t *testing.T) {
			testPanel(t)
			osInfo.ID = tc.id
			osInfo.Version = tc.version
			osInfo.Family = tc.family
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			t.Setenv("PATH", bin)
			t.Setenv("PHP_REPO_TEST_LOG", log)
			cfg.Dry = false
			t.Cleanup(func() { cfg.Dry = true })
			p := "/etc/apt/sources.list.d/ondrej.list"
			if e := writeFile(p, []byte("deb https://ppa.launchpadcontent.net/ondrej/php/ubuntu resolute main\n"), 0644); e != nil {
				t.Fatal(e)
			}
			t.Setenv("PHP_REPO_TEST_SOURCE", P(p))
			for _, cmd := range []string{"bash", "apt-get", "curl", "dpkg"} {
				script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PHP_REPO_TEST_LOG\"\n"
				if cmd == "apt-get" {
					script += "if [ \"$1\" = update ] && /usr/bin/grep -q '^deb .*ondrej.*resolute' \"$PHP_REPO_TEST_SOURCE\"; then exit 100; fi\n"
				}
				script += "exit 0\n"
				if e := os.WriteFile(filepath.Join(bin, cmd), []byte(script), 0755); e != nil {
					t.Fatal(e)
				}
			}
			if e := phpRepoSetup(); e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(log)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(b), tc.expected) {
				t.Fatalf("wrong repo: %s", b)
			}
			if ubuntuResolute() {
				if strings.Contains(string(b), "add-apt-repository") {
					t.Fatal("PPA used for Resolute")
				}
				if !strings.Contains(readFile("/etc/apt/sources.list.d/itrupanel-php-resolute.list"), "signed-by=/usr/share/keyrings/debsuryorg-archive-keyring.gpg") {
					t.Fatal("missing signing key")
				}
			}
		})
	}
}

func TestExistingResoluteSurySource(t *testing.T) {
	for _, tc := range []struct {
		source       string
		deb822, want bool
	}{
		{"deb [signed-by=/custom.gpg] https://packages.sury.org/php/ resolute main", false, true},
		{"# deb https://packages.sury.org/php/ resolute main", false, false},
		{"deb https://example.test/php resolute main # https://packages.sury.org/php/ resolute", false, false},
		{"Types: deb\nURIs: https://packages.sury.org/php/\nSuites: resolute\n", true, true},
		{"Types: deb\nURIs: https://packages.sury.org/php/\nSuites: resolute\nEnabled: no\n", true, false},
	} {
		if got := hasResoluteSury(tc.source, tc.deb822); got != tc.want {
			t.Fatalf("source %q: %v", tc.source, got)
		}
	}
}
