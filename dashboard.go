// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"runtime"
	"time"
)

type DashboardStats struct {
	Network   map[string]ioCounters `json:"network"`
	Disks     map[string]ioCounters `json:"disks"`
	CPU       *float64              `json:"cpu"`
	CPUTemp   *float64              `json:"cpu_temp,omitempty"` // °C, если у сервера есть датчик
	Cores     int                   `json:"cores"`
	Load      []float64             `json:"load"`
	MemTotal  uint64                `json:"mem_total"`
	MemUsed   uint64                `json:"mem_used"`
	DiskTotal uint64                `json:"disk_total"`
	DiskUsed  uint64                `json:"disk_used"`
	Sites     int                   `json:"sites"`
	Enabled   int                   `json:"enabled"`
	Suspended int                   `json:"suspended"`
	FTP       int                   `json:"ftp"`
	DBs       int                   `json:"dbs"`
	Warnings  int                   `json:"warnings"`
	Updated   time.Time             `json:"updated"`
}

func cpuPercent(oldIdle, oldTotal, idle, total uint64) *float64 {
	if total <= oldTotal || idle < oldIdle || idle-oldIdle > total-oldTotal {
		return nil
	}
	v := round1(100 * (1 - float64(idle-oldIdle)/float64(total-oldTotal)))
	return &v
}

func dashboardStats(ctx context.Context) (*DashboardStats, error) {
	d := &DashboardStats{Cores: runtime.NumCPU()}
	if cfg.Dry {
		cpu, temp := 12.0, 46.0
		d.CPU, d.CPUTemp, d.Cores, d.Load = &cpu, &temp, 4, []float64{0.32, 0.28, 0.21}
		d.MemTotal, d.MemUsed = 8<<30, 2<<30
		d.DiskTotal, d.DiskUsed = 80<<30, 36<<30
	} else {
		idle, total := cpuTimes()
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		nextIdle, nextTotal := cpuTimes()
		d.CPU = cpuPercent(idle, total, nextIdle, nextTotal)
		d.CPUTemp = cpuTemperature("/sys")
		d.Load = loadAvg()
		var available uint64
		d.MemTotal, available = memInfo()
		if available <= d.MemTotal {
			d.MemUsed = d.MemTotal - available
		}
		d.DiskTotal, d.DiskUsed = diskUsage("/")
	}
	mu.Lock()
	d.Sites, d.FTP, d.DBs = len(store.Sites), len(store.FTP), len(store.DBs)
	for _, site := range store.Sites {
		if site.Suspended {
			d.Suspended++
		} else {
			d.Enabled++
		}
	}
	for _, notice := range store.Notices {
		if !notice.Read && (notice.Level == "warn" || notice.Level == "error") {
			d.Warnings++
		}
	}
	mu.Unlock()
	d.Network, d.Disks = dashboardIO()
	d.Updated = time.Now()
	return d, nil
}
