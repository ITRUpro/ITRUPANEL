// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Имена, ведущие на другой сервер, исключаются; если сервер не видит ни одно имя — решает Let’s Encrypt.
func TestACMEReachable(t *testing.T) {
	testPanel(t)
	oldDry, oldClient := cfg.Dry, acmeProbeClient
	defer func() { cfg.Dry, acmeProbeClient = oldDry, oldClient }()
	cfg.Dry = false
	here := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(P(acmeRoot + r.URL.Path))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer here.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://closed.test"+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer other.Close()
	addr := map[string]string{"site.test:80": here.Listener.Addr().String(), "www.site.test:80": here.Listener.Addr().String(), "old.test:80": other.Listener.Addr().String()}
	acmeProbeClient = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
		if to, ok := addr[a]; ok {
			return (&net.Dialer{}).DialContext(ctx, network, to)
		}
		return nil, &net.OpError{Op: "dial", Err: os.ErrNotExist}
	}}}
	ok, bad := acmeReachable([]string{"site.test", "www.site.test", "old.test"})
	if strings.Join(ok, ",") != "site.test,www.site.test" || strings.Join(bad, ",") != "old.test" {
		t.Fatalf("ok=%v bad=%v", ok, bad)
	}
	ok, bad = acmeReachable([]string{"old.test", "closed.test"})
	if len(ok) != 2 || bad != nil {
		t.Fatalf("unreachable server must leave the decision to Let's Encrypt: ok=%v bad=%v", ok, bad)
	}
	if entries, _ := os.ReadDir(P(acmeRoot + "/.well-known/acme-challenge")); len(entries) != 0 {
		t.Fatalf("probe files left behind: %v", entries)
	}
}
