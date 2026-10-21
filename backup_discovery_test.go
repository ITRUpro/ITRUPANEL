// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

func discoveryArchive(t *testing.T, m backupMetadata) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data, _ := json.Marshal(m)
	if err := tw.WriteHeader(&tar.Header{Name: "meta.json", Mode: 0600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(data)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}
func discoveryMeta() backupMetadata {
	return backupMetadata{Version: 2, Domain: "example.test", Kind: "files", Created: time.Now().UTC()}
}
func TestBackupDiscoveryListsOnlySafeUniqueArchives(t *testing.T) {
	name := "example.test_20261013-120000-aabb.tar.gz"
	names, err := discoverFTPNames(strings.NewReader(name + "\r\n../" + name + "\n" + name + "\nnotes.txt\n"))
	if err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("%v %v", names, err)
	}
	if _, err = discoverFTPNames(strings.NewReader(strings.Repeat("x", (1<<20)+1))); err == nil {
		t.Fatal("unbounded listing")
	}
}
func TestBackupDiscoveryRejectsInvalidManifests(t *testing.T) {
	f := BackupFTP{Host: "ftp.example.test", User: "backup", Dir: "/old"}
	name := "example.test_20261013-120000-aabb.tar.gz"
	for _, tc := range []struct {
		name string
		edit func(*backupMetadata)
	}{
		{"wrong domain", func(m *backupMetadata) { m.Domain = "other.test" }},
		{"future version", func(m *backupMetadata) { m.Version = 999 }},
		{"missing date", func(m *backupMetadata) { m.Created = time.Time{} }},
		{"parent traversal", func(m *backupMetadata) { m.ParentID = "../../root" }},
		{"self parent", func(m *backupMetadata) { m.ParentID = "20261013-120000-aabb" }},
		{"deleted traversal", func(m *backupMetadata) { m.Deleted = []string{"../../secret"} }},
		{"unknown kind", func(m *backupMetadata) { m.Kind = "root" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := discoveryMeta()
			tc.edit(&m)
			if _, err := inspectBackupManifest(bytes.NewReader(discoveryArchive(t, m)), name, f); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}
func TestBackupDiscoveryImportIsIdempotentAndPreservesArchives(t *testing.T) {
	s := backupTestSite(t)
	f := BackupFTP{Host: "ftp.example.test", User: "backup", Dir: "/old"}
	store.Settings.BackupFTP = f
	m := discoveryMeta()
	name := "example.test_20261013-120000-aabb.tar.gz"
	b, err := inspectBackupManifest(bytes.NewReader(discoveryArchive(t, m)), name, f)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Imported || b.Auto || b.Local || b.Remote != "/old/"+name || b.ManifestHash == "" {
		t.Fatalf("bad import: %+v", b)
	}
	n, _, err := mergeDiscoveredBackups([]*Backup{b})
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	n, old, err := mergeDiscoveredBackups([]*Backup{b})
	if err != nil || n != 0 || old != 1 || len(store.Backups) != 1 {
		t.Fatalf("duplicate: %d %d %v", n, old, err)
	}
	conflict := *b
	conflict.Remote = "/other/" + name
	if _, _, err = mergeDiscoveredBackups([]*Backup{&conflict}); err == nil || store.Backups[0].Remote != b.Remote {
		t.Fatal("ID collision changed state")
	}
	if issue := backupRestoreIssue(b); issue != "" {
		t.Fatal(issue)
	}
	store.Sites = nil
	if issue := backupRestoreIssue(b); issue == "" {
		t.Fatal("missing site")
	}
	store.Sites = []*Site{s}
	store.Settings.BackupFTP.Host = "other.test"
	if issue := backupRestoreIssue(b); issue == "" {
		t.Fatal("wrong FTP source")
	}
	store.Settings.BackupFTP = f
	mode, parent, _, _, err := prepareIncrementalBackup(*s, "files", false, BackupOptions{Mode: "incremental"})
	if err != nil || mode != "full" || parent != "" {
		t.Fatalf("new chain not full: %s %s %v", mode, parent, err)
	}
}
func TestBackupDiscoveryMissingAndCyclicChains(t *testing.T) {
	backupTestSite(t)
	f := BackupFTP{Host: "ftp.example.test", User: "backup"}
	store.Settings.BackupFTP = f
	m := discoveryMeta()
	m.ParentID = "20261013-120000-aabb"
	b, err := inspectBackupManifest(bytes.NewReader(discoveryArchive(t, m)), "example.test_20261013-130000-aabb.tar.gz", f)
	if err != nil {
		t.Fatal(err)
	}
	mergeDiscoveredBackups([]*Backup{b})
	if issue := backupRestoreIssue(b); !strings.Contains(issue, m.ParentID) {
		t.Fatal(issue)
	}
	base := *b
	base.ID = m.ParentID
	base.ParentID = ""
	base.Mode = "full"
	base.Remote = "/base.tar.gz"
	mergeDiscoveredBackups([]*Backup{&base})
	if issue := backupRestoreIssue(b); issue != "" {
		t.Fatal(issue)
	}
	base.ParentID = b.ID
	if issue := backupRestoreIssue(b); issue != "Повреждена цепочка копий" {
		t.Fatal(issue)
	}
}
func TestDiscoveredBackupRestoresFilesOnNewStateAndChecksManifest(t *testing.T) {
	s := backupTestSite(t)
	os.WriteFile(P(s.Root+"/hello.txt"), []byte("saved"), 0600)
	original, err := makeBackup(s.Domain, "files", false, BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(P(original.File))
	if err != nil {
		t.Fatal(err)
	}
	f := BackupFTP{Host: "ftp.example.test", User: "backup", Dir: "/old"}
	store.Settings.BackupFTP = f
	imported, err := inspectBackupManifest(bytes.NewReader(archive), path.Base(original.File), f)
	if err != nil {
		t.Fatal(err)
	}
	store.Backups = nil
	mergeDiscoveredBackups([]*Backup{imported})
	// Emulate the downloaded archive: all state originated from FTP metadata.
	imported.Local = true
	os.WriteFile(P(s.Root+"/hello.txt"), []byte("changed"), 0600)
	if err = restoreBackup(imported.ID, BackupRestoreOptions{Files: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(P(s.Root + "/hello.txt"))
	if string(data) != "saved" {
		t.Fatal(string(data))
	}
	imported.ManifestHash = strings.Repeat("0", 64)
	os.WriteFile(P(s.Root+"/hello.txt"), []byte("keep"), 0600)
	if err = restoreBackup(imported.ID, BackupRestoreOptions{Files: true}); err == nil {
		t.Fatal("modified manifest accepted")
	}
	data, _ = os.ReadFile(P(s.Root + "/hello.txt"))
	if string(data) != "keep" {
		t.Fatal("failed validation changed live files")
	}
}
func TestBackupInspectionDoesNotReadWholeArchive(t *testing.T) {
	data := discoveryArchive(t, discoveryMeta())
	r := io.MultiReader(bytes.NewReader(data), errorReader{})
	if _, err := inspectBackupManifest(r, "example.test_20261013-120000-aabb.tar.gz", BackupFTP{}); err != nil {
		t.Fatal(err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { panic("read beyond manifest") }
