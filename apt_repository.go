// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"net/url"
	"regexp"
	"strings"
)

var debianMirrorHost = regexp.MustCompile(`^ftp\.[a-z]{2}\.debian\.org$`)

func retiredBackportsURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Host)
	return (host == "deb.debian.org" || host == "ftp.debian.org" || debianMirrorHost.MatchString(host)) && strings.TrimRight(u.Path, "/") == "/debian"
}

func disableRetiredBullseyeBackports(data string, deb822 bool) string {
	return disableAPTSources(data, deb822, retiredBackportsURI, func(s string) bool {
		return s == "bullseye-backports" || s == "bullseye-backports-sloppy"
	}, "retired bullseye-backports")
}

const debianSecurityArchive = "http://archive.debian.org/debian-security"

// retiredSecurityURI — зеркало обновлений безопасности Debian, с которого после окончания
// поддержки выпуска убираются пакеты (список ещё отдаётся, а файлы — 404).
func retiredSecurityURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host, path := strings.ToLower(u.Host), strings.TrimRight(u.Path, "/")
	if host == "security.debian.org" {
		return path == "" || path == "/debian-security"
	}
	return (host == "deb.debian.org" || host == "ftp.debian.org" || debianMirrorHost.MatchString(host)) && path == "/debian-security"
}

// archiveBullseyeSecurity переводит bullseye-security на archive.debian.org: в сентябре 2026
// поддержка Debian 11 закончилась, и пакеты безопасности остались только в архиве.
func archiveBullseyeSecurity(data string, deb822 bool) string {
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
			if j+1 < len(f) && f[j+1] == "bullseye-security" && retiredSecurityURI(f[j]) {
				f[j] = debianSecurityArchive
				lines[i] = strings.Join(f, " ")
			}
		}
		return strings.Join(lines, "\n")
	}
	blocks := regexp.MustCompile(`\n[ \t]*\n`).Split(data, -1)
	changed := false
	for i, block := range blocks {
		fields := sourceFields(block)
		if strings.EqualFold(fields["enabled"], "no") || strings.Join(strings.Fields(fields["suites"]), " ") != "bullseye-security" {
			continue
		}
		uris := strings.Fields(fields["uris"])
		touched := false
		for k, u := range uris {
			if retiredSecurityURI(u) {
				uris[k], touched = debianSecurityArchive, true
			}
		}
		if touched {
			blocks[i], changed = sourceField(block, "URIs", strings.Join(uris, " ")), true
		}
	}
	if !changed {
		return data
	}
	return strings.Join(blocks, "\n\n")
}

func prepareAPTRepositories() error {
	if osInfo.ID == "debian" && strings.Split(osInfo.Version, ".")[0] == "11" {
		if err := repairAPTSources(disableRetiredBullseyeBackports, ".itrupanel-before-bullseye-backports"); err != nil {
			return err
		}
		if err := repairAPTSources(archiveBullseyeSecurity, ".itrupanel-before-bullseye-security-archive"); err != nil {
			return err
		}
	}
	return preparePHPRepositories()
}
