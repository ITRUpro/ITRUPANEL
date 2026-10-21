// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"
)

// Копии разделены на три вида: файлы сайта (kind files/settings, а также старые full и db),
// отдельная база данных (kind database, по копии на базу) и настройки панели (kind panel).
// Формат архива общий: meta.json первым, затем db/…; поэтому FTP, цепочки и поиск копий
// работают для всех видов одинаково.

var backupDBArchiveName = regexp.MustCompile(`^db-([A-Za-z0-9_]{1,64})_([0-9]{8}-[0-9]{6}-[a-f0-9]+)\.tar\.gz$`)

func backupIsSite(b *Backup) bool { return b.Kind != "database" && b.Kind != "panel" }

// backupGroup — к чему относится копия: для хранения последних N автоматических копий и
// для отметки «создаётся» в списке.
func backupGroup(b *Backup) string {
	switch b.Kind {
	case "database":
		return "db:" + b.DB
	case "panel":
		return "panel:"
	}
	return b.Site
}

// newBackupID создаёт временную папку копии с пустым разделом баз.
func newBackupWork() (id, tmp string, err error) {
	id = time.Now().Format("20060102-150405") + "-" + randToken(2)
	tmp = backupDir + "/.tmp-" + id
	err = os.MkdirAll(P(tmp+"/db"), 0700)
	return
}

func writeBackupMeta(tmp string, meta backupMetadata) error {
	data, err := json.MarshalIndent(meta, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(P(tmp+"/meta.json"), data, 0600)
}

// storeNewBackup записывает готовый архив в список копий и отправляет его на FTP.
func storeNewBackup(b *Backup) (*Backup, error) {
	os.Chmod(P(b.File), 0600)
	if fi, err := os.Stat(P(b.File)); err == nil {
		b.Size = fi.Size()
	}
	b.Local = true
	if b.Mode == "" {
		b.Mode = "full"
	}
	mu.Lock()
	store.Backups = append(store.Backups, b)
	if err := saveChecked(); err != nil {
		store.Backups = store.Backups[:len(store.Backups)-1]
		mu.Unlock()
		os.Remove(P(b.File))
		os.Remove(backupIndexPath(b.ID))
		os.Remove(backupDBsPath(b.ID))
		return nil, err
	}
	mu.Unlock()
	if err := uploadBackup(b); err != nil {
		return b, err
	}
	return b, nil
}

// makeDBBackup сохраняет одну базу данных отдельной копией.
func makeDBBackup(name string, auto bool) (*Backup, error) {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	mu.Lock()
	d := findDB(name)
	var server string
	if d != nil {
		server = d.Server
	}
	mu.Unlock()
	if d == nil {
		return nil, errf("База %s не найдена", name)
	}
	id, tmp, err := newBackupWork()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(P(tmp))
	meta := backupMetadata{Version: 2, Kind: "database", Created: time.Now(), DBs: []string{name}, DBServers: map[string]string{name: server}}
	if err = writeBackupMeta(tmp, meta); err != nil {
		return nil, err
	}
	if err = dbDumpTo(server, name, tmp+"/db/"+name+".sql"); err != nil {
		return nil, errf("mysqldump %s: %v", name, err)
	}
	file := fmt.Sprintf("%s/db-%s_%s.tar.gz", backupDir, name, id)
	if err = writeBackupArchive(P(file), P(tmp), "", nil, ""); err != nil {
		os.Remove(P(file))
		return nil, err
	}
	saveBackupDBs(id, []string{name})
	return storeNewBackup(&Backup{ID: id, File: file, Kind: "database", DB: name, Created: time.Now(), Auto: auto})
}

// makePanelBackup сохраняет переносимые настройки панели (без учётной записи, адреса и ключей).
func makePanelBackup(auto bool) (*Backup, error) {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	mu.Lock()
	panel := capturePanelBackupSettings(store.Settings)
	mu.Unlock()
	id, tmp, err := newBackupWork()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(P(tmp))
	meta := backupMetadata{Version: 2, Kind: "panel", Created: time.Now(), Panel: &panel, DBs: []string{}, DBServers: map[string]string{}}
	if err = writeBackupMeta(tmp, meta); err != nil {
		return nil, err
	}
	file := fmt.Sprintf("%s/panel_%s.tar.gz", backupDir, id)
	if err = writeBackupArchive(P(file), P(tmp), "", nil, ""); err != nil {
		os.Remove(P(file))
		return nil, err
	}
	return storeNewBackup(&Backup{ID: id, File: file, Kind: "panel", PanelSettings: true, Created: time.Now(), Auto: auto})
}

// restoreDBBackup заменяет базу содержимым копии. База должна существовать в панели:
// так известны её сервер и владелец.
func restoreDBBackup(b *Backup) error {
	mu.Lock()
	d := findDB(b.DB)
	var server string
	if d != nil {
		server = d.Server
	}
	mu.Unlock()
	if d == nil {
		return errf("Сначала создайте базу %s в разделе «Базы данных»", b.DB)
	}
	tmp, err := unpackBackupTemp(b)
	if err != nil {
		return err
	}
	defer os.RemoveAll(P(tmp))
	dump := tmp + "/db/" + b.DB + ".sql"
	if !fileExists(dump) {
		return errf("Базы %s нет в копии", b.DB)
	}
	return dbImportAsRoot(server, b.DB, dump)
}

func restorePanelBackup(b *Backup) error {
	tmp, err := unpackBackupTemp(b)
	if err != nil {
		return err
	}
	defer os.RemoveAll(P(tmp))
	data, err := os.ReadFile(P(tmp + "/meta.json"))
	if err != nil {
		return err
	}
	m, err := decodeBackupMetadata(data)
	if err != nil {
		return err
	}
	if m.Version != 2 || m.Kind != "panel" || m.Panel == nil {
		return errf("В копии нет настроек панели")
	}
	if err = m.Panel.validate(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	old := store.Settings
	m.Panel.apply(&store.Settings)
	if err = saveChecked(); err != nil {
		store.Settings = old
		return fmt.Errorf("Сохранение настроек: %w", err)
	}
	return nil
}

func unpackBackupTemp(b *Backup) (string, error) {
	tmp := backupDir + "/.restore-" + randToken(4)
	if err := os.MkdirAll(P(tmp), 0700); err != nil {
		return "", err
	}
	if err := unpackBackupPart(b, P(tmp)); err != nil {
		os.RemoveAll(P(tmp))
		return "", err
	}
	return tmp, nil
}

// pruneAutoBackups оставляет keep последних автоматических копий группы.
func pruneAutoBackups(group string, keep int) {
	mu.Lock()
	var autos []*Backup
	for _, b := range store.Backups {
		if b.Auto && backupGroup(b) == group {
			autos = append(autos, b)
		}
	}
	sort.Slice(autos, func(i, j int) bool { return autos[i].Created.After(autos[j].Created) })
	var old []string
	for i, b := range autos {
		if i >= keep {
			old = append(old, b.ID)
		}
	}
	mu.Unlock()
	for _, id := range old {
		deleteBackup(id)
	}
}
