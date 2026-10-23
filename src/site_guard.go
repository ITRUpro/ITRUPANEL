// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Защита сайта галочками вместо ручных правил nginx: заголовки безопасности, защита картинок от копирования
// (hotlink), пароль на сайт и доступ к админке только с выбранных IP-адресов.

type SiteGuard struct {
	Headers  bool     `json:"headers,omitempty"`
	Hotlink  bool     `json:"hotlink,omitempty"`
	AuthUser string   `json:"auth_user,omitempty"`
	AuthHash string   `json:"auth_hash,omitempty"`
	AdminIPs []string `json:"admin_ips,omitempty"`
	AuthSet  bool     `json:"auth_set,omitempty"` // только в ответах интерфейсу: пароль задан (сам хеш наружу не отдаётся)
}

const siteAuthDir = "/etc/nginx/itrupanel/auth"

var reAuthUser = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

func siteAuthFile(domain string) string { return siteAuthDir + "/" + domain + ".htpasswd" }

func guardZone(domain string) string {
	return fmt.Sprintf("itru_adm_%x", sha256.Sum256([]byte(domain)))[:21]
}

// parseGuardIPs разбирает список адресов и сетей через пробел, запятую или перенос строки.
func parseGuardIPs(text string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ';' }) {
		v := f
		if strings.Contains(f, "/") {
			_, n, err := net.ParseCIDR(f)
			if err != nil {
				return nil, errf("Неверная сеть: %s", f)
			}
			v = n.String()
		} else if ip := net.ParseIP(f); ip != nil {
			v = ip.String()
		} else {
			return nil, errf("Неверный IP-адрес: %s", f)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > 50 {
		return nil, errf("Не больше 50 адресов и сетей")
	}
	return out, nil
}

// guardFromInput собирает настройки защиты из формы; текущие (prev) нужны, чтобы оставить пароль, если новый не задан.
func guardFromInput(in siteIn, prev SiteGuard) (SiteGuard, error) {
	g := SiteGuard{Headers: in.GuardHeaders, Hotlink: in.GuardHotlink}
	ips, err := parseGuardIPs(in.GuardAdminIPs)
	if err != nil {
		return g, err
	}
	g.AdminIPs = ips
	user := strings.TrimSpace(in.AuthUser)
	if user != "" {
		if !reAuthUser.MatchString(user) {
			return g, errf("Логин для пароля на сайт: латинские буквы, цифры, точка, дефис, подчёркивание (до 32 знаков)")
		}
		g.AuthUser = user
		switch {
		case in.AuthPassword != "":
			if len(in.AuthPassword) < 8 || len(in.AuthPassword) > 72 {
				return g, errf("Пароль на сайт: от 8 до 72 знаков")
			}
			h, err := bcrypt.GenerateFromPassword([]byte(in.AuthPassword), 10)
			if err != nil {
				return g, err
			}
			g.AuthHash = strings.Replace(string(h), "$2a$", "$2y$", 1)
		case prev.AuthHash != "":
			g.AuthHash = prev.AuthHash
		default:
			return g, errf("Укажите пароль для доступа к сайту")
		}
	}
	return g, nil
}

// siteForUI — копия сайта для ответа интерфейсу: хеш пароля сайта не передаётся, вместо него признак «пароль задан».
func siteForUI(s *Site) *Site {
	c := *s
	c.Guard.AuthSet = c.Guard.AuthHash != ""
	c.Guard.AuthHash = ""
	return &c
}

// guardHTTP — строка для области http: список адресов, которым разрешена админка.
func guardHTTP(s *Site) string {
	if len(s.Guard.AdminIPs) == 0 || s.Suspended {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "geo $%s {\n    default 0;\n    127.0.0.1 1;\n    ::1 1;\n", guardZone(s.Domain))
	for _, ip := range s.Guard.AdminIPs {
		fmt.Fprintf(&b, "    %s 1;\n", ip)
	}
	b.WriteString("}\n")
	return b.String()
}

// guardServer — правила внутри server {}: заголовки, hotlink, доступ к админке. Пароль добавляется отдельно (auth_basic).
func guardServer(s *Site) string {
	g := s.Guard
	if s.Suspended {
		return ""
	}
	var b strings.Builder
	if g.Headers {
		b.WriteString("\n    # Защита сайта: заголовки безопасности.\n")
		b.WriteString("    add_header X-Content-Type-Options \"nosniff\" always;\n")
		b.WriteString("    add_header X-Frame-Options \"SAMEORIGIN\" always;\n")
		b.WriteString("    add_header Referrer-Policy \"strict-origin-when-cross-origin\" always;\n")
		if s.SSL != "" && s.RedirectHTTPS {
			b.WriteString("    add_header Strict-Transport-Security \"max-age=31536000\" always;\n")
		}
	}
	if g.Hotlink {
		b.WriteString("\n    # Защита сайта: картинки и видео не показываются на чужих сайтах (поисковики и соцсети разрешены).\n")
		b.WriteString("    set $itru_hot \"\";\n")
		b.WriteString("    if ($uri ~* \"\\.(jpe?g|png|gif|webp|avif|svg|ico|mp4|webm|mp3)$\") { set $itru_hot \"I\"; }\n")
		b.WriteString("    valid_referers none blocked server_names ~(^|\\.)(google|yandex|bing|yahoo|duckduckgo)\\. ~(^|\\.)(mail\\.ru|vk\\.com|facebook\\.com|t\\.me|telegram\\.org);\n")
		b.WriteString("    if ($invalid_referer) { set $itru_hot \"${itru_hot}R\"; }\n")
		b.WriteString("    if ($itru_hot = \"IR\") { return 403; }\n")
	}
	if len(g.AdminIPs) > 0 {
		z := guardZone(s.Domain)
		b.WriteString("\n    # Защита сайта: админка только с выбранных адресов.\n")
		b.WriteString("    set $itru_adm \"\";\n")
		b.WriteString("    if ($uri ~* \"^/(wp-admin|wp-login\\.php|administrator|bitrix/admin|admin|manager|phpmyadmin|pma)(/|$)\") { set $itru_adm \"A\"; }\n")
		b.WriteString("    if ($uri ~* \"admin-ajax\\.php$\") { set $itru_adm \"\"; }\n")
		fmt.Fprintf(&b, "    if ($%s = 0) { set $itru_adm \"${itru_adm}N\"; }\n", z)
		b.WriteString("    if ($itru_adm = \"AN\") { return 403; }\n")
	}
	if g.AuthHash != "" {
		b.WriteString("\n    # Защита сайта: пароль на весь сайт (проверка Let’s Encrypt исключена).\n")
		b.WriteString("    auth_basic \"Доступ ограничен\";\n")
		fmt.Fprintf(&b, "    auth_basic_user_file %s;\n", siteAuthFile(s.Domain))
	}
	return b.String()
}

// writeSiteAuth записывает файл паролей сайта (читает рабочий процесс nginx) или удаляет его.
func writeSiteAuth(s *Site) error {
	path := siteAuthFile(s.Domain)
	if s.Guard.AuthHash == "" || s.Guard.AuthUser == "" {
		os.Remove(P(path))
		return nil
	}
	if err := os.MkdirAll(P(siteAuthDir), 0750); err != nil {
		return err
	}
	if err := writeFile(path, []byte(s.Guard.AuthUser+":"+s.Guard.AuthHash+"\n"), 0640); err != nil {
		return err
	}
	if !cfg.Dry {
		run("chown", "root:"+osInfo.WebUser, P(siteAuthDir), P(path))
		run("chmod", "750", P(siteAuthDir))
	}
	return nil
}
