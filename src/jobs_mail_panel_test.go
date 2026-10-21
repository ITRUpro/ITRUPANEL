// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func waitTestJob(t *testing.T, id string) Job {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		jobsMu.Lock()
		j := *jobs[id]
		jobsMu.Unlock()
		if j.Status != "running" {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job never completed")
	return Job{}
}
func TestJobFailureDuplicateAndRestart(t *testing.T) {
	mux := testPanel(t)
	jobsMu.Lock()
	jobs = map[string]*Job{}
	jobsMu.Unlock()
	finish := make(chan struct{})
	id := startJob("one", "Install", "admin", func(j *Job) (any, error) {
		j.progress("Download")
		j.Write([]byte("Connection timed out"))
		<-finish
		return nil, errors.New("apt exit 100")
	})
	id2 := startJob("one", "Duplicate", "admin", func(*Job) (any, error) { return nil, nil })
	if id != id2 {
		t.Fatal("duplicate work started")
	}
	close(finish)
	j := waitTestJob(t, id)
	if j.Status != "error" || !strings.Contains(j.Log, "Connection timed out") || j.Error != "apt exit 100" {
		t.Fatalf("lost error: %+v", j)
	}
	code, result := testAPI(t, mux, "jobs.get", `{"id":"`+id+`"}`)
	if code != 200 || result["data"] == nil {
		t.Fatalf("cannot retrieve job: %v", result)
	}
	jobsMu.Lock()
	jobs["restart"] = &Job{ID: "restart", Status: "running", Owner: "admin"}
	saveJobsLocked()
	jobsMu.Unlock()
	loadJobs()
	jobsMu.Lock()
	restarted := *jobs["restart"]
	jobsMu.Unlock()
	if restarted.Status != "error" || !strings.Contains(restarted.Error, "перезапущена") {
		t.Fatal("restart left a phantom running task")
	}
}
func TestMailLifecycleAndSecrets(t *testing.T) {
	mux := testPanel(t)
	osInfo.Family = "debian"
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	call := func(name, body string) map[string]any {
		t.Helper()
		code, r := testAPI(t, mux, name, body)
		if code != 200 || r["ok"] != true {
			t.Fatalf("%s: %v", name, r)
		}
		return r
	}
	call("mail.domain.add", `{"name":"example.test"}`)
	r := call("mail.box.save", `{"address":"info@example.test"}`)
	data := r["data"].(map[string]any)
	pass := data["password"].(string)
	if len(pass) < 12 {
		t.Fatal("missing generated password")
	}
	hash := store.Mail.Boxes[0].Hash
	if err := bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(hash, "{BLF-CRYPT}")), []byte(pass)); err != nil {
		t.Fatal(err)
	}
	r = call("mail.list", `{}`)
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), pass) || strings.Contains(string(b), hash) || strings.Contains(string(b), "BLF-CRYPT") {
		t.Fatal("list leaked credentials")
	}
	call("mail.alias.save", `{"address":"sales@example.test","target":"info@example.test"}`)
	_, bad := testAPI(t, mux, "mail.alias.save", `{"address":"loop@example.test","target":"sales@example.test"}`)
	if bad["error"] == nil {
		t.Fatal("alias chain accepted")
	}
	call("mail.box.save", `{"address":"info@example.test","edit":true,"enabled":false}`)
	for _, p := range []string{"/etc/dovecot/itrupanel-users", "/etc/postfix/itrupanel/boxes", "/etc/postfix/itrupanel/aliases"} {
		if strings.Contains(readFile(p), "@example.test") {
			t.Fatalf("disabled recipient still active in %s", p)
		}
	}
	call("mail.box.save", `{"address":"info@example.test","edit":true,"enabled":true}`)
	if store.Mail.Boxes[0].Hash != hash {
		t.Fatal("empty password changed credentials")
	}
	if !strings.Contains(readFile("/etc/postfix/itrupanel/aliases"), "sales@example.test") {
		t.Fatal("alias not reactivated")
	}
	_, bad = testAPI(t, mux, "mail.box.delete", `{"address":"info@example.test"}`)
	if bad["error"] == nil {
		t.Fatal("deleted alias target")
	}
	call("mail.alias.delete", `{"address":"sales@example.test"}`)
	path := mailHome("info@example.test") + "/Maildir/cur/message"
	if err := writeFile(path, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	call("mail.box.delete", `{"address":"info@example.test"}`)
	if readFile(path) != "keep me" {
		t.Fatal("deleted mail data")
	}
}
func TestMailAddressRejectsInjection(t *testing.T) {
	for _, s := range []string{"a\nX@x.test", "../root@x.test", "a@x.test\nfoo", "a:b@x.test", "a@@x.test", "a..b@x.test"} {
		if _, err := mailAddress(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestMailProtocolsAndRelayRestrictions(t *testing.T) {
	testPanel(t)
	m := MailState{Hostname: "mail.example.test", UID: 999, GID: 999, Domains: []MailDomain{{Name: "example.test"}}}
	for _, modern := range []bool{false, true} {
		m.Dovecot24 = modern
		files, err := mailConfigFiles(m)
		if err != nil {
			t.Fatal(err)
		}
		main := files["/etc/postfix/main.cf"]
		for _, required := range []string{"reject_unauth_destination", "smtpd_sasl_auth_enable = no", "mynetworks = 127.0.0.0/8", "smtpd_tls_auth_only = yes"} {
			if !strings.Contains(main, required) {
				t.Fatal(required)
			}
		}
		master := files["/etc/postfix/master.cf"]
		for _, required := range []string{"smtpd_tls_security_level=encrypt", "smtpd_relay_restrictions=permit_sasl_authenticated,reject", "reject_authenticated_sender_login_mismatch"} {
			if !strings.Contains(master, required) {
				t.Fatal(required)
			}
		}
		d := files["/etc/dovecot/dovecot.conf"]
		if !strings.Contains(d, "ssl = required") || !strings.Contains(d, "port = 993") {
			t.Fatal("IMAP without TLS")
		}
		if modern && (!strings.HasPrefix(d, "dovecot_config_version = 2.4.0") || strings.Contains(d, "mail_location")) {
			t.Fatal("old config used for 2.4")
		}
		if !modern && (!strings.Contains(d, "ssl_cert = <") || strings.Contains(d, "passwd_file_path")) {
			t.Fatal("new config used for 2.3")
		}
	}
}
func TestMailFailureRestoresFiles(t *testing.T) {
	testPanel(t)
	m := MailState{Hostname: "mail.example.test", UID: 999, GID: 999}
	if _, err := applyMail(m); err != nil {
		t.Fatal(err)
	}
	before := readFile("/etc/postfix/main.cf")
	bin := t.TempDir()
	for name, body := range map[string]string{"chown": "exit 0", "doveconf": "echo invalid config >&2; exit 1"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	m.Hostname = "other.example.test"
	if _, err := applyMail(m); err == nil {
		t.Fatal("accepted invalid runtime config")
	}
	if readFile("/etc/postfix/main.cf") != before {
		t.Fatal("failed config not rolled back")
	}
}
func TestPanelCertificateSwitch(t *testing.T) {
	mux := testPanel(t)
	cfg.Listen = ":3333"
	jobsMu.Lock()
	jobs = map[string]*Job{}
	jobsMu.Unlock()
	c, k, err := selfSigned([]string{"panel.example.test"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := saveCert("panel-custom", "custom", c, k, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = validatedPair(cert.Crt, cert.Key, "wrong.example.test"); err == nil {
		t.Fatal("accepted wrong hostname")
	}
	code, r := testAPI(t, mux, "panel.configure", `{"domain":"panel.example.test","mode":"existing","certificate":"panel-custom"}`)
	if code != 200 || r["ok"] != true {
		t.Fatal(r)
	}
	id := r["data"].(map[string]any)["job"].(string)
	j := waitTestJob(t, id)
	if j.Status != "done" {
		t.Fatalf("%+v", j)
	}
	if store.Settings.PanelDomain != "panel.example.test" || activePanelCert.Load() == nil {
		t.Fatal("TLS not applied")
	}
	if err := activePanelCert.Load().Leaf.VerifyHostname("panel.example.test"); err != nil {
		t.Fatal(err)
	}
	if panelURL("panel.example.test") != "https://panel.example.test:3333" {
		t.Fatal("wrong panel URL")
	}
	_, bad := testAPI(t, mux, "ssl.delete", `{"name":"panel-custom"}`)
	if bad["error"] == nil {
		t.Fatal("deleted live panel certificate")
	}
	if _, err = saveCert("panel-custom", "custom", c, k, ""); err == nil {
		t.Fatal("overwrote active cert")
	}
}
func TestOLSRepositoryHTTPS(t *testing.T) {
	testPanel(t)
	path := "/etc/apt/sources.list.d/litespeed.sources"
	original := "Types: deb\nURIs: http://rpms.litespeedtech.com/debian/\nSuites: trixie\nSigned-By: /etc/apt/keyrings/litespeed.gpg\n"
	if err := writeFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := olsHTTPSRepo(); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original, "http:", "https:", 1)
	if readFile(path) != want {
		t.Fatal("repository credentials/options changed")
	}
}

func TestOLSRepositoryHTTPSRHEL(t *testing.T) {
	testPanel(t)
	osInfo.Family = "rhel"
	defer detectOS()
	if olsRepoConfigured() {
		t.Fatal("репозиторий без файла считается подключённым")
	}
	path := "/etc/yum.repos.d/litespeed.repo"
	original := "[litespeed]\nname=LiteSpeed Tech Repository for CentOS $releasever - $basearch\nbaseurl=http://rpms.litespeedtech.com/centos/$releasever/$basearch/\ngpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-litespeed\nenabled=1\ngpgcheck=1\n"
	if err := writeFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	writeFile("/etc/yum.repos.d/other.repo", []byte("[x]\nbaseurl=http://mirror.example/el/\n"), 0644)
	if !olsRepoConfigured() {
		t.Fatal("репозиторий LiteSpeed не найден")
	}
	if err := olsHTTPSRepo(); err != nil {
		t.Fatal(err)
	}
	if readFile(path) != strings.Replace(original, "http://rpms", "https://rpms", 1) {
		t.Fatalf("репозиторий: %s", readFile(path))
	}
	if !strings.Contains(readFile("/etc/yum.repos.d/other.repo"), "http://mirror.example") {
		t.Fatal("изменён чужой репозиторий")
	}
}
