// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ioCounters struct {
	In  uint64 `json:"in"`
	Out uint64 `json:"out"`
}

func parseNetworkCounters(data string) map[string]ioCounters {
	out := map[string]ioCounters{}
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "lo" || name == "" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		rx, e1 := strconv.ParseUint(fields[0], 10, 64)
		tx, e2 := strconv.ParseUint(fields[8], 10, 64)
		if e1 == nil && e2 == nil {
			out[name] = ioCounters{rx, tx}
		}
	}
	return out
}
func parseDiskCounters(data string, devices map[string]bool) map[string]ioCounters {
	out := map[string]ioCounters{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 14 || !devices[f[2]] {
			continue
		}
		read, e1 := strconv.ParseUint(f[5], 10, 64)
		write, e2 := strconv.ParseUint(f[9], 10, 64)
		// Linux diskstats sectors always use 512 bytes, regardless of hardware sector size.
		if e1 == nil && e2 == nil && read <= ^uint64(0)/512 && write <= ^uint64(0)/512 {
			out[f[2]] = ioCounters{read * 512, write * 512}
		}
	}
	return out
}
func dashboardIO() (map[string]ioCounters, map[string]ioCounters) {
	if cfg.Dry {
		t := uint64(time.Now().UnixMilli() % 86400000)
		return map[string]ioCounters{"eth0": {t * 420, t * 180}, "eth1": {t * 45, t * 12}}, map[string]ioCounters{"vda": {t * 800, t * 360}}
	}
	if isBSD() {
		nets := map[string]ioCounters{}
		for name, c := range bsdNetCounters() {
			nets[name] = ioCounters{c[0], c[1]}
		}
		return nets, map[string]ioCounters{}
	}
	netData, _ := os.ReadFile("/proc/net/dev")
	diskData, _ := os.ReadFile("/proc/diskstats")
	devices := map[string]bool{}
	entries, _ := os.ReadDir("/sys/block")
	for _, entry := range entries {
		n := entry.Name()
		if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") {
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/block", n, "device")); err == nil || strings.HasPrefix(n, "dm-") || strings.HasPrefix(n, "md") {
			devices[n] = true
		}
	}
	return parseNetworkCounters(string(netData)), parseDiskCounters(string(diskData), devices)
}
