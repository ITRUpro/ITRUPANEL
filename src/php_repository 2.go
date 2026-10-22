// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func ubuntuResolute() bool { return osInfo.ID == "ubuntu" && osInfo.Version == "26.04" }
func oldPHPURI(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && (u.Host == "ppa.launchpadcontent.net" || u.Host == "ppa.launchpad.net") && strings.TrimRight(u.Path, "/") == "/ondrej/php/ubuntu"
}

var aptField = regexp.MustCompile(`(?m)^([A-Za-z][A-Za-z0-9-]*):[^\n]*(?:\n[ \t]+[^\n]*)*`)

func sourceFields(s string) map[string]string {
	fields := map[string]string{}
	for _, m := range aptField.FindAllString(s, -1) {
		k, v, _ := strings.Cut(m, ":")
		fields[strings.ToLower(k)] = strings.Join(strings.Fields(v), " ")
	}
	return fields
}
func sourceField(s, key, value string) string {
	found := false
	s = aptField.ReplaceAllStringFunc(s, func(line string) string {
		k, _, _ := strings.Cut(line, ":")
		if strings.EqualFold(k, key) {
			found = true
			return key + ": " + value
		}
		return line
	})
	if !found {
		s = strings.TrimRight(s, "\n") + "\n" + key + ": " + value
	}
	return s
}

// Disable only the missing Resolute/PHP PPA combination. Mixed Deb822 sources
// retain unrelated URIs and suites, rather than disabling their entire file.
func disableResolutePPA(data string, deb822 bool) string {
	return disableAPTSources(data, deb822, oldPHPURI, func(s string) bool { return s == "resolute" }, "obsolete Resolute PHP PPA")
}

func disableAPTSources(data string, deb822 bool, matchURI, matchSuite func(string) bool, reason string) string {
	if !deb822 {
		lines := strings.Split(data, "\n")
		for i, line := range lines {
			f := strings.Fields(line)
			if len(f) < 4 || (f[0] != "deb" && f[0] != "deb-src") {
				continue
			}
			j := 1
			if strings.HasPrefix(f[j], "[") {
				for j < len(f) && !strings.HasSuffix(f[j], "]") {
					j++
				}
				j++
			}
			if j+1 < len(f) && matchURI(f[j]) && matchSuite(f[j+1]) {
				lines[i] = "# ITRUPANEL: " + reason + ": " + line
			}
		}
		return strings.Join(lines, "\n")
	}
	blocks := regexp.MustCompile(`\n[ \t]*\n`).Split(data, -1)
	changed := false
	for i, block := range blocks {
		fields := sourceFields(block)
		if strings.EqualFold(fields["enabled"], "no") {
			continue
		}
		legacy, other := []string{}, []string{}
		for _, u := range strings.Fields(fields["uris"]) {
			if matchURI(u) {
				legacy = append(legacy, u)
			} else {
				other = append(other, u)
			}
		}
		suites := []string{}
		target := false
		for _, s := range strings.Fields(fields["suites"]) {
			if matchSuite(s) {
				target = true
			} else {
				suites = append(suites, s)
			}
		}
		if !target || len(legacy) == 0 {
			continue
		}
		changed = true
		if len(other) == 0 {
			if len(suites) == 0 {
				blocks[i] = sourceField(block, "Enabled", "no")
			} else {
				blocks[i] = sourceField(block, "Suites", strings.Join(suites, " "))
			}
		} else {
			blocks[i] = sourceField(block, "URIs", strings.Join(other, " "))
			if len(suites) > 0 {
				blocks[i] += "\n\n" + sourceField(sourceField(block, "URIs", strings.Join(legacy, " ")), "Suites", strings.Join(suites, " "))
			}
		}
	}
	if !changed {
		return data
	}
	return strings.Join(blocks, "\n\n")
}

func preparePHPRepositories() error {
	if !ubuntuResolute() {
		return nil
	}
	return repairAPTSources(disableResolutePPA, ".itrupanel-before-resolute")
}

func repairAPTSources(transform func(string, bool) string, backupSuffix string) error {
	paths := []string{P("/etc/apt/sources.list")}
	for _, ext := range []string{"list", "sources"} {
		matches, e := filepath.Glob(P("/etc/apt/sources.list.d/*." + ext))
		if e != nil {
			return e
		}
		paths = append(paths, matches...)
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		next := transform(string(b), strings.HasSuffix(p, ".sources"))
		if next == string(b) {
			continue
		}
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		backup, e := os.OpenFile(p+backupSuffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
		if e == nil {
			_, e = backup.Write(b)
			closeErr := backup.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		} else if !os.IsExist(e) {
			return e
		}
		tmp, e := os.CreateTemp(filepath.Dir(p), ".itrupanel-sources-")
		if e != nil {
			return e
		}
		name := tmp.Name()
		defer os.Remove(name)
		if e = tmp.Chmod(st.Mode().Perm()); e == nil {
			_, e = tmp.WriteString(next)
		}
		closeErr := tmp.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if e = os.Rename(name, p); e != nil {
			return e
		}
	}
	return nil
}

func setupResolutePHPRepository() error {
	if e := preparePHPRepositories(); e != nil {
		return e
	}
	if _, e := runTimeout(10*time.Minute, "", "apt-get", "update"); e != nil {
		return e
	}
	// Reuse an administrator's already working Sury source; avoid conflicting
	// Signed-By options for the same URI/suite in a second source file.
	for _, glob := range []string{"/etc/apt/sources.list", "/etc/apt/sources.list.d/*.list", "/etc/apt/sources.list.d/*.sources"} {
		paths, e := filepath.Glob(P(glob))
		if e != nil {
			return e
		}
		for _, path := range paths {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if hasResoluteSury(string(b), strings.HasSuffix(path, ".sources")) {
				return nil
			}
		}
	}
	if _, e := runTimeout(10*time.Minute, "", "apt-get", "install", "-y", "ca-certificates", "curl"); e != nil {
		return e
	}
	dir, e := os.MkdirTemp("", "itrupanel-php-keyring-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	key := filepath.Join(dir, "debsuryorg-archive-keyring.deb")
	if _, e = runTimeout(90*time.Second, "", "curl", "--fail", "--show-error", "--location", "--proto", "=https", "--proto-redir", "=https", "--max-time", "60", "https://packages.sury.org/debsuryorg-archive-keyring.deb", "-o", key); e != nil {
		return e
	}
	if _, e = runTimeout(2*time.Minute, "", "dpkg", "-i", key); e != nil {
		return e
	}
	const sourcePath = "/etc/apt/sources.list.d/itrupanel-php-resolute.list"
	const marker = "# Managed by ITRUPANEL: Ubuntu 26.04 PHP\n"
	if b, readErr := os.ReadFile(P(sourcePath)); readErr == nil && !strings.HasPrefix(string(b), marker) {
		return fmt.Errorf("Refusing to overwrite unmanaged PHP source: %s", sourcePath)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if e = writeFile(sourcePath, []byte(marker+"deb [signed-by=/usr/share/keyrings/debsuryorg-archive-keyring.gpg] https://packages.sury.org/php/ resolute main\n"), 0644); e != nil {
		return fmt.Errorf("PHP repository: %w", e)
	}
	_, e = runTimeout(10*time.Minute, "", "apt-get", "update")
	return e
}

func hasResoluteSury(data string, deb822 bool) bool {
	validURI := func(s string) bool {
		u, e := url.Parse(s)
		return e == nil && u.Scheme == "https" && u.Host == "packages.sury.org" && strings.TrimRight(u.Path, "/") == "/php"
	}
	if deb822 {
		for _, block := range regexp.MustCompile(`\n[ \t]*\n`).Split(data, -1) {
			f := sourceFields(block)
			if strings.EqualFold(f["enabled"], "no") {
				continue
			}
			suite := false
			for _, s := range strings.Fields(f["suites"]) {
				suite = suite || s == "resolute"
			}
			if suite {
				for _, u := range strings.Fields(f["uris"]) {
					if validURI(u) {
						return true
					}
				}
			}
		}
		return false
	}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "deb" {
			continue
		}
		j := 1
		if strings.HasPrefix(f[j], "[") {
			for j < len(f) && !strings.HasSuffix(f[j], "]") {
				j++
			}
			j++
		}
		if j+1 < len(f) && validURI(f[j]) && f[j+1] == "resolute" {
			return true
		}
	}
	return false
}
