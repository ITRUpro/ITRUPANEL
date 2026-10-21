// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Cookies are shared between ports and SameSite includes sibling subdomains.
// Require the actual browser origin, never X-Forwarded-Host supplied by a caller.
// Non-browser clients without Origin retain access through normal authentication.
func browserRequestAllowed(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 || values[0] == "" {
		return false
	}
	u, err := url.Parse(values[0])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return false
	}
	if r.TLS != nil && u.Scheme != "https" {
		return false
	}
	host := func(raw, scheme string) string {
		h := strings.ToLower(raw)
		if scheme == "https" {
			return strings.TrimSuffix(h, ":443")
		}
		return strings.TrimSuffix(h, ":80")
	}
	return host(u.Host, u.Scheme) == host(r.Host, u.Scheme)
}
func checkBrowserRequest(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !browserRequestAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Недопустимый источник запроса"})
		return false
	}
	return true
}
func setSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	// Compatible with the embedded terminal and phpMyAdmin; disallow foreign frames
	// and HTML base-tag URL rewriting without breaking existing inline handlers.
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'; base-uri 'self'; object-src 'none'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
}
func pruneSessionsLocked(now time.Time) {
	for id, s := range sessions {
		if now.After(s.Expires) {
			delete(sessions, id)
		}
	}
}
func trimUserSessionsLocked(user string, keep int) {
	ids := []string{}
	for id, s := range sessions {
		if s.User == user {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return sessions[ids[i]].Expires.Before(sessions[ids[j]].Expires) })
	for len(ids) > keep {
		delete(sessions, ids[0])
		ids = ids[1:]
	}
	pruneSessionsLocked(time.Now())
}

// Unlike getSession, monitoring a WebSocket must not extend its lifetime.
func sessionStillActive(r *http.Request, original *session) bool {
	c, e := r.Cookie(sessCookie)
	if e != nil {
		return false
	}
	sessMu.Lock()
	current := sessions[c.Value]
	valid := current == original && current != nil && time.Now().Before(current.Expires)
	sessMu.Unlock()
	return valid && isAdmin(original.User)
}
