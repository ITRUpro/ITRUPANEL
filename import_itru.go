// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Клиент API другой ITRUPANEL (https://хост:3333). Панель только читается: списки сайтов, баз,
// почты, DNS, FTP и cron. Пароль передаётся один раз при входе, дальше работает сессия в cookie.

type itruClient struct {
	base   string
	http   *http.Client // обычные запросы: общий таймаут минута
	stream *http.Client // потоки файлов и дампов: без общего таймаута
	fp     string
}

func itruPanelBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errf("Укажите адрес старой панели ITRUPANEL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errf("Неверный адрес панели: пример https://сервер:3333")
	}
	if u.Port() == "" {
		u.Host += ":3333"
	}
	return u.Scheme + "://" + u.Host, nil
}

func itruLogin(ctx context.Context, cf ImpPanelConf) (*itruClient, error) {
	base, err := itruPanelBase(cf.URL)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	c := &itruClient{base: base}
	// У панели обычно самоподписанный сертификат: проверка отключена, отпечаток запоминается.
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) > 0 {
			c.fp = certFingerprint(raw[0])
			if cf.Fingerprint != "" && c.fp != cf.Fingerprint {
				return errf("Сертификат старой панели изменился с момента проверки: перенос остановлен")
			}
		}
		return nil
	}}}
	c.http = &http.Client{Jar: jar, Timeout: 60 * time.Second, Transport: tr}
	c.stream = &http.Client{Jar: jar, Transport: tr}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	code, err := c.post(ctx, "login", map[string]string{"user": cf.Login, "password": cf.Password}, &res)
	if err != nil {
		return nil, err
	}
	switch {
	case code == 429:
		return nil, errf("Старая панель временно блокирует вход после неудачных попыток: повторите позже")
	case res.Error != "":
		return nil, errf("Старая панель: %s", res.Error)
	case !res.OK:
		return nil, errf("Старая панель не пустила: проверьте адрес, логин и пароль (код %d)", code)
	}
	return c, nil
}

// post вызывает /api/имя с телом JSON и разбирает ответ в out.
func (c *itruClient) post(ctx context.Context, name string, body any, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/api/"+name, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Itrupanel", "1")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, errf("Панель %s не отвечает: %v", c.base, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return res.StatusCode, errf("Ответ не похож на ITRUPANEL (код %d): проверьте адрес и порт, обычно %s", res.StatusCode, "https://сервер:3333")
	}
	return res.StatusCode, nil
}

// call вызывает метод API и разбирает поле data.
func (c *itruClient) call(ctx context.Context, name string, out any) error {
	var res struct {
		OK    bool            `json:"ok"`
		Error string          `json:"error"`
		Data  json.RawMessage `json:"data"`
	}
	code, err := c.post(ctx, name, map[string]string{}, &res)
	if err != nil {
		return err
	}
	if res.Error != "" {
		if res.Error == "terms" || strings.Contains(res.Error, "услов") {
			return errf("На старой панели не приняты условия использования: войдите в неё и примите их")
		}
		return errf("Старая панель (%s): %s", name, res.Error)
	}
	if !res.OK {
		return errf("Старая панель (%s) ответила кодом %d", name, code)
	}
	return json.Unmarshal(res.Data, out)
}

// impFromITRUPANEL дополняет описание сервера данными старой панели: владельцы, корни, версии PHP,
// алиасы, базы, почта, DNS, FTP, cron. Собранное по SSH не теряется: размеры остаются.
func impFromITRUPANEL(ctx context.Context, c *itruClient, inv *ImpInventory) error {
	inv.Panel = "itrupanel"
	var sites []struct {
		Site Site `json:"site"`
	}
	if err := c.call(ctx, "sites.list", &sites); err != nil {
		return err
	}
	idx := map[string]int{}
	for i := range inv.Sites {
		idx[inv.Sites[i].Domain] = i
	}
	for _, r := range sites {
		s := r.Site
		d := strings.ToLower(s.Domain)
		if d == "" {
			continue
		}
		i, ok := idx[d]
		if !ok {
			inv.Sites = append(inv.Sites, ImpSite{Domain: d})
			i = len(inv.Sites) - 1
			idx[d] = i
		}
		x := &inv.Sites[i]
		x.Aliases, x.Owner, x.Root, x.Mode, x.PHP, x.ProxyTo, x.SSL = s.Aliases, s.User, s.Root, s.Mode, s.PHP, s.ProxyTo, s.SSL != ""
		if x.Mode == "" {
			x.Mode = "php"
		}
		if s.Backend != "" {
			inv.Warnings = append(inv.Warnings, fmt.Sprintf("Сайт %s: на старом сервере работает через %s; на этом сервере он будет создан на nginx", d, s.Backend))
		}
	}
	var dbs struct {
		Rows []struct {
			DB   DB    `json:"db"`
			Size int64 `json:"size"`
		} `json:"rows"`
	}
	if err := c.call(ctx, "db.list", &dbs); err == nil {
		byName := map[string]int{}
		for i := range inv.DBs {
			byName[inv.DBs[i].Name] = i
		}
		for _, r := range dbs.Rows {
			if r.DB.Server != "" {
				inv.Warnings = append(inv.Warnings, fmt.Sprintf("База %s хранится на отдельном сервере баз данных и не переносится", r.DB.Name))
				continue
			}
			i, ok := byName[r.DB.Name]
			if !ok {
				inv.DBs = append(inv.DBs, ImpDB{Name: r.DB.Name})
				i = len(inv.DBs) - 1
				byName[r.DB.Name] = i
			}
			if r.Size > 0 {
				inv.DBs[i].Size = r.Size
			}
			if r.DB.User != "" && len(inv.DBs[i].Users) == 0 {
				inv.DBs[i].Users = []string{r.DB.User}
			}
		}
	}
	if len(inv.Cron) == 0 {
		var cr struct {
			Rows []CronJob `json:"rows"`
		}
		if err := c.call(ctx, "cron.list", &cr); err == nil {
			for _, j := range cr.Rows {
				if j.Enabled && j.Command != "" {
					inv.Cron = append(inv.Cron, ImpCron{User: j.User, Schedule: j.Schedule, Command: j.Command})
				}
			}
		}
	}
	var mail struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
		Boxes []struct {
			Address string `json:"address"`
		} `json:"boxes"`
	}
	if len(inv.MailDomains) == 0 && c.call(ctx, "mail.list", &mail) == nil {
		boxes := map[string]int{}
		for _, d := range mail.Domains {
			boxes[strings.ToLower(d.Name)] += 0
		}
		for _, b := range mail.Boxes {
			if i := strings.LastIndex(b.Address, "@"); i > 0 {
				boxes[strings.ToLower(b.Address[i+1:])]++
			}
		}
		names := make([]string, 0, len(boxes))
		for d := range boxes {
			names = append(names, d)
		}
		sort.Strings(names)
		for _, d := range names {
			inv.MailDomains = append(inv.MailDomains, ImpMailDomain{Name: d, Boxes: boxes[d]})
		}
	}
	if len(inv.DNS) == 0 {
		var z struct {
			Rows []struct {
				Name string `json:"name"`
			} `json:"rows"`
		}
		if c.call(ctx, "dns.list", &z) == nil {
			for _, r := range z.Rows {
				inv.DNS = append(inv.DNS, ImpDNSZone{Name: r.Name})
			}
		}
	}
	// Версия и система старой панели; сведения для переноса (размеры, базы, PHP) — только у панелей,
	// где есть выгрузка (0.1.94 и новее).
	var me struct {
		Version string `json:"version"`
		OS      string `json:"os"`
	}
	_ = c.call(ctx, "me", &me)
	inv.OS = me.OS
	var info struct {
		OS          string          `json:"os"`
		Arch        string          `json:"arch"`
		PHPVersions []string        `json:"php_versions"`
		Sites       []impExportSite `json:"sites"`
		Backup      impExportBackup `json:"backup"`
	}
	if err := c.call(ctx, "transfer.info", &info); err != nil {
		inv.CanTransfer = false
		ver := me.Version
		if ver == "" {
			ver = "неизвестна"
		}
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("Старая панель (версия %s) не умеет отдавать файлы и базы для переноса: обновите её до версии %s. Сейчас можно только посмотреть, что на ней есть.", ver, Version))
	} else {
		inv.CanTransfer = true
		inv.OS = strings.TrimSpace(info.OS + " (" + info.Arch + ")")
		inv.PHPVersions = info.PHPVersions
		if info.Backup.Target != "" {
			inv.Backup = &ImpBackup{Target: info.Backup.Target, Host: info.Backup.FTP.Host, Dir: info.Backup.FTP.Dir, Daily: info.Backup.Daily, Keep: info.Backup.Keep, Hour: info.Backup.Hour}
			if backupTargetIsS3(info.Backup.Target) {
				inv.Backup.Host = strings.TrimPrefix(strings.TrimPrefix(info.Backup.S3.Endpoint, "https://"), "http://")
				inv.Backup.Dir = strings.Trim(info.Backup.S3.Bucket+"/"+info.Backup.S3.Prefix, "/")
			}
		}
		for _, es := range info.Sites {
			if i, ok := idx[strings.ToLower(es.Domain)]; ok {
				inv.Sites[i].Size = es.Size
			}
		}
	}
	inv.PanelFP = c.fp
	if len(inv.FTP) == 0 {
		var f struct {
			Rows []FTPUser `json:"rows"`
		}
		if c.call(ctx, "ftp.list", &f) == nil {
			for _, r := range f.Rows {
				inv.FTP = append(inv.FTP, ImpFTP{Name: r.Name, Owner: r.Owner, Home: r.Home})
			}
		}
	}
	return nil
}

// impPanelSource — источник, который отдаёт файлы и базы через API старой панели, а не по SSH.
type impPanelSource interface {
	DBCharset(name string) string
	DBHash(user string) string
	Tree(ctx context.Context, root string, w io.Writer) error
	Dump(ctx context.Context, name, charset string, w io.Writer) error
}

type impPanel struct {
	c       *itruClient
	domains map[string]string // корень сайта на старой панели → домен
	dbs     map[string]impExportDB
	users   map[string]impExportDB
	ip      string // публичный IP старого сервера
	dns     []impExportZone
	ftp     []impExportFTP
	mail    impExportMail
	backup  impExportBackup
}

func (p *impPanel) Run(context.Context, string) (string, error) {
	return "", errf("Команды на старой панели не выполняются")
}
func (p *impPanel) Stream(context.Context, string, io.Writer) error {
	return errf("Команды на старой панели не выполняются")
}
func (p *impPanel) Close() error { return nil }

func (p *impPanel) DBCharset(name string) string {
	if d, ok := p.dbs[name]; ok && d.Charset != "" {
		return d.Charset
	}
	return "utf8mb4"
}
func (p *impPanel) DBHash(user string) string { return p.users[user].Hash }

func (c *itruClient) download(ctx context.Context, path string, body any, w io.Writer) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Itrupanel", "1")
	res, err := c.stream.Do(req)
	if err != nil {
		return errf("Старая панель не отвечает: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return errf("Старая панель отказала (код %d): %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if _, err := io.Copy(w, res.Body); err != nil {
		return errf("Передача со старой панели прервалась: %v", err)
	}
	return nil
}

func (p *impPanel) Tree(ctx context.Context, root string, w io.Writer) error {
	d := p.domains[root]
	if d == "" {
		return errf("Сайт с корнем %s не найден на старой панели", root)
	}
	return p.c.download(ctx, "/api/transfer/tree", map[string]string{"domain": d}, w)
}

func (p *impPanel) Dump(ctx context.Context, name, charset string, w io.Writer) error {
	return p.c.download(ctx, "/api/transfer/dump", map[string]string{"name": name, "charset": charset}, w)
}

// impOpenPanel входит в старую панель и готовит источник для переноса.
func impOpenPanel(ctx context.Context, sess *impSession) (*impPanel, error) {
	cf := sess.Panel
	cf.Fingerprint = sess.Inv.PanelFP
	c, err := itruLogin(ctx, cf)
	if err != nil {
		return nil, err
	}
	p := &impPanel{c: c, domains: map[string]string{}, dbs: map[string]impExportDB{}, users: map[string]impExportDB{}}
	for _, s := range sess.Inv.Sites {
		p.domains[s.Root] = s.Domain
	}
	var info struct {
		DBs    []impExportDB   `json:"dbs"`
		IP     string          `json:"ip"`
		DNS    []impExportZone `json:"dns"`
		FTP    []impExportFTP  `json:"ftp"`
		Mail   impExportMail   `json:"mail"`
		Backup impExportBackup `json:"backup"`
	}
	if err := c.call(ctx, "transfer.info", &info); err != nil {
		return nil, err
	}
	p.ip, p.dns, p.ftp, p.mail, p.backup = info.IP, info.DNS, info.FTP, info.Mail, info.Backup
	for _, d := range info.DBs {
		p.dbs[d.Name], p.users[d.User] = d, d
	}
	return p, nil
}
