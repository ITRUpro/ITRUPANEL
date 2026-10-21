// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const backupDir = "/var/backups/itrupanel"

var backupWorkMu sync.Mutex

var backupJobs = map[string]string{} // сайт → "running"

// makeBackup архивирует папку сайта и его базы: files/…, db/<имя>.sql, meta.json.
func makeBackup(domain, kind string, auto bool, options BackupOptions) (*Backup, error) {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	mu.Lock()
	s := findSite(domain)
	if s == nil {
		mu.Unlock()
		return nil, errf("Сайт не найден")
	}
	site := *s
	panelSettings := capturePanelBackupSettings(store.Settings)
	var dbs []string
	dbServer := map[string]string{}
	for _, d := range store.DBs {
		if d.Site == domain {
			dbs = append(dbs, d.Name)
			dbServer[d.Name] = d.Server
		}
	}
	mu.Unlock()
	if kind == "" {
		kind = "full"
	}
	id := time.Now().Format("20060102-150405") + "-" + randToken(2)
	tmp := backupDir + "/.tmp-" + id
	if err := os.MkdirAll(P(tmp+"/db"), 0700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(P(tmp))
	mode, parent, index, deleted, err := prepareIncrementalBackup(site, kind, auto, options)
	if err != nil {
		return nil, err
	}
	meta := newBackupMetadata(site, kind, options, panelSettings, readFile(customPath(domain)))
	meta.DBs, meta.DBServers = dbs, dbServer
	meta.ParentID, meta.Deleted = parent, deleted

	data, err := json.MarshalIndent(meta, "", " ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(P(tmp+"/meta.json"), data, 0600); err != nil {
		return nil, err
	}

	if kind == "full" || kind == "db" {
		for _, d := range dbs {
			if err := dbDumpTo(dbServer[d], d, tmp+"/db/"+d+".sql"); err != nil {
				return nil, errf("mysqldump %s: %v", d, err)
			}
		}
	}
	file := fmt.Sprintf("%s/%s_%s.tar.gz", backupDir, domain, id)
	if err := writeBackupArchive(P(file), P(tmp), P(site.Root), index, parent, P(userHome(site.User))); err != nil {
		os.Remove(P(file))
		return nil, err
	}
	if index != nil {
		if err := saveBackupIndex(id, site.Root, index); err != nil {
			os.Remove(P(file))
			return nil, err
		}
	}
	// Список баз копии для просмотра содержимого без скачивания архива.
	if kind == "full" || kind == "db" {
		saveBackupDBs(id, dbs)
	}

	return storeNewBackup(&Backup{ID: id, File: file, Site: domain, Kind: kind, Created: time.Now(), Auto: auto, Mode: mode, ParentID: parent, SiteSettings: options.SiteSettings, PanelSettings: options.PanelSettings})
}

func restoreBackup(id string, options BackupRestoreOptions) error {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	siteSaveMu.Lock()
	defer siteSaveMu.Unlock()
	mu.Lock()
	var b *Backup
	for _, x := range store.Backups {
		if x.ID == id {
			b = x
		}
	}
	var s *Site
	if b != nil {
		s = findSite(b.Site)
	}
	mu.Unlock()
	if b == nil {
		return errf("Копия не найдена")
	}
	switch b.Kind {
	case "database":
		return restoreDBBackup(b)
	case "panel":
		return restorePanelBackup(b)
	}
	if s == nil {
		return errf("Сайт %s не существует — создайте его заново, затем восстановите копию", b.Site)
	}
	chain, err := backupChain(b)
	if err != nil {
		return err
	}
	tmp := backupDir + "/.restore-" + randToken(4)
	if err := os.MkdirAll(P(tmp), 0700); err != nil {
		return err
	}
	defer os.RemoveAll(P(tmp))
	// Assemble every required archive before replacing any live data.
	for _, part := range chain {
		if err := unpackBackupPart(part, P(tmp)); err != nil {
			return err
		}
	}

	// Validate selected settings before touching site data.
	var meta backupMetadata
	if options.SiteSettings || options.PanelSettings {
		data, err := os.ReadFile(P(tmp + "/meta.json"))
		if err != nil {
			return err
		}
		meta, err = decodeBackupMetadata(data)
		if err != nil {
			return err
		}
		mu.Lock()
		err = validateBackupSettings(b.Site, meta, options)
		mu.Unlock()
		if err != nil {
			return err
		}
	}
	if len(options.Paths) > 0 {
		// Выбранные файлы и папки заменяются по своему пути; корень сайта — весь сайт.
		rels, err := backupRelPaths(options.Paths)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			if rel == "" {
				err = restoreSiteFiles(s, tmp+"/files")
			} else {
				err = restoreSitePath(s, tmp+"/files", rel)
			}
			if err != nil {
				return err
			}
		}
	} else if options.Files && fileExists(tmp+"/files") {
		if err := restoreSiteFiles(s, tmp+"/files"); err != nil {
			return err
		}
	}
	var entries []os.DirEntry
	if options.Databases || len(options.DBs) > 0 {
		entries, _ = os.ReadDir(P(tmp + "/db"))
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sql")
		if !reDBName.MatchString(name) {
			continue
		}
		if len(options.DBs) > 0 && !contains(options.DBs, name) {
			continue
		}
		server := ""
		mu.Lock()
		if d := findDB(name); d != nil {
			server = d.Server
		}
		mu.Unlock()
		sqlOn(server, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4;", name))
		if err := dbImportAsRoot(server, name, tmp+"/db/"+e.Name()); err != nil {
			return err
		}
	}
	if options.SiteSettings || options.PanelSettings {
		return restoreBackupSettings(b.Site, meta, options)
	}
	return nil
}

func deleteBackup(id string) error {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	for _, b := range store.Backups {
		if b.ParentID == id {
			return errf("Копия нужна для инкрементной копии %s. Сначала удалите зависимые копии", b.ID)
		}
	}
	var keep []*Backup
	found := false
	for _, b := range store.Backups {
		if b.ID == id {
			found = true
			os.Remove(P(b.File))
			os.Remove(backupIndexPath(b.ID))
			os.Remove(backupDBsPath(b.ID))
			os.Remove(backupBrowseIndexPath(b.ID))
			go deleteRemote(b)
			continue
		}
		keep = append(keep, b)
	}
	if !found {
		return errf("Копия не найдена")
	}
	store.Backups = keep
	save()
	return nil
}

// autoBackups — ежедневные копии всех сайтов, хранится BackupKeep последних автоматических.
func autoBackups() {
	updateGate.RLock()
	defer updateGate.RUnlock()
	if panelUpdating() {
		return
	}
	mu.Lock()
	var domains, dbs []string
	for _, s := range store.Sites {
		domains = append(domains, s.Domain)
	}
	if !store.Settings.BackupSkipDBs {
		for _, d := range store.DBs {
			dbs = append(dbs, d.Name)
		}
	}
	keep := store.Settings.BackupKeep
	panel := store.Settings.BackupPanelSettings
	options := BackupOptions{Mode: store.Settings.BackupMode, SiteSettings: store.Settings.BackupSiteSettings}
	mu.Unlock()
	failed := func(what string, err error) {
		mu.Lock()
		addNotice("error", "", "Резервная копия "+what+" не создана: "+firstLine(err.Error()))
		save()
		mu.Unlock()
	}
	// Файлы сайтов, каждая база и настройки панели — отдельными копиями.
	for _, d := range domains {
		if _, err := makeBackup(d, "files", true, options); err != nil {
			failed(d, err)
		}
		pruneAutoBackups(d, keep)
	}
	for _, name := range dbs {
		if _, err := makeDBBackup(name, true); err != nil {
			failed(name, err)
		}
		pruneAutoBackups("db:"+name, keep)
	}
	if panel {
		if _, err := makePanelBackup(true); err != nil {
			failed("настроек панели", err)
		}
		pruneAutoBackups("panel:", keep)
	}
}

func registerBackups() {
	registerBackupBrowse()
	registerBackupFTP()
	registerBackupCloud()
	api("backups.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		rows := make([]*Backup, 0, len(store.Backups))
		for _, b := range store.Backups {
			copy := *b
			copy.RestoreIssue = backupRestoreIssue(b)
			rows = append(rows, &copy)
		}
		dbSites := map[string]string{}
		for _, d := range store.DBs {
			dbSites[d.Name] = d.Site
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
		jobs := []string{}
		for k := range backupJobs {
			jobs = append(jobs, k)
		}
		return map[string]any{"rows": rows, "running": jobs, "settings": map[string]any{"daily": store.Settings.BackupDaily, "keep": store.Settings.BackupKeep, "hour": store.Settings.BackupHour, "site_settings": store.Settings.BackupSiteSettings, "panel_settings": store.Settings.BackupPanelSettings, "mode": store.Settings.BackupMode, "databases": !store.Settings.BackupSkipDBs}, "dir": backupDir, "db_sites": dbSites}, nil
	})
	apiOp("backups.create", "Резервная копия: создание", func(q *Req) (any, error) {
		var in struct {
			Site, Kind, DB string
			BackupOptions
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Mode != "" && in.Mode != "full" && in.Mode != "incremental" {
			return nil, errf("Неверный режим архивации")
		}
		if in.Kind == "" {
			in.Kind = "files"
		}
		if in.Kind != "full" && in.Kind != "files" && in.Kind != "db" && in.Kind != "settings" && in.Kind != "database" && in.Kind != "panel" {
			return nil, errf("Неверный состав копии")
		}
		if in.Kind == "settings" && !in.SiteSettings && !in.PanelSettings {
			return nil, errf("Выберите настройки для копирования")
		}
		type task struct {
			key, name string
			run       func() error
		}
		var tasks []task
		mu.Lock()
		switch in.Kind {
		case "database":
			for _, d := range store.DBs {
				if in.DB == "*" || d.Name == in.DB {
					name := d.Name
					tasks = append(tasks, task{"db:" + name, name, func() error { _, err := makeDBBackup(name, false); return err }})
				}
			}
		case "panel":
			tasks = append(tasks, task{"panel:", "настроек панели", func() error { _, err := makePanelBackup(false); return err }})
		default:
			for _, s := range store.Sites {
				if in.Site == "*" || s.Domain == in.Site {
					domain := s.Domain
					tasks = append(tasks, task{domain, domain, func() error { _, err := makeBackup(domain, in.Kind, false, in.BackupOptions); return err }})
				}
			}
		}
		for _, t := range tasks {
			if backupJobs[t.key] != "" {
				mu.Unlock()
				return nil, errf("Копия %s уже создаётся", t.name)
			}
		}
		for _, t := range tasks {
			backupJobs[t.key] = "running"
		}
		mu.Unlock()
		if len(tasks) == 0 {
			if in.Kind == "database" {
				return nil, errf("Выберите базу")
			}
			return nil, errf("Выберите сайт")
		}
		user, ip := q.S.User, q.S.IP
		go func() {
			for _, t := range tasks {
				start := time.Now()
				err := t.run()
				e := OpEntry{Time: start, User: user, IP: ip, Action: "Резервная копия (готово)", Object: t.name, Status: "ok", Ms: time.Since(start).Milliseconds()}
				mu.Lock()
				delete(backupJobs, t.key)
				if err != nil {
					e.Status, e.Error = "error", err.Error()
					addNotice("error", "", "Резервная копия "+t.name+" не создана: "+firstLine(err.Error()))
					save()
				}
				mu.Unlock()
				appendJSONL("ops.jsonl", e)
			}
		}()
		return "started", nil
	})
	apiOp("backups.restore", "Резервная копия: восстановление", func(q *Req) (any, error) {
		var in struct {
			ID      string
			Options *BackupRestoreOptions `json:"options"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		options := BackupRestoreOptions{Files: true, Databases: true}
		if in.Options != nil {
			options = *in.Options
		}
		if !options.Files && !options.Databases && !options.SiteSettings && !options.PanelSettings && len(options.Paths) == 0 && len(options.DBs) == 0 {
			return nil, errf("Выберите данные для восстановления")
		}
		return nil, restoreBackup(in.ID, options)
	})
	apiOp("backups.delete", "Резервная копия: удаление", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, deleteBackup(in.ID)
	})
	apiOp("backups.settings", "Резервные копии: расписание", func(q *Req) (any, error) {
		var in struct {
			Daily bool
			Keep  int
			Hour  int
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Keep < 1 || in.Keep > 60 {
			return nil, errf("Хранить от 1 до 60 копий")
		}
		if in.Hour < 0 || in.Hour > 23 {
			return nil, errf("Час от 0 до 23")
		}
		mu.Lock()
		store.Settings.BackupDaily, store.Settings.BackupKeep, store.Settings.BackupHour = in.Daily, in.Keep, in.Hour
		save()
		mu.Unlock()
		return nil, nil
	})
}

func handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	var bk *Backup
	for _, b := range store.Backups {
		if b.ID == id {
			bk = b
		}
	}
	mu.Unlock()
	if bk == nil {
		http.Error(w, "not found", 404)
		return
	}
	file, isTmp, err := fetchBackup(bk)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if isTmp {
		defer os.Remove(P(file))
	}
	f, err := os.Open(P(file))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(bk.File))
	w.Header().Set("Content-Type", "application/gzip")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
