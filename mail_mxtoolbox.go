// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const mxLookupURL = "https://api.mxtoolbox.com/api/v1/Lookup/blacklist/"
const mxCacheTTL = 10 * time.Minute

var mxKeyPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var mxHTTP = &http.Client{
	Timeout: 25 * time.Second,
	// Never forward the Authorization header to a redirected destination.
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}
var mxRunGate = make(chan struct{}, 1)
var mxCache = struct {
	sync.Mutex
	data map[string]reputationResult
}{data: map[string]reputationResult{}}

func registerMXToolbox() {
	api("mail.mxtoolbox.targets", func(q *Req) (any, error) {
		var in struct{ Name string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		mu.Lock()
		v, e := mailDNSViewFor(in.Name)
		mu.Unlock()
		if e != nil {
			return nil, e
		}
		ctx, cancel := context.WithTimeout(q.R.Context(), 5*time.Second)
		defer cancel()
		targets, _ := mxTargets(ctx, v)
		values := []string{}
		for _, t := range targets {
			values = append(values, t.value)
		}
		return values, nil
	})
	api("mail.mxtoolbox.settings", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"configured": store.Mail.MXToolboxKey != ""}, nil
	})
	apiOp("mail.mxtoolbox.save", "Настройка MXToolbox", func(q *Req) (any, error) {
		var in struct {
			Key    string
			Remove bool
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		key := strings.TrimSpace(in.Key)
		if in.Remove && key != "" {
			return nil, errf("Выберите сохранение или удаление ключа")
		}
		if key != "" && !mxKeyPattern.MatchString(key) {
			return nil, errf("API-ключ MXToolbox должен быть в формате UUID")
		}
		mu.Lock()
		defer mu.Unlock()
		old := store.Mail.MXToolboxKey
		if in.Remove {
			store.Mail.MXToolboxKey = ""
		} else if key != "" {
			store.Mail.MXToolboxKey = key
		}
		if e := saveChecked(); e != nil {
			store.Mail.MXToolboxKey = old
			return nil, errf("Не удалось сохранить настройки MXToolbox")
		}
		mxCache.Lock()
		mxCache.data = map[string]reputationResult{}
		mxCache.Unlock()
		return map[string]any{"configured": store.Mail.MXToolboxKey != ""}, nil
	})
}

type mxCheck struct{ Name, Info string }
type mxResponse struct {
	Command, CommandArgument           string
	Failed, Warnings, Passed, Timeouts []mxCheck
}

// Vendor messages may contain HTML. Keep only inert, bounded text; don't return
// arbitrary vendor URLs or error response bodies (which could echo credentials).
func mxPlain(s, secret string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := false
	for {
		t := z.Next()
		if t == html.ErrorToken {
			break
		}
		switch t {
		case html.StartTagToken:
			name, _ := z.TagName()
			if string(name) == "script" || string(name) == "style" {
				skip = true
			}
			b.WriteByte(' ')
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "script" || string(name) == "style" {
				skip = false
			}
			b.WriteByte(' ')
		case html.TextToken:
			if !skip {
				b.Write(z.Text())
			}
		}
	}
	text := strings.Join(strings.Fields(b.String()), " ")
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	runes := []rune(text)
	if len(runes) > 1200 {
		text = string(runes[:1200]) + "…"
	}
	return text
}
func mxUnknown(kind, target, message string) []reputationRow {
	return []reputationRow{{Kind: kind, Target: target, List: "MXToolbox", Status: "unknown", Message: message}}
}
func mxLookup(ctx context.Context, client *http.Client, key, kind, target string) []reputationRow {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, mxLookupURL+"?argument="+url.QueryEscape(target), nil)
	if e != nil {
		return mxUnknown(kind, target, "Не удалось подготовить запрос MXToolbox")
	}
	req.Header.Set("Authorization", key)
	req.Header.Set("Accept", "application/json")
	response, e := client.Do(req)
	if e != nil {
		return mxUnknown(kind, target, "MXToolbox не ответил. Проверьте соединение и повторите позже")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		msg := "MXToolbox временно недоступен"
		switch response.StatusCode {
		case 401:
			msg = "MXToolbox отклонил API-ключ"
		case 402, 403:
			msg = "MXToolbox запретил запрос: проверьте тариф, доступ к Blacklist Lookup и лимиты"
		case 429:
			msg = "Превышен лимит запросов MXToolbox. Повторите позже"
		}
		return mxUnknown(kind, target, msg)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(data) > 1<<20 {
		return mxUnknown(kind, target, "Некорректный ответ MXToolbox")
	}
	var result mxResponse
	if json.Unmarshal(data, &result) != nil || !strings.EqualFold(result.Command, "blacklist") || !strings.EqualFold(strings.TrimSuffix(result.CommandArgument, "."), strings.TrimSuffix(target, ".")) {
		return mxUnknown(kind, target, "Некорректный ответ MXToolbox")
	}
	rows := []reputationRow{}
	// Failed means a failed vendor test, not necessarily a confirmed listing.
	// Preserve the vendor's per-test explanation instead of guessing from prose.
	groups := []struct {
		checks          []mxCheck
		status, message string
	}{
		{result.Failed, "failed", "Проверка не пройдена"},
		{result.Warnings, "unknown", "Предупреждение MXToolbox"},
		{result.Timeouts, "unknown", "Истекло время проверки списка"},
		{result.Passed, "clear", "Проверка пройдена"},
	}
	for _, g := range groups {
		for _, c := range g.checks {
			if len(rows) >= 500 {
				return append(rows, mxUnknown(kind, target, "Ответ MXToolbox слишком большой; показана часть проверок")...)
			}
			name := mxPlain(c.Name, key)
			if name == "" {
				rows = append(rows, mxUnknown(kind, target, "Некорректный ответ MXToolbox")...)
				continue
			}
			rows = append(rows, reputationRow{Kind: kind, Target: target, List: name, Status: g.status, Message: g.message, Details: mxPlain(c.Info, key)})
		}
	}
	if len(rows) == 0 {
		return mxUnknown(kind, target, "MXToolbox не вернул результатов проверки")
	}
	return rows
}

func mxReputation(parent context.Context, v mailDNSView, key string) (reputationResult, error) {
	empty := reputationResult{}
	if key == "" {
		return empty, errf("Сначала добавьте API-ключ в настройках MXToolbox")
	}
	hash := sha256.Sum256([]byte(key))
	cacheKey := hex.EncodeToString(hash[:]) + "|" + v.Domain + "|" + v.Hostname + "|" + v.Options.IPv4
	mxCache.Lock()
	cached, ok := mxCache.data[cacheKey]
	mxCache.Unlock()
	if ok && time.Since(cached.Checked) < mxCacheTTL {
		return cached, nil
	}
	// Checks are explicitly initiated, bounded, cached and single-flight to avoid
	// consuming paid lookup quotas through concurrent clicks.
	select {
	case mxRunGate <- struct{}{}:
		defer func() { <-mxRunGate }()
	default:
		return empty, errf("Проверка MXToolbox уже выполняется. Дождитесь результата")
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	result := reputationResult{Checked: time.Now(), Provider: "mxtoolbox", Rows: []reputationRow{}}
	targets, unknown := mxTargets(ctx, v)
	result.Rows = append(result.Rows, unknown...)
	rows := make([][]reputationRow, len(targets))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 3)
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t mxTarget) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				rows[i] = mxUnknown(t.kind, t.value, "Истекло время проверки MXToolbox")
				return
			}
			rows[i] = mxLookup(ctx, mxHTTP, key, t.kind, t.value)
		}(i, t)
	}
	wg.Wait()
	for _, r := range rows {
		result.Rows = append(result.Rows, r...)
	}
	if parent.Err() != nil {
		return empty, errf("Проверка MXToolbox прервана")
	}
	mxCache.Lock()
	if len(mxCache.data) >= 128 {
		mxCache.data = map[string]reputationResult{}
	}
	mxCache.data[cacheKey] = result
	mxCache.Unlock()
	return result, nil
}

type mxTarget struct{ kind, value string }

func mxTargets(ctx context.Context, v mailDNSView) ([]mxTarget, []reputationRow) {
	unknown := []reputationRow{}
	targets := []mxTarget{{"domain", v.Domain}}
	if v.Hostname != "" && v.Hostname != v.Domain {
		targets = append(targets, mxTarget{"domain", v.Hostname})
	}
	ips := []string{}
	if v.Options.IPv4 != "" {
		ips = append(ips, v.Options.IPv4)
	} else if v.Hostname != "" {
		dnsCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		found, e := net.DefaultResolver.LookupIP(dnsCtx, "ip4", v.Hostname+".")
		stop()
		if e == nil {
			for _, ip := range found {
				if ip.To4() != nil {
					ips = append(ips, ip.String())
				}
			}
		}
	}
	if len(ips) == 0 {
		unknown = append(unknown, mxUnknown("ip", v.Hostname, "IP почтового сервера не определён")...)
	}
	seen := map[string]bool{}
	for _, ip := range ips {
		if seen[ip] {
			continue
		}
		seen[ip] = true
		if len(seen) > 8 {
			unknown = append(unknown, mxUnknown("ip", v.Hostname, "Слишком много IP: проверены первые восемь")...)
			break
		}
		if !mailDNSPublicIPv4(net.ParseIP(ip)) {
			unknown = append(unknown, mxUnknown("ip", ip, "Нужен публичный IPv4")...)
			continue
		}
		targets = append(targets, mxTarget{"ip", ip})
	}

	return targets, unknown
}
