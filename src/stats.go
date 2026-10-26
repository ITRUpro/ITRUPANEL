// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------- потребление ресурсов ----------

type Sample struct {
	T    int64   `json:"t"`
	CPU  float64 `json:"cpu"`  // %
	Mem  float64 `json:"mem"`  // %
	Load float64 `json:"load"` // la1
	Disk float64 `json:"disk"` // % корня
	RX   float64 `json:"rx"`   // байт/с
	TX   float64 `json:"tx"`
}

var (
	statMu  sync.Mutex
	samples []Sample                        // раз в минуту, 7 суток
	traffic = map[string]map[string]int64{} // день → сайт → байты
	offsets = map[string]int64{}            // журнал → позиция чтения
)

func cpuTimes() (idle, total uint64) {
	if isBSD() {
		return bsdCPUTimes()
	}
	b, _ := os.ReadFile("/proc/stat")
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	for i, x := range f[1:] {
		// guest and guest_nice are already included in user and nice.
		if i >= 8 {
			break
		}
		v, _ := strconv.ParseUint(x, 10, 64)
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return
}

func memInfo() (total, avail uint64) {
	if isBSD() {
		return bsdMemInfo()
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		n, _ := strconv.ParseUint(strings.Fields(v + " 0")[0], 10, 64)
		switch k {
		case "MemTotal":
			total = n * 1024
		case "MemAvailable":
			avail = n * 1024
		}
	}
	return
}

func netBytes() (rx, tx uint64) {
	if isBSD() {
		return bsdNetBytes()
	}
	b, _ := os.ReadFile("/proc/net/dev")
	lines := strings.Split(string(b), "\n")
	if len(lines) < 3 {
		return
	}
	for _, l := range lines[2:] {
		name, rest, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) > 8 {
			r, _ := strconv.ParseUint(f[0], 10, 64)
			t, _ := strconv.ParseUint(f[8], 10, 64)
			rx += r
			tx += t
		}
	}
	return
}

func diskUsage(path string) (total, used uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return
	}
	// Типы полей Statfs_t различаются между Linux и FreeBSD, поэтому приводим явно.
	total = uint64(st.Blocks) * uint64(st.Bsize)
	free := uint64(st.Bavail) * uint64(st.Bsize)
	return total, total - free
}

func loadAvg() []float64 {
	if isBSD() {
		return bsdLoadAvg()
	}
	b, _ := os.ReadFile("/proc/loadavg")
	f := strings.Fields(string(b))
	out := []float64{0, 0, 0}
	for i := 0; i < 3 && i < len(f); i++ {
		out[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return out
}

func statsLoop() {
	loadStats()
	if cfg.Dry {
		demoStats()
	}
	pi, pt := cpuTimes()
	prx, ptx := netBytes()
	last := time.Now()
	tick := time.NewTicker(time.Minute)
	n := 0
	for range tick.C {
		if cfg.Dry {
			continue
		}
		i, t := cpuTimes()
		rx, tx := netBytes()
		dt := time.Since(last).Seconds()
		last = time.Now()
		s := Sample{T: time.Now().Unix()}
		if t > pt {
			s.CPU = round1(100 * (1 - float64(i-pi)/float64(t-pt)))
		}
		mt, ma := memInfo()
		if mt > 0 {
			s.Mem = round1(100 * float64(mt-ma) / float64(mt))
		}
		s.Load = loadAvg()[0]
		dtot, dused := diskUsage("/")
		if dtot > 0 {
			s.Disk = round1(100 * float64(dused) / float64(dtot))
		}
		if rx >= prx && tx >= ptx {
			s.RX, s.TX = float64(rx-prx)/dt, float64(tx-ptx)/dt
		}
		pi, pt, prx, ptx = i, t, rx, tx
		statMu.Lock()
		samples = append(samples, s)
		if len(samples) > 7*24*60 {
			samples = samples[len(samples)-7*24*60:]
		}
		statMu.Unlock()
		checkThresholds(s)
		n++
		if n%10 == 0 {
			scanTraffic()
			saveStats()
		}
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func checkThresholds(s Sample) {
	mu.Lock()
	defer mu.Unlock()
	if s.Disk >= float64(store.Settings.DiskWarn) {
		addNotice("warn", "disk", fmt.Sprintf("Диск заполнен на %.0f%%", s.Disk))
	}
	if s.Mem >= 95 {
		addNotice("warn", "mem", fmt.Sprintf("Память занята на %.0f%%", s.Mem))
	}
}

func loadStats() {
	b, err := os.ReadFile(filepath.Join(cfg.Home, "var", "stats.json"))
	if err != nil {
		return
	}
	var st struct {
		Samples []Sample
		Traffic map[string]map[string]int64
		Offsets map[string]int64
	}
	if json.Unmarshal(b, &st) == nil {
		samples, traffic, offsets = st.Samples, st.Traffic, st.Offsets
		if traffic == nil {
			traffic = map[string]map[string]int64{}
		}
		if offsets == nil {
			offsets = map[string]int64{}
		}
	}
}

func saveStats() {
	statMu.Lock()
	b, _ := json.Marshal(map[string]any{"Samples": samples, "Traffic": traffic, "Offsets": offsets})
	statMu.Unlock()
	p := filepath.Join(cfg.Home, "var", "stats.json")
	os.WriteFile(p+".tmp", b, 0600)
	os.Rename(p+".tmp", p)
}

func demoStats() {
	now := time.Now().Unix()
	statMu.Lock()
	defer statMu.Unlock()
	samples = nil
	for i := 24 * 60; i > 0; i-- {
		h := float64((now - int64(i*60)) % 86400 / 3600)
		samples = append(samples, Sample{T: now - int64(i*60), CPU: round1(8 + 10*rand.Float64() + h/2), Mem: round1(42 + 5*rand.Float64()), Load: round1(0.3 + rand.Float64()), Disk: 37, RX: 20000 + 50000*rand.Float64(), TX: 80000 + 200000*rand.Float64()})
	}
	for d := 0; d < 30; d++ {
		day := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
		traffic[day] = map[string]int64{"example.ru": int64(2e8 + rand.Int63n(5e8)), "shop.example.ru": int64(5e7 + rand.Int63n(2e8))}
	}
}

// scanTraffic дочитывает access-журналы сайтов и суммирует $body_bytes_sent по дням.
func scanTraffic() {
	mu.Lock()
	type lf struct{ site, path string }
	var files []lf
	for _, s := range store.Sites {
		if u := findUser(s.User); u != nil {
			files = append(files, lf{s.Domain, u.Home + "/data/logs/" + s.Domain + ".access.log"})
		}
	}
	mu.Unlock()
	for _, f := range files {
		fh, err := os.Open(f.path)
		if err != nil {
			continue
		}
		fi, _ := fh.Stat()
		statMu.Lock()
		off := offsets[f.path]
		statMu.Unlock()
		if off > fi.Size() { // журнал повернули
			off = 0
		}
		fh.Seek(off, io.SeekStart)
		rd := bufio.NewReaderSize(fh, 1<<16)
		add := map[string]int64{}
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				break
			}
			off += int64(len(line))
			// combined: ip - - [02/Jan/2006:15:04:05 +0300] "GET / HTTP/1.1" 200 1234 "-" "UA"
			lb := strings.IndexByte(line, '[')
			q2 := strings.Index(line, "\" ")
			if lb < 0 || q2 < 0 || lb+12 > len(line) {
				continue
			}
			t, err := time.Parse("02/Jan/2006", line[lb+1:lb+12])
			if err != nil {
				continue
			}
			rest := strings.Fields(line[q2+2:])
			if len(rest) < 2 {
				continue
			}
			n, _ := strconv.ParseInt(rest[1], 10, 64)
			add[t.Format("2006-01-02")] += n
		}
		fh.Close()
		statMu.Lock()
		offsets[f.path] = off
		for d, n := range add {
			if traffic[d] == nil {
				traffic[d] = map[string]int64{}
			}
			traffic[d][f.site] += n
		}
		// храним 400 дней
		for d := range traffic {
			if t, _ := time.Parse("2006-01-02", d); time.Since(t) > 400*24*time.Hour {
				delete(traffic, d)
			}
		}
		statMu.Unlock()
	}
}

func registerStats() {
	api("stats.monitor", func(q *Req) (any, error) { return dashboardMonitor(q.R.Context()) })
	api("stats.dashboard", func(q *Req) (any, error) {
		return dashboardStats(q.R.Context())
	})
	api("stats.resources", func(q *Req) (any, error) {
		var in struct{ Hours int }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Hours <= 0 || in.Hours > 168 {
			in.Hours = 24
		}
		from := time.Now().Add(-time.Duration(in.Hours) * time.Hour).Unix()
		statMu.Lock()
		var pts []Sample
		for _, s := range samples {
			if s.T >= from {
				pts = append(pts, s)
			}
		}
		statMu.Unlock()
		// прореживаем до ~300 точек
		step := len(pts)/300 + 1
		out := []Sample{} // пустой массив, а не null: на свежей установке точек ещё нет
		for i := 0; i < len(pts); i += step {
			out = append(out, pts[i])
		}
		mt, ma := memInfo()
		dt, du := diskUsage("/")
		return map[string]any{"points": out, "now": map[string]any{"mem_total": mt, "mem_used": mt - ma, "disk_total": dt, "disk_used": du, "load": loadAvg()}}, nil
	})
	api("stats.traffic", func(q *Req) (any, error) {
		var in struct{ Days int }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Days <= 0 || in.Days > 400 {
			in.Days = 30
		}
		statMu.Lock()
		defer statMu.Unlock()
		type row struct {
			Day   string           `json:"day"`
			Sites map[string]int64 `json:"sites"`
			Total int64            `json:"total"`
		}
		var rows []row
		totals := map[string]int64{}
		for i := in.Days - 1; i >= 0; i-- {
			d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
			r := row{Day: d, Sites: map[string]int64{}}
			for s, n := range traffic[d] {
				r.Sites[s] = n
				r.Total += n
				totals[s] += n
			}
			rows = append(rows, r)
		}
		sites := []map[string]any{}
		for s, n := range totals {
			sites = append(sites, map[string]any{"site": s, "bytes": n})
		}
		sort.Slice(sites, func(i, j int) bool { return sites[i]["bytes"].(int64) > sites[j]["bytes"].(int64) })
		return map[string]any{"days": rows, "sites": sites}, nil
	})
	apiOp("stats.disk.clean", "Диск: очистка", func(q *Req) (any, error) {
		var in struct{ What string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		info, err := diskClean(in.What)
		if err != nil {
			return nil, err
		}
		return map[string]string{"info": info}, nil
	})
	api("stats.disk", func(q *Req) (any, error) {
		var parts []map[string]any
		if isBSD() {
			for _, m := range bsdMounts() {
				t, u := diskUsage(m[1])
				parts = append(parts, map[string]any{"dev": m[0], "mount": m[1], "fs": m[2], "total": t, "used": u})
			}
		} else if b, err := os.ReadFile("/proc/mounts"); err == nil {
			seen := map[string]bool{}
			for _, l := range strings.Split(string(b), "\n") {
				f := strings.Fields(l)
				if len(f) < 3 || !strings.HasPrefix(f[0], "/dev/") || seen[f[0]] {
					continue
				}
				seen[f[0]] = true
				t, u := diskUsage(f[1])
				parts = append(parts, map[string]any{"dev": f[0], "mount": f[1], "fs": f[2], "total": t, "used": u})
			}
		}
		if len(parts) == 0 {
			t, u := diskUsage("/")
			parts = append(parts, map[string]any{"dev": "/", "mount": "/", "fs": "", "total": t, "used": u})
		}
		mu.Lock()
		var users []SiteUser
		for _, u := range store.Users {
			users = append(users, *u)
		}
		var sites []Site
		for _, s := range store.Sites {
			sites = append(sites, *s)
		}
		mu.Unlock()
		return diskReport(parts, users, sites), nil
	})
}

// ---------- планировщик ----------

func schedulerLoop() {
	lastDay, lastBackup := "", ""
	time.Sleep(20 * time.Second)
	for {
		now := time.Now()
		day := now.Format("2006-01-02")
		if day != lastDay {
			lastDay = day
			if !cfg.Dry {
				func() {
					updateGate.RLock()
					defer updateGate.RUnlock()
					if !panelUpdating() {
						renewCerts()
					}
				}()
			}
		}
		mu.Lock()
		daily, hour := store.Settings.BackupDaily, store.Settings.BackupHour
		mu.Unlock()
		if daily && now.Hour() == hour && lastBackup != day && !cfg.Dry {
			lastBackup = day
			go autoBackups()
		}
		if !cfg.Dry {
			avAutoScan(now)
			avAutoUpdateDB(now)
			avAutoUpdateRules(now)
			maldetAutoUpdate(now)
			tplLibAutoUpdate(now)
			compAutoCheck(now)
		}
		panelAutoUpdateTick(now)
		os.MkdirAll(P(sockDir), 0755)
		time.Sleep(5 * time.Minute)
	}
}

// diskSystemDirs — крупные системные папки: так видно всё, что занимает место на разделе,
// а не только сайты и базы. clean — что можно безопасно очистить кнопкой.
var diskSystemDirs = []struct {
	name, kind, clean string
	paths             []string
}{
	{"Система: программы и библиотеки", "sys", "", []string{"/usr"}},
	{"Ядро и загрузчик", "sys", "", []string{"/boot"}},
	{"Дополнительные программы (PHP Remi и др.)", "sys", "", []string{"/opt"}},
	{"Docker: образы, контейнеры и тома", "docker", "", []string{"/var/lib/docker", "/var/lib/containerd"}},
	{"Базы данных MariaDB", "sys", "", []string{"/var/lib/mysql"}},
	{"Системные журналы", "sys", "journal", []string{"/var/log"}},
	{"Кэш пакетов", "sys", "pkgcache", []string{"/var/cache/dnf", "/var/cache/apt", "/var/cache/yum"}},
	{"Временные файлы", "sys", "", []string{"/tmp", "/var/tmp"}},
	{"Резервные копии", "sys", "", []string{backupDir}},
	{"Корзина менеджера файлов", "sys", "recycle", []string{recycleDir}},
	{"Базы антивируса ClamAV", "sys", "", []string{"/var/lib/clamav"}},
	{"Карантин антивируса", "sys", "", []string{avQuarantineDir}},
	{"Почта", "sys", "", []string{"/var/vmail"}},
	{"Файлы root", "sys", "", []string{"/root"}},
}

// duBytes считает размер папок одной командой du (в пределах своей файловой системы).
func duBytes(paths []string) map[string]int64 {
	out := map[string]int64{}
	var exist []string
	for _, p := range paths {
		if fileExists(p) {
			exist = append(exist, P(p))
		}
	}
	if len(exist) == 0 {
		return out
	}
	if cfg.Dry {
		for _, p := range paths {
			if fileExists(p) {
				out[P(p)] = dirSize(p)
			}
		}
		return out
	}
	flags, unit := duFlags()
	text, _ := runTimeout(3*time.Minute, "", "du", append([]string{flags}, exist...)...)
	for _, line := range strings.Split(text, "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) == 2 {
			var n int64
			fmt.Sscan(f[0], &n)
			out[f[1]] = n * unit
		}
	}
	return out
}

func sameDevice(a, b string) bool {
	sa, ea := os.Stat(P(a))
	sb, eb := os.Stat(P(b))
	if ea != nil || eb != nil {
		return false
	}
	da, ok1 := sa.Sys().(*syscall.Stat_t)
	db, ok2 := sb.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && da.Dev == db.Dev
}

func diskReport(parts []map[string]any, users []SiteUser, sites []Site) map[string]any {
	var all []string
	for _, u := range users {
		all = append(all, u.Home)
	}
	for _, s := range sites {
		all = append(all, s.Root)
	}
	for _, d := range diskSystemDirs {
		all = append(all, d.paths...)
	}
	sizes := duBytes(all)
	var items []map[string]any
	var counted int64 // занятое на корневом разделе и учтённое в списке (без вложенных сайтов)
	for _, u := range users {
		n := sizes[P(u.Home)]
		items = append(items, map[string]any{"kind": "user", "name": u.Name, "path": u.Home, "size": n})
		if sameDevice(u.Home, "/") {
			counted += n
		}
	}
	for _, s := range sites {
		items = append(items, map[string]any{"kind": "site", "name": s.Domain, "path": s.Root, "size": sizes[P(s.Root)]})
	}
	for _, d := range diskSystemDirs {
		var n int64
		path := ""
		for _, p := range d.paths {
			if v, ok := sizes[P(p)]; ok {
				n += v
				if path == "" {
					path = p
				}
				if sameDevice(p, "/") {
					counted += v
				}
			}
		}
		if path == "" || n == 0 && d.clean == "" {
			continue
		}
		items = append(items, map[string]any{"kind": d.kind, "name": d.name, "path": path, "size": n, "clean": d.clean})
	}
	// Подкачка — один файл, du его не покажет в списке папок.
	for _, sw := range swapFiles() {
		if fi, err := os.Stat(P(sw)); err == nil {
			items = append(items, map[string]any{"kind": "sys", "name": "Файл подкачки", "path": sw, "size": fi.Size()})
			if sameDevice(sw, "/") {
				counted += fi.Size()
			}
		}
	}
	for _, p := range parts {
		if p["mount"] == "/" && !cfg.Dry {
			if used, _ := p["used"].(uint64); int64(used) > counted {
				items = append(items, map[string]any{"kind": "other", "name": "Прочее на разделе /", "path": "/", "size": int64(used) - counted})
			}
		}
	}
	return map[string]any{"parts": parts, "items": items}
}

// swapFiles — файлы подкачки (не разделы) из /proc/swaps.
func swapFiles() []string {
	var out []string
	data, _ := os.ReadFile("/proc/swaps")
	for i, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if i > 0 && len(f) > 1 && f[1] == "file" {
			out = append(out, f[0])
		}
	}
	return out
}

// diskClean — безопасная очистка: кэш пакетов, старые журналы systemd, корзина.
func diskClean(what string) (string, error) {
	switch what {
	case "pkgcache":
		if osInfo.Family == "rhel" {
			_, err := run("dnf", "clean", "all")
			return "Кэш пакетов очищен", err
		}
		_, err := run("apt-get", "clean")
		return "Кэш пакетов очищен", err
	case "journal":
		_, err := run("journalctl", "--vacuum-size=100M")
		return "Системный журнал сокращён до 100 МБ", err
	case "recycle":
		return "Корзина очищена", recycleEmpty()
	}
	return "", errf("Неизвестная очистка")
}
