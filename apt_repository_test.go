// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"strings"
	"testing"
)

func TestRetiredBullseyeSources(t *testing.T) {
	for _, line := range []string{
		"deb http://deb.debian.org/debian bullseye-backports main",
		"deb-src [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg arch=arm64] https://ftp.debian.org/debian/ bullseye-backports main contrib",
		"deb https://ftp.de.debian.org/debian bullseye-backports-sloppy main",
	} {
		got := disableRetiredBullseyeBackports(line+"\n", false)
		if got != "# ITRUPANEL: retired bullseye-backports: "+line+"\n" {
			t.Fatal(got)
		}
		if disableRetiredBullseyeBackports(got, false) != got {
			t.Fatal("repeat repair changed the file")
		}
	}
	for _, line := range []string{
		"# deb http://deb.debian.org/debian bullseye-backports main",
		"deb https://archive.debian.org/debian bullseye-backports main",
		"deb https://deb.debian.org/debian bullseye main # bullseye-backports",
		"deb https://deb.debian.org/debian bullseye-updates main",
		"deb https://security.debian.org/debian-security bullseye-security main",
		"deb https://deb.debian.org/debian bookworm-backports main",
		"deb https://example.org/debian bullseye-backports main",
		"deb https://deb.debian.org.evil.test/debian bullseye-backports main",
		"deb https://download.docker.com/linux/debian bullseye stable",
		"deb https://repo.homebridge.io stable main",
	} {
		if disableRetiredBullseyeBackports(line, false) != line {
			t.Fatal("unrelated source changed: " + line)
		}
	}
}

func TestRetiredBullseyeDeb822(t *testing.T) {
	const single = "Types: deb deb-src\nURIs: https://deb.debian.org/debian\nSuites: bullseye-backports\nComponents: main\nSigned-By: /key.gpg\n"
	got := disableRetiredBullseyeBackports(single, true)
	if sourceFields(got)["enabled"] != "no" || sourceFields(got)["signed-by"] != "/key.gpg" {
		t.Fatal(got)
	}
	if disableRetiredBullseyeBackports(got, true) != got {
		t.Fatal("disabled entry modified again")
	}
	mixed := strings.Replace(single, "Suites: bullseye-backports", "Suites: bullseye bullseye-updates\n bullseye-backports bullseye-backports-sloppy", 1)
	mixed = strings.Replace(mixed, "URIs: https://deb.debian.org/debian", "URIs: https://deb.debian.org/debian\n https://archive.debian.org/debian", 1)
	got = disableRetiredBullseyeBackports(mixed, true)
	blocks := strings.Split(got, "\n\n")
	if len(blocks) != 2 {
		t.Fatal(got)
	}
	a, b := sourceFields(blocks[0]), sourceFields(blocks[1])
	if a["uris"] != "https://archive.debian.org/debian" || a["suites"] != "bullseye bullseye-updates bullseye-backports bullseye-backports-sloppy" || b["uris"] != "https://deb.debian.org/debian" || b["suites"] != "bullseye bullseye-updates" || b["signed-by"] != "/key.gpg" {
		t.Fatal(got)
	}
	if disableRetiredBullseyeBackports(got, true) != got {
		t.Fatal("mixed stanza repair not idempotent")
	}
}

func TestAPTPreparationBackupAndOSScope(t *testing.T) {
	for _, tc := range []struct{ id, version string }{{"debian", "11"}, {"debian", "11.11"}, {"debian", "12"}, {"ubuntu", "24.04"}, {"almalinux", "10"}} {
		t.Run(tc.id+tc.version, func(t *testing.T) {
			testPanel(t)
			osInfo.ID, osInfo.Version = tc.id, tc.version
			for _, path := range []string{"/etc/apt/sources.list", "/etc/apt/sources.list.d/backports.list", "/etc/apt/sources.list.d/backports.sources"} {
				before := "deb http://deb.debian.org/debian bullseye main\ndeb http://deb.debian.org/debian bullseye-backports main\n"
				if strings.HasSuffix(path, ".sources") {
					before = "Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bullseye bullseye-backports\nComponents: main\n"
				}
				if err := writeFile(path, []byte(before), 0640); err != nil {
					t.Fatal(err)
				}
				if err := prepareAPTRepositories(); err != nil {
					t.Fatal(err)
				}
				backup := path + ".itrupanel-before-bullseye-backports"
				if tc.id == "debian" && strings.HasPrefix(tc.version, "11") {
					if readFile(path) == before || readFile(backup) != before {
						t.Fatal("repair/backup missing")
					}
					after := readFile(path)
					if err := prepareAPTRepositories(); err != nil {
						t.Fatal(err)
					}
					if readFile(path) != after || readFile(backup) != before {
						t.Fatal("repeat changed sources/backup")
					}
				} else if readFile(path) != before || fileExists(backup) {
					t.Fatal("unrelated OS changed")
				}
				st, err := os.Stat(P(path))
				if err != nil || st.Mode().Perm() != 0640 {
					t.Fatal("permissions changed", err)
				}
			}
		})
	}
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(data), "\"$BIN\" prepare-apt-repositories") > strings.Index(string(data), "apt-get update -q") || !strings.Contains(string(data), "\"$BIN\" prepare-apt-repositories") {
		t.Fatal("repair must precede first apt update")
	}
}

func TestBullseyeSecurityArchive(t *testing.T) {
	in := "deb http://deb.debian.org/debian bullseye main\n" +
		"deb http://deb.debian.org/debian-security bullseye-security main\n" +
		"deb [arch=amd64] http://security.debian.org/debian-security bullseye-security main contrib\n" +
		"deb http://security.debian.org bullseye-security main\n" +
		"deb http://deb.debian.org/debian bullseye-updates main\n" +
		"deb http://mirror.example.com/debian-security bullseye-security main\n"
	out := archiveBullseyeSecurity(in, false)
	want := "deb http://deb.debian.org/debian bullseye main\n" +
		"deb http://archive.debian.org/debian-security bullseye-security main\n" +
		"deb [arch=amd64] http://archive.debian.org/debian-security bullseye-security main contrib\n" +
		"deb http://archive.debian.org/debian-security bullseye-security main\n" +
		"deb http://deb.debian.org/debian bullseye-updates main\n" +
		"deb http://mirror.example.com/debian-security bullseye-security main\n"
	if out != want {
		t.Fatalf("sources.list:\n%s", out)
	}
	if archiveBullseyeSecurity(out, false) != out {
		t.Fatal("повторное исправление изменило файл")
	}
	src := "Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bullseye bullseye-updates\nComponents: main\n\nTypes: deb\nURIs: http://deb.debian.org/debian-security\nSuites: bullseye-security\nComponents: main\n"
	got := archiveBullseyeSecurity(src, true)
	if !strings.Contains(got, "URIs: http://archive.debian.org/debian-security\nSuites: bullseye-security") || !strings.Contains(got, "URIs: http://deb.debian.org/debian\nSuites: bullseye bullseye-updates") {
		t.Fatalf("deb822:\n%s", got)
	}
}
