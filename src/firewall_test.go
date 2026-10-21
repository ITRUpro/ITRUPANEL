// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func firewallTestRule() FirewallRule {
	return FirewallRule{Name: "MySQL", Action: "allow_selected", Protocol: "tcp", Port: "3306", Source: "0.0.0.0", Addresses: []string{"192.168.31.2", "2001:db8::/64"}, Enabled: true}
}
func TestFirewallValidation(t *testing.T) {
	r, e := normalizeFWRule(firewallTestRule())
	if e != nil || r.Source != "*" || r.Addresses[0] != "192.168.31.2/32" {
		t.Fatalf("normalize: %+v %v", r, e)
	}
	cases := []func(*FirewallRule){
		func(r *FirewallRule) { r.Port = "22; flush ruleset" },
		func(r *FirewallRule) { r.Port = "0" }, func(r *FirewallRule) { r.Port = "65536" }, func(r *FirewallRule) { r.Port = "90-80" },
		func(r *FirewallRule) { r.Source = "8.8.8.0/24" }, func(r *FirewallRule) { r.Addresses = []string{"bad"} },
		func(r *FirewallRule) { r.Addresses = nil }, func(r *FirewallRule) { r.Protocol = "icmp"; r.Port = "" },
		func(r *FirewallRule) { r.Protocol = "icmpv6"; r.Port = ""; r.Source = "10.0.0.0/8" },
		func(r *FirewallRule) { r.Action = "reject" }, func(r *FirewallRule) { r.Source = "0.0.0.0;drop" },
	}
	for i, change := range cases {
		r := firewallTestRule()
		change(&r)
		if _, e := normalizeFWRule(r); e == nil {
			t.Errorf("invalid case %d accepted", i)
		}
	}
	r = firewallTestRule()
	r.Source = "192.168.31.0/24"
	r.Addresses = []string{"192.168.31.5/16"}
	if _, e = normalizeFWRule(r); e == nil {
		t.Fatal("broader trusted subnet accepted")
	}
	r = firewallTestRule()
	r.Port = "8000:8100"
	r.Action = "deny"
	r.Source = "192.168.31.77/24"
	r, e = normalizeFWRule(r)
	if e != nil || r.Port != "8000-8100" || r.Source != "192.168.31.0/24" || len(r.Addresses) != 0 {
		t.Fatal(r, e)
	}
}
func TestFirewallRuleSemantics(t *testing.T) {
	for _, action := range []string{"allow", "deny", "allow_selected", "deny_selected"} {
		r := firewallTestRule()
		r.Action = action
		script, e := fwScript(FirewallState{Enabled: true, Rules: []FirewallRule{r}}, []int{22, 3333})
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(script, "flush ruleset") || strings.Count(script, "table inet "+fwTable) != 3 {
			t.Fatal("unexpected table scope", script)
		}
		guard := strings.Index(script, "tcp dport { 22, 3333 } accept")
		rule := strings.Index(script, "tcp dport 3306")
		if guard < 0 || guard >= rule {
			t.Fatal("management not protected")
		}
		switch action {
		case "allow":
			if !strings.Contains(script, "tcp dport 3306 counter accept") {
				t.Fatal(script)
			}
		case "deny":
			if !strings.Contains(script, "tcp dport 3306 counter drop") {
				t.Fatal(script)
			}
		default:
			first, last := "accept", "drop"
			if action == "deny_selected" {
				first, last = last, first
			}
			v4 := "ip saddr 192.168.31.2/32 tcp dport 3306 counter " + first
			v6 := "ip6 saddr 2001:db8::/64 tcp dport 3306 counter " + first
			fallback := "\n tcp dport 3306 counter " + last
			if !strings.Contains(script, v4) || !strings.Contains(script, v6) || strings.Index(script, v6) >= strings.Index(script, fallback) {
				t.Fatal(script)
			}
		}
	}
	script, _ := fwScript(FirewallState{}, nil)
	if strings.Contains(script, "hook") || strings.Contains(script, "flush") {
		t.Fatal(script)
	}
	r := firewallTestRule()
	r.Enabled = false
	script, _ = fwScript(FirewallState{Enabled: true, Rules: []FirewallRule{r}}, []int{22})
	if strings.Contains(script, "3306") {
		t.Fatal("disabled rule active")
	}
}
func TestFirewallLifecycleAndConflict(t *testing.T) {
	mux := testPanel(t)
	if e := fwInstall(nil); e != nil {
		t.Fatal(e)
	}
	if !store.Firewall.Ready || store.Firewall.Enabled {
		t.Fatal(store.Firewall)
	}
	call := func(op string, body any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		_, r := testAPI(t, mux, "firewall."+op, string(b))
		if r["ok"] != true {
			t.Fatalf("%s: %v", op, r)
		}
		return r
	}
	call("save", map[string]any{"revision": store.Firewall.Revision, "rule": firewallTestRule()})
	_, bad := testAPI(t, mux, "firewall.enable", `{"enabled":true,"revision":0}`)
	if bad["error"] == nil || store.Firewall.Enabled {
		t.Fatal("stale revision applied")
	}
	call("enable", map[string]any{"revision": store.Firewall.Revision, "enabled": true})
	if !strings.Contains(readFile(fwConfig), "hook input") {
		t.Fatal("no persisted rules")
	}
	first := store.Firewall.Rules[0].ID
	r := firewallTestRule()
	r.Name = "Second"
	r.Action = "deny"
	call("save", map[string]any{"revision": store.Firewall.Revision, "rule": r})
	call("move", map[string]any{"revision": store.Firewall.Revision, "id": first, "direction": 1})
	if store.Firewall.Rules[1].ID != first {
		t.Fatal("order not saved")
	}
	call("delete", map[string]any{"revision": store.Firewall.Revision, "id": first})
	call("enable", map[string]any{"revision": store.Firewall.Revision, "enabled": false})
	if strings.Contains(readFile(fwConfig), "hook input") || len(store.Firewall.Rules) != 1 {
		t.Fatal("disable removed rules or kept hook")
	}
}
func TestFirewallRollbackAndCommandFailure(t *testing.T) {
	testPanel(t)
	if e := fwInstall(nil); e != nil {
		t.Fatal(e)
	}
	oldFile := readFile(fwConfig)
	old := fwCopy(store.Firewall)
	// Execute a fake nft process, never the host firewall. Fail validation first.
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("PATH", bin)
	t.Setenv("FW_TEST_LOG", log)
	fake := `#!/bin/sh
if [ "$1" = "-j" ]; then echo '{"nftables":[]}'; exit 0; fi
if [ "$1" = "-c" ] && [ "$FW_TEST_FAIL" = 1 ]; then echo 'invalid nft syntax'; exit 1; fi
/bin/cat >> "$FW_TEST_LOG"
echo END >> "$FW_TEST_LOG"
`
	if e := os.WriteFile(filepath.Join(bin, "nft"), []byte(fake), 0755); e != nil {
		t.Fatal(e)
	}
	cfg.Dry = false
	t.Setenv("FW_TEST_FAIL", "1")
	next := fwCopy(old)
	next.Enabled = true
	next.Rules = []FirewallRule{firewallTestRule()}
	if e := fwApplyLocked(next); e == nil || !strings.Contains(e.Error(), "invalid nft syntax") {
		t.Fatal("validation failure lost", e)
	}
	if readFile(fwConfig) != oldFile || store.Firewall.Revision != old.Revision {
		t.Fatal("validation failure changed state")
	}
	t.Setenv("FW_TEST_FAIL", "0")
	// Fail state persistence after runtime apply. The final transaction must remove
	// the newly-created table and restore the prior boot configuration.
	if e := os.Mkdir(statePath()+".tmp", 0700); e != nil {
		t.Fatal(e)
	}
	if e := fwApplyLocked(next); e == nil {
		t.Fatal("persistence failure ignored")
	}
	if readFile(fwConfig) != oldFile || store.Firewall.Enabled || store.Firewall.Revision != old.Revision {
		t.Fatal("rollback failed")
	}
	calls, _ := os.ReadFile(log)
	remove, _ := fwScript(FirewallState{}, nil)
	if !strings.HasSuffix(string(calls), remove+"END\n") {
		t.Fatal("runtime not rolled back", string(calls))
	}
}
func TestFirewallSSHSocketPorts(t *testing.T) {
	testPanel(t)
	cfg.Dry = false
	cfg.Listen = ":4444"
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	fake := `#!/bin/sh
if [ "$2" = "ssh.socket" ]; then echo 'LoadState=loaded'; echo 'Listen=[::]:2222 (Stream)'; else echo 'LoadState=not-found'; exit 1; fi
`
	if e := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(fake), 0755); e != nil {
		t.Fatal(e)
	}
	p, e := fwProtectedPorts()
	if e != nil {
		t.Fatal(e)
	}
	got, _ := json.Marshal(p)
	if string(got) != "[22,2222,3333,4444]" {
		t.Fatal(string(got))
	}
}

func TestFirewallCanDisableWithBrokenSSHConfig(t *testing.T) {
	testPanel(t)
	if e := fwInstall(nil); e != nil {
		t.Fatal(e)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	cfg.Dry = false
	if e := os.WriteFile(filepath.Join(bin, "sshd"), []byte("#!/bin/sh\necho broken ssh config; exit 1\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(bin, "nft"), []byte("#!/bin/sh\nif [ \"$1\" = -j ]; then echo '{\"nftables\":[]}'; fi\nexit 0\n"), 0755); e != nil {
		t.Fatal(e)
	}
	next := fwCopy(store.Firewall)
	next.Enabled = true
	if e := fwApplyLocked(next); e == nil {
		t.Fatal("enabled with unknown SSH ports")
	}
	next.Enabled = false
	if e := fwApplyLocked(next); e != nil {
		t.Fatal("could not disable", e)
	}
}
