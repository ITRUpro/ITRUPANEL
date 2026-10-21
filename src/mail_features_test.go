// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMailDomainFeatureConfigs(t *testing.T) {
	testPanel(t)
	m := MailState{Ready: true, Hostname: "mail.one.test", Webmail: WebmailSettings{Ready: true, Hostname: "mail.one.test"}, Domains: []MailDomain{
		{Name: "one.test"},
		{Name: "two.test", Hostname: "mail.two.test", Webmail: true, NoSpam: true},
		{Name: "three.test", Hostname: "mail.three.test", NoSpam: true},
		{Name: "four.test", Hostname: "mail.two.test", Webmail: true},
	}}
	spam := rspamdDomainSettings(m)
	if !strings.Contains(spam, `rcpt = ["@two.test", "@three.test"];`) || !strings.Contains(spam, "want_spam = yes;") || strings.Contains(spam, "one.test") {
		t.Fatalf("rspamd settings:\n%s", spam)
	}
	if s := rspamdDomainSettings(MailState{Domains: []MailDomain{{Name: "one.test"}}}); strings.Contains(s, "itrupanel_no_spam") {
		t.Fatalf("no excluded domains must give an empty rule set:\n%s", s)
	}
	files, err := webmailVhostFiles(m)
	if err != nil {
		t.Fatal(err)
	}
	conf := files[webmailVhost]
	if strings.Count(conf, "server_name mail.one.test;") != 2 || strings.Count(conf, "server_name mail.two.test;") != 2 || strings.Contains(conf, "mail.three.test") {
		t.Fatalf("webmail vhost:\n%s", conf)
	}
	if !strings.Contains(conf, webmailCertDir+"/mail.two.test.crt") || strings.Count(conf, "# ITRUPANEL: Roundcube") != 1 {
		t.Fatalf("domain host must use its own certificate and one header:\n%s", conf)
	}
	for _, host := range []string{"mail.one.test", "mail.two.test"} {
		if files[vhostDir+"/000-itrupanel-mail-"+host+".conf"] == "" {
			t.Fatalf("ACME stub for %s missing", host)
		}
		if !webmailHostOn(m, host) {
			t.Fatalf("%s must be a webmail host", host)
		}
	}
	if webmailHostOn(m, "mail.three.test") {
		t.Fatal("mail.three.test has webmail switched off")
	}
}

// Сбой Let’s Encrypt (DNS ещё ведёт на старый сервер) не должен терять добавляемый домен.
func TestCreateMailDomainKeepsDomainWhenLEFails(t *testing.T) {
	testPanel(t)
	store.Mail = MailState{Ready: true, Hostname: "mail.one.test"}
	old := mailHostLE
	defer func() { mailHostLE = old }()
	mailHostLE = func(*Job, string, string) (string, error) { return "", errf("certbot: Detail: 404") }
	warning, err := createMailDomain(&Job{}, "two.test", "mail.two.test", true, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warning, "mail.two.test") || !strings.Contains(warning, "самоподписанным") {
		t.Fatalf("warning: %q", warning)
	}
	if !mailDomainExists(store.Mail, "two.test") {
		t.Fatal("domain lost after Let's Encrypt failure")
	}
	for _, d := range store.Mail.Domains {
		if d.Name == "two.test" && (d.Hostname != "mail.two.test" || d.Certificate != "") {
			t.Fatalf("domain stored as %+v", d)
		}
	}
}

// Пароль ящика хранится зашифрованным и расшифровывается тем же ключом; в state.json открытого пароля нет.
func TestMailPasswordSealRoundTrip(t *testing.T) {
	testPanel(t)
	sealed, err := mailSealPassword("MailPassword123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "MailPassword123") {
		t.Fatal("password stored in clear text")
	}
	plain, err := mailOpenPassword(sealed)
	if err != nil || plain != "MailPassword123" {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	other, _ := mailSealPassword("MailPassword123")
	if other == sealed {
		t.Fatal("nonce must differ between seals")
	}
	if _, err := mailOpenPassword(sealed[:len(sealed)-4] + "AAAA"); err == nil {
		t.Fatal("tampered secret accepted")
	}
}

// Добавление ящика меняет только файлы Dovecot/Postfix: Rspamd не проверяется и не перезапускается.
func TestApplyMailReloadsOnlyChangedServices(t *testing.T) {
	testPanel(t)
	bin, logFile := t.TempDir(), filepath.Join(t.TempDir(), "calls.log")
	for _, name := range []string{"chown", "doveconf", "postfix", "opendkim", "systemctl", "rspamadm", "nginx"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + logFile + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	writeFile(spamRedisSecret, []byte(strings.Repeat("a", 64)), 0600)
	cfg.Dry = false
	oldSystemd := osInfo.Systemd
	osInfo.Systemd = true
	t.Cleanup(func() { cfg.Dry, osInfo.Systemd = true, oldSystemd })
	m := MailState{Ready: true, Hostname: "mail.one.test", UID: 999, GID: 999, Spam: SpamSettings{Installed: true, Enabled: true, Mark: 6, Reject: 15}, Domains: []MailDomain{{Name: "one.test"}}}
	store.Mail = m
	if _, err := applyMail(m); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(logFile)
	if !strings.Contains(string(first), "systemctl restart rspamd") {
		t.Fatalf("first apply must start Rspamd:\n%s", first)
	}
	os.Remove(logFile)
	m.Boxes = append(m.Boxes, MailBox{Address: "info@one.test", Hash: "{BLF-CRYPT}x", Enabled: true})
	if _, err := applyMail(m); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(logFile)
	log := string(calls)
	if strings.Contains(log, "rspamd") || strings.Contains(log, "spam-redis") || strings.Contains(log, "opendkim") {
		t.Fatalf("mailbox change restarted unrelated services:\n%s", log)
	}
	if !strings.Contains(log, "systemctl reload dovecot") {
		t.Fatalf("dovecot not reloaded:\n%s", log)
	}
	os.Remove(logFile)
	if _, err := applyMail(m); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(logFile); len(calls) != 0 {
		t.Fatalf("unchanged config must not reload anything:\n%s", calls)
	}
}
