// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPanelProxyNginxConf(t *testing.T) {
	c := &Cert{Crt: "/etc/ssl/panel.crt", Key: "/etc/ssl/panel.key"}
	conf := panelNginxConf("panel.example.test", "https://panel.example.test", c, "https://127.0.0.1:3333", "s3cret")
	for _, want := range []string{"listen 443", "server_name panel.example.test;", "ssl_certificate /etc/ssl/panel.crt;", "proxy_pass https://127.0.0.1:3333;", "proxy_set_header X-Itrupanel-Proxy s3cret;", "proxy_set_header X-Real-IP $remote_addr;", "Upgrade $http_upgrade", "client_max_body_size 0;", "return 302 https://panel.example.test$request_uri;", "acme-challenge"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("нет %q в конфигурации:\n%s", want, conf)
		}
	}
	if plain := panelNginxConf("panel.example.test", "https://panel.example.test:3333", nil, "", ""); strings.Contains(plain, "443") || strings.Contains(plain, "proxy_pass") {
		t.Fatalf("без открытия без порта 443 не нужен:\n%s", plain)
	}
}

func TestPanelProxyTrustAndGuard(t *testing.T) {
	panelProxy.Store(&panelProxyInfo{Proxy: true, Closed: true, Domain: "panel.example.test", Secret: "s3cret"})
	defer panelProxy.Store(&panelProxyInfo{})
	req := func(remote, secret, real string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if secret != "" {
			r.Header.Set(panelProxyHeader, secret)
		}
		r.Header.Set("X-Real-IP", real)
		return r
	}
	if ip := clientIP(req("127.0.0.1:5000", "s3cret", "203.0.113.7")); ip != "203.0.113.7" {
		t.Fatalf("адрес от nginx панели: %s", ip)
	}
	if ip := clientIP(req("127.0.0.1:5000", "wrong", "203.0.113.7")); ip != "127.0.0.1" {
		t.Fatalf("поверили чужому X-Real-IP: %s", ip)
	}
	if ip := clientIP(req("198.51.100.9:5000", "s3cret", "203.0.113.7")); ip != "198.51.100.9" {
		t.Fatalf("поверили X-Real-IP не с сервера: %s", ip)
	}
	h := panelDirectGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for remote, want := range map[string]int{"198.51.100.9:5000": 403, "127.0.0.1:5000": 204, "[::1]:5000": 204} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req(remote, "", ""))
		if w.Code != want {
			t.Fatalf("%s: код %d, ожидался %d", remote, w.Code, want)
		}
	}
}

func TestPanelProxyConfigure(t *testing.T) {
	mux := testPanel(t)
	cfg.Listen = ":3333"
	panelCloseDelay = 50 * time.Millisecond
	defer func() { panelCloseDelay = 30 * time.Second }()
	c, k, err := selfSigned([]string{"panel.example.test"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveCert("panel-custom", "custom", c, k, ""); err != nil {
		t.Fatal(err)
	}
	if _, r := testAPI(t, mux, "panel.configure", `{"domain":"panel.example.test","mode":"self","proxy":true}`); r["error"] == nil {
		t.Fatal("открытие без порта с самоподписанным сертификатом")
	}
	if _, r := testAPI(t, mux, "panel.configure", `{"domain":"panel.example.test","mode":"existing","certificate":"panel-custom","close_direct":true}`); r["error"] == nil {
		t.Fatal("порт закрыт без открытия через nginx")
	}
	_, r := testAPI(t, mux, "panel.configure", `{"domain":"panel.example.test","mode":"existing","certificate":"panel-custom","proxy":true,"close_direct":true}`)
	if r["error"] != nil {
		t.Fatal(r["error"])
	}
	if j := waitTestJob(t, r["data"].(map[string]any)["job"].(string)); j.Status != "done" {
		t.Fatalf("%+v", j)
	}
	conf := readFile(vhostDir + "/000-itrupanel-address.conf")
	if !strings.Contains(conf, "proxy_pass http://127.0.0.1:3333;") || !strings.Contains(conf, store.Settings.PanelProxySecret) || store.Settings.PanelProxySecret == "" {
		t.Fatalf("конфигурация nginx (тестовая панель без TLS):\n%s", conf)
	}
	if panelURL("panel.example.test") != "https://panel.example.test" || !store.Settings.PanelProxy || !store.Settings.PanelDirectClosed {
		t.Fatalf("настройки: %s %+v", panelURL("panel.example.test"), store.Settings)
	}
	if panelProxy.Load().Closed {
		t.Fatal("порт закрыт сразу, без паузы для текущей вкладки")
	}
	time.Sleep(200 * time.Millisecond)
	if !panelProxy.Load().Closed {
		t.Fatal("порт не закрылся после паузы")
	}
	// Выключение возвращает адрес с портом и убирает 443
	_, r = testAPI(t, mux, "panel.configure", `{"domain":"panel.example.test","mode":"existing","certificate":"panel-custom"}`)
	waitTestJob(t, r["data"].(map[string]any)["job"].(string))
	conf = readFile(vhostDir + "/000-itrupanel-address.conf")
	if strings.Contains(conf, "443") || panelProxy.Load().Closed || panelURL("panel.example.test") != "https://panel.example.test:3333" {
		t.Fatalf("выключение:\n%s", conf)
	}
}
