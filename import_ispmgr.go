// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Клиент API ISPmanager 5/6 (https://хост:1500/ispmgr). Формат ответов — XML, он описан в
// документации; пароль передаётся один раз для получения сессии (func=auth), дальше — её id.
// Панель только читается: используются списки сайтов, баз, почты, DNS и заданий cron.

type ispmgrClient struct {
	base string
	auth string
	http *http.Client
	fp   string
}

type ImpPanelConf struct {
	URL         string `json:"url"`
	Login       string `json:"login"`
	Password    string `json:"password"`
	Fingerprint string `json:"fingerprint"` // SHA256 сертификата: если задан, должен совпасть
}

func impPanelBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errf("Укажите адрес панели ISPmanager")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errf("Неверный адрес панели: пример https://сервер:1500")
	}
	if u.Port() == "" {
		u.Host += ":1500"
	}
	return u.Scheme + "://" + u.Host, nil
}

func ispmgrLogin(ctx context.Context, cf ImpPanelConf) (*ispmgrClient, error) {
	base, err := impPanelBase(cf.URL)
	if err != nil {
		return nil, err
	}
	c := &ispmgrClient{base: base}
	// У панели обычно самоподписанный сертификат: проверка отключена, отпечаток запоминается и
	// показывается пользователю.
	c.http = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) > 0 {
			c.fp = certFingerprint(raw[0])
		}
		return nil
	}}}}
	doc, err := c.call(ctx, url.Values{"func": {"auth"}, "username": {cf.Login}, "password": {cf.Password}}, false)
	if err != nil {
		return nil, err
	}
	if doc.Auth.ID == "" {
		return nil, errf("Панель не вернула сессию: проверьте логин и пароль")
	}
	c.auth = doc.Auth.ID
	return c, nil
}

func certFingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}

type ispmgrDoc struct {
	XMLName xml.Name `xml:"doc"`
	Error   *struct {
		Text string `xml:",chardata"`
		Msg  string `xml:"msg"`
	} `xml:"error"`
	Auth struct {
		ID string `xml:"id,attr"`
	} `xml:"auth"`
	Elems []ispmgrElem `xml:"elem"`
}

// ispmgrElem — строка списка: дочерние элементы <имя>текст</имя> превращаются в карту.
type ispmgrElem map[string]string

func (e *ispmgrElem) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	*e = ispmgrElem{}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var text struct {
				V string `xml:",chardata"`
			}
			if err := d.DecodeElement(&text, &t); err != nil {
				return err
			}
			(*e)[t.Name.Local] = strings.TrimSpace(text.V)
		case xml.EndElement:
			return nil
		}
	}
}

func (c *ispmgrClient) call(ctx context.Context, q url.Values, withAuth bool) (*ispmgrDoc, error) {
	q.Set("out", "xml")
	if withAuth {
		q.Set("auth", c.auth)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/ispmgr", strings.NewReader(q.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errf("Панель %s не отвечает: %v", c.base, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	var doc ispmgrDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, errf("Панель вернула не XML (код %d): это не ISPmanager или другой адрес", res.StatusCode)
	}
	if doc.Error != nil {
		msg := strings.TrimSpace(doc.Error.Msg + " " + doc.Error.Text)
		if msg == "" {
			msg = "ошибка панели"
		}
		return nil, errf("Панель ISPmanager: %s", msg)
	}
	return &doc, nil
}

// list возвращает строки списка функции; недоступная функция (нет модуля) — не ошибка.
func (c *ispmgrClient) list(ctx context.Context, fn string, extra ...string) []ispmgrElem {
	q := url.Values{"func": {fn}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	doc, err := c.call(ctx, q, true)
	if err != nil {
		return nil
	}
	return doc.Elems
}

func ispmgrPHP(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if m := rePHPVer.FindStringSubmatch("php" + strings.ReplaceAll(v, "native", "")); m != nil && v != "" {
		return m[1] + "." + m[2]
	}
	return ""
}

// impFromISPmanager дополняет описание старого сервера сведениями из панели: версии PHP сайтов,
// владельцев, почтовые ящики, DNS-зоны, задания cron, FTP. Данные панели точнее разбора конфигов.
func impFromISPmanager(ctx context.Context, c *ispmgrClient, inv *ImpInventory) {
	inv.Panel = "ispmanager"
	known := map[string]*ImpSite{}
	for i := range inv.Sites {
		known[inv.Sites[i].Domain] = &inv.Sites[i]
	}
	for _, e := range c.list(ctx, "webdomain") {
		name := strings.ToLower(e["name"])
		if name == "" {
			continue
		}
		s := known[name]
		if s == nil {
			inv.Sites = append(inv.Sites, ImpSite{Domain: name, Mode: "php"})
			s = &inv.Sites[len(inv.Sites)-1]
			known = map[string]*ImpSite{}
			for i := range inv.Sites {
				known[inv.Sites[i].Domain] = &inv.Sites[i]
			}
		}
		if e["owner"] != "" {
			s.Owner = e["owner"]
		}
		if e["docroot"] != "" {
			s.Root = e["docroot"]
		}
		if v := ispmgrPHP(e["php_version"]); v != "" {
			s.PHP = v
		}
		if e["php_mode"] == "php_mode_off" || e["php_mode"] == "off" {
			s.Mode, s.PHP = "static", ""
		}
	}
	if len(inv.MailDomains) == 0 {
		boxes := map[string]int{}
		for _, e := range c.list(ctx, "emaildomain") {
			if n := strings.ToLower(e["name"]); n != "" {
				boxes[n] += 0
			}
		}
		for _, e := range c.list(ctx, "email") {
			if n := strings.ToLower(e["name"]); strings.Contains(n, "@") {
				boxes[n[strings.LastIndex(n, "@")+1:]]++
			}
		}
		for d, n := range boxes {
			inv.MailDomains = append(inv.MailDomains, ImpMailDomain{Name: d, Boxes: n})
		}
	}
	if len(inv.DNS) == 0 {
		for _, e := range c.list(ctx, "domain") {
			if n := strings.ToLower(firstOf(e["name"], e["displayname"])); n != "" {
				inv.DNS = append(inv.DNS, ImpDNSZone{Name: n})
			}
		}
	}
	if len(inv.DBs) == 0 && inv.DBAccessError == "" {
		for _, e := range c.list(ctx, "db") {
			if n := e["name"]; n != "" {
				inv.DBs = append(inv.DBs, ImpDB{Name: n, Users: []string{strings.SplitN(e["pair"], "->", 2)[0]}})
			}
		}
	}
	if len(inv.Cron) == 0 {
		for _, e := range c.list(ctx, "scheduler") {
			if e["command"] != "" && e["interval"] != "" {
				inv.Cron = append(inv.Cron, ImpCron{User: e["owner"], Schedule: e["interval"], Command: e["command"]})
			}
		}
	}
}

func firstOf(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
