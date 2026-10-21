// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeMailDNS struct {
	mx   []*net.MX
	ips  []net.IP
	txt  map[string][]string
	ptr  []string
	err  error
	wait bool
}

func (f fakeMailDNS) errFor(ctx context.Context) error {
	if f.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}
func (f fakeMailDNS) LookupMX(c context.Context, n string) ([]*net.MX, error) {
	return f.mx, f.errFor(c)
}
func (f fakeMailDNS) LookupIP(c context.Context, network, n string) ([]net.IP, error) {
	return f.ips, f.errFor(c)
}
func (f fakeMailDNS) LookupTXT(c context.Context, n string) ([]string, error) {
	return f.txt[n], f.errFor(c)
}
func (f fakeMailDNS) LookupAddr(c context.Context, n string) ([]string, error) {
	return f.ptr, f.errFor(c)
}
func dnsFixture(t *testing.T) mailDNSView {
	t.Helper()
	testPanel(t)
	store.Mail = MailState{Ready: true, Hostname: "mail.example.test", Domains: []MailDomain{{Name: "example.test", PublicKey: "PUBLICKEY", DNS: MailDNSOptions{IPv4: "203.0.113.5", Policy: "none"}}}}
	v, e := mailDNSViewFor("example.test")
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func goodMailDNS(v mailDNSView) fakeMailDNS {
	return fakeMailDNS{mx: []*net.MX{{Host: v.Hostname + ".", Pref: 10}}, ips: []net.IP{net.ParseIP(v.Options.IPv4)}, txt: map[string][]string{v.Domain: {"v=spf1 mx -all"}, "itru._domainkey." + v.Domain: {"v=DKIM1; k=rsa; p=PUBLICKEY"}, "_dmarc." + v.Domain: {"v=DMARC1; p=none"}}, ptr: []string{v.Hostname + "."}}
}
func TestMailDNSChecks(t *testing.T) {
	v := dnsFixture(t)
	f := goodMailDNS(v)
	for _, r := range checkMailDNS(context.Background(), f, v) {
		if r.Status != "ok" {
			t.Fatalf("%+v", r)
		}
	}
	cases := []struct {
		name   string
		index  int
		change func(*fakeMailDNS)
		status string
	}{
		{"foreign MX", 0, func(f *fakeMailDNS) { f.mx = []*net.MX{{Host: "other.test.", Pref: 10}} }, "error"},
		{"additional MX", 0, func(f *fakeMailDNS) { f.mx = append(f.mx, &net.MX{Host: "backup.test."}) }, "warn"},
		{"wrong A", 1, func(f *fakeMailDNS) { f.ips = []net.IP{net.ParseIP("203.0.113.6")} }, "error"},
		{"duplicate SPF", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 mx -all", "v=spf1 include:other.test -all"} }, "error"},
		{"advanced SPF", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 include:other.test -all"} }, "warn"},
		{"wrong DKIM", 3, func(f *fakeMailDNS) { f.txt["itru._domainkey."+v.Domain] = []string{"v=DKIM1; p=OTHER"} }, "error"},
		{"revoked DKIM", 3, func(f *fakeMailDNS) { f.txt["itru._domainkey."+v.Domain] = []string{"v=DKIM1; p="} }, "error"},
		{"testing DKIM", 3, func(f *fakeMailDNS) { f.txt["itru._domainkey."+v.Domain] = []string{"v=DKIM1; p=PUBLICKEY; t=y"} }, "warn"},
		{"invalid DMARC", 4, func(f *fakeMailDNS) { f.txt["_dmarc."+v.Domain] = []string{"v=DMARC1; p=bad"} }, "error"},
		{"different DMARC", 4, func(f *fakeMailDNS) { f.txt["_dmarc."+v.Domain] = []string{"v=DMARC1; p=reject"} }, "ok"},
		{"DMARC external reports", 4, func(f *fakeMailDNS) {
			f.txt["_dmarc."+v.Domain] = []string{"v=DMARC1; p=none; rua=mailto:dmarc@other.test"}
		}, "warn"},
		{"SPF by server IP softfail", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 ip4:" + v.Options.IPv4 + " ~all"} }, "ok"},
		{"SPF by subnet", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 ip4:" + v.Options.IPv4 + "/24 -all"} }, "ok"},
		{"SPF without this server", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 ip4:198.51.100.7 -all"} }, "error"},
		{"SPF without all", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 mx"} }, "warn"},
		{"SPF pass all", 2, func(f *fakeMailDNS) { f.txt[v.Domain] = []string{"v=spf1 mx +all"} }, "warn"},
		{"wrong PTR", 5, func(f *fakeMailDNS) { f.ptr = []string{"other.test."} }, "error"},
		{"PTR forward mismatch", 5, func(f *fakeMailDNS) { f.ips = []net.IP{net.ParseIP("203.0.113.6")} }, "error"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := goodMailDNS(v)
			tt.change(&f)
			r := mailDNSCheckRecord(context.Background(), f, v, v.Records[tt.index])
			if r.Status != tt.status {
				t.Fatalf("%+v", r)
			}
		})
	}
	for _, e := range []error{errors.New("timeout"), context.DeadlineExceeded, &net.DNSError{IsNotFound: true, Name: "example.test"}} {
		f.err = e
		rows := checkMailDNS(context.Background(), f, v)
		want := "unknown"
		var de *net.DNSError
		if errors.As(e, &de) {
			want = "error"
		}
		for _, r := range rows {
			if r.Status != want {
				t.Fatalf("%+v", r)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	f.wait = true
	for _, r := range checkMailDNS(ctx, f, v) {
		if r.Status != "unknown" {
			t.Fatal(r)
		}
	}
	v.Options.IPv4 = ""
	v.Records[1].Value = ""
	v.Records[5].Name = ""
	f = goodMailDNS(v)
	for _, i := range []int{1, 5} {
		if r := mailDNSCheckRecord(context.Background(), f, v, v.Records[i]); r.Status != "warn" {
			t.Fatal(r)
		}
	}
}
func mailDNSZoneFixture(t *testing.T, v mailDNSView) *DNSZone {
	t.Helper()
	z := &DNSZone{Name: v.Domain, TTL: 3600, Email: "hostmaster." + v.Domain, Records: []*DNSRecord{
		{ID: "ns", Name: "@", Type: "NS", Value: "ns1.example.test."},
		{ID: "nsa", Name: "ns1", Type: "A", Value: "203.0.113.5"},
		{ID: "site", Name: "@", Type: "A", Value: "203.0.113.9"},
		{ID: "verify", Name: "@", Type: "TXT", Value: "site-verification=keep"},
		{ID: "spf", Name: "@", Type: "TXT", Value: "\"v=spf1 include:external.test -all\""},
		{ID: "mx", Name: "@", Type: "MX", Value: "old.test.", Prio: 20},
	}}
	store.DNSZones = []*DNSZone{z}
	if e := applyZone(z); e != nil {
		t.Fatal(e)
	}
	return z
}
func TestMailDNSPlanApplyPreservesOtherRecords(t *testing.T) {
	v := dnsFixture(t)
	z := mailDNSZoneFixture(t, v)
	p := planMailDNS(v)
	if !p.CanApply || len(p.Changes) != 5 || len(p.Changes[2].Before) != 1 {
		t.Fatal(p)
	}
	if e := applyMailDNS(v, "invalid"); e == nil {
		t.Fatal("missing stale token check")
	}
	if e := applyMailDNS(v, p.Token); e != nil {
		t.Fatal(e)
	}
	ids := map[string]bool{}
	for _, r := range z.Records {
		ids[r.ID] = true
	}
	if !ids["site"] || !ids["verify"] {
		t.Fatal("unrelated DNS removed")
	}
	count := len(z.Records)
	p = planMailDNS(v)
	if e := applyMailDNS(v, p.Token); e != nil {
		t.Fatal(e)
	}
	if len(z.Records) != count {
		t.Fatal("repeat created duplicates")
	}
	if strings.Contains(readFile(dnsDir()+"/"+v.Domain+".db"), "PTR") {
		t.Fatal("PTR in forward zone")
	}
	z.Records = append(z.Records, &DNSRecord{ID: "cname", Name: "mail", Type: "CNAME", Value: "other.test."})
	if planMailDNS(v).CanApply {
		t.Fatal("CNAME conflict allowed")
	}
	store.DNSZones = nil
	if planMailDNS(v).CanApply {
		t.Fatal("missing zone allowed")
	}
}
func TestMailDNSRollbackOnValidatorReloadAndSaveFailure(t *testing.T) {
	for _, stage := range []string{"named-checkzone", "rndc", "save"} {
		t.Run(stage, func(t *testing.T) {
			v := dnsFixture(t)
			mailDNSZoneFixture(t, v)
			before, _ := json.Marshal(store.DNSZones)
			path := dnsDir() + "/" + v.Domain + ".db"
			old := readFile(path)
			p := planMailDNS(v)
			if stage == "save" {
				os.Remove(statePath())
				os.Mkdir(statePath(), 0700)
			} else {
				bin := t.TempDir()
				t.Setenv("PATH", bin)
				for _, cmd := range []string{"named-checkzone", "named-checkconf", "rndc"} {
					code := "0"
					if cmd == stage {
						code = "1"
					}
					if e := os.WriteFile(filepath.Join(bin, cmd), []byte("#!/bin/sh\nexit "+code+"\n"), 0755); e != nil {
						t.Fatal(e)
					}
				}
				os.MkdirAll(P("/usr/sbin"), 0755)
				os.WriteFile(P("/usr/sbin/named"), nil, 0755)
				cfg.Dry = false
				osInfo.Systemd = false
			}
			if e := applyMailDNS(v, p.Token); e == nil {
				t.Fatal("failure ignored")
			}
			after, _ := json.Marshal(store.DNSZones)
			if string(before) != string(after) || old != readFile(path) {
				t.Fatal("failed transaction was not restored")
			}
		})
	}
}
func TestMailDNSSettingsAPI(t *testing.T) {
	mux := testPanel(t)
	store.Mail = MailState{Hostname: "mail.example.test", Domains: []MailDomain{{Name: "example.test", PublicKey: "public-only"}}}
	for _, body := range []string{`{"ipv4":"127.0.0.1"}`, `{"ipv4":"192.168.1.1"}`, `{"ipv4":"bad"}`, `{"ipv4":"198.18.0.1"}`, `{"ipv4":"100.64.0.1"}`, `{"ipv4":"203.0.113.5"}`, `{"policy":"bad"}`, `{"report":"bad\naddress"}`} {
		_, r := testAPI(t, mux, "mail.dns.settings", `{"name":"example.test","options":`+body+`}`)
		if r["ok"] == true {
			t.Fatal("invalid settings accepted", body)
		}
	}
	_, r := testAPI(t, mux, "mail.dns.settings", `{"name":"example.test","options":{"ipv4":"93.184.216.34","policy":"quarantine","report":"reports@example.test"}}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	mustLoad()
	v, e := mailDNSViewFor("example.test")
	if e != nil || v.Options.Policy != "quarantine" || !strings.Contains(v.Records[4].Value, "rua=mailto:reports@example.test") {
		t.Fatal(v, e)
	}
	_, r = testAPI(t, mux, "mail.dns.check", `{"name":"example.test"}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), `"status":"ok"`) {
		t.Fatal("demo fabricated successful check")
	}
}
