// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Only presentation fields are translated. Configuration, filenames, DNS values,
// editor contents, raw service logs and protocol enums remain byte-for-byte intact.
//
//go:embed i18n/en-server.json
var englishCatalogJSON []byte
var englishCatalog map[string]string
var messagePatterns []messagePattern
var messageFragments []string
var formatToken = regexp.MustCompile(`%[-+ #0]*[0-9]*(?:\.[0-9]+)?[svwdqf]|%%`)

type messagePattern struct {
	re     *regexp.Regexp
	target string
	verbs  []string
}

func init() {
	if err := json.Unmarshal(englishCatalogJSON, &englishCatalog); err != nil {
		panic(err)
	}
	keys := make([]string, 0, len(englishCatalog))
	for k := range englishCatalog {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	for _, k := range keys {
		tokens := formatToken.FindAllStringIndex(k, -1)
		if len(tokens) > 0 {
			var b strings.Builder
			b.WriteString(`(?s)^`)
			at := 0
			var verbs []string
			for _, p := range tokens {
				b.WriteString(regexp.QuoteMeta(k[at:p[0]]))
				t := k[p[0]:p[1]]
				if t == "%%" {
					b.WriteString("%")
				} else {
					b.WriteString(`(.*?)`)
					verbs = append(verbs, t)
				}
				at = p[1]
			}
			b.WriteString(regexp.QuoteMeta(k[at:]))
			b.WriteString("$")
			messagePatterns = append(messagePatterns, messagePattern{regexp.MustCompile(b.String()), englishCatalog[k], verbs})
		} else if len([]rune(strings.TrimSpace(k))) >= 3 && (strings.TrimSpace(k) != k || strings.HasSuffix(k, "=") || strings.HasPrefix(k, ". ") || strings.HasPrefix(k, "; ")) {
			messageFragments = append(messageFragments, k)
		}
	}
}
func englishText(s string) string { return englishTextDepth(s, 0) }
func englishTextDepth(s string, depth int) string {
	if v, ok := englishCatalog[s]; ok {
		return v
	}
	if depth > 4 {
		return s
	}
	for _, p := range messagePatterns {
		m := p.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		n := 0
		return formatToken.ReplaceAllStringFunc(p.target, func(t string) string {
			if t == "%%" {
				return "%"
			}
			n++
			if n >= len(m) {
				return t
			}
			v := m[n]
			verb := p.verbs[n-1]
			if strings.HasSuffix(verb, "w") || strings.HasSuffix(verb, "v") {
				v = englishTextDepth(v, depth+1)
			}
			return v
		})
	}
	for _, k := range messageFragments {
		if strings.HasPrefix(s, k) {
			return englishCatalog[k] + englishTextDepth(s[len(k):], depth+1)
		}
		if strings.HasSuffix(s, k) {
			return englishTextDepth(s[:len(s)-len(k)], depth+1) + englishCatalog[k]
		}
	}
	return s
}
func requestLanguage(r *http.Request) string {
	if c, err := r.Cookie("itrupanel_lang"); err == nil && (c.Value == "en" || c.Value == "ru") {
		return c.Value
	}
	// Browser bootstrap sends its effective preference in a cookie. Header fallback
	// is for API clients and the first request, before that cookie is set.
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if tag == "" {
			continue
		}
		if tag == "ru" || strings.HasPrefix(tag, "ru-") {
			return "ru"
		}
		return "en"
	}
	return "ru"
}

type localizedWriter struct {
	http.ResponseWriter
	route string
}

func languageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestLanguage(r) == "en" && strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/shell" && !strings.HasSuffix(r.URL.Path, "/download") {
			w = &localizedWriter{w, strings.TrimPrefix(r.URL.Path, "/api/")}
		}
		next.ServeHTTP(w, r)
	})
}
func localizeResponse(value any, route string) any {
	b, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var clone any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&clone) != nil {
		return value
	}
	var walk func(any, string) any
	walk = func(v any, key string) any {
		switch x := v.(type) {
		case map[string]any:
			if strings.HasPrefix(route, "updates.") {
				if notes, ok := x["notes_en"].([]any); ok && len(notes) > 0 {
					x["notes"] = notes
				}
			}
			for k, child := range x {
				if k == "name" && route == "stats.disk" && x["kind"] == "sys" {
					if s, ok := child.(string); ok {
						x[k] = englishText(s)
					}
					continue
				}
				x[k] = walk(child, k)
			}
		case []any:
			for i, child := range x {
				x[i] = walk(child, key)
			}
		case string:
			allowed := key == "error" || key == "last_err" || key == "warning" || key == "info" || key == "unsupported" || (route == "backups.list" && key == "restore_issue")
			if strings.HasPrefix(route, "jobs.") || strings.HasPrefix(route, "updates.") {
				allowed = allowed || key == "title" || key == "stage" || key == "notes"
			}
			if strings.HasPrefix(route, "mail.dns.") || route == "mail.reputation" {
				allowed = allowed || key == "message" || key == "blocked"
			}
			if route == "system.info" {
				allowed = allowed || key == "title"
			}
			if route == "import.needs" {
				allowed = allowed || key == "title" || key == "detail"
			}
			if strings.HasPrefix(route, "jobs.") {
				allowed = allowed || key == "detail" || key == "warnings"
			}
			if route == "system.ips" {
				allowed = allowed || key == "scope"
			}
			if strings.HasPrefix(route, "logs.") {
				allowed = allowed || key == "action"
			}
			if route == "notices.list" {
				allowed = allowed || key == "text" || key == "Text" || key == "title"
			}
			if allowed {
				return englishText(x)
			}
		}
		return v
	}
	return walk(clone, "")
}
