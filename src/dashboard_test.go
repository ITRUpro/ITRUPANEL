// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"errors"
	"testing"
)

func TestCPUPercent(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		oldIdle, oldTotal, idle, total uint64
		want                           float64
		valid                          bool
	}{
		{"half busy", 20, 100, 70, 200, 50, true},
		{"idle", 20, 100, 120, 200, 0, true},
		{"busy", 20, 100, 20, 200, 100, true},
		{"not available", 0, 0, 0, 0, 0, false},
		{"counter reset", 20, 100, 10, 50, 0, false},
		{"bad idle counter", 20, 100, 150, 200, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cpuPercent(tc.oldIdle, tc.oldTotal, tc.idle, tc.total)
			if (got != nil) != tc.valid || got != nil && *got != tc.want {
				t.Fatalf("cpuPercent = %v, want %v (valid %v)", got, tc.want, tc.valid)
			}
		})
	}
}

func TestDashboardCounts(t *testing.T) {
	mux := testPanel(t)
	store.Sites = []*Site{{Domain: "a.example"}, {Domain: "b.example", Suspended: true}}
	store.FTP = []*FTPUser{{Name: "ftp1"}}
	store.DBs = []*DB{{Name: "db1"}, {Name: "db2"}, {Name: "db3"}}
	store.Notices = []*Notice{{Level: "warn"}, {Level: "error"}, {Level: "info"}, {Level: "error", Read: true}}
	status, result := testAPI(t, mux, "stats.dashboard", `{}`)
	if status != 200 || result["ok"] != true {
		t.Fatal(result)
	}
	d := result["data"].(map[string]any)
	for key, want := range map[string]float64{"sites": 2, "enabled": 1, "suspended": 1, "ftp": 1, "dbs": 3, "warnings": 2} {
		if d[key] != want {
			t.Fatalf("%s = %v, want %v", key, d[key], want)
		}
	}
	if d["cpu"] == nil || d["updated"] == nil {
		t.Fatal("missing metrics or timestamp")
	}
}

func TestDashboardCancelledRequest(t *testing.T) {
	testPanel(t)
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dashboardStats(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}

// На свежей установке точек статистики ещё нет: страница ресурсов ждёт массив, а не null.
func TestResourcesEmptyPoints(t *testing.T) {
	mux := testPanel(t)
	statMu.Lock()
	saved := samples
	samples = nil
	statMu.Unlock()
	defer func() { statMu.Lock(); samples = saved; statMu.Unlock() }()
	_, r := testAPI(t, mux, "stats.resources", `{"hours":24}`)
	pts, ok := r["data"].(map[string]any)["points"].([]any)
	if !ok || len(pts) != 0 {
		t.Fatalf("points = %#v, нужен пустой массив", r["data"].(map[string]any)["points"])
	}
}
