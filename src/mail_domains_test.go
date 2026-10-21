// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestMailDomainLimitsCatchAll(t *testing.T) {
	mux := testPanel(t)
	if e := setupMail("mail.example.test", "", nil); e != nil {
		t.Fatal(e)
	}
	call := func(method, body string, ok bool) {
		t.Helper()
		_, r := testAPI(t, mux, method, body)
		if (r["ok"] == true) != ok {
			t.Fatalf("%s %v", method, r)
		}
	}
	call("mail.domain.add", `{"name":"example.test"}`, true)
	call("mail.domain.save", `{"name":"example.test","quota_mb":10,"default_quota_mb":5,"max_boxes":2}`, true)
	call("mail.box.save", `{"address":"a@example.test"}`, true)
	if store.Mail.Boxes[0].QuotaMB != 5 {
		t.Fatal("default quota not applied")
	}
	call("mail.box.save", `{"address":"b@example.test","quota_mb":6}`, false)
	call("mail.box.save", `{"address":"b@example.test","quota_mb":5}`, true)
	call("mail.box.save", `{"address":"c@example.test","quota_mb":1}`, false)
	call("mail.domain.save", `{"name":"example.test","quota_mb":10,"default_quota_mb":5,"max_boxes":2,"catch_all":"a@example.test"}`, true)
	a := readFile("/etc/postfix/itrupanel/aliases")
	if !strings.Contains(a, "@example.test a@example.test") || !strings.Contains(a, "b@example.test b@example.test") {
		t.Fatal(a)
	}
	call("mail.box.save", `{"address":"a@example.test","edit":true,"enabled":false}`, false)
	call("mail.box.bulk", `{"addresses":["a@example.test"],"action":"delete"}`, false)
	call("mail.forward.save", `{"address":"a@example.test","targets":["b@example.test"],"enabled":true}`, false)
	call("mail.box.bulk", `{"addresses":["b@example.test"],"action":"disable"}`, true)
	if !strings.Contains(readFile("/etc/postfix/itrupanel/blocked"), "b@example.test REJECT") {
		t.Fatal("disabled mailbox not rejected before catchall")
	}
	mustLoad()
	if store.Mail.Domains[0].CatchAll != "a@example.test" {
		t.Fatal("settings not persisted")
	}
}
func TestMailDomainTLSAndSecrets(t *testing.T) {
	testPanel(t)
	cr, key, e := selfSigned([]string{"mail.second.test"}, 90)
	if e != nil {
		t.Fatal(e)
	}
	c, e := saveCert("mail-second", "self", cr, key, "")
	if e != nil {
		t.Fatal(e)
	}
	m := MailState{Hostname: "mail.example.test", UID: 5000, GID: 5000, Domains: []MailDomain{{Name: "second.test", Hostname: "mail.second.test", Certificate: c.Name}}}
	for _, modern := range []bool{true, false} {
		m.Dovecot24 = modern
		f, e := mailConfigFiles(m)
		if e != nil {
			t.Fatal(e)
		}
		rows := strings.Fields(f["/etc/postfix/itrupanel/sni"])
		if len(rows) != 2 {
			t.Fatal("SNI missing")
		}
		if rows[1] != c.Key+","+c.Crt {
			t.Fatal("wrong SNI key/chain paths")
		}

		if !strings.Contains(f["/etc/dovecot/dovecot.conf"], "local_name mail.second.test") {
			t.Fatal("IMAP SNI missing")
		}
	}
	if mailFileMode("/etc/postfix/itrupanel/sni") != os.FileMode(0600) {
		t.Fatal("SNI secrets readable")
	}
	store.Mail = m
	if !mailDomainCertUsed(c.Name) {
		t.Fatal("certificate in-use guard missing")
	}
	m.Domains[0].Hostname = "wrong.test"
	if _, _, e := mailDomainTLS(m); e == nil {
		t.Fatal("hostname mismatch accepted")
	}
}

type repFake struct {
	answers map[string][]net.IP
	err     error
}

func (r repFake) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	if v, ok := r.answers[host]; ok {
		return v, nil
	}
	if r.err != nil {
		return nil, r.err
	}
	return nil, &net.DNSError{IsNotFound: true}
}
func TestReputationDoesNotInventCleanResult(t *testing.T) {
	ctx := context.Background()
	zone := "dbl.spamhaus.org"
	target := "example.test"
	key := target + "." + zone + "."
	cases := []struct {
		r    repFake
		want string
	}{
		{repFake{answers: map[string][]net.IP{key: {net.ParseIP("127.0.1.2")}}}, "listed"},
		{repFake{answers: map[string][]net.IP{key: {net.ParseIP("127.255.255.254")}}}, "unknown"},
		{repFake{answers: map[string][]net.IP{key: {net.ParseIP("127.0.1.255")}}}, "unknown"},
		{repFake{answers: map[string][]net.IP{}}, "unknown"},
		{repFake{err: errors.New("timeout")}, "unknown"},
		{repFake{answers: map[string][]net.IP{"dbltest.com." + zone + ".": {net.ParseIP("127.0.1.2")}}}, "clear"},
	}
	for _, c := range cases {
		if got := checkReputation(ctx, c.r, "domain", target, zone); got.Status != c.want {
			t.Fatalf("%+v want %s", got, c.want)
		}
	}
}

func TestMailDomainSSLJobPersistenceAndDNS(t *testing.T) {
	mux := testPanel(t)
	if e := setupMail("mail.primary.test", "", nil); e != nil {
		t.Fatal(e)
	}
	_, r := testAPI(t, mux, "mail.domain.add", `{"name":"example.test"}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	_, r = testAPI(t, mux, "mail.domain.ssl", `{"name":"example.test","hostname":"mail.example.test","mode":"le"}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	j := waitTestJob(t, r["data"].(map[string]any)["job"].(string))
	if j.Status != "done" {
		t.Fatalf("SSL job failed: %+v", j)
	}
	mustLoad()
	d := store.Mail.Domains[0]
	if d.Hostname != "mail.example.test" || d.Certificate == "" {
		t.Fatal("domain SSL not persisted")
	}
	if !strings.Contains(readFile(vhostDir+"/000-itrupanel-mail-mail.example.test.conf"), "/.well-known/acme-challenge/") {
		t.Fatal("renewal challenge missing")
	}
	view, e := mailDNSViewFor(d.Name)
	if e != nil {
		t.Fatal(e)
	}
	if view.Hostname != d.Hostname || view.Records[5].Value != "mail.primary.test." {
		t.Fatal("MX/connection host and outgoing PTR must remain distinct")
	}
	_, r = testAPI(t, mux, "mail.list", `{}`)
	certs := r["data"].(map[string]any)["certs"].([]any)
	if len(certs) != 1 || certs[0].(map[string]any)["name"] != d.Certificate {
		t.Fatal("certificate selector missing normalized names")
	}
	if _, leak := certs[0].(map[string]any)["key"]; leak {
		t.Fatal("unnecessary private key path disclosed")
	}
	_, r = testAPI(t, mux, "ssl.delete", `{"name":"`+d.Certificate+`"}`)
	if r["ok"] == true {
		t.Fatal("removed in-use mail certificate")
	}
}
