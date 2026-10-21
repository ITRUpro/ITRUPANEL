// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"runtime"
	"testing"
)

// Runs only in a disposable container prepared by test/mail-native.sh.
// Package validators and daemons are real; only systemctl is a container shim.
func TestNativeMailRuntime(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("ITRUPANEL_NATIVE_MAIL") != "disposable-container" {
		t.Skip("opt-in Linux mail integration")
	}
	if _, e := os.Stat("/.dockerenv"); e != nil {
		t.Fatal("disposable Docker container required")
	}
	mux := testPanel(t)
	cfg.Root = "/"
	cfg.Dry = false
	detectOS()
	osInfo.Systemd = true
	// These packages were explicitly preinstalled in this disposable fixture.
	for _, p := range []string{mailOwned, spamOwned, webmailOwned} {
		if e := writeFile(p, []byte("ITRUPANEL test\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := setupMail("mail.example.test", "", nil); e != nil {
		t.Fatal(e)
	}
	call := func(name, body string) {
		t.Helper()
		_, r := testAPI(t, mux, name, body)
		if r["ok"] != true {
			t.Fatalf("%s: %v", name, r)
		}
	}
	call("mail.domain.add", `{"name":"example.test"}`)
	call("mail.box.save", `{"address":"a@example.test","password":"native-test-password","quota_mb":1}`)
	call("mail.box.save", `{"address":"b@example.test","password":"native-test-password","quota_mb":0}`)
	call("mail.forward.save", `{"address":"a@example.test","targets":["b@example.test"],"keep_copy":true,"enabled":true}`)
	if e := setupSpam(SpamSettings{Enabled: true, Mark: 6, Reject: 15}, nil); e != nil {
		t.Fatal(e)
	}
	if fileExists("/usr/sbin/php-fpm8.3") {
		os.MkdirAll(sockDir, 0755)
		os.MkdirAll("/run/php", 0755)
		if e := writeFile("/etc/nginx/conf.d/itrupanel-test.conf", []byte("include "+vhostDir+"/*.conf;\n"), 0644); e != nil {
			t.Fatal(e)
		}
		if e := setupRoundcube("mail.example.test", "", nil); e != nil {
			t.Fatal(e)
		}
		// Retry exercises the SQLite backup and schema upgrade path.
		if e := setupRoundcube("mail.example.test", "", nil); e != nil {
			t.Fatal(e)
		}
	}

	call("mail.domain.add", `{"name":"second.test"}`)
	call("mail.box.save", `{"address":"c@second.test","password":"native-test-password","quota_mb":2}`)
	call("mail.box.save", `{"address":"disabled@second.test","password":"native-test-password","quota_mb":2}`)
	call("mail.box.save", `{"address":"disabled@second.test","edit":true,"enabled":false}`)
	call("mail.domain.save", `{"name":"second.test","quota_mb":4,"default_quota_mb":2,"max_boxes":2,"catch_all":"c@second.test"}`)
	cr, key, e := selfSigned([]string{"mail.second.test"}, 90)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := saveCert("native-mail-second", "self", cr, key, "")
	if e != nil {
		t.Fatal(e)
	}
	if e := changeMail(func(m *MailState) error {
		for i := range m.Domains {
			if m.Domains[i].Name == "second.test" {
				m.Domains[i].Hostname = "mail.second.test"
				m.Domains[i].Certificate = cert.Name
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	t.Log("Real package validators, mailbox API, quota config, forwarding and Rspamd configuration passed")
}
