// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

type reputationRow struct {
	Kind    string   `json:"kind"`
	Target  string   `json:"target"`
	List    string   `json:"list"`
	Status  string   `json:"status"`
	Codes   []string `json:"codes"`
	Message string   `json:"message"`
	Details string   `json:"details,omitempty"`
}
type reputationResult struct {
	Checked  time.Time       `json:"checked"`
	Rows     []reputationRow `json:"rows"`
	Provider string          `json:"provider,omitempty"`
}

var reputationCache = struct {
	sync.Mutex
	Data map[string]reputationResult
}{Data: map[string]reputationResult{}}

type reputationResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

func reputationCode(zone string, ip net.IP) bool {
	p := ip.To4()
	if p == nil || p[0] != 127 || p[1] != 0 {
		return false
	}
	if zone == "zen.spamhaus.org" && p[2] == 0 {
		switch p[3] {
		case 2, 3, 4, 9, 10, 11:
			return true
		}
	}
	if zone == "dbl.spamhaus.org" && p[2] == 1 {
		switch p[3] {
		case 2, 4, 5, 6, 102, 103, 104, 105, 106:
			return true
		}
	}
	return false
}
func checkReputation(ctx context.Context, r reputationResolver, kind, target, zone string) reputationRow {
	row := reputationRow{Kind: kind, Target: target, List: zone, Status: "unknown", Codes: []string{}}
	query := target
	if kind == "ip" {
		ip := net.ParseIP(target).To4()
		if ip == nil {
			row.Message = "Проверка этого IP не поддерживается"
			return row
		}
		p := strings.Split(ip.String(), ".")
		query = p[3] + "." + p[2] + "." + p[1] + "." + p[0]
	}
	ips, e := r.LookupIP(ctx, "ip4", query+"."+zone+".")
	if e != nil {
		var de *net.DNSError
		if !errors.As(e, &de) || !de.IsNotFound {
			row.Message = "DNS-проверка недоступна"
			return row
		}
		// A known listed control detects resolvers which silently filter DNSBLs.
		control := "2.0.0.127." + zone + "."
		if kind == "domain" {
			control = "dbltest.com." + zone + "."
		}
		test, err := r.LookupIP(ctx, "ip4", control)
		valid := err == nil && len(test) > 0
		for _, ip := range test {
			if !reputationCode(zone, ip) {
				valid = false
			}
		}
		if !valid {
			row.Message = "DNSBL недоступен через текущий DNS-резолвер"
			return row
		}
		row.Status = "clear"
		row.Message = "Не найден в этом списке"
		return row
	}
	if len(ips) == 0 {
		row.Message = "Пустой ответ DNS"
		return row
	}
	for _, ip := range ips {
		row.Codes = append(row.Codes, ip.String())
		if !reputationCode(zone, ip) {
			row.Message = "Сервис ограничил запрос или вернул неизвестный код"
			return row
		}
	}
	row.Status = "listed"
	row.Message = "Найден в списке"
	return row
}
func registerMailReputation() {
	registerMXToolbox()
	api("mail.reputation", func(q *Req) (any, error) {
		var in struct{ Name, Provider string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		v, e := mailDNSViewFor(in.Name)
		apiKey := store.Mail.MXToolboxKey
		mu.Unlock()
		if e != nil {
			return nil, e
		}
		if in.Provider == "mxtoolbox" {
			return mxReputation(q.R.Context(), v, apiKey)
		}
		if in.Provider != "" && in.Provider != "spamhaus" {
			return nil, errf("Неизвестный сервис проверки")
		}
		key := v.Domain + "|" + v.Hostname + "|" + v.Options.IPv4
		reputationCache.Lock()
		old, ok := reputationCache.Data[key]
		reputationCache.Unlock()
		if ok && time.Since(old.Checked) < time.Minute {
			return old, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		r := net.DefaultResolver
		result := reputationResult{Checked: time.Now(), Rows: []reputationRow{}}
		type task struct{ kind, target, zone string }
		tasks := []task{{"domain", v.Domain, "dbl.spamhaus.org"}}
		if v.Hostname != v.Domain {
			tasks = append(tasks, task{"domain", v.Hostname, "dbl.spamhaus.org"})
		}
		ips := []string{}
		if v.Options.IPv4 != "" {
			ips = append(ips, v.Options.IPv4)
		} else {
			if found, err := r.LookupIP(ctx, "ip4", v.Hostname+"."); err == nil {
				for _, ip := range found {
					if ip.To4() != nil {
						ips = append(ips, ip.String())
					}
				}
			}
		}
		if len(ips) == 0 {
			result.Rows = append(result.Rows, reputationRow{Kind: "ip", Target: v.Hostname, List: "zen.spamhaus.org", Status: "unknown", Message: "IP почтового сервера не определён"})
		}
		for i, ip := range ips {
			if i >= 8 {
				break
			}
			p := net.ParseIP(ip)
			if !mailDNSPublicIPv4(p) {
				result.Rows = append(result.Rows, reputationRow{Kind: "ip", Target: ip, List: "zen.spamhaus.org", Status: "unknown", Message: "Нужен публичный IPv4"})
				continue
			}
			tasks = append(tasks, task{"ip", ip, "zen.spamhaus.org"})
		}
		rows := make([]reputationRow, len(tasks))
		var wg sync.WaitGroup
		for i, t := range tasks {
			wg.Add(1)
			go func(i int, t task) { defer wg.Done(); rows[i] = checkReputation(ctx, r, t.kind, t.target, t.zone) }(i, t)
		}
		wg.Wait()
		result.Rows = append(result.Rows, rows...)
		reputationCache.Lock()
		if len(reputationCache.Data) > 128 {
			reputationCache.Data = map[string]reputationResult{}
		}
		reputationCache.Data[key] = result
		reputationCache.Unlock()
		return result, nil
	})
}
