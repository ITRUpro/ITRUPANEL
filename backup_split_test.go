// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Файлы сайтов, каждая база и настройки панели копируются отдельно; по умолчанию каждый день
// копируются все базы сервера, в том числе не привязанные к сайту.
func TestAutoBackupsSplitSitesDatabasesPanel(t *testing.T) {
	s := backupTestSite(t)
	os.WriteFile(filepath.Join(P(s.Root), "index.php"), []byte("site"), 0644)
	store.DBs = []*DB{{Name: "shop_db", Site: s.Domain}, {Name: "orphan_db"}}
	store.Settings.BackupKeep, store.Settings.BackupPanelSettings = 2, true
	for i := 0; i < 3; i++ {
		autoBackups()
	}
	count := map[string]int{}
	for _, b := range store.Backups {
		count[backupGroup(b)]++
		switch b.Kind {
		case "files":
			if names := strings.Join(archiveNames(t, b), ","); strings.Contains(names, "db/") || !strings.Contains(names, "files/index.php") {
				t.Fatalf("site backup must hold only files: %s", names)
			}
		case "database":
			if base := filepath.Base(b.File); !backupDBArchiveName.MatchString(base) || b.Site != "" {
				t.Fatalf("database backup: %s %+v", base, b)
			}
			if names := strings.Join(archiveNames(t, b), ","); names != "meta.json,db,db/"+b.DB+".sql" {
				t.Fatalf("database archive: %s", names)
			}
		case "panel":
			if filepath.Base(b.File) != "panel_"+b.ID+".tar.gz" || !b.PanelSettings {
				t.Fatalf("panel backup: %+v", b)
			}
		default:
			t.Fatalf("unexpected kind %q", b.Kind)
		}
	}
	want := map[string]int{s.Domain: 2, "db:shop_db": 2, "db:orphan_db": 2, "panel:": 2}
	for k, v := range want {
		if count[k] != v {
			t.Fatalf("kept copies %v, want %v", count, want)
		}
	}

	store.Backups, store.Settings.BackupSkipDBs, store.Settings.BackupPanelSettings = nil, true, false
	autoBackups()
	if len(store.Backups) != 1 || store.Backups[0].Kind != "files" {
		t.Fatalf("databases must be skipped only when switched off: %+v", store.Backups)
	}
}

// Копия базы скачивается как .sql.gz и восстанавливается отдельно; без базы в панели
// восстановление недоступно.
func TestDatabaseBackupDownloadAndRestore(t *testing.T) {
	backupTestSite(t)
	store.DBs = []*DB{{Name: "orphan_db"}}
	b, err := makeDBBackup("orphan_db", false)
	if err != nil {
		t.Fatal(err)
	}
	file, err := extractBackupItems(&Job{}, b.ID, nil, []string{"orphan_db"})
	if err != nil || filepath.Base(file) != "orphan_db.sql.gz" {
		t.Fatalf("download: %s %v", file, err)
	}
	f, _ := os.Open(P(backupDownloadsDir() + "/" + file))
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(gz)
	f.Close()
	if !strings.Contains(string(data), "orphan_db") {
		t.Fatalf("dump: %q", data)
	}
	if err := restoreBackup(b.ID, BackupRestoreOptions{Databases: true}); err != nil {
		t.Fatal(err)
	}
	if issue := backupRestoreIssue(b); issue != "" {
		t.Fatalf("restore issue: %s", issue)
	}
	store.DBs = nil
	if issue := backupRestoreIssue(b); !strings.Contains(issue, "orphan_db") {
		t.Fatalf("missing database must be reported: %q", issue)
	}
	if err := restoreBackup(b.ID, BackupRestoreOptions{Databases: true}); err == nil {
		t.Fatal("restore into a missing database accepted")
	}
}

func TestPanelBackupRestore(t *testing.T) {
	backupTestSite(t)
	store.Settings.Email = "before@example.test"
	b, err := makePanelBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	store.Settings.Email = "after@example.test"
	if err := restoreBackup(b.ID, BackupRestoreOptions{BackupOptions: BackupOptions{PanelSettings: true}}); err != nil {
		t.Fatal(err)
	}
	if store.Settings.Email != "before@example.test" {
		t.Fatalf("panel settings not restored: %s", store.Settings.Email)
	}
}

// Копии баз и настроек панели находятся на FTP по манифесту так же, как копии сайтов.
func TestDiscoverDatabaseAndPanelManifests(t *testing.T) {
	backupTestSite(t)
	store.DBs = []*DB{{Name: "Shop_DB"}}
	db, err := makeDBBackup("Shop_DB", false)
	if err != nil {
		t.Fatal(err)
	}
	panel, err := makePanelBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	inspect := func(b *Backup, name string) (*Backup, error) {
		f, err := os.Open(P(b.File))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		return inspectBackupManifest(f, name, BackupFTP{Host: "ftp.test", User: "u", Dir: "/b"})
	}
	names, _ := discoverFTPNames(strings.NewReader(filepath.Base(db.File) + "\n" + filepath.Base(panel.File) + "\n"))
	if len(names) != 2 {
		t.Fatalf("listing: %v", names)
	}
	got, err := inspect(db, filepath.Base(db.File))
	if err != nil || got.Kind != "database" || got.DB != "Shop_DB" || got.Site != "" {
		t.Fatalf("database manifest: %+v %v", got, err)
	}
	if _, err := inspect(db, "db-Other_DB_"+db.ID+".tar.gz"); err == nil {
		t.Fatal("database name mismatch accepted")
	}
	got, err = inspect(panel, filepath.Base(panel.File))
	if err != nil || got.Kind != "panel" || !got.PanelSettings {
		t.Fatalf("panel manifest: %+v %v", got, err)
	}
	if _, err := inspect(panel, "example.test_"+panel.ID+".tar.gz"); err == nil {
		t.Fatal("panel manifest under a site name accepted")
	}
}
