// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Выгрузка для переноса на другой сервер ITRUPANEL (раздел «Импорт данных» на новом сервере).
// Доступно только вошедшему администратору. Отдаются только сайты и базы, известные панели:
// произвольные пути и команды выполнить нельзя.

var impDumpCharsets = map[string]bool{"utf8mb4": true, "utf8": true, "cp1251": true, "latin1": true}

type impExportSite struct {
	Domain string `json:"domain"`
	Size   int64  `json:"size"`
}
type impExportZone struct {
	Name    string       `json:"name"`
	Email   string       `json:"email"`
	TTL     int          `json:"ttl"`
	Records []*DNSRecord `json:"records"`
}
type impExportFTP struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	Home  string `json:"home"`
	Hash  string `json:"hash,omitempty"`
}
type impExportMailDomain struct {
	Name           string `json:"name"`
	Hostname       string `json:"hostname"`
	Webmail        bool   `json:"webmail"`
	Spam           bool   `json:"spam"`
	DefaultQuotaMB int64  `json:"default_quota_mb"`
}
type impExportMailBox struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	Hash    string `json:"hash"`
	Enabled bool   `json:"enabled"`
	QuotaMB int64  `json:"quota_mb"`
}
type impExportMail struct {
	Ready    bool                  `json:"ready"`
	Domains  []impExportMailDomain `json:"domains"`
	Boxes    []impExportMailBox    `json:"boxes"`
	Aliases  []MailAlias           `json:"aliases"`
	Forwards []MailForward         `json:"forwards"`
}

// impFTPHashes читает хеши паролей FTP-пользователей из файла Pure-FTPd (имя → хеш).
func impFTPHashes() map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(readFile(ftpPasswd), "\n") {
		f := strings.Split(l, ":")
		if len(f) > 2 && f[0] != "" && f[1] != "" {
			out[f[0]] = f[1]
		}
	}
	return out
}

type impExportBackup struct {
	Target        string        `json:"target"`
	FTP           BackupFTP     `json:"ftp"`
	S3            BackupS3      `json:"s3"`
	DAV           BackupWebDAV  `json:"webdav"`
	Dropbox       BackupDropbox `json:"dropbox"`
	GDrive        BackupGDrive  `json:"gdrive"`
	Daily         bool          `json:"daily"`
	Keep          int           `json:"keep"`
	Hour          int           `json:"hour"`
	Mode          string        `json:"mode"`
	SiteSettings  bool          `json:"site_settings"`
	PanelSettings bool          `json:"panel_settings"`
	SkipDBs       bool          `json:"skip_dbs"`
}

type impExportDB struct {
	Name    string `json:"name"`
	User    string `json:"user"`
	Charset string `json:"charset"`
	Hash    string `json:"hash,omitempty"`
}

// impDirSize возвращает размер папки (du -sb) не дольше двух минут.
func impDirSize(root string) int64 {
	out, err := runTimeout(2*time.Minute, "", "du", "-sb", root)
	if err != nil {
		return 0
	}
	var n int64
	if f := strings.Fields(out); len(f) > 0 {
		for _, c := range f[0] {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
	}
	return n
}

func registerImportExport() {
	apiOp("transfer.info", "Импорт: выгрузка описания сервера для другой панели", func(q *Req) (any, error) {
		type siteRef struct{ domain, root string }
		mu.Lock()
		var sites []siteRef
		for _, s := range store.Sites {
			sites = append(sites, siteRef{s.Domain, s.Root})
		}
		var dbs []DB
		for _, d := range store.DBs {
			if d.Server == "" {
				dbs = append(dbs, *d)
			}
		}
		mu.Unlock()
		out := map[string]any{"version": Version, "os": osInfo.Pretty, "arch": runtime.GOARCH, "php_versions": phpInstalled()}
		es := []impExportSite{}
		for _, s := range sites {
			es = append(es, impExportSite{Domain: s.domain, Size: impDirSize(s.root)})
		}
		ed := []impExportDB{}
		for _, d := range dbs {
			x := impExportDB{Name: d.Name, User: d.User, Charset: "utf8mb4"}
			if o, err := sqlOn("", `SELECT default_character_set_name FROM information_schema.schemata WHERE schema_name=`+sqlStr(d.Name)); err == nil {
				x.Charset = impNormCharset(o)
			}
			for _, qy := range []string{
				`SELECT COALESCE(NULLIF(Password,''), authentication_string), plugin FROM mysql.user WHERE user=` + sqlStr(d.User) + ` LIMIT 1`,
				`SELECT authentication_string, plugin FROM mysql.user WHERE user=` + sqlStr(d.User) + ` LIMIT 1`,
			} {
				if o, err := sqlOn("", qy); err == nil {
					x.Hash = impPickHash(o)
					break
				}
			}
			ed = append(ed, x)
		}
		out["sites"], out["dbs"] = es, ed
		// DNS-зоны, FTP-пользователи, почта и IP сервера — для переноса на другую панель.
		out["ip"] = defaultIP()
		mu.Lock()
		zones := []impExportZone{}
		for _, z := range store.DNSZones {
			zones = append(zones, impExportZone{Name: z.Name, Email: z.Email, TTL: z.TTL, Records: z.Records})
		}
		ftpUsers := []impExportFTP{}
		hashes := impFTPHashes()
		for _, f := range store.FTP {
			ftpUsers = append(ftpUsers, impExportFTP{Name: f.Name, Owner: f.Owner, Home: f.Home, Hash: hashes[f.Name]})
		}
		m := impExportMail{Ready: store.Mail.Ready, Aliases: store.Mail.Aliases, Forwards: store.Mail.Forwards}
		for _, d := range store.Mail.Domains {
			m.Domains = append(m.Domains, impExportMailDomain{Name: d.Name, Hostname: mailDomainHost(store.Mail, d), Webmail: d.Webmail, Spam: !d.NoSpam, DefaultQuotaMB: d.DefaultQuotaMB})
		}
		for _, b := range store.Mail.Boxes {
			m.Boxes = append(m.Boxes, impExportMailBox{Address: b.Address, Name: b.Name, Hash: b.Hash, Enabled: b.Enabled, QuotaMB: b.QuotaMB})
		}
		bs := store.Settings
		bk := impExportBackup{Target: bs.BackupTarget, Daily: bs.BackupDaily, Keep: bs.BackupKeep, Hour: bs.BackupHour, Mode: bs.BackupMode, SiteSettings: bs.BackupSiteSettings, PanelSettings: bs.BackupPanelSettings, SkipDBs: bs.BackupSkipDBs}
		if bk.Target == "" {
			bk.Target = "local"
		}
		if bk.Target == "ftp" || bk.Target == "both" {
			bk.FTP = bs.BackupFTP
		}
		switch cloudKindOfTarget(bk.Target) {
		case cloudS3:
			bk.S3 = bs.BackupS3
		case cloudWebDAV:
			bk.DAV = bs.BackupDAV
		case cloudDropbox:
			bk.Dropbox = bs.BackupDropbox
		case cloudGDrive:
			bk.GDrive = bs.BackupGDrive
		}
		mu.Unlock()
		out["dns"], out["ftp"], out["mail"], out["backup"] = zones, ftpUsers, m, bk
		return out, nil
	})
}

func streamExport(w http.ResponseWriter, r *http.Request, ctype string, okExit int, name string, args ...string) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	cmd := exec.CommandContext(r.Context(), name, args...)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() > okExit {
			// Заголовки уже отправлены: обрыв потока виден получателю как ошибка распаковки.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}
	}
}

func transferBody(w http.ResponseWriter, r *http.Request, in any) bool {
	if r.Method != http.MethodPost || r.Header.Get("X-Itrupanel") != "1" {
		http.Error(w, "bad request", 400)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(in); err != nil {
		http.Error(w, "bad request", 400)
		return false
	}
	return true
}

// handleTransferTree отдаёт tar папки сайта (права, владельцы и ссылки сохраняются).
func handleTransferTree(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain string }
	if !transferBody(w, r, &in) {
		return
	}
	mu.Lock()
	s := findSite(in.Domain)
	root := ""
	if s != nil {
		root = s.Root
	}
	mu.Unlock()
	if root == "" {
		http.Error(w, "site not found", 404)
		return
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		http.Error(w, "root not found", 404)
		return
	}
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: getSession(r).User, IP: getSession(r).IP, Action: "Импорт: выгрузка файлов сайта", Object: in.Domain, Status: "ok"})
	if cfg.Dry {
		w.Header().Set("Content-Type", "application/x-tar")
		return
	}
	streamExport(w, r, "application/x-tar", 1, "tar", "-C", root, "-cf", "-", ".")
}

// handleTransferDump отдаёт дамп базы (gzip).
func handleTransferDump(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Charset string }
	if !transferBody(w, r, &in) {
		return
	}
	mu.Lock()
	d := findDB(in.Name)
	ok := d != nil && d.Server == ""
	mu.Unlock()
	if !ok {
		http.Error(w, "database not found", 404)
		return
	}
	if !impDumpCharsets[in.Charset] {
		in.Charset = "utf8mb4"
	}
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: getSession(r).User, IP: getSession(r).IP, Action: "Импорт: выгрузка базы", Object: in.Name, Status: "ok"})
	if cfg.Dry {
		w.Header().Set("Content-Type", "application/gzip")
		return
	}
	streamExport(w, r, "application/gzip", 0, "bash", "-o", "pipefail", "-c", `mysqldump --single-transaction --quick --routines --triggers --events --default-character-set=`+in.Charset+` `+shQuote(in.Name)+` | gzip -c`)
}

// handleTransferMail отдаёт tar почтового каталога ящика (письма Maildir).
func handleTransferMail(w http.ResponseWriter, r *http.Request) {
	var in struct{ Address string }
	if !transferBody(w, r, &in) {
		return
	}
	mu.Lock()
	found := false
	for _, b := range store.Mail.Boxes {
		if b.Address == in.Address {
			found = true
		}
	}
	mu.Unlock()
	if !found {
		http.Error(w, "mailbox not found", 404)
		return
	}
	home := mailHome(in.Address)
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: getSession(r).User, IP: getSession(r).IP, Action: "Импорт: выгрузка почты ящика", Object: in.Address, Status: "ok"})
	if cfg.Dry {
		w.Header().Set("Content-Type", "application/x-tar")
		return
	}
	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		w.Header().Set("Content-Type", "application/x-tar")
		return
	}
	streamExport(w, r, "application/x-tar", 1, "tar", "-C", home, "-cf", "-", ".")
}
