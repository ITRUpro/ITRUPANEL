// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Показатели сервера на FreeBSD: там нет /proc, данные берутся из sysctl и netstat.

func bsdSysctl(name string) string {
	out, _ := exec.Command("sysctl", "-n", name).Output()
	return strings.TrimSpace(string(out))
}

func bsdUint(name string) uint64 {
	n, _ := strconv.ParseUint(bsdSysctl(name), 10, 64)
	return n
}

// bsdCPUTimes — kern.cp_time: user nice sys intr idle.
func bsdCPUTimes() (idle, total uint64) {
	f := strings.Fields(bsdSysctl("kern.cp_time"))
	for i, x := range f {
		v, _ := strconv.ParseUint(x, 10, 64)
		total += v
		if i == 4 {
			idle = v
		}
	}
	return
}

func bsdMemInfo() (total, avail uint64) {
	total = bsdUint("hw.physmem")
	page := bsdUint("hw.pagesize")
	free := bsdUint("vm.stats.vm.v_free_count") + bsdUint("vm.stats.vm.v_inactive_count")
	return total, free * page
}

func bsdLoadAvg() []float64 {
	out := []float64{0, 0, 0}
	f := strings.Fields(strings.Trim(bsdSysctl("vm.loadavg"), "{} "))
	for i := 0; i < 3 && i < len(f); i++ {
		out[i], _ = strconv.ParseFloat(strings.Replace(f[i], ",", ".", 1), 64)
	}
	return out
}

var reBootSec = regexp.MustCompile(`sec = (\d+)`)

func bsdUptime() time.Duration {
	if m := reBootSec.FindStringSubmatch(bsdSysctl("kern.boottime")); m != nil {
		sec, _ := strconv.ParseInt(m[1], 10, 64)
		if sec > 0 {
			return time.Since(time.Unix(sec, 0))
		}
	}
	return 0
}

// bsdNetCounters — байты по сетевым интерфейсам из `netstat -ibn` (строки <Link#N>).
func bsdNetCounters() map[string][2]uint64 {
	out, _ := exec.Command("netstat", "-ibn").Output()
	res := map[string][2]uint64{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 8 || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		name := strings.TrimSuffix(f[0], "*")
		if strings.HasPrefix(name, "lo") || strings.HasPrefix(name, "pflog") || strings.HasPrefix(name, "enc") {
			continue
		}
		rx, _ := strconv.ParseUint(f[len(f)-5], 10, 64)
		tx, _ := strconv.ParseUint(f[len(f)-2], 10, 64)
		res[name] = [2]uint64{rx, tx}
	}
	return res
}

func bsdNetBytes() (rx, tx uint64) {
	for _, c := range bsdNetCounters() {
		rx += c[0]
		tx += c[1]
	}
	return
}

// kernelRelease — версия ядра (Linux: /proc/sys/kernel/osrelease, FreeBSD: sysctl).
func kernelRelease() string {
	if isBSD() {
		return bsdSysctl("kern.osrelease")
	}
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.TrimSpace(string(b))
}

// bsdMounts — разделы с устройством /dev/…: устройство, точка монтирования, файловая система.
func bsdMounts() [][3]string {
	out, _ := exec.Command("mount", "-p").Output()
	var res [][3]string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && strings.HasPrefix(f[0], "/dev/") {
			res = append(res, [3]string{f[0], f[1], f[2]})
		}
	}
	return res
}

// duFlags — ключи du для видимого размера в байтах и множитель вывода: у GNU du есть -b,
// у du во FreeBSD размер в килобайтах (-Ak).
func duFlags() (string, int64) {
	if isBSD() {
		return "-sxAk", 1024
	}
	return "-sxb", 1
}
