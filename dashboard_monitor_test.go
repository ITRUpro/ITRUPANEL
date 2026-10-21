// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"testing"
)

func TestMonitorUnitStatesAndUnavailableMemory(t *testing.T) {
	for _, tc := range []struct {
		load, active, memory, want string
		known                      bool
	}{
		{"loaded", "active", "12345", "running", true},
		{"loaded", "inactive", "0", "stopped", true},
		{"loaded", "failed", "18446744073709551615", "failed", false},
		{"loaded", "activating", "[not set]", "changing", false},
		{"not-found", "inactive", "", "missing", false},
		{"masked", "inactive", "", "masked", false},
		{"error", "inactive", "", "unknown", false},
		{"loaded", "", "", "unknown", false},
	} {
		out := fmt.Sprintf("Id=named.service\nNames=named.service bind9.service\nLoadState=%s\nActiveState=%s\nMemoryCurrent=%s\n", tc.load, tc.active, tc.memory)
		got := parseMonitoredUnits(out)
		if got["named.service"].State != tc.want || (got["named.service"].Memory != nil) != tc.known {
			t.Fatalf("%+v: %+v", tc, got)
		}
		if got["bind9.service"].State != tc.want {
			t.Fatal("alias lost")
		}
	}
	if len(parseMonitoredUnits("Failed to connect to bus")) != 0 {
		t.Fatal("systemd failure must not produce a valid state")
	}
	out := "Id=a.service\nLoadState=loaded\nActiveState=active\n\nId=b.service\nLoadState=not-found\nActiveState=inactive\n"
	if len(parseMonitoredUnits(out)) != 2 {
		t.Fatal("multi-unit output lost")
	}
}

func TestMonitorAPI(t *testing.T) {
	mux := testPanel(t)
	status, result := testAPI(t, mux, "stats.monitor", `{}`)
	if status != 200 || result["ok"] != true {
		t.Fatal(result)
	}
	d := result["data"].(map[string]any)
	if d["hostname"] == "" || d["updated"] == nil || len(d["services"].([]any)) == 0 {
		t.Fatal(d)
	}
}

func TestDashboardIOParsers(t *testing.T) {
	net := parseNetworkCounters(" lo: 999 0 0 0 0 0 0 0 888 0 0 0 0 0 0 0\neth0: 1200 0 0 0 0 0 0 0 3400 0 0 0 0 0 0 0\nbad: garbage\n")
	if len(net) != 1 || net["eth0"].In != 1200 || net["eth0"].Out != 3400 {
		t.Fatal(net)
	}
	disk := parseDiskCounters("8 0 sda 10 0 200 5 20 0 400 5 0 0 0\n8 1 sda1 10 0 200 5 20 0 400 5 0 0 0", map[string]bool{"sda": true})
	if len(disk) != 1 || disk["sda"].In != 200*512 || disk["sda"].Out != 400*512 {
		t.Fatal(disk)
	}
	if len(parseDiskCounters("8 0 sda 10 0 nope 5 20 0 400 5 0 0 0", map[string]bool{"sda": true})) != 0 {
		t.Fatal("malformed counters accepted")
	}
}
