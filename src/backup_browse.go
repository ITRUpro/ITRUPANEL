// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Просмотр содержимого резервной копии: файлы сайта по папкам и базы данных; скачивание и
// восстановление отдельного файла, папки или базы. Индекс файлов берётся из сохранённого при
// создании копии; для копий без индекса (подключённых с FTP) содержимое читается из архива в
// отдельный индекс для просмотра — инкрементные копии на него не опираются.

var reExtractFile = regexp.MustCompile(`^[0-9a-f]{16}/[^/\\]{1,255}$`)

func backupDBsPath(id string) string {
	return P(backupDir + "/.indexes/" + filepath.Base(id) + ".dbs.json")
}
func backupBrowseIndexPath(id string) string {
	return P(backupDir + "/.indexes/" + filepath.Base(id) + ".browse.json")
}

func saveBackupDBs(id string, dbs []string) error {
	if dbs == nil {
		dbs = []string{}
	}
	data, err := json.Marshal(dbs)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(backupDBsPath(id)), 0700); err != nil {
		return err
	}
	return os.WriteFile(backupDBsPath(id), data, 0600)
}
func readBackupDBs(id string) ([]string, bool) {
	data, err := os.ReadFile(backupDBsPath(id))
	if err != nil {
		return nil, false
	}
	var dbs []string
	if json.Unmarshal(data, &dbs) != nil {
		return nil, false
	}
	return dbs, true
}

// backupBrowseIndex — индекс файлов копии: сохранённый при создании или прочитанный из архива.
func backupBrowseIndex(id string) *backupFileIndex {
	if index, err := readBackupIndex(id); err == nil {
		return index
	}
	data, err := os.ReadFile(backupBrowseIndexPath(id))
	if err != nil {
		return nil
	}
	var index backupFileIndex
	if json.Unmarshal(data, &index) != nil || index.Files == nil {
		return nil
	}
	return &index
}

func backupByID(id string) *Backup {
	mu.Lock()
	defer mu.Unlock()
	for _, b := range store.Backups {
		if b.ID == id {
			c := *b
			return &c
		}
	}
	return nil
}

func backupHasFiles(b *Backup) bool { return b.Kind == "full" || b.Kind == "files" }
func backupHasDBs(b *Backup) bool {
	return b.Kind == "full" || b.Kind == "db" || b.Kind == "database"
}

// backupRelPath приводит путь внутри сайта к виду «a/b»; "" — корень сайта.
func backupRelPath(p string) (string, error) {
	p = strings.Trim(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	if p == "" || p == "." {
		return "", nil
	}
	if _, err := backupSafeName(p); err != nil {
		return "", err
	}
	return p, nil
}

func backupMissing(rel string) error {
	if rel == "" {
		return errf("В копии нет файлов сайта")
	}
	return errf("В копии нет: %s", rel)
}

func backupRelPaths(paths []string) ([]string, error) {
	if len(paths) > 1000 {
		return nil, errf("Слишком много выбранных элементов")
	}
	var out []string
	for _, p := range paths {
		rel, err := backupRelPath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

type backupEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

func backupBrowse(id, dir string) (map[string]any, error) {
	b := backupByID(id)
	if b == nil {
		return nil, errf("Копия не найдена")
	}
	dir, err := backupRelPath(dir)
	if err != nil {
		return nil, err
	}
	index := backupBrowseIndex(b.ID)
	dbs, dbsKnown := readBackupDBs(b.ID)
	needsScan := (backupHasFiles(b) && index == nil) || (backupHasDBs(b) && !dbsKnown)
	entries := []backupEntry{}
	if index != nil && backupHasFiles(b) {
		prefix := ""
		if dir != "" {
			prefix = dir + "/"
			if _, ok := index.Files[dir]; !ok {
				return nil, errf("Папка %s не найдена в копии", dir)
			}
		}
		seen := map[string]*backupEntry{}
		for name, st := range index.Files {
			if name == "." || !strings.HasPrefix(name, prefix) {
				continue
			}
			rest := name[len(prefix):]
			child, deeper := rest, false
			if i := strings.Index(rest, "/"); i >= 0 {
				child, deeper = rest[:i], true
			}
			e := seen[child]
			if e == nil {
				e = &backupEntry{Name: child}
				seen[child] = e
			}
			if deeper || os.FileMode(st.Mode).IsDir() {
				e.Dir = true
			} else {
				e.Size, e.Modified = st.Size, st.Modified/int64(time.Second)
			}
		}
		for _, e := range seen {
			entries = append(entries, *e)
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Dir != entries[j].Dir {
				return entries[i].Dir
			}
			return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
		})
	}
	if dir != "" || dbs == nil {
		dbs = []string{}
	}
	return map[string]any{"id": b.ID, "site": b.Site, "kind": b.Kind, "created": b.Created, "path": dir, "entries": entries, "dbs": dbs,
		"needs_scan": needsScan, "has_files": backupHasFiles(b), "has_db": backupHasDBs(b)}, nil
}

// listBackupPart читает заголовки одной части архива: файлы сайта, удалённые пути и дампы баз.
func listBackupPart(b *Backup) (backupMetadata, map[string]backupFileState, []string, error) {
	var meta backupMetadata
	file, temporary, err := fetchBackup(b)
	if err != nil {
		return meta, nil, nil, err
	}
	if temporary {
		defer os.Remove(P(file))
	}
	f, err := os.Open(P(file))
	if err != nil {
		return meta, nil, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return meta, nil, nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]backupFileState{}
	var dbs []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return meta, nil, nil, err
		}
		name, err := backupSafeName(h.Name)
		if err != nil {
			return meta, nil, nil, err
		}
		name = filepath.ToSlash(name)
		switch {
		case name == "meta.json":
			data, err := io.ReadAll(io.LimitReader(tr, 64<<20))
			if err != nil {
				return meta, nil, nil, err
			}
			if meta, err = decodeBackupMetadata(data); err != nil {
				return meta, nil, nil, err
			}
		case strings.HasPrefix(name, "files/"):
			info := h.FileInfo()
			files[strings.TrimPrefix(name, "files/")] = backupFileState{Mode: uint32(info.Mode()), Size: h.Size, Modified: h.ModTime.UnixNano(), Link: h.Linkname}
		case strings.HasPrefix(name, "db/") && strings.HasSuffix(name, ".sql"):
			dbs = append(dbs, strings.TrimSuffix(strings.TrimPrefix(name, "db/"), ".sql"))
		}
	}
	return meta, files, dbs, nil
}

// scanBackupContents собирает содержимое копии по всей цепочке для просмотра.
func scanBackupContents(j *Job, id string) error {
	b := backupByID(id)
	if b == nil {
		return errf("Копия не найдена")
	}
	chain, err := backupChain(b)
	if err != nil {
		return err
	}
	files := map[string]backupFileState{}
	var dbs []string
	for i, part := range chain {
		jobStage(j, fmt.Sprintf("Чтение архива: часть %d из %d", i+1, len(chain)))
		meta, partFiles, partDBs, err := listBackupPart(part)
		if err != nil {
			return err
		}
		if part.ParentID == "" {
			files = map[string]backupFileState{}
		}
		for _, deleted := range meta.Deleted {
			rel := strings.Trim(path.Clean("/"+deleted), "/")
			for name := range files {
				if name == rel || strings.HasPrefix(name, rel+"/") {
					delete(files, name)
				}
			}
		}
		for name, st := range partFiles {
			files[name] = st
		}
		if part.ID == b.ID {
			dbs = partDBs
		}
	}
	if backupHasFiles(b) {
		files["."] = backupFileState{Mode: uint32(os.ModeDir | 0755)}
		data, err := json.Marshal(backupFileIndex{Files: files})
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(backupBrowseIndexPath(b.ID)), 0700); err != nil {
			return err
		}
		if err = os.WriteFile(backupBrowseIndexPath(b.ID), data, 0600); err != nil {
			return err
		}
	}
	return saveBackupDBs(b.ID, dbs)
}

// assembleBackup распаковывает всю цепочку копии во временный каталог (как при восстановлении).
func assembleBackup(j *Job, b *Backup) (string, error) {
	chain, err := backupChain(b)
	if err != nil {
		return "", err
	}
	tmp := backupDir + "/.extract-" + randToken(6)
	if err = os.MkdirAll(P(tmp), 0700); err != nil {
		return "", err
	}
	for i, part := range chain {
		jobStage(j, fmt.Sprintf("Распаковка архива: часть %d из %d", i+1, len(chain)))
		i, n := i, len(chain)
		last := -1
		// Распаковка занимает почти всё время: 1–90 %, остальное — упаковка выбранного.
		report := func(done, total int64) {
			pct := int((float64(i) + float64(done)/float64(total)) / float64(n) * 90)
			if pct != last {
				last = pct
				jobPercent(j, pct)
			}
		}
		if err = unpackBackupPartProgress(part, P(tmp), report); err != nil {
			os.RemoveAll(P(tmp))
			return "", err
		}
	}
	return tmp, nil
}

func backupDownloadsDir() string { return backupDir + "/.downloads" }

// extractBackupItems готовит к скачиванию выбранное из копии: один файл — как есть, одну базу —
// .sql.gz, всё остальное (папки, несколько элементов) — одним архивом .tar.gz.
func extractBackupItems(j *Job, id string, rels, dbs []string) (string, error) {
	b := backupByID(id)
	if b == nil {
		return "", errf("Копия не найдена")
	}
	if len(rels)+len(dbs) == 0 {
		return "", errf("Выберите файлы, папки или базы")
	}
	for _, db := range dbs {
		if !reDBName.MatchString(db) {
			return "", errf("Неверное имя базы")
		}
	}
	// Подготовленные раньше и не скачанные файлы старше суток удаляются.
	if old, err := os.ReadDir(P(backupDownloadsDir())); err == nil {
		for _, e := range old {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				os.RemoveAll(P(backupDownloadsDir() + "/" + e.Name()))
			}
		}
	}
	work, err := assembleBackup(j, b)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(P(work))
	token := randToken(8)
	outDir := backupDownloadsDir() + "/" + token
	if err = os.MkdirAll(P(outDir), 0700); err != nil {
		return "", err
	}
	jobStage(j, "Подготовка файла для скачивания")
	jobPercent(j, 92)
	type item struct{ src, name string }
	var items []item
	for _, rel := range rels {
		src := P(work + "/files")
		name := "files"
		if rel != "" {
			src, name = filepath.Join(src, filepath.FromSlash(rel)), "files/"+rel
		}
		if _, err := os.Lstat(src); err != nil {
			return "", backupMissing(rel)
		}
		items = append(items, item{src, name})
	}
	for _, db := range dbs {
		src := P(work + "/db/" + db + ".sql")
		if _, err := os.Stat(src); err != nil {
			return "", errf("Базы %s нет в копии", db)
		}
		items = append(items, item{src, "db/" + db + ".sql"})
	}
	if len(items) == 1 {
		if len(dbs) == 1 {
			name := dbs[0] + ".sql.gz"
			return token + "/" + name, gzipFile(items[0].src, P(outDir+"/"+name))
		}
		if info, err := os.Lstat(items[0].src); err == nil && info.Mode().IsRegular() {
			name := path.Base(rels[0])
			return token + "/" + name, copyFile(items[0].src, P(outDir+"/"+name))
		}
	}
	name := b.Site + "-" + b.Created.Format("2006-01-02") + ".tar.gz"
	if b.Site == "" {
		name = "backup-" + b.Created.Format("2006-01-02") + ".tar.gz"
	}
	out, err := os.OpenFile(P(outDir+"/"+name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, it := range items {
		if err = tarAdd(tw, it.src, it.name); err != nil {
			break
		}
	}
	for _, e := range []error{tw.Close(), gz.Close(), out.Close()} {
		if err == nil {
			err = e
		}
	}
	return token + "/" + name, err
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	_, err = io.Copy(gz, in)
	if e := gz.Close(); err == nil {
		err = e
	}
	if e := out.Close(); err == nil {
		err = e
	}
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if e := out.Close(); err == nil {
		err = e
	}
	return err
}

// tarAdd добавляет файл или папку (рекурсивно) в архив под именем top.
func tarAdd(tw *tar.Writer, src, top string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(filepath.Join(top, rel))
		if info.IsDir() {
			h.Name += "/"
		}
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func handleBackupExtracted(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("f")
	if !reExtractFile.MatchString(rel) {
		http.Error(w, "bad request", 400)
		return
	}
	dir := P(backupDownloadsDir() + "/" + path.Dir(rel))
	file := filepath.Join(dir, path.Base(rel))
	f, err := os.Open(file)
	if err != nil {
		http.Error(w, "Файл не найден или уже скачан", 404)
		return
	}
	defer os.RemoveAll(dir)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "Файл не найден", 404)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(path.Base(rel))))
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	io.Copy(w, f)
}

func registerBackupBrowse() {
	api("backups.browse", func(q *Req) (any, error) {
		var in struct{ ID, Path string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return backupBrowse(in.ID, in.Path)
	})
	apiOp("backups.scan", "Резервная копия: чтение содержимого", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if backupByID(in.ID) == nil {
			return nil, errf("Копия не найдена")
		}
		id := startJob("backup:scan:"+in.ID, "Чтение содержимого копии", q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Содержимое копии прочитано"}, scanBackupContents(j, in.ID)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("backups.extract", "Резервная копия: скачивание выбранного", func(q *Req) (any, error) {
		var in struct {
			ID         string
			Paths, DBs []string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		rels, err := backupRelPaths(in.Paths)
		if err != nil {
			return nil, err
		}
		if backupByID(in.ID) == nil {
			return nil, errf("Копия не найдена")
		}
		id := startJob("backup:extract:"+in.ID, "Подготовка файла из копии", q.S.User, func(j *Job) (any, error) {
			backupWorkMu.Lock()
			defer backupWorkMu.Unlock()
			file, err := extractBackupItems(j, in.ID, rels, in.DBs)
			if err != nil {
				return nil, err
			}
			return map[string]string{"info": "Файл готов: " + path.Base(file), "download": "/api/backups/extracted?f=" + file}, nil
		})
		return map[string]string{"job": id}, nil
	})
}
