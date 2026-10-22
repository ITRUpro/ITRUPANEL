// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

// ITRUPANEL — панель управления веб-сервером (Debian, Ubuntu, AlmaLinux/Rocky).
// Один исполняемый файл: HTTPS-сервер панели, API и встроенный интерфейс.
package main

import (
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

//go:embed templates
var tplFS embed.FS

var Version = "0.2.8"

type Config struct {
	Listen   string // адрес панели, по умолчанию :3333
	Home     string // каталог панели: etc/, var/, www/
	Root     string // префикс для системных путей ("/" на сервере, папка в режиме demo)
	Dry      bool   // demo: команды не выполняются, только пишутся в журнал
	Insecure bool   // без TLS (для отладки)
}

var cfg Config

func main() {
	if len(os.Args) == 2 && os.Args[1] == "restore-files-worker" {
		restoreFilesWorker()
		return
	}
	if err := os.Setenv("PATH", systemPath(os.Getenv("PATH"))); err != nil {
		log.Fatal(err)
	}
	flag.StringVar(&cfg.Listen, "listen", ":3333", "адрес панели")
	flag.StringVar(&cfg.Home, "home", "/usr/local/itrupanel", "каталог панели")
	demo := flag.Bool("demo", false, "демо-режим: системные команды не выполняются, файлы пишутся во временную папку")
	flag.BoolVar(&cfg.Insecure, "http", false, "без TLS")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ITRUPANEL %s\n\nЗапуск:  itrupanel [флаги]\nКоманды: itrupanel passwd <пароль>   — сменить пароль администратора\n         itrupanel setup [8.3|none]  — настроить сервер (вызывает install.sh)\n         itrupanel repair-pma       — исправить настройку phpMyAdmin\n         itrupanel version\n\n", Version)
		flag.PrintDefaults()
	}
	flag.Parse()

	cfg.Root = "/"
	if *demo {
		cfg.Dry = true
		cfg.Root = filepath.Join(os.TempDir(), "itrupanel-demo-root")
		cfg.Home = filepath.Join(cfg.Root, "usr/local/itrupanel")
		os.MkdirAll(cfg.Home, 0755)
	}

	switch flag.Arg(0) {
	case "version":
		fmt.Println(Version)
		return
	case "prepare-php-repositories", "prepare-apt-repositories":
		if !cfg.Dry && os.Geteuid() != 0 {
			log.Fatal("Требуется root")
		}
		detectOS()
		if err := prepareAPTRepositories(); err != nil {
			log.Fatal(err)
		}
		return
	case "check-update-from":
		cmp, err := compareVersions(Version, flag.Arg(1))
		if err != nil || cmp < 0 {
			log.Fatal("Downgrade is not supported")
		}
		return
	case "health-check":
		if err := checkPanelHealth(); err != nil {
			log.Fatal(err)
		}
		return
	case "update-worker":
		if err := runPanelUpdateWorker(flag.Arg(1)); err != nil {
			log.Fatal(err)
		}
		return
	case "migrate-update", "rollback-update":
		if !cfg.Dry && os.Geteuid() != 0 {
			log.Fatal("Требуется root")
		}
		if flag.Arg(1) == "" {
			log.Fatal("Нужен каталог резервной копии")
		}
		detectOS()
		var err error
		if flag.Arg(0) == "migrate-update" {
			mustLoad()
			err = runUpdateMigrations(flag.Arg(1), panelMigrations())
		} else {
			err = rollbackUpdateMigrations(flag.Arg(1))
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	case "passwd":
		mustLoad()
		if flag.Arg(1) == "" {
			log.Fatal("укажите пароль: itrupanel passwd <пароль> [логин]")
		}
		if l := flag.Arg(2); l != "" {
			if err := setAdminLogin(l); err != nil {
				log.Fatal(err)
			}
		}
		if err := setAdminPassword(flag.Arg(1)); err != nil {
			log.Fatal(err)
		}
		if flag.Arg(2) != "" {
			fmt.Println("Логин и пароль администратора изменены")
		} else {
			fmt.Println("Пароль администратора изменён")
		}
		return
	case "2fa-off":
		mustLoad()
		cliDisable2FA()
		fmt.Println("Двухэтапная аутентификация отключена")
		return
	case "allow-all-ips":
		mustLoad()
		cliAllowAllIPs()
		fmt.Println("Ограничение доступа по IP снято: панель открыта с любого адреса")
		return
	case "login":
		mustLoad()
		if flag.Arg(1) == "" {
			log.Fatal("укажите логин: itrupanel login <логин>")
		}
		if err := setAdminLogin(flag.Arg(1)); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Логин администратора изменён")
		return
	case "setup":
		mustLoad()
		php := flag.Arg(1)
		if php == "" {
			php = "8.3"
		}
		fmt.Println("Настройка сервера:")
		if err := runSetup(php); err != nil {
			log.Fatalf("Настройка не завершена: %v", err)
		}
		return
	case "repair-pma":
		if !cfg.Dry && os.Geteuid() != 0 {
			log.Fatal("Команду нужно выполнять от root")
		}
		mustLoad()
		detectOS()
		if err := installPMA(); err != nil {
			log.Fatalf("Настройка phpMyAdmin: %v", err)
		}
		fmt.Println("phpMyAdmin настроен; сессии и временные файлы хранятся в " + pmaDataDir)
		return
	case "renew":
		mustLoad()
		renewCerts()
		return
	}

	mustLoad()
	detectOS()
	if cfg.Dry {
		if store.Settings.AdminHash == "" {
			setAdminPassword("admin")
		}
		demoSeed()
	}
	if store.Settings.AdminHash == "" {
		l, p := randomLogin(), randToken(8)
		setAdminLogin(l)
		setAdminPassword(p)
		log.Printf("Созданы данные администратора: %s / %s", l, p)
	}

	loadJobs()
	go statsLoop()
	go schedulerLoop()
	go autoSSLLoop()

	mux := http.NewServeMux()
	registerAPI(mux)
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/phpmyadmin/", requirePMAPage(pmaProxy()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/vendor/") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".svg") || strings.HasSuffix(r.URL.Path, ".png") || strings.HasSuffix(r.URL.Path, ".ico") {
			w.Header().Set("Cache-Control", "no-cache")
			static.ServeHTTP(w, r)
			return
		}
		data, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           panelDirectGuard(panelIPGuard(logRequests(languageMiddleware(mux)))),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	log.Printf("ITRUPANEL %s слушает %s (ОС: %s %s)", Version, cfg.Listen, osInfo.ID, osInfo.Version)
	if cfg.Insecure {
		listener, err := net.Listen("tcp", cfg.Listen)
		if err != nil {
			log.Fatal(err)
		}
		if err = startPanelHealth(); err != nil {
			listener.Close()
			log.Fatal(err)
		}
		log.Fatal(srv.Serve(listener))
	}
	crt, key, err := panelCert()
	if err != nil {
		log.Fatal(err)
	}
	fallback, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		log.Fatal(err)
	}
	activePanelCert.Store(&fallback)
	mu.Lock()
	err = syncPanelTLSLocked()
	mu.Unlock()
	if err != nil {
		log.Printf("Сертификат панели не применён, используется резервный: %v", err)
	}
	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return activePanelCert.Load(), nil }}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	if err = startPanelHealth(); err != nil {
		listener.Close()
		log.Fatal(err)
	}
	log.Fatal(srv.ServeTLS(listener, "", ""))
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w, r)
		r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
		h.ServeHTTP(w, r)
	})
}
