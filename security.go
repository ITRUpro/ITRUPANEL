// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Per-site policy travels with site settings and backups. Zero value is off.
type SiteSecurity struct {
	Mode        string   `json:"mode"`
	Rate        int      `json:"rate"`
	Burst       int      `json:"burst"`
	Connections int      `json:"connections"`
	Allow       []string `json:"allow"`
	Deny        []string `json:"deny"`
	Revision    uint64   `json:"revision"`
}

func securityDefaults(p SiteSecurity) SiteSecurity {
	if p.Mode == "" {
		p.Mode = "off"
	}
	if p.Rate == 0 {
		p.Rate = 10
	}
	if p.Connections == 0 {
		p.Connections = 30
	}
	return p
}
func securityNetworks(values []string) ([]string, error) {
	if len(values) > 256 {
		return nil, errf("Не более 256 IP или подсетей в одном списке")
	}
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if ip, e := netip.ParseAddr(value); e == nil && ip.Zone() == "" {
			value = netip.PrefixFrom(ip, ip.BitLen()).String()
		}
		prefix, e := netip.ParsePrefix(value)
		if e != nil || prefix.Addr().Is4In6() {
			return nil, errf("Неверный IP или CIDR: %s", value)
		}
		value = prefix.Masked().String()
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	sort.Strings(result)
	return result, nil
}
func normalizeSecurity(p SiteSecurity) (SiteSecurity, error) {
	p = securityDefaults(p)
	if p.Mode != "off" && p.Mode != "monitor" && p.Mode != "enforce" {
		return p, errf("Неизвестный режим защиты")
	}
	if p.Rate < 1 || p.Rate > 10000 || p.Burst < 0 || p.Burst > 10000 || p.Connections < 1 || p.Connections > 10000 {
		return p, errf("Лимиты: 1–10000; запас запросов: 0–10000")
	}
	var e error
	if p.Allow, e = securityNetworks(p.Allow); e != nil {
		return p, e
	}
	if p.Deny, e = securityNetworks(p.Deny); e != nil {
		return p, e
	}
	return p, nil
}
func securityCustomCheck(p SiteSecurity, custom string) error {
	if p.Mode != "off" && p.Mode != "" && regexp.MustCompile(`(?m)(?:^|[;{}\n])\s*(?:limit_req|limit_conn|limit_req_dry_run|limit_conn_dry_run|access_log)\s`).MatchString(custom) {
		return errf("Пользовательские правила Nginx содержат лимиты или access_log. Уберите конфликтующие директивы перед включением защиты")
	}
	return nil
}
func prepareSecurityLogs(s *Site) error {
	if s.Security.Mode == "" || s.Security.Mode == "off" {
		return nil
	}
	if e := os.MkdirAll(P("/var/log/itrupanel-security"), 0750); e != nil {
		return e
	}
	return writeFile("/etc/logrotate.d/itrupanel-security", []byte(securityRotation), 0644)
}
func securityID(s *Site) string {
	return fmt.Sprintf("itru_sec_%x", sha256.Sum256([]byte(s.Domain)))[:29]
}
func securityLogPath(s *Site) string { return "/var/log/itrupanel-security/" + s.Domain + ".log" }

// Each vhost is included at http scope, so zones/maps and the server are
// replaced together. No shared global configuration or untrusted snippets.
func securityNginx(s *Site) (string, string, string, error) {
	p, e := normalizeSecurity(s.Security)
	if e != nil {
		return "", "", "", e
	}
	if p.Mode == "off" || s.Suspended {
		return "", "", "", nil
	}
	id := securityID(s)
	var pre strings.Builder
	for _, list := range []struct {
		name   string
		values []string
	}{{"allow", p.Allow}, {"deny", p.Deny}} {
		fmt.Fprintf(&pre, "geo $remote_addr $%s_%s {\n default 0;\n", id, list.name)
		for _, cidr := range list.values {
			fmt.Fprintf(&pre, " %s 1;\n", cidr)
		}
		pre.WriteString("}\n")
	}
	fmt.Fprintf(&pre, `map $request_uri $%[1]s_acme { default 0; ~^/\.well-known/acme-challenge/ 1; }
map "$%[1]s_allow:$%[1]s_acme" $%[1]s_key { default $binary_remote_addr; ~1 ""; }
map "$%[1]s_allow:$%[1]s_acme:$%[1]s_deny" $%[1]s_block { default 0; "0:0:1" 1; }
limit_req_zone $%[1]s_key zone=%[1]s_req:1m rate=%[2]dr/s;
limit_conn_zone $%[1]s_key zone=%[1]s_conn:1m;
map "$%[1]s_block:$limit_req_status:$limit_conn_status" $%[1]s_event {
 default 0;
 ~^1: 1;
 ~REJECTED 1;
 ~DELAYED 1;
}
log_format %[1]s escape=json '{"time":"$time_iso8601","ip":"$remote_addr","method":"$request_method","uri":"$uri","status":$status,"req":"$limit_req_status","conn":"$limit_conn_status","listed":"$%[1]s_block","mode":"%[3]s"}';
`, id, p.Rate, p.Mode)
	logDirective := fmt.Sprintf("access_log %s %s buffer=32k flush=5s if=$%s_event;", securityLogPath(s), id, id)
	burst := ""
	if p.Burst > 0 {
		burst = fmt.Sprintf(" burst=%d nodelay", p.Burst)
	}
	server := fmt.Sprintf("limit_req zone=%s_req%s;\n    limit_req_status 429;\n    limit_conn %s_conn %d;\n    limit_conn_status 429;\n    %s\n", id, burst, id, p.Connections, logDirective)
	if p.Mode == "monitor" {
		server += "    limit_req_dry_run on;\n    limit_conn_dry_run on;\n"
	} else {
		server += fmt.Sprintf("    if ($%s_block) { return 403; }\n", id)
	}
	return pre.String(), server, logDirective, nil
}

const securityRotation = `/var/log/itrupanel-security/*.log {
 daily
 maxsize 20M
 rotate 7
 compress
 delaycompress
 missingok
 notifempty
 copytruncate
}
`

// Must hold mu. Persist only after nginx accepts the candidate; on any failure
// restore both the state and the live vhost. Existing custom rules are retained.
func saveSecurity(s *Site, p SiteSecurity) (resultErr error) {
	p, e := normalizeSecurity(p)
	if e != nil {
		return e
	}
	if p.Revision != s.Security.Revision {
		return errf("Настройки уже изменились. Обновите страницу")
	}
	u := findUser(s.User)
	if u == nil {
		return errf("Пользователь сайта не найден")
	}
	custom, e := os.ReadFile(P(customPath(s.Domain)))
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	previous := s.Security
	p.Revision++
	candidate := *s
	candidate.Security = p
	rendered, e := renderSiteNginx(&candidate, u, string(custom))
	if e != nil {
		return e
	}
	path := vhostPath(s.Domain)
	before, e := os.ReadFile(P(path))
	existed := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = prepareSecurityLogs(&candidate); e != nil {
		return e
	}

	if !fileExists(customPath(s.Domain)) {
		if e = writeFile(customPath(s.Domain), custom, 0644); e != nil {
			return e
		}
	}
	if e = writeFile(path, rendered, 0644); e != nil {
		return e
	}
	defer func() {
		if resultErr == nil {
			return
		}
		s.Security = previous
		var rollback error
		if existed {
			rollback = writeFile(path, before, 0644)
		} else {
			rollback = os.Remove(P(path))
		}
		rollback = errors.Join(rollback, nginxTestReload())
		if rollback != nil {
			resultErr = fmt.Errorf("%w; восстановление Nginx: %v", resultErr, rollback)
		}
	}()
	if e = nginxTestReload(); e != nil {
		return e
	}
	s.Security = p
	if e = saveChecked(); e != nil {
		return e
	}
	return nil
}

type SecurityEvent struct {
	Time   string `json:"time"`
	IP     string `json:"ip"`
	Method string `json:"method"`
	URI    string `json:"uri"`
	Status int    `json:"status"`
	Req    string `json:"req"`
	Conn   string `json:"conn"`
	Listed string `json:"listed"`
	Mode   string `json:"mode"`
	Domain string `json:"domain"`
}

// A bounded tail, never a full scan of logs during an attack.
func securityEvents(s *Site) ([]SecurityEvent, error) {
	events := []SecurityEvent{}
	f, e := os.Open(P(securityLogPath(s)))
	if os.IsNotExist(e) {
		return events, nil
	}
	if e != nil {
		return events, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return events, e
	}
	const maxBytes = 256 << 10
	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, e = f.Seek(start, io.SeekStart); e != nil {
		return events, e
	}
	data, e := io.ReadAll(io.LimitReader(f, maxBytes))
	if e != nil {
		return events, e
	}
	lines := strings.Split(string(data), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		var event SecurityEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if _, e := time.Parse(time.RFC3339, event.Time); e != nil {
			continue
		}
		event.Domain = s.Domain
		events = append(events, event)
	}
	if len(events) > 200 {
		events = events[len(events)-200:]
	}
	return events, nil
}
func registerSecurity() {
	api("security.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		sites := []map[string]any{}
		events := []SecurityEvent{}
		warnings := []string{}
		for _, s := range store.Sites {
			sites = append(sites, map[string]any{"domain": s.Domain, "policy": securityDefaults(s.Security), "suspended": s.Suspended})
			// Bound the total request cost as well as each file.
			if len(store.Sites) <= 100 {
				es, e := securityEvents(s)
				if e != nil {
					warnings = append(warnings, s.Domain+": "+e.Error())
				}
				events = append(events, es...)
			}
		}
		if len(store.Sites) > 100 {
			warnings = append(warnings, "Журнал обзора доступен для серверов с числом сайтов до 100")
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Time > events[j].Time })
		if len(events) > 300 {
			events = events[:300]
		}
		return map[string]any{"sites": sites, "events": events, "demo": cfg.Dry, "warnings": warnings}, nil
	})
	apiOp("security.save", "Настройка защиты сайта", func(q *Req) (any, error) {
		var in struct {
			Domain string       `json:"domain"`
			Policy SiteSecurity `json:"policy"`
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		defer mu.Unlock()
		s := findSite(in.Domain)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		if e := saveSecurity(s, in.Policy); e != nil {
			return nil, e
		}
		return securityDefaults(s.Security), nil
	})
}
