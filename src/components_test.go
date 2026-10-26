// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestUpstreamVersion(t *testing.T) {
	cases := map[string]string{
		"1:1.26.3-3+deb13u1": "1.26.3", "1.30.6-1~bookworm": "1.30.6", "2.4.66-1.el9": "2.4.66", "8.3.36-1+0~20261001": "8.3.36",
		"1.31.8_1,2": "1.31.8", "": "", "abc": "",
	}
	for in, want := range cases {
		if got := upstreamVersion(in); got != want {
			t.Errorf("upstreamVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNumVerCmp(t *testing.T) {
	if numVerCmp("1.30.10", "1.30.6") <= 0 || numVerCmp("1.31.0", "1.30.99") <= 0 || numVerCmp("1.30.6", "1.30.6") != 0 || numVerCmp("1.30", "1.30.1") >= 0 {
		t.Error("сравнение версий")
	}
}

func TestParseDebPackages(t *testing.T) {
	text := "Package: nginx-module-xslt\nVersion: 1.30.6-1~bookworm\n\nPackage: nginx\nVersion: 1.30.5-1~bookworm\nArchitecture: arm64\n\nPackage: nginx\nVersion: 1.30.6-1~bookworm\n"
	vs := parseDebPackages(text, "nginx")
	if len(vs) != 2 || newestUpstream(vs) != "1.30.6" {
		t.Errorf("версии nginx: %v, новейшая %s", vs, newestUpstream(vs))
	}
}

func TestParseRPMPrimary(t *testing.T) {
	x := `<?xml version="1.0"?><metadata><package type="rpm"><name>nginx</name><arch>aarch64</arch><version epoch="1" ver="1.30.6" rel="1.el9.ngx"/></package>
<package type="rpm"><name>nginx</name><arch>x86_64</arch><version epoch="1" ver="1.31.0" rel="1.el9.ngx"/></package>
<package type="rpm"><name>nginx-module-xslt</name><arch>aarch64</arch><version epoch="1" ver="1.31.9" rel="1.el9.ngx"/></package>
<package type="rpm"><name>nginx</name><arch>aarch64</arch><version epoch="1" ver="1.30.5" rel="1.el9.ngx"/></package></metadata>`
	vs := parseRPMPrimary(strings.NewReader(x), "nginx", "aarch64")
	if len(vs) != 2 || newestUpstream(vs) != "1.30.6" {
		t.Errorf("версии: %v", vs)
	}
}

func TestNginxConfSetUser(t *testing.T) {
	conf := "user nginx;\nworker_processes auto;\nevents {}\n"
	if got := nginxConfSetUser(conf, "www-data"); !strings.HasPrefix(got, "user www-data;\n") || strings.Count(got, "user ") != 1 {
		t.Errorf("замена пользователя:\n%s", got)
	}
	if got := nginxConfSetUser("worker_processes 1;\n", "www"); !strings.HasPrefix(got, "user www;\nworker") {
		t.Errorf("добавление пользователя:\n%s", got)
	}
	if got := nginxConfSetUser("# user nobody;\nuser  www-data ;\nhttp {}\n", "nginx"); !strings.Contains(got, "user nginx;") || !strings.Contains(got, "# user nobody;") {
		t.Errorf("комментарий не должен меняться:\n%s", got)
	}
	again := nginxConfSetUser(nginxConfSetUser(conf, "www-data"), "www-data")
	if again != nginxConfSetUser(conf, "www-data") {
		t.Error("повторное применение меняет файл")
	}
}
