// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const loginLimit = 5
const loginWindow = 10 * time.Minute
const loginBan = 15 * time.Minute

type loginRecord struct {
	Failures []time.Time `json:"failures,omitempty"`
	Until    time.Time   `json:"until"`
	Pending  int         `json:"-"`
}
type loginGuardState struct {
	sync.Mutex
	home    string
	records map[string]*loginRecord
	active  int
	err     error
}

var loginGuard loginGuardState

// Only the socket peer identifies a client. Untrusted forwarding headers must
// never allow a caller to rotate its identity and evade this limiter.
func (g *loginGuardState) loadLocked(now time.Time) {
	if g.home == cfg.Home && g.records != nil {
		return
	}
	g.home, g.records, g.active, g.err = cfg.Home, map[string]*loginRecord{}, 0, nil
	f, e := os.Open(filepath.Join(g.home, "var", "login-guard.json"))
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		g.err = e
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if e != nil {
		g.err = e
		return
	}
	if len(b) > 8<<20 {
		g.err = fmt.Errorf("Журнал защиты входа слишком велик")
		return
	}
	if len(bytes.TrimSpace(b)) == 0 {
		// Пустой файл остаётся после сбоя питания: счётчики попыток просто начинаются заново, вход не блокируется.
		log.Printf("login guard: журнал защиты входа пуст, начат заново")
		return
	}
	if e = json.Unmarshal(b, &g.records); e != nil || g.records == nil {
		g.records = map[string]*loginRecord{}
		p := filepath.Join(g.home, "var", "login-guard.json")
		os.Rename(p, p+".corrupt")
		log.Printf("login guard: журнал защиты входа повреждён и перенесён в %s.corrupt, начат заново", p)
		return
	}
	for ip, r := range g.records {
		if _, e := netip.ParseAddr(ip); e != nil || r == nil {
			delete(g.records, ip)
		}
	}
	g.pruneLocked(now)
}
func (g *loginGuardState) pruneLocked(now time.Time) {
	for ip, r := range g.records {
		kept := r.Failures[:0]
		for _, t := range r.Failures {
			if t.After(now.Add(-loginWindow)) {
				kept = append(kept, t)
			}
		}
		r.Failures = kept
		if r.Pending == 0 && len(kept) == 0 && !r.Until.After(now) {
			delete(g.records, ip)
		}
	}
}
func (g *loginGuardState) saveLocked() error {
	b, e := json.Marshal(g.records)
	if e != nil {
		return e
	}
	p := filepath.Join(g.home, "var", "login-guard.json")
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	return writeFileAtomic(p, b, 0600)
}
func (g *loginGuardState) begin(ip string, now time.Time) (time.Duration, error) {
	g.Lock()
	defer g.Unlock()
	g.loadLocked(now)
	if g.err != nil {
		return 0, g.err
	}
	r := g.records[ip]
	if r != nil && r.Until.After(now) {
		return r.Until.Sub(now), nil
	}
	if g.active >= 4 {
		return time.Second, nil
	}
	// Bound memory even when many distinct addresses send failures.
	if r == nil && len(g.records) >= 10000 {
		g.pruneLocked(now)
		if len(g.records) >= 10000 {
			return time.Minute, nil
		}
	}
	if r == nil {
		r = &loginRecord{}
		g.records[ip] = r
	}
	kept := r.Failures[:0]
	for _, t := range r.Failures {
		if t.After(now.Add(-loginWindow)) {
			kept = append(kept, t)
		}
	}
	r.Failures = kept
	if len(r.Failures)+r.Pending >= loginLimit {
		return time.Second, nil
	}
	r.Pending++
	g.active++
	return 0, nil
}
func (g *loginGuardState) finish(ip string, success bool, now time.Time) error {
	g.Lock()
	defer g.Unlock()
	r := g.records[ip]
	if r == nil {
		return fmt.Errorf("Попытка входа не зарегистрирована")
	}
	r.Pending--
	g.active--
	if success {
		r.Failures = nil
	} else {
		r.Failures = append(r.Failures, now)
		if len(r.Failures) >= loginLimit {
			r.Until = now.Add(loginBan)
		}
	}
	// A successful in-flight attempt must not cancel a ban caused by others.
	g.err = g.saveLocked()
	return g.err
}

// finishNeutral завершает попытку, не считая её ни успехом, ни ошибкой: пароль верный, но нужен код второго шага.
// Прежние ошибки с этого адреса остаются, поэтому перебор кодов этим путём не обнулить.
func (g *loginGuardState) finishNeutral(ip string) error {
	g.Lock()
	defer g.Unlock()
	r := g.records[ip]
	if r == nil {
		return fmt.Errorf("Попытка входа не зарегистрирована")
	}
	r.Pending--
	g.active--
	g.err = g.saveLocked()
	return g.err
}
func (g *loginGuardState) unban(ip string) error {
	g.Lock()
	defer g.Unlock()
	g.loadLocked(time.Now())
	r := g.records[ip]
	if r != nil {
		r.Failures = nil
		r.Until = time.Time{}
		if r.Pending == 0 {
			delete(g.records, ip)
		}
	}
	g.err = g.saveLocked()
	return g.err
}
func (g *loginGuardState) status() ([]map[string]any, string) {
	g.Lock()
	defer g.Unlock()
	now := time.Now()
	g.loadLocked(now)
	rows := []map[string]any{}
	for ip, r := range g.records {
		if r.Until.After(now) {
			rows = append(rows, map[string]any{"ip": ip, "until": r.Until, "service": "panel"})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["ip"].(string) < rows[j]["ip"].(string) })
	warning := ""
	if g.err != nil {
		warning = g.err.Error()
	}
	return rows, warning
}

func registerAuthGuard() {
	api("authguard.status", func(q *Req) (any, error) {
		bans, warning := loginGuard.status()
		ftp := ftpGuardStatus()
		return map[string]any{"bans": bans, "warning": warning, "ftp": ftp, "demo": cfg.Dry, "ip": clientIP(q.R)}, nil
	})
	apiOp("authguard.unban", "Снятие блокировки входа", func(q *Req) (any, error) {
		var in struct{ IP, Service string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		a, e := netip.ParseAddr(in.IP)
		if e != nil {
			return nil, errf("Некорректный IP-адрес")
		}
		ip := a.Unmap().String()
		if in.Service == "panel" {
			return nil, loginGuard.unban(ip)
		}
		if in.Service != "ftp" {
			return nil, errf("Неизвестная служба")
		}
		ftpGuardMu.Lock()
		defer ftpGuardMu.Unlock()
		_, e = runTimeout(15*time.Second, "", "fail2ban-client", "set", ftpGuardJail, "unbanip", ip)
		return nil, e
	})
	apiOp("authguard.ftp.enable", "Включение защиты FTP", func(q *Req) (any, error) {
		var in struct{ Port int }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		if in.Port < 1 || in.Port > 65535 {
			return nil, errf("Порт должен быть от 1 до 65535")
		}
		id := startJob("authguard:ftp", "Защита FTP от перебора паролей", q.S.User, func(j *Job) (any, error) {
			e := enableFTPGuard(j, in.Port)
			return map[string]any{"info": "Настройка защиты FTP завершена"}, e
		})
		return map[string]any{"job": id}, nil
	})
}
