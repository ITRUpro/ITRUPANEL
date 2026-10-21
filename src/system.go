// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func uptime() time.Duration {
	b, _ := os.ReadFile("/proc/uptime")
	var f float64
	if fs := strings.Fields(string(b)); len(fs) > 0 {
		json.Unmarshal([]byte(fs[0]), &f)
	}
	return time.Duration(f) * time.Second
}

func cpuModel() (string, int) {
	b, _ := os.ReadFile("/proc/cpuinfo")
	model := ""
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "model name") {
			_, model, _ = strings.Cut(l, ":")
			break
		}
	}
	return strings.TrimSpace(model), runtime.NumCPU()
}

func verOf(cmd string, args ...string) string {
	if cfg.Dry {
		return "demo"
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return ""
	}
	out, _ := exec.Command(cmd, args...).CombinedOutput()
	s := strings.TrimSpace(firstLine(string(out)))
	if len(s) > 90 {
		s = s[:90]
	}
	return s
}

var services = []struct{ Name, Title string }{
	{"nginx", "Веб-сервер nginx"},
	{"mariadb", "СУБД MariaDB"},
	{"pure-ftpd", "FTP-сервер pure-ftpd"},
	{"cron", "Планировщик cron"},
	{"named", "DNS-сервер BIND"},
}

func cronService() string {
	if osInfo.Family == "rhel" {
		return "crond"
	}
	return "cron"
}

func registerSystem() {
	api("system.info", func(q *Req) (any, error) {
		host, _ := os.Hostname()
		model, cores := cpuModel()
		mt, ma := memInfo()
		kernel := ""
		if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			kernel = strings.TrimSpace(string(b))
		}
		mu.Lock()
		nSites, nDBs := len(store.Sites), len(store.DBs)
		mu.Unlock()
		var svcs []map[string]any
		for _, s := range services {
			name := s.Name
			if name == "cron" {
				name = cronService()
			}
			svcs = append(svcs, map[string]any{"name": name, "title": s.Title, "active": serviceActive(name)})
		}
		mu.Lock()
		mailReady := store.Mail.Ready
		mu.Unlock()
		if mailReady {
			for _, svc := range []struct{ Name, Title string }{{"postfix", "Почта SMTP (Postfix)"}, {"dovecot", "Почта IMAP (Dovecot)"}, {"opendkim", "Подпись писем DKIM"}} {
				svcs = append(svcs, map[string]any{"name": svc.Name, "title": svc.Title, "active": serviceActive(svc.Name)})
			}
		}
		if olsInstalled() && olsManaged() {
			svcs = append(svcs, map[string]any{"name": "lsws", "title": "OpenLiteSpeed (за nginx)", "active": serviceActive("lsws")})
		}
		if apacheReady() {
			svcs = append(svcs, map[string]any{"name": apacheService(), "title": "Apache (за nginx)", "active": serviceActive(apacheService())})
		}
		for _, v := range phpInstalled() {
			svcs = append(svcs, map[string]any{"name": phpService(v), "title": "PHP-FPM " + v, "active": serviceActive(phpService(v))})
		}
		return map[string]any{
			"hostname": host, "os": osInfo.Pretty, "kernel": kernel, "arch": runtime.GOARCH, "cpu": model, "cores": cores,
			"mem_total": mt, "mem_used": mt - ma, "uptime": int64(uptime().Seconds()), "load": loadAvg(),
			"panel": Version, "time": time.Now().Format("02.01.2006 15:04:05 MST"), "reboot_required": rebootRequired(),
			"versions": map[string]string{
				"nginx":   verOf("nginx", "-v"),
				"mariadb": verOf("mysql", "--version"),
				"certbot": verOf("certbot", "--version"),
				"pure-ftpd": func() string {
					if have("pure-ftpd") {
						return "установлен"
					}
					return ""
				}(),
			},
			"services": svcs,
			"sites":    nSites, "dbs": nDBs,
		}, nil
	})
	apiOp("system.reboot", "Сервер: перезагрузка", func(q *Req) (any, error) {
		if cfg.Dry {
			return nil, errf("В демо-режиме сервер не перезагружается")
		}
		if runningPanelUpdate(reconcilePanelUpdate()) {
			return nil, errf("Сначала завершите обновление панели")
		}
		// Перезагрузка посреди установки пакетов или резервного копирования может их испортить.
		var busy []string
		jobsMu.Lock()
		for _, j := range jobs {
			if j.Status == "running" {
				busy = append(busy, j.Title)
			}
		}
		jobsMu.Unlock()
		mu.Lock()
		for site := range backupJobs {
			busy = append(busy, "Резервная копия "+site)
		}
		mu.Unlock()
		if len(busy) > 0 {
			return nil, errf("Выполняются задачи: %s. Дождитесь их завершения или остановите их в разделе «Задачи»", strings.Join(busy, ", "))
		}
		mu.Lock()
		saveChecked()
		mu.Unlock()
		// Ответ успевает уйти в браузер, затем systemd корректно останавливает службы.
		go func() {
			time.Sleep(3 * time.Second)
			run("systemctl", "reboot")
		}()
		return map[string]string{"info": "Сервер перезагружается"}, nil
	})
	apiOp("system.service", "Служба: управление", func(q *Req) (any, error) {
		var in struct{ Name, Action string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ok := false
		for _, s := range services {
			ok = ok || s.Name == in.Name
		}
		ok = ok || in.Name == "lsws" && olsManaged() || in.Name == cronService() || in.Name == apacheService() || strings.HasSuffix(in.Name, "-fpm") && svcVersion(in.Name) != ""
		mu.Lock()
		ok = ok || store.Mail.Ready && (in.Name == "postfix" || in.Name == "dovecot" || in.Name == "opendkim")
		mu.Unlock()
		if !ok || (in.Action != "restart" && in.Action != "stop" && in.Action != "start" && in.Action != "reload") {
			return nil, errf("Недопустимое действие")
		}
		if in.Name == "nginx" && in.Action != "stop" {
			if _, err := run("nginx", "-t"); err != nil {
				return nil, err
			}
		}
		if in.Name == "lsws" && in.Action != "stop" {
			if err := olsTest(); err != nil {
				return nil, err
			}
		}
		return nil, service(in.Action, in.Name)
	})
	api("system.ips", func(q *Req) (any, error) {
		ifs, _ := net.Interfaces()
		var out []map[string]any
		mu.Lock()
		nsites := len(store.Sites)
		mu.Unlock()
		for _, i := range ifs {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				ip, nw, err := net.ParseCIDR(a.String())
				if err != nil || ip.IsLoopback() {
					continue
				}
				v := "IPv4"
				if ip.To4() == nil {
					v = "IPv6"
				}
				ones, _ := nw.Mask.Size()
				scope := "публичный"
				if ip.IsPrivate() {
					scope = "частный"
				} else if ip.IsLinkLocalUnicast() {
					scope = "link-local"
				}
				out = append(out, map[string]any{"ip": ip.String(), "prefix": ones, "iface": i.Name, "version": v, "scope": scope, "up": i.Flags&net.FlagUp != 0, "sites": nsites})
			}
		}
		return out, nil
	})
}

// ---------- Shell-клиент (WebSocket + PTY) ----------

var upgrader = websocket.Upgrader{
	CheckOrigin: browserRequestAllowed,
}

func handleShell(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	if s == nil {
		http.Error(w, "auth", 401)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(64 << 10)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if !sessionStillActive(r, s) {
					ws.Close()
					return
				}
			}
		}
	}()
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: s.User, IP: s.IP, Action: "Shell-клиент: вход", Status: "ok"})
	if cfg.Dry {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\nДемо-режим: терминал доступен только на сервере.\r\n"))
		return
	}
	cmd := exec.Command("bash", "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=C.UTF-8", "HOME=/root")
	cmd.Dir = "/root"
	f, err := pty.Start(cmd)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Ошибка: "+err.Error()))
		return
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		f.Close()
	}()
	ptyBridge(ws, f)
}

// ptyBridge передаёт данные между WebSocket и терминалом до закрытия одной из сторон.
func ptyBridge(ws *websocket.Conn, f *os.File) {
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if err != nil {
				ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				ws.Close()
				return
			}
			if ws.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
				return
			}
		}
	}()
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var m struct {
				Type       string
				Cols, Rows uint16
			}
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				pty.Setsize(f, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
				continue
			}
		}
		f.Write(data)
	}
}

// rebootRequired — после обновления ядра или системных библиотек нужна перезагрузка.
func rebootRequired() bool {
	if cfg.Dry {
		return false
	}
	if fileExists("/var/run/reboot-required") {
		return true
	}
	if osInfo.Family == "rhel" && have("needs-restarting") {
		// needs-restarting -r: код 1 — перезагрузка нужна.
		_, err := runTimeout(15*time.Second, "", "needs-restarting", "-r")
		return err != nil && strings.Contains(err.Error(), "exit status 1")
	}
	return false
}
