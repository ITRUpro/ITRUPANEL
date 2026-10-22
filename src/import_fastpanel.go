// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// Импорт из FASTPANEL по SSH. Данные о сайтах, базах, FTP и почте берутся из консольной программы FASTPANEL
// (fastpanel … --json); если её нет или она не ответила, остаётся общий просмотр сервера по SSH (настройки
// nginx и MySQL). Файлы сайтов и писем копируются через tar, базы — через mysqldump, хеши паролей FTP
// и почты читаются из файлов ProFTPD и Dovecot. На старом сервере ничего не изменяется.

const (
	fpBin       = "/usr/local/fastpanel2/fastpanel"
	fpFTPPasswd = "/etc/proftpd/ftpd.passwd"
	fpMailPass  = "/etc/dovecot/dovecot.passwd"
)

type fpOwner struct {
	Username string `json:"username"`
}

type fpSite struct {
	Domain  string `json:"domain"`
	Enabled bool   `json:"enabled"`
	Aliases []struct {
		Name string `json:"name"`
	} `json:"aliases"`
	IndexDir    string  `json:"index_dir"`
	Certificate any     `json:"certificate"`
	Owner       fpOwner `json:"owner"`
	Backend     struct {
		Type           string `json:"type"`
		Handler        string `json:"handler"`
		HandlerVersion string `json:"handler_version"`
		ListenAddr     string `json:"listen_addr"`
		Port           int    `json:"port"`
	} `json:"main_backend"`
}

type fpDB struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type fpFTP struct {
	Name    string  `json:"name"`
	HomeDir string  `json:"home_dir"`
	Enabled bool    `json:"enabled"`
	Owner   fpOwner `json:"owner"`
}

type fpMailDomain struct {
	Name    string  `json:"name"`
	Enabled bool    `json:"enabled"`
	Owner   fpOwner `json:"owner"`
}

type fpMailBox struct {
	Login    string          `json:"login"`
	Address  string          `json:"address"`
	Enabled  bool            `json:"enabled"`
	Quota    int64           `json:"quota"`
	Aliases  json.RawMessage `json:"aliases"`
	Redirect json.RawMessage `json:"redirects"`
}

// fpPHP приводит версию из FASTPANEL («81») к виду «8.1».
func fpPHP(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, ".") {
		return phpVersionFrom("php" + v)
	}
	if len(v) == 2 && v[0] >= '5' && v[0] <= '9' {
		return v[:1] + "." + v[1:]
	}
	return ""
}

var reFPUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// fpJSON запускает консольную программу FASTPANEL и разбирает её JSON-ответ.
func fpJSON(ctx context.Context, r impRunner, out any, args ...string) error {
	text, err := r.Run(ctx, fpBin+" "+strings.Join(args, " ")+" --json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), out); err != nil {
		return errf("непонятный ответ FASTPANEL: %s", firstLine(text))
	}
	return nil
}

func fpAvailable(ctx context.Context, r impRunner) bool {
	out, err := r.Run(ctx, `[ -x `+fpBin+` ] && echo yes`)
	return err == nil && strings.TrimSpace(out) == "yes"
}

// impFastpanelEnrich дополняет результат общего просмотра данными самой панели.
func impFastpanelEnrich(ctx context.Context, r impRunner, inv *ImpInventory) {
	inv.Panel = "fastpanel"
	if !fpAvailable(ctx, r) {
		inv.Warnings = append(inv.Warnings, "Консольная программа FASTPANEL не найдена: сайты и базы определены по настройкам nginx и MySQL; FTP-пользователи и почта не определены")
		return
	}
	var sites []fpSite
	if err := fpJSON(ctx, r, &sites, "sites", "list"); err != nil {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("Список сайтов FASTPANEL не прочитан (%s): сайты определены по настройкам nginx", firstLine(err.Error())))
	} else {
		var list []ImpSite
		for _, s := range sites {
			d := strings.ToLower(strings.TrimSpace(s.Domain))
			if !reDomain.MatchString(d) || !strings.HasPrefix(s.IndexDir, "/var/www/") || strings.Contains(s.IndexDir, "..") {
				continue
			}
			it := ImpSite{Domain: d, Owner: s.Owner.Username, Root: s.IndexDir, Mode: "php", PHP: fpPHP(s.Backend.HandlerVersion), SSL: s.Certificate != nil}
			for _, a := range s.Aliases {
				if n := strings.ToLower(strings.TrimSpace(a.Name)); reDomain.MatchString(n) && n != d {
					it.Aliases = append(it.Aliases, n)
				}
			}
			if t := s.Backend.Type; t != "" && t != "php" {
				it.Mode = "static"
				inv.Warnings = append(inv.Warnings, fmt.Sprintf("%s: приложение типа «%s» не переносится как приложение, будут скопированы только файлы", d, t))
			}
			if !s.Enabled {
				inv.Warnings = append(inv.Warnings, fmt.Sprintf("%s: на FASTPANEL сайт отключён", d))
			}
			list = append(list, it)
		}
		if len(list) > 0 {
			// Размеры берём из общего просмотра, если он их нашёл.
			size := map[string]int64{}
			for _, o := range inv.Sites {
				size[o.Root] = o.Size
			}
			for i := range list {
				list[i].Size = size[list[i].Root]
			}
			if len(list) > 0 && list[0].Size == 0 {
				fpSizes(ctx, r, list)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Domain < list[j].Domain })
			inv.Sites = list
		}
	}
	// Базы: только созданные в FASTPANEL (служебные базы phpMyAdmin и Roundcube не нужны).
	var dbs []fpDB
	if err := fpJSON(ctx, r, &dbs, "databases", "list"); err == nil {
		keep := map[string]bool{}
		for _, d := range dbs {
			keep[d.Name] = true
		}
		var list []ImpDB
		for _, d := range inv.DBs {
			if keep[d.Name] {
				list = append(list, d)
			}
		}
		inv.DBs = list
	} else {
		var list []ImpDB
		for _, d := range inv.DBs {
			if !strings.HasPrefix(d.Name, "fp2") {
				list = append(list, d)
			}
		}
		inv.DBs = list
	}
	// FTP-пользователи.
	var ftp []fpFTP
	if err := fpJSON(ctx, r, &ftp, "ftp_account", "list"); err == nil {
		inv.FTP = nil
		for _, f := range ftp {
			if reFTPName.MatchString(f.Name) && strings.HasPrefix(f.HomeDir, "/var/www/") {
				inv.FTP = append(inv.FTP, ImpFTP{Name: f.Name, Owner: f.Owner.Username, Home: f.HomeDir})
			}
		}
	}
	// Почта.
	var md []fpMailDomain
	if err := fpJSON(ctx, r, &md, "emails", "domains", "list"); err == nil {
		inv.MailDomains = nil
		for _, d := range md {
			name := strings.ToLower(d.Name)
			if !reDomain.MatchString(name) {
				continue
			}
			n := 0
			var boxes []fpMailBox
			if fpJSON(ctx, r, &boxes, "emails", "boxes", "list", "--domain="+shQuote(name)) == nil {
				n = len(boxes)
			}
			inv.MailDomains = append(inv.MailDomains, ImpMailDomain{Name: name, Boxes: n})
		}
		inv.Services["exim4"], inv.Services["dovecot"] = true, true
	}
	inv.DNS = nil // DNS в FASTPANEL ведётся у внешних провайдеров, переносить нечего
}

func fpSizes(ctx context.Context, r impRunner, list []ImpSite) {
	var roots []string
	for _, s := range list {
		roots = append(roots, shQuote(s.Root))
	}
	out, err := r.Run(ctx, `for d in `+strings.Join(roots, " ")+`; do printf '%s\t%s\n' "$d" "$(timeout 120 du -sb "$d" 2>/dev/null | cut -f1)"; done`)
	if err != nil {
		return
	}
	sz := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, "\t"); len(f) == 2 {
			var n int64
			for _, c := range f[1] {
				if c >= '0' && c <= '9' {
					n = n*10 + int64(c-'0')
				}
			}
			sz[f[0]] = n
		}
	}
	for i := range list {
		list[i].Size = sz[list[i].Root]
	}
}

// ---------- чтение паролей и почтовых ящиков для переноса ----------

type fpMailSource struct {
	export impExportMail
	homes  map[string]string // адрес ящика → каталог Maildir на старом сервере
	notes  []string
}

// fpPasswdFields читает файл вида «имя:хеш:…», возвращая для каждого имени все поля.
func fpPasswdFields(text string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) > 2 && f[0] != "" && f[1] != "" {
			out[f[0]] = f
		}
	}
	return out
}

// fpLoadFTP возвращает FTP-пользователей FASTPANEL вместе с хешами паролей.
func fpLoadFTP(ctx context.Context, r impRunner) ([]impExportFTP, error) {
	var ftp []fpFTP
	if err := fpJSON(ctx, r, &ftp, "ftp_account", "list"); err != nil {
		return nil, err
	}
	text, _ := r.Run(ctx, `cat `+fpFTPPasswd+` 2>/dev/null`)
	hashes := fpPasswdFields(text)
	var out []impExportFTP
	for _, f := range ftp {
		if !reFTPName.MatchString(f.Name) || !strings.HasPrefix(f.HomeDir, "/var/www/") || strings.Contains(f.HomeDir, "..") {
			continue
		}
		e := impExportFTP{Name: f.Name, Owner: f.Owner.Username, Home: f.HomeDir}
		if h := hashes[f.Name]; len(h) > 1 {
			e.Hash = h[1]
		}
		out = append(out, e)
	}
	return out, nil
}

// fpLoadMail возвращает почтовые домены и ящики FASTPANEL с хешами паролей и путями к письмам.
func fpLoadMail(ctx context.Context, r impRunner) (*fpMailSource, error) {
	var domains []fpMailDomain
	if err := fpJSON(ctx, r, &domains, "emails", "domains", "list"); err != nil {
		return nil, err
	}
	text, _ := r.Run(ctx, `cat `+fpMailPass+` 2>/dev/null`)
	pw := fpPasswdFields(text)
	src := &fpMailSource{homes: map[string]string{}}
	src.export.Ready = true
	for _, d := range domains {
		name := strings.ToLower(d.Name)
		if !reDomain.MatchString(name) {
			continue
		}
		src.export.Domains = append(src.export.Domains, impExportMailDomain{Name: name, Hostname: "mail." + name, Webmail: true, Spam: true})
		var boxes []fpMailBox
		if err := fpJSON(ctx, r, &boxes, "emails", "boxes", "list", "--domain="+shQuote(name)); err != nil {
			src.notes = append(src.notes, fmt.Sprintf("ящики %s: %s", name, firstLine(err.Error())))
			continue
		}
		for _, b := range boxes {
			addr := strings.ToLower(b.Address)
			f := pw[addr]
			if len(f) < 6 || !strings.HasSuffix(addr, "@"+name) {
				src.notes = append(src.notes, fmt.Sprintf("ящик %s: пароль не найден, пропущен", addr))
				continue
			}
			home := f[5]
			if !strings.HasPrefix(home, "/var/www/") || strings.Contains(home, "..") || strings.ContainsAny(home, "\n\r\x00") {
				src.notes = append(src.notes, fmt.Sprintf("ящик %s: недопустимый путь к письмам, пропущен", addr))
				continue
			}
			src.export.Boxes = append(src.export.Boxes, impExportMailBox{Address: addr, Hash: f[1], Enabled: b.Enabled, QuotaMB: b.Quota})
			src.homes[addr] = home + "/.maildir"
			if len(b.Aliases) > 2 || len(b.Redirect) > 2 {
				src.notes = append(src.notes, fmt.Sprintf("ящик %s: псевдонимы и пересылки не переносятся", addr))
			}
		}
	}
	return src, nil
}

// fpMailExtract копирует письма ящика (Maildir) в Maildir нового ящика от имени пользователя почты.
func fpMailExtract(ctx context.Context, r impRunner, src, address string) (int64, error) {
	dst := mailHome(address) + "/Maildir"
	cmd := exec.CommandContext(ctx, "runuser", "-u", "itrumail", "--", "tar", "-xpf", "-", "-C", dst)
	in, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	cw := &countWriter{w: in}
	serr := r.Stream(ctx, `[ -d `+shQuote(src)+` ] || exit 0; `+impTarCmd(src), cw)
	in.Close()
	werr := cmd.Wait()
	if serr != nil {
		return cw.n.Load(), serr
	}
	if werr != nil {
		return cw.n.Load(), errf("Не удалось распаковать письма: %s", firstLine(stderr.String()+" "+werr.Error()))
	}
	return cw.n.Load(), nil
}

// impFastpanelExtras переносит FTP-пользователей и почту выбранного.
func impFastpanelExtras(ctx context.Context, j *Job, r impRunner, sess *impSession, sel impSelection, owners map[string]string, res *ImpResult) {
	if len(sel.FTP) > 0 {
		ftp, err := fpLoadFTP(ctx, r)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("FTP не прочитан: %s", firstLine(err.Error())))
		}
		chosen := impSet(sel.FTP)
		for _, f := range ftp {
			if !chosen[f.Name] || jobStopped(j) {
				continue
			}
			jobStage(j, "Перенос FTP-пользователя "+f.Name)
			it := impTransferFTP(nil, f, owners, sess.Secrets)
			res.FTP = append(res.FTP, it)
			if it.Status == "error" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("FTP %s: %s", f.Name, it.Detail))
			}
		}
	}
	if len(sel.Mail) > 0 {
		src, err := fpLoadMail(ctx, r)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("почта не прочитана: %s", firstLine(err.Error())))
			return
		}
		res.Warnings = append(res.Warnings, src.notes...)
		chosen := impSet(sel.Mail)
		for _, d := range src.export.Domains {
			if !chosen[d.Name] || jobStopped(j) {
				continue
			}
			jobStage(j, "Перенос почты домена "+d.Name)
			it := impTransferMailDomainFrom(ctx, j, src.export, d, func(ctx context.Context, address string) (int64, error) {
				home := src.homes[address]
				if home == "" {
					return 0, nil
				}
				return fpMailExtract(ctx, r, home, address)
			})
			res.Mail = append(res.Mail, it)
			if it.Status == "error" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("почта %s: %s", d.Name, it.Detail))
			}
		}
	}
}
