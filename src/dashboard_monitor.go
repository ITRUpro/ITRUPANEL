// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type monitoredService struct {
	Name   string  `json:"name"`
	Title  string  `json:"title"`
	State  string  `json:"state"`
	Memory *uint64 `json:"memory"`
}

// A missing or unreadable unit is never reported as a stopped service.
func parseMonitoredUnits(out string) map[string]monitoredService {
	result := map[string]monitoredService{}
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		p := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				p[k] = v
			}
		}
		if p["Id"] == "" {
			continue
		}
		s := monitoredService{Name: strings.TrimSuffix(p["Id"], ".service"), State: "unknown"}
		switch {
		case p["LoadState"] == "not-found":
			s.State = "missing"
		case p["LoadState"] == "masked":
			s.State = "masked"
		case p["LoadState"] == "loaded":
			switch p["ActiveState"] {
			case "active":
				s.State = "running"
			case "inactive":
				s.State = "stopped"
			case "failed":
				s.State = "failed"
			case "activating", "deactivating", "reloading", "refreshing":
				s.State = "changing"
			}
		}
		if n, err := strconv.ParseUint(p["MemoryCurrent"], 10, 64); err == nil && n != ^uint64(0) {
			s.Memory = &n
		}
		result[p["Id"]] = s
		for _, alias := range strings.Fields(p["Names"]) {
			result[alias] = s
		}
	}
	return result
}

func dashboardMonitor(ctx context.Context) (any, error) {
	host, _ := os.Hostname()
	arch := runtime.GOARCH
	model, cores := cpuModel()
	kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	mt, ma := memInfo()
	dt, du := diskUsage("/")
	ips := []string{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ip, _, e := net.ParseCIDR(a.String())
			if e == nil && ip.IsGlobalUnicast() {
				ips = append(ips, ip.String())
			}
		}
	}
	zone, offset := time.Now().Zone()
	list := []monitoredService{}
	add := func(name, title string) {
		list = append(list, monitoredService{Name: name, Title: title, State: "unknown"})
	}
	for _, s := range services {
		name := s.Name
		if name == "cron" {
			name = cronService()
		}
		add(name, s.Title)
	}
	for _, v := range phpInstalled() {
		add(phpService(v), "PHP-FPM "+v)
	}
	for _, s := range []struct{ name, title string }{{"postfix", "SMTP · Postfix"}, {"dovecot", "IMAP · Dovecot"}, {"opendkim", "DKIM · OpenDKIM"}, {"rspamd", "Антиспам · Rspamd"}, {"lsws", "OpenLiteSpeed"}, {apacheService(), "Apache"}} {
		add(s.name, s.title)
	}
	if cfg.Dry {
		host, model, cores = "server.example.com", "AMD EPYC", 4
		arch = "amd64"
		kernel = []byte("6.12.0-amd64")
		mt, ma, dt, du = 8<<30, 6<<30, 80<<30, 36<<30
		ips = []string{"203.0.113.10"}
		zone, offset = "UTC", 0
		for i := range list {
			list[i].State = "running"
			n := uint64(20+i*3) << 20
			list[i].Memory = &n
			if list[i].Name == "lsws" || list[i].Name == apacheService() {
				list[i].State = "missing"
				list[i].Memory = nil
			}
			if list[i].Name == "rspamd" {
				list[i].State = "stopped"
				list[i].Memory = nil
			}
		}
	} else {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		args := []string{"show", "--no-pager", "--property=Id,Names,LoadState,ActiveState,MemoryCurrent"}
		for _, s := range list {
			name := s.Name
			if name == "lsws" {
				name = olsServiceName()
			}
			args = append(args, name+".service")
		}
		out, err := exec.CommandContext(bounded, "systemctl", args...).Output()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// systemctl can return nonzero for missing units while emitting valid states for others.
		units := parseMonitoredUnits(string(out))
		if err != nil && len(units) == 0 {
			return nil, errf("Не удалось получить состояния сервисов systemd")
		}
		for i, s := range list {
			name := s.Name
			if name == "lsws" {
				name = olsServiceName()
			}
			if unit, ok := units[name+".service"]; ok {
				unit.Name = s.Name
				unit.Title = s.Title
				list[i] = unit
			}
		}
	}
	used := uint64(0)
	if ma <= mt {
		used = mt - ma
	}
	up := int64(uptime().Seconds())
	if cfg.Dry {
		up = 9*86400 + 6*3600
	}
	return map[string]any{"services": list, "updated": time.Now(), "hostname": host, "ips": ips, "os": osInfo.Pretty, "kernel": strings.TrimSpace(string(kernel)), "arch": arch, "cpu": model, "cores": cores, "mem_total": mt, "mem_used": used, "disk_total": dt, "disk_used": du, "uptime": up, "timezone": zone, "utc_offset": offset, "version": Version}, nil
}
