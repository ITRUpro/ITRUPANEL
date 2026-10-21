// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Содержимое копии видно по папкам; выбранный файл и папка скачиваются и восстанавливаются
// по отдельности, остальные файлы сайта не трогаются. Работает и через инкрементную цепочку.
func TestBackupBrowseExtractAndPartialRestore(t *testing.T) {
	s := backupTestSite(t)
	root := P(s.Root)
	put := func(name, value string) {
		t.Helper()
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("index.php", "v1")
	put("wp/config.php", "secret-v1")
	put("wp/uploads/a.jpg", "image")
	o := BackupOptions{Mode: "incremental"}
	if _, err := makeBackup(s.Domain, "files", false, o); err != nil {
		t.Fatal(err)
	}
	put("index.php", "v2")
	b, err := makeBackup(s.Domain, "files", false, o)
	if err != nil || b.ParentID == "" {
		t.Fatalf("incremental copy: %+v %v", b, err)
	}

	r, err := backupBrowse(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var top []string
	for _, e := range r["entries"].([]backupEntry) {
		top = append(top, e.Name)
	}
	if strings.Join(top, ",") != "wp,index.php" {
		t.Fatalf("root listing: %v", top)
	}
	r, _ = backupBrowse(b.ID, "wp")
	var wp []string
	for _, e := range r["entries"].([]backupEntry) {
		wp = append(wp, e.Name)
	}
	sort.Strings(wp)
	if strings.Join(wp, ",") != "config.php,uploads" {
		t.Fatalf("wp listing: %v", wp)
	}
	if _, err := backupBrowse(b.ID, "../etc"); err == nil {
		t.Fatal("path outside the site accepted")
	}

	// Один файл скачивается как есть, несколько элементов — одним архивом.
	file, err := extractBackupItems(&Job{}, b.ID, []string{"index.php"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(P(backupDownloadsDir() + "/" + file)); string(data) != "v2" {
		t.Fatalf("extracted file: %q", data)
	}
	bundle, err := extractBackupItems(&Job{}, b.ID, []string{"wp/uploads", "index.php"}, nil)
	if err != nil || !strings.HasSuffix(bundle, ".tar.gz") {
		t.Fatalf("bundle: %s %v", bundle, err)
	}
	f, _ := os.Open(P(backupDownloadsDir() + "/" + bundle))
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	f.Close()
	if got := strings.Join(names, ","); !strings.Contains(got, "files/wp/uploads/a.jpg") || !strings.Contains(got, "files/index.php") || strings.Contains(got, "config.php") {
		t.Fatalf("bundle entries: %s", got)
	}

	// Восстановление одного файла не трогает остальные файлы сайта.
	put("index.php", "broken")
	put("wp/config.php", "secret-v2")
	put("new.txt", "keep me")
	if err := restoreBackup(b.ID, BackupRestoreOptions{Paths: []string{"index.php"}}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"index.php": "v2", "wp/config.php": "secret-v2", "new.txt": "keep me"} {
		if data, _ := os.ReadFile(filepath.Join(root, name)); string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
	// Папка восстанавливается целиком по своему пути.
	put("wp/uploads/extra.jpg", "extra")
	if err := restoreBackup(b.ID, BackupRestoreOptions{Paths: []string{"wp"}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "wp/config.php")); string(data) != "secret-v1" {
		t.Fatalf("wp/config.php = %q", data)
	}
	if _, err := os.Stat(filepath.Join(root, "wp/uploads/extra.jpg")); !os.IsNotExist(err) {
		t.Fatal("restored folder must match the copy")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(data) != "keep me" {
		t.Fatal("files outside the restored folder changed")
	}
}

// У копии без индекса (подключённой с FTP) содержимое читается из архивов цепочки.
func TestBackupScanWithoutIndex(t *testing.T) {
	s := backupTestSite(t)
	os.WriteFile(P(s.Root+"/keep.txt"), []byte("1"), 0644)
	os.WriteFile(P(s.Root+"/gone.txt"), []byte("1"), 0644)
	o := BackupOptions{Mode: "incremental"}
	if _, err := makeBackup(s.Domain, "files", false, o); err != nil {
		t.Fatal(err)
	}
	os.Remove(P(s.Root + "/gone.txt"))
	os.WriteFile(P(s.Root+"/new.txt"), []byte("2"), 0644)
	b, err := makeBackup(s.Domain, "files", false, o)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(backupIndexPath(b.ID))
	if r, _ := backupBrowse(b.ID, ""); r["needs_scan"] != true {
		t.Fatal("copy without index must ask to read the archive")
	}
	if err := scanBackupContents(&Job{}, b.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := backupBrowse(b.ID, "")
	var names []string
	for _, e := range r["entries"].([]backupEntry) {
		names = append(names, e.Name)
	}
	if r["needs_scan"] == true || strings.Join(names, ",") != "keep.txt,new.txt" {
		t.Fatalf("scanned listing: %v needs_scan=%v", names, r["needs_scan"])
	}
	if _, err := os.Stat(backupIndexPath(b.ID)); !os.IsNotExist(err) {
		t.Fatal("scan must not create the incremental index")
	}
}

// API восстановления принимает выбор из содержимого копии без общих флажков.
func TestBackupRestoreAPIAcceptsSelection(t *testing.T) {
	s := backupTestSite(t)
	mux := testPanel(t)
	store.Sites, store.Users = []*Site{s}, []*SiteUser{{Name: s.User, Home: userHome(s.User)}}
	os.MkdirAll(P(s.Root), 0755)
	os.WriteFile(P(s.Root+"/index.php"), []byte("v1"), 0644)
	b, err := makeBackup(s.Domain, "files", false, BackupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(P(s.Root+"/index.php"), []byte("broken"), 0644)
	if _, r := testAPI(t, mux, "backups.restore", `{"id":"`+b.ID+`","options":{"paths":["index.php"]}}`); r["error"] != nil {
		t.Fatalf("restore selection: %v", r["error"])
	}
	if data, _ := os.ReadFile(P(s.Root + "/index.php")); string(data) != "v1" {
		t.Fatalf("index.php = %q", data)
	}
}
