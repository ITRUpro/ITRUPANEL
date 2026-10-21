// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Перенос FTP-пользователей, почты и DNS-зон с другой ITRUPANEL. Всё идёт через API старой панели:
// хеши паролей переносятся как есть, поэтому пароли ящиков и FTP-пользователей не меняются.

type ImpItemResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | skipped | error
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"`
}

func impSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range list {
		m[x] = true
	}
	return m
}

// ---------- FTP ----------

func impTransferFTP(p *impPanel, f impExportFTP, owners map[string]string, secrets map[string]string) ImpItemResult {
	res := ImpItemResult{Name: f.Name, Status: "error"}
	if !reFTPName.MatchString(f.Name) {
		res.Detail = "недопустимое имя пользователя"
		return res
	}
	newOwner := owners[f.Owner]
	if newOwner == "" {
		res.Status, res.Detail = "skipped", "сайт владельца не перенесён: выберите его тоже"
		return res
	}
	ftpMutationMu.Lock()
	defer ftpMutationMu.Unlock()
	mu.Lock()
	u := findUser(newOwner)
	exists := false
	for _, x := range store.FTP {
		exists = exists || x.Name == f.Name
	}
	var uhome string
	if u != nil {
		uhome = u.Home
	}
	mu.Unlock()
	if u == nil {
		res.Detail = "владелец не найден на этом сервере"
		return res
	}
	if exists {
		res.Status, res.Detail = "skipped", "пользователь с таким именем уже есть на этом сервере"
		return res
	}
	rel := strings.TrimPrefix(f.Home, "/var/www/"+f.Owner)
	if rel == f.Home || rel == "" {
		rel = "/data/www"
	}
	home, ok := safeUnder(uhome, uhome+rel)
	if !ok {
		res.Detail = "домашняя папка выходит за пределы владельца"
		return res
	}
	uid, _ := run("id", "-u", newOwner)
	gid, _ := run("id", "-g", newOwner)
	uid, gid = strings.TrimSpace(uid), strings.TrimSpace(gid)
	pass := randPass(14)
	if _, err := runInput(pass+"\n"+pass+"\n", "pure-pw", "useradd", f.Name, "-f", ftpPasswd, "-F", ftpPDB, "-u", uid, "-g", gid, "-d", home, "-m"); err != nil {
		res.Detail = firstLine(err.Error())
		return res
	}
	kept := false
	incompatible := f.Hash != "" && !ftpHashSupported(f.Hash)
	if f.Hash != "" && !incompatible && !strings.ContainsAny(f.Hash, ":\n\r") {
		lines := strings.Split(readFile(ftpPasswd), "\n")
		for i, l := range lines {
			fl := strings.Split(l, ":")
			if len(fl) > 2 && fl[0] == f.Name {
				fl[1] = f.Hash
				lines[i] = strings.Join(fl, ":")
				if writeFile(ftpPasswd, []byte(strings.Join(lines, "\n")), 0600) == nil {
					_, err := run("pure-pw", "mkdb", ftpPDB, "-f", ftpPasswd)
					kept = err == nil
				}
				break
			}
		}
	}
	mu.Lock()
	store.FTP = append(store.FTP, &FTPUser{Name: f.Name, Owner: newOwner, Home: home, Created: time.Now()})
	save()
	mu.Unlock()
	res.Status = "ok"
	if !kept {
		secrets["FTP "+f.Name] = pass
		res.Detail = "пароль изменён: новый показан в отчёте"
		if incompatible {
			res.Detail = "пароль изменён: FTP-сервер этого сервера не понимает формат старого пароля; новый показан в отчёте"
		}
	} else {
		res.Detail = "пароль сохранён"
	}
	return res
}

// ftpHashSupported: Pure-FTPd с libsodium хранит пароли в argon2id, сборки без неё (например, RHEL) такой хеш
// проверить не могут; остальные форматы (bcrypt, SHA-crypt) понимают все сборки.
func ftpHashSupported(h string) bool {
	if !strings.HasPrefix(h, "$argon2") {
		return true
	}
	out, _ := run("bash", "-c", "ldd \"$(command -v pure-ftpd || echo /usr/sbin/pure-ftpd)\" 2>/dev/null")
	return strings.Contains(out, "sodium")
}

// ---------- DNS ----------

func impTransferDNS(j *Job, z impExportZone, oldIP string, mailMoved map[string]bool) (ImpItemResult, string) {
	res := ImpItemResult{Name: z.Name, Status: "error"}
	name, err := toASCII(z.Name)
	if err != nil {
		res.Detail = err.Error()
		return res, ""
	}
	newIP := defaultIP()
	mu.Lock()
	defer mu.Unlock()
	if findZone(name) != nil {
		res.Status, res.Detail = "skipped", "зона уже есть на этом сервере"
		return res, ""
	}
	nz := &DNSZone{Name: name, Email: z.Email, TTL: z.TTL, Created: time.Now()}
	if nz.TTL == 0 {
		nz.TTL = 3600
	}
	replaced, dropped := 0, 0
	for _, r := range z.Records {
		c := *r
		c.ID = randToken(8)
		if mailMoved[name] && c.Type == "TXT" && strings.HasSuffix(strings.ToLower(strings.TrimSuffix(c.Name, ".")), "._domainkey") {
			dropped++ // новый ключ DKIM создан на этом сервере и опубликуется ниже
			continue
		}
		if (c.Type == "A" || c.Type == "AAAA") && oldIP != "" && newIP != "" && c.Value == oldIP {
			if ip := net.ParseIP(newIP); ip != nil && (ip.To4() != nil) == (c.Type == "A") {
				c.Value = newIP
				replaced++
			}
		}
		if err := validateRecord(&c); err != nil {
			dropped++
			continue
		}
		nz.Records = append(nz.Records, &c)
	}
	store.DNSZones = append(store.DNSZones, nz)
	if err := applyZone(nz); err != nil {
		store.DNSZones = store.DNSZones[:len(store.DNSZones)-1]
		res.Detail = firstLine(err.Error())
		return res, ""
	}
	save()
	res.Status, res.Count = "ok", len(nz.Records)
	parts := []string{fmt.Sprintf("записей: %d", len(nz.Records))}
	if replaced > 0 {
		parts = append(parts, fmt.Sprintf("IP старого сервера заменён на IP этого в %d", replaced))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("пропущено записей: %d", dropped))
	}
	res.Detail = strings.Join(parts, ", ")
	return res, name
}

// ---------- Почта ----------

func impMailExtract(ctx context.Context, p *impPanel, address string) (int64, error) {
	home := mailHome(address)
	cmd := exec.CommandContext(ctx, "runuser", "-u", "itrumail", "--", "tar", "-xpf", "-", "-C", home)
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
	derr := p.c.download(ctx, "/api/transfer/mail", map[string]string{"address": address}, cw)
	in.Close()
	werr := cmd.Wait()
	if cw.n.Load() == 0 {
		return 0, derr // пустой каталог на старом сервере
	}
	if derr != nil {
		return cw.n.Load(), derr
	}
	if werr != nil {
		return cw.n.Load(), errf("Не удалось распаковать письма: %s", firstLine(stderr.String()+" "+werr.Error()))
	}
	return cw.n.Load(), nil
}

func impTransferMailDomain(ctx context.Context, j *Job, p *impPanel, d impExportMailDomain) ImpItemResult {
	res := ImpItemResult{Name: d.Name, Status: "error"}
	mu.Lock()
	exists := mailDomainExists(store.Mail, d.Name)
	mu.Unlock()
	if exists {
		res.Status, res.Detail = "skipped", "почтовый домен уже есть на этом сервере"
		return res
	}
	host := d.Hostname
	if host == "" {
		host = "mail." + d.Name
	}
	// Сертификат пока самоподписанный: домен ещё не указывает на этот сервер. Let’s Encrypt выпускается в разделе «Почта».
	warning, err := createMailDomain(j, d.Name, host, false, "", d.Webmail, d.Spam)
	if err != nil {
		res.Detail = firstLine(err.Error())
		return res
	}
	_ = warning
	var boxes []impExportMailBox
	for _, b := range p.mail.Boxes {
		if strings.HasSuffix(b.Address, "@"+d.Name) {
			boxes = append(boxes, b)
		}
	}
	err = changeMail(func(m *MailState) error {
		for i := range m.Domains {
			if m.Domains[i].Name == d.Name && d.DefaultQuotaMB > 0 {
				m.Domains[i].DefaultQuotaMB = d.DefaultQuotaMB
			}
		}
		for _, b := range boxes {
			addr, e := mailAddress(b.Address)
			if e != nil || mailAddressUsed(*m, addr) {
				continue
			}
			home := mailHome(addr)
			for _, sub := range []string{"", "/Maildir/cur", "/Maildir/new", "/Maildir/tmp"} {
				if e := os.MkdirAll(P(home+sub), 0700); e != nil {
					return e
				}
			}
			dom := strings.SplitN(addr, "@", 2)[1]
			if _, e := run("chown", "itrumail:itrumail", mailRoot+"/"+dom, home, home+"/Maildir", home+"/Maildir/cur", home+"/Maildir/new", home+"/Maildir/tmp"); e != nil {
				return e
			}
			m.Boxes = append(m.Boxes, MailBox{Address: addr, Name: b.Name, Hash: b.Hash, Enabled: b.Enabled, Created: time.Now(), QuotaMB: b.QuotaMB})
		}
		for _, a := range p.mail.Aliases {
			if strings.HasSuffix(a.Address, "@"+d.Name) && !mailAddressUsed(*m, a.Address) {
				m.Aliases = append(m.Aliases, a)
			}
		}
		for _, f := range p.mail.Forwards {
			if strings.HasSuffix(f.Address, "@"+d.Name) && !mailAddressUsed(*m, f.Address) {
				m.Forwards = append(m.Forwards, f)
			}
		}
		return nil
	})
	if err != nil {
		res.Detail = "домен создан, но ящики не добавлены: " + firstLine(err.Error())
		return res
	}
	var total int64
	failed := 0
	for _, b := range boxes {
		if jobStopped(j) {
			break
		}
		jobStage(j, "Перенос писем "+b.Address)
		n, e := impMailExtract(ctx, p, b.Address)
		total += n
		if e != nil {
			failed++
		}
	}
	res.Status, res.Count = "ok", len(boxes)
	res.Detail = fmt.Sprintf("ящиков: %d, пароли сохранены", len(boxes))
	if failed > 0 {
		res.Detail += fmt.Sprintf("; письма не перенесены у ящиков: %d", failed)
	}
	return res
}

// impTransferBackup переносит настройки копирования на FTP. Расписание на новом сервере остаётся выключенным:
// пока работает старая панель, обе не должны одновременно писать копии одних и тех же сайтов.
// Папка FTP остаётся прежней, чтобы найти и подключить уже существующие копии.
func impTransferBackup(j *Job, p *impPanel) ImpItemResult {
	b := p.backup
	res := ImpItemResult{Name: "backup", Status: "error"}
	if backupTargetIsCloud(b.Target) {
		return impTransferBackupCloud(j, b)
	}
	f := b.FTP
	if (b.Target != "ftp" && b.Target != "both") || f.Host == "" || f.User == "" {
		res.Status, res.Detail = "skipped", "на старом сервере копии хранятся только на самом сервере: переносить нечего"
		return res
	}
	if f.Port < 0 || f.Port > 65535 || strings.ContainsAny(f.Host+f.User+f.Pass+f.Dir, " \t\r\n") {
		res.Detail = "некорректные настройки FTP"
		return res
	}
	if b.Keep < 1 || b.Keep > 60 {
		b.Keep = 7
	}
	if b.Hour < 0 || b.Hour > 23 {
		b.Hour = 3
	}
	warn := ""
	if _, err := curlFTP(f, "--ftp-create-dirs", "-l", ftpBaseURL(f)); err != nil {
		warn = firstLine(err.Error())
	}
	mu.Lock()
	st := &store.Settings
	st.BackupTarget, st.BackupFTP = b.Target, f
	if b.Mode == "full" || b.Mode == "incremental" {
		st.BackupMode = b.Mode
	}
	st.BackupKeep, st.BackupHour, st.BackupDaily = b.Keep, b.Hour, false
	st.BackupSiteSettings, st.BackupPanelSettings, st.BackupSkipDBs = b.SiteSettings, b.PanelSettings, b.SkipDBs
	save()
	mu.Unlock()
	sched := "расписание не было включено"
	if b.Daily {
		sched = fmt.Sprintf("ежедневно в %d:00, хранить копий: %d", b.Hour, b.Keep)
	}
	res.Status = "ok"
	res.Detail = fmt.Sprintf("FTP %s сохранён; расписание выключено (на старом: %s)", f.Host, sched)
	if warn != "" {
		res.Detail += "; подключиться к FTP с этого сервера не удалось: " + warn
		return res
	}
	if j != nil {
		jobStage(j, "Поиск существующих копий на FTP")
		if _, err := scanBackupFTP(j); err != nil {
			res.Detail += "; поиск копий на FTP: " + firstLine(err.Error())
		} else {
			res.Detail += "; существующие копии на FTP найдены и подключены"
		}
	}
	return res
}

// impTransferBackupCloud переносит настройки облачного хранилища копий (S3, WebDAV, Dropbox, Google Drive);
// расписание на новом сервере остаётся выключенным.
func impTransferBackupCloud(j *Job, b impExportBackup) ImpItemResult {
	res := ImpItemResult{Name: "backup", Status: "error"}
	kind := cloudKindOfTarget(b.Target)
	var (
		cl    cloudStore
		where string
		set   func(st *Settings)
	)
	switch kind {
	case cloudS3:
		c, err := s3Normalize(b.S3)
		if err != nil {
			res.Detail = "некорректные настройки облака: " + firstLine(err.Error())
			return res
		}
		if s, e := newS3Client(c); e == nil {
			cl = s3Store{s}
		}
		where = strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://") + "/" + c.Bucket
		set = func(st *Settings) { st.BackupS3 = c }
	case cloudWebDAV:
		c, err := davNormalize(b.DAV)
		if err != nil {
			res.Detail = "некорректные настройки WebDAV: " + firstLine(err.Error())
			return res
		}
		cl = &davStore{c: c, hc: cloudHTTP(c.Insecure)}
		where = strings.TrimPrefix(strings.TrimPrefix(c.URL, "https://"), "http://")
		set = func(st *Settings) { st.BackupDAV = c }
	case cloudDropbox:
		c, err := dropboxNormalize(b.Dropbox)
		if err != nil {
			res.Detail = "некорректные настройки Dropbox: " + firstLine(err.Error())
			return res
		}
		cl = &dropboxStore{c: c, hc: cloudHTTP(false)}
		where = "Dropbox"
		set = func(st *Settings) { st.BackupDropbox = c }
	case cloudGDrive:
		c, err := gdriveNormalize(b.GDrive)
		if err != nil {
			res.Detail = "некорректные настройки Google Drive: " + firstLine(err.Error())
			return res
		}
		cl = &gdriveStore{c: c, hc: cloudHTTP(false)}
		where = "Google Drive"
		set = func(st *Settings) { st.BackupGDrive = c }
	}
	if b.Keep < 1 || b.Keep > 60 {
		b.Keep = 7
	}
	if b.Hour < 0 || b.Hour > 23 {
		b.Hour = 3
	}
	warn := ""
	if cl == nil {
		warn = "хранилище не настроено"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if e := cl.Test(ctx); e != nil {
			warn = firstLine(e.Error())
		}
		cancel()
	}
	mu.Lock()
	st := &store.Settings
	st.BackupTarget = b.Target
	set(st)
	if b.Mode == "full" || b.Mode == "incremental" {
		st.BackupMode = b.Mode
	}
	st.BackupKeep, st.BackupHour, st.BackupDaily = b.Keep, b.Hour, false
	st.BackupSiteSettings, st.BackupPanelSettings, st.BackupSkipDBs = b.SiteSettings, b.PanelSettings, b.SkipDBs
	save()
	mu.Unlock()
	sched := "расписание не было включено"
	if b.Daily {
		sched = fmt.Sprintf("ежедневно в %d:00, хранить копий: %d", b.Hour, b.Keep)
	}
	res.Status = "ok"
	res.Detail = fmt.Sprintf("облако %s сохранено; расписание выключено (на старом: %s)", where, sched)
	if warn != "" {
		res.Detail += "; подключиться к облаку с этого сервера не удалось: " + warn
		return res
	}
	if j != nil {
		jobStage(j, "Поиск существующих копий в облаке")
		if _, err := scanBackupCloud(j, kind); err != nil {
			res.Detail += "; поиск копий в облаке: " + firstLine(err.Error())
		} else {
			res.Detail += "; существующие копии в облаке найдены и подключены"
		}
	}
	return res
}

// impTransferExtras переносит FTP, почту и DNS выбранного. Работает только через API старой ITRUPANEL.
func impTransferExtras(ctx context.Context, j *Job, p *impPanel, sess *impSession, sel impSelection, owners map[string]string, res *ImpResult) {
	// Почта.
	mailMoved := map[string]bool{}
	mailSel := impSet(sel.Mail)
	for _, d := range p.mail.Domains {
		if !mailSel[d.Name] || jobStopped(j) {
			continue
		}
		jobStage(j, "Перенос почты домена "+d.Name)
		r := impTransferMailDomain(ctx, j, p, d)
		res.Mail = append(res.Mail, r)
		if r.Status == "ok" {
			mailMoved[d.Name] = true
		} else if r.Status == "error" {
			res.Warnings = append(res.Warnings, "почта "+d.Name+": "+r.Detail)
		}
	}
	// DNS.
	dnsSel := impSet(sel.DNS)
	var zones []impExportZone
	for _, z := range p.dns {
		if dnsSel[z.Name] {
			zones = append(zones, z)
		}
	}
	if len(zones) > 0 && !dnsInstalled() && !cfg.Dry {
		jobStage(j, "Установка DNS-сервера BIND")
		if err := setupBind(); err != nil {
			res.Warnings = append(res.Warnings, "DNS-зоны не перенесены: "+firstLine(err.Error()))
			zones = nil
		}
	}
	for _, z := range zones {
		if jobStopped(j) {
			break
		}
		jobStage(j, "Перенос DNS-зоны "+z.Name)
		r, name := impTransferDNS(j, z, p.ip, mailMoved)
		res.DNS = append(res.DNS, r)
		if r.Status == "error" {
			res.Warnings = append(res.Warnings, "DNS "+z.Name+": "+r.Detail)
			continue
		}
		if name != "" && mailMoved[name] {
			// Записи MX, SPF, DKIM и DMARC для переехавшей почты публикуются из настроек этого сервера.
			mu.Lock()
			v, e := mailDNSViewFor(name)
			if e == nil {
				e = applyMailDNS(v, planMailDNS(v).Token)
			}
			mu.Unlock()
			if e != nil {
				res.Warnings = append(res.Warnings, "почтовые записи DNS "+name+": "+firstLine(e.Error()))
			}
		}
	}
	// Настройки резервного копирования.
	if sel.Backup && !jobStopped(j) {
		jobStage(j, "Перенос настроек резервного копирования")
		r := impTransferBackup(j, p)
		res.Backup = &r
		if r.Status == "error" {
			res.Warnings = append(res.Warnings, "резервное копирование: "+r.Detail)
		}
	}
	// FTP.
	ftpSel := impSet(sel.FTP)
	for _, f := range p.ftp {
		if !ftpSel[f.Name] || jobStopped(j) {
			continue
		}
		jobStage(j, "Перенос FTP-пользователя "+f.Name)
		r := impTransferFTP(p, f, owners, sess.Secrets)
		res.FTP = append(res.FTP, r)
		if r.Status == "error" {
			res.Warnings = append(res.Warnings, "FTP "+f.Name+": "+r.Detail)
		}
	}
}
