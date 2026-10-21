// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func backupTestSite(t *testing.T) *Site {
	t.Helper()
	testPanel(t)
	s := &Site{Domain: "example.test", User: "alice", Root: "/var/www/alice/data/www/example.test", Mode: "static"}
	store.Sites = []*Site{s}
	if err := os.MkdirAll(P(s.Root), 0755); err != nil {
		t.Fatal(err)
	}
	return s
}
func archiveNames(t *testing.T, b *Backup) []string {
	t.Helper()
	f, err := os.Open(P(b.File))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	r := tar.NewReader(g)
	var names []string
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}
func TestIncrementalArchiveRestoresChangedNewDeletedAndLinks(t *testing.T) {
	s := backupTestSite(t)
	root := P(s.Root)
	put := func(name, value string) {
		t.Helper()
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(value), 0640); err != nil {
			t.Fatal(err)
		}
	}
	put("unchanged.txt", "same")
	put("changed.txt", "before")
	put("removed/sub.txt", "delete me")
	put("becomes-link", "old file")
	o := BackupOptions{Mode: "incremental", SiteSettings: true}
	first, err := makeBackup(s.Domain, "files", false, o)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode != "full" || first.ParentID != "" {
		t.Fatal("first copy must be full")
	}
	put("changed.txt", "after")
	put("new/nested.txt", "new")
	os.RemoveAll(filepath.Join(root, "removed"))
	os.Remove(filepath.Join(root, "becomes-link"))
	os.Symlink("changed.txt", filepath.Join(root, "becomes-link"))
	second, err := makeBackup(s.Domain, "files", false, o)
	if err != nil {
		t.Fatal(err)
	}
	if second.Mode != "incremental" || second.ParentID != first.ID {
		t.Fatal("incremental parent missing")
	}
	names := strings.Join(archiveNames(t, second), "\n")
	if strings.Contains(names, "unchanged.txt") || !strings.Contains(names, "changed.txt") || strings.Contains(names, "removed/sub.txt") {
		t.Fatal("wrong delta contents", names)
	}
	chain, err := backupChain(second)
	if err != nil || len(chain) != 2 {
		t.Fatal("chain", err)
	}
	dest := t.TempDir()
	for _, b := range chain {
		if err := unpackBackupPart(b, dest); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"unchanged.txt": "same", "changed.txt": "after", "new/nested.txt": "new"} {
		data, err := os.ReadFile(filepath.Join(dest, "files", name))
		if err != nil || string(data) != want {
			t.Fatal(name, string(data), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "files/removed")); !os.IsNotExist(err) {
		t.Fatal("deleted directory restored")
	}
	link, err := os.Readlink(filepath.Join(dest, "files/becomes-link"))
	if err != nil || link != "changed.txt" {
		t.Fatal("link", link, err)
	}
	if err := deleteBackup(first.ID); err == nil {
		t.Fatal("required parent deleted")
	}
	if err := deleteBackup(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := deleteBackup(first.ID); err != nil {
		t.Fatal(err)
	}
}
func TestIncrementalCheckpointAndMissingIndex(t *testing.T) {
	s := backupTestSite(t)
	os.WriteFile(P(s.Root+"/a"), []byte("a"), 0644)
	o := BackupOptions{Mode: "incremental"}
	var latest *Backup
	for i := 0; i < 8; i++ {
		b, err := makeBackup(s.Domain, "files", true, o)
		if err != nil {
			t.Fatal(err)
		}
		latest = b
		if (i == 0 || i == 7) != (b.Mode == "full") {
			t.Fatalf("checkpoint %d: %s", i, b.Mode)
		}
	}
	os.Remove(backupIndexPath(latest.ID))
	b, err := makeBackup(s.Domain, "files", true, o)
	if err != nil || b.Mode != "full" {
		t.Fatal("missing index must start full", err)
	}
	b, err = makeBackup(s.Domain, "settings", true, BackupOptions{Mode: "incremental", PanelSettings: true})
	if err != nil || b.Mode != "full" {
		t.Fatal("settings without files must remain independent", err)
	}
}
func TestIncrementalChainMissingParentFails(t *testing.T) {
	backupTestSite(t)
	b := &Backup{ID: "child", Site: "example.test", Kind: "files", ParentID: "missing"}
	store.Backups = []*Backup{b}
	if _, err := backupChain(b); err == nil {
		t.Fatal("missing parent accepted")
	}
	b.ParentID = b.ID
	if _, err := backupChain(b); err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestBackupExtractionRejectsEscapingSymlink(t *testing.T) {
	backupTestSite(t)
	file := backupDir + "/unsafe.tar.gz"
	os.MkdirAll(P(backupDir), 0700)
	f, _ := os.Create(P(file))
	g := gzip.NewWriter(f)
	w := tar.NewWriter(g)
	m, _ := json.Marshal(backupMetadata{Version: 2, Domain: "example.test"})
	w.WriteHeader(&tar.Header{Name: "meta.json", Mode: 0600, Size: int64(len(m)), Typeflag: tar.TypeReg})
	w.Write(m)
	w.WriteHeader(&tar.Header{Name: "files", Mode: 0755, Typeflag: tar.TypeDir})
	outside := t.TempDir()
	w.WriteHeader(&tar.Header{Name: "files/escape", Linkname: outside, Typeflag: tar.TypeSymlink})
	w.WriteHeader(&tar.Header{Name: "files/escape/stolen", Size: 1, Mode: 0600, Typeflag: tar.TypeReg})
	w.Write([]byte("x"))
	w.Close()
	g.Close()
	f.Close()
	b := &Backup{File: file, Local: true, Site: "example.test"}
	if err := unpackBackupPart(b, t.TempDir()); err == nil {
		t.Fatal("archive traversed symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "stolen")); !os.IsNotExist(err) {
		t.Fatal("wrote outside staging")
	}
}

func TestAutomaticIncrementalRetentionKeepsRequiredParents(t *testing.T) {
	s := backupTestSite(t)
	os.WriteFile(P(s.Root+"/a"), []byte("a"), 0644)
	store.Settings.BackupMode = "incremental"
	store.Settings.BackupKeep = 2
	for i := 0; i < 10; i++ {
		autoBackups()
	}
	if len(store.Backups) > 8 {
		t.Fatalf("retention grew beyond restore points plus required parents: %d", len(store.Backups))
	}
	for _, b := range store.Backups {
		chain, err := backupChain(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range chain {
			if !fileExists(part.File) {
				t.Fatal("retention deleted required archive")
			}
		}
	}
}
