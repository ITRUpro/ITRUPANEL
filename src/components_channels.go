// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Источники версий: nginx.org (stable и mainline) для nginx и репозиторий Ondřej Surý для Apache.
// Последние версии в источниках берутся из метаданных репозиториев и запоминаются на час.

var compHTTP = &http.Client{Timeout: 25 * time.Second}

func compFetch(url string) ([]byte, error) {
	resp, err := compHTTP.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func compExists(url string) bool {
	resp, err := compHTTP.Head(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// debArch — архитектура в именах Debian.
func debArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func rpmArch() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

// parseDebPackages возвращает версии пакета pkg из файла Packages репозитория apt.
func parseDebPackages(text, pkg string) []string {
	var out []string
	var name, ver string
	flush := func() {
		if name == pkg && ver != "" {
			out = append(out, ver)
		}
		name, ver = "", ""
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case l == "":
			flush()
		case strings.HasPrefix(l, "Package: "):
			name = strings.TrimSpace(strings.TrimPrefix(l, "Package: "))
		case strings.HasPrefix(l, "Version: "):
			ver = strings.TrimSpace(strings.TrimPrefix(l, "Version: "))
		}
	}
	flush()
	return out
}

// newestUpstream — самая новая версия программы из списка версий пакетов.
func newestUpstream(versions []string) string {
	best := ""
	for _, v := range versions {
		u := upstreamVersion(v)
		if u != "" && (best == "" || numVerCmp(u, best) > 0) {
			best = u
		}
	}
	return best
}

// parseRPMPrimary возвращает версии пакета pkg для архитектуры arch из primary.xml репозитория dnf.
func parseRPMPrimary(r io.Reader, pkg, arch string) []string {
	dec := xml.NewDecoder(r)
	var out []string
	var name, ar, ver string
	inPkg := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "package":
				inPkg, name, ar, ver = true, "", "", ""
			case "name":
				if inPkg {
					var s string
					dec.DecodeElement(&s, &t)
					name = s
				}
			case "arch":
				if inPkg {
					var s string
					dec.DecodeElement(&s, &t)
					ar = s
				}
			case "version":
				if inPkg {
					for _, a := range t.Attr {
						if a.Name.Local == "ver" {
							ver = a.Value
						}
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "package" {
				if name == pkg && ver != "" && (ar == arch || ar == "noarch") {
					out = append(out, ver)
				}
				inPkg = false
			}
		}
	}
	return out
}

type repomd struct {
	Data []struct {
		Type     string `xml:"type,attr"`
		Location struct {
			Href string `xml:"href,attr"`
		} `xml:"location"`
	} `xml:"data"`
}

// rpmRepoVersions читает метаданные репозитория dnf по адресу base и возвращает версии пакета.
func rpmRepoVersions(base, pkg, arch string) ([]string, error) {
	b, err := compFetch(base + "/repodata/repomd.xml")
	if err != nil {
		return nil, err
	}
	var md repomd
	if err = xml.Unmarshal(b, &md); err != nil {
		return nil, err
	}
	for _, d := range md.Data {
		if d.Type != "primary" {
			continue
		}
		data, err := compFetch(base + "/" + d.Location.Href)
		if err != nil {
			return nil, err
		}
		var rd io.Reader = bytes.NewReader(data)
		if strings.HasSuffix(d.Location.Href, ".gz") {
			gz, err := gzip.NewReader(rd)
			if err != nil {
				return nil, err
			}
			rd = gz
		} else if !strings.HasSuffix(d.Location.Href, ".xml") {
			return nil, errf("Неизвестный формат метаданных %s", d.Location.Href)
		}
		return parseRPMPrimary(rd, pkg, arch), nil
	}
	return nil, errf("В метаданных репозитория нет списка пакетов")
}

func debPackagesVersions(base, codename, component, pkg string) ([]string, error) {
	url := base + "/dists/" + codename + "/" + component + "/binary-" + debArch() + "/Packages"
	b, err := compFetch(url + ".gz")
	if err == nil {
		if gz, e := gzip.NewReader(bytes.NewReader(b)); e == nil {
			b, err = io.ReadAll(io.LimitReader(gz, 128<<20))
		} else {
			err = e
		}
	}
	if err != nil {
		if b, err = compFetch(url); err != nil {
			return nil, err
		}
	}
	return parseDebPackages(string(b), pkg), nil
}

// ---------- nginx.org ----------

func osCodename() string {
	for _, l := range strings.Split(readFile("/etc/os-release"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k == "VERSION_CODENAME" {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func nginxOrgBase(channel string) string {
	base := "https://nginx.org/packages/"
	if channel == "mainline" {
		base += "mainline/"
	}
	return base
}

// nginxOrgDebBase — адрес репозитория nginx.org для Debian/Ubuntu.
func nginxOrgDebBase(channel string) string {
	return nginxOrgBase(channel) + osInfo.ID
}

// nginxOrgRPMBase — адрес репозитория nginx.org для RHEL-систем: сначала rhel, потом centos.
func nginxOrgRPMBase(channel string) string {
	major := strings.Split(osInfo.Version, ".")[0]
	for _, d := range []string{"rhel", "centos"} {
		base := nginxOrgBase(channel) + d + "/" + major + "/" + rpmArch()
		if compExists(base + "/repodata/repomd.xml") {
			return base
		}
	}
	return ""
}

var (
	chMu    sync.Mutex
	chCache = map[string]struct {
		at  time.Time
		ver string
		err string
	}{}
)

// chVersion возвращает версию из источника, запоминая её на час (и ошибку тоже, на 5 минут).
func chVersion(key string, fn func() (string, error)) (string, string) {
	chMu.Lock()
	c, ok := chCache[key]
	chMu.Unlock()
	if ok && ((c.err == "" && time.Since(c.at) < time.Hour) || (c.err != "" && time.Since(c.at) < 5*time.Minute)) {
		return c.ver, c.err
	}
	v, err := fn()
	e := ""
	if err != nil {
		e = firstLine(err.Error())
	}
	chMu.Lock()
	chCache[key] = struct {
		at  time.Time
		ver string
		err string
	}{time.Now(), v, e}
	chMu.Unlock()
	return v, e
}

func nginxOrgVersion(channel string) (string, error) {
	switch {
	case osInfo.Family == "debian":
		code := osCodename()
		vs, err := debPackagesVersions(nginxOrgDebBase(channel), code, "nginx", "nginx")
		if err != nil {
			return "", errf("для %s %s у nginx.org нет пакетов", osInfo.ID, code)
		}
		return newestUpstream(vs), nil
	case osInfo.Family == "rhel":
		base := nginxOrgRPMBase(channel)
		if base == "" {
			return "", errf("для вашей версии системы у nginx.org нет пакетов")
		}
		vs, err := rpmRepoVersions(base, "nginx", rpmArch())
		if err != nil {
			return "", err
		}
		return newestUpstream(vs), nil
	}
	return "", errf("недоступно")
}

// systemCandidate — версия пакета из источников самой системы.
func systemCandidate(pkg string) string {
	if cfg.Dry {
		return ""
	}
	switch {
	case isBSD():
		out, _ := run("pkg", "rquery", "%v", pkg)
		return upstreamVersion(firstLine(out))
	case osInfo.Family == "rhel":
		out, _ := run("dnf", "-q", "repoquery", "--disablerepo=nginx-itrupanel", "--qf", "%{version}-%{release}\n", pkg)
		var best string
		for _, l := range strings.Fields(out) {
			if best == "" || numVerCmp(upstreamVersion(l), upstreamVersion(best)) > 0 {
				best = l
			}
		}
		return upstreamVersion(best)
	}
	out, _ := run("apt-cache", "madison", pkg)
	var vs []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "|")
		if len(f) >= 3 && !strings.Contains(f[2], "nginx.org") && !strings.Contains(f[2], "sury.org") && !strings.Contains(f[2], "ondrej") {
			vs = append(vs, strings.TrimSpace(f[1]))
		}
	}
	return newestUpstream(vs)
}

func nginxChannels(cur string) []compChannel {
	var out []compChannel
	if isBSD() {
		for _, c := range []struct{ id, title, pkg string }{{"stable", "Стабильная (пакет nginx)", "nginx"}, {"mainline", "Основная (пакет nginx-devel)", "nginx-devel"}} {
			out = append(out, compChannel{ID: c.id, Title: c.title, Version: systemCandidate(c.pkg), Current: cur == c.id})
		}
		return out
	}
	out = append(out, compChannel{ID: "system", Title: "Пакеты системы", Version: systemCandidate("nginx"), Current: cur == "system"})
	for _, c := range []struct{ id, title string }{{"stable", "nginx.org — стабильная (stable)"}, {"mainline", "nginx.org — основная (mainline)"}} {
		id := c.id
		v, e := chVersion("nginx-"+id+"-"+osInfo.ID+osInfo.Version, func() (string, error) { return nginxOrgVersion(id) })
		out = append(out, compChannel{ID: id, Title: c.title, Version: v, Current: cur == id, Note: e})
	}
	return out
}

// ---------- Apache: Ondřej Surý ----------

func suryApacheBase() string {
	if osInfo.ID == "ubuntu" {
		return "https://ppa.launchpadcontent.net/ondrej/apache2/ubuntu"
	}
	return "https://packages.sury.org/apache2"
}

func apacheChannels(cur string) []compChannel {
	main := "apache2"
	switch {
	case isBSD():
		main = "apache24"
	case osInfo.Family == "rhel":
		main = "httpd"
	}
	out := []compChannel{{ID: "system", Title: "Пакеты системы", Version: systemCandidate(main), Current: cur == "system"}}
	if osInfo.Family == "debian" {
		v, e := chVersion("apache-ondrej-"+osInfo.ID+osInfo.Version, func() (string, error) {
			vs, err := debPackagesVersions(suryApacheBase(), osCodename(), "main", "apache2")
			if err != nil {
				return "", errf("для %s %s у репозитория нет пакетов", osInfo.ID, osCodename())
			}
			return newestUpstream(vs), nil
		})
		out = append(out, compChannel{ID: "ondrej", Title: "Репозиторий Ondřej Surý (свежее)", Version: v, Current: cur == "ondrej", Note: e})
	}
	return out
}
