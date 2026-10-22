// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in: real BIND on loopback-only high ports in a disposable Linux container.
func TestNativeMailDNS(t *testing.T) {
	if os.Getenv("ITRUPANEL_NATIVE_DNS") != "disposable-container" {
		t.Skip("opt-in native DNS test")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("Linux container required")
	}
	if _, e := os.Stat("/.dockerenv"); e != nil {
		t.Fatal("container required")
	}
	v := dnsFixture(t)
	mailDNSZoneFixture(t, v)
	dir := t.TempDir()
	conf := filepath.Join(dir, "named.conf")
	rndc := filepath.Join(dir, "rndc.conf")
	key := `key "test" { algorithm hmac-sha256; secret "dGVzdC1vbmx5LW5vbi1wcm9kdWN0aW9u"; };`
	config := fmt.Sprintf(`%s
options { directory "%s"; listen-on port 1053 {127.0.0.1;}; listen-on-v6 {none;}; recursion no; dnssec-validation no; pid-file "%s/named.pid"; };
controls { inet 127.0.0.1 port 1953 allow {127.0.0.1;} keys {"test";}; };
zone "%s" { type primary; file "%s"; };
`, key, dir, dir, v.Domain, P(dnsDir()+"/"+v.Domain+".db"))
	for p, s := range map[string]string{conf: config, rndc: key} {
		if e := os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command("/usr/sbin/named", "-g", "-c", conf, "-n", "1")
	logfile, e := os.Create(filepath.Join(dir, "named.log"))
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); logfile.Close() })
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:1053")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, e := resolver.LookupMX(ctx, v.Domain)
		if e == nil {
			break
		}
		if ctx.Err() != nil {
			b, _ := os.ReadFile(filepath.Join(dir, "named.log"))
			t.Fatalf("BIND did not start: %v\n%s", e, b)
		}
		time.Sleep(30 * time.Millisecond)
	}
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0755)
	for name, body := range map[string]string{"named-checkconf": "exec /usr/bin/named-checkconf '" + conf + "'", "rndc": "exec /usr/sbin/rndc -c '" + rndc + "' -s 127.0.0.1 -p 1953 -y test \"$@\""} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	// Debian ships named-checkconf in /usr/bin; named-checkzone stays on the real PATH.
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	os.MkdirAll(P("/usr/sbin"), 0755)
	os.WriteFile(P("/usr/sbin/named"), nil, 0755)
	cfg.Dry = false
	osInfo.Systemd = false
	plan := planMailDNS(v)
	if e := applyMailDNS(v, plan.Token); e != nil {
		t.Fatal(e)
	}
	// rndc reload queues the reload; wait for authoritative A before checking all records.
	ready, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	for {
		ips, _ := resolver.LookupIP(ready, "ip4", v.Hostname)
		if len(ips) > 0 && ips[0].String() == v.Options.IPv4 {
			break
		}
		if ready.Err() != nil {
			t.Fatal("BIND did not publish updated A")
		}
		time.Sleep(25 * time.Millisecond)
	}
	checks := checkMailDNS(context.Background(), resolver, v)
	for _, c := range checks {
		if c.Kind != "PTR" && c.Status != "ok" {
			t.Fatalf("%+v", c)
		}
	}
	if checks[5].Status == "ok" {
		t.Fatal("PTR unexpectedly exists")
	}
	before := readFile(dnsDir() + "/" + v.Domain + ".db")
	store.DNSZones[0].Records = append(store.DNSZones[0].Records, &DNSRecord{ID: "invalid", Name: "broken", Type: "A", Value: "not-an-IP"})
	p := planMailDNS(v)
	if e := applyMailDNS(v, p.Token); e == nil {
		t.Fatal("BIND validation failure ignored")
	}
	if readFile(dnsDir()+"/"+v.Domain+".db") != before {
		t.Fatal("real zone not restored")
	}
	txt, e := resolver.LookupTXT(context.Background(), v.Domain)
	if e != nil || !strings.Contains(strings.Join(txt, " "), "v=spf1 mx -all") {
		t.Fatal("previous authoritative records lost", txt, e)
	}
	t.Log("Real BIND: zone validation, reload, authoritative MX/A/SPF/DKIM/DMARC, absent PTR, rollback passed")
}
