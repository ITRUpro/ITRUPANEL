// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type backupFileState struct {
	Hash     string `json:"hash,omitempty"`
	Link     string `json:"link,omitempty"`
	Mode     uint32 `json:"mode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type backupFileIndex struct {
	Root    string                     `json:"root"`
	Files   map[string]backupFileState `json:"files"`
	Changed map[string]bool            `json:"-"`
}

func backupIndexPath(id string) string {
	return P(backupDir + "/.indexes/" + filepath.Base(id) + ".json")
}
func saveBackupIndex(id, root string, index *backupFileIndex) error {
	index.Root = root
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	path := backupIndexPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func readBackupIndex(id string) (*backupFileIndex, error) {
	data, err := os.ReadFile(backupIndexPath(id))
	if err != nil {
		return nil, err
	}
	var index backupFileIndex
	if err = json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index.Files == nil {
		return nil, errf("Нет индекса файлов копии")
	}
	return &index, nil
}
func openBackupSiteRoot(root string, boundary []string) (*os.Root, error) {
	if len(boundary) == 0 {
		return os.OpenRoot(root)
	}
	base, e := os.OpenRoot(boundary[0])
	if e != nil {
		return nil, e
	}
	defer base.Close()
	rel, e := filepath.Rel(boundary[0], root)
	if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, forbidden()
	}
	return base.OpenRoot(rel)
}
func scanBackupFiles(root string, boundary ...string) (*backupFileIndex, error) {
	r, err := openBackupSiteRoot(root, boundary)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	index := &backupFileIndex{Files: map[string]backupFileState{}, Changed: map[string]bool{}}
	err = fs.WalkDir(r.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := r.Lstat(name)
		if e != nil {
			return e
		}
		if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		st := backupFileState{Mode: uint32(info.Mode()), Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if info.Mode()&os.ModeSymlink != 0 {
			st.Link, e = r.Readlink(name)
		} else if info.Mode().IsRegular() {
			f, err := r.Open(name)
			if err != nil {
				return err
			}
			opened, err := f.Stat()
			if err != nil || !opened.Mode().IsRegular() {
				f.Close()
				return errf("Файл изменился во время копирования")
			}
			h := sha256.New()
			_, e = io.Copy(h, f)
			f.Close()
			st.Hash = hex.EncodeToString(h.Sum(nil))
		}
		if e != nil {
			return e
		}
		index.Files[name] = st
		index.Changed[name] = true
		return nil
	})
	return index, err
}
func prepareIncrementalBackup(site Site, kind string, auto bool, o BackupOptions) (string, string, *backupFileIndex, []string, error) {
	if kind != "files" && kind != "full" {
		return "full", "", nil, nil, nil
	}
	next, err := scanBackupFiles(P(site.Root), P(userHome(site.User)))
	if err != nil {
		return "", "", nil, nil, err
	}
	next.Root = site.Root
	if o.Mode != "incremental" {
		return "full", "", next, nil, nil
	}
	mu.Lock()
	var candidate *Backup
	for _, b := range store.Backups {
		if !b.Imported && b.Site == site.Domain && b.Kind == kind && b.Auto == auto && b.SiteSettings == o.SiteSettings && b.PanelSettings == o.PanelSettings && (candidate == nil || b.Created.After(candidate.Created)) {
			copy := *b
			candidate = &copy
		}
	}
	mu.Unlock()
	if candidate == nil {
		return "full", "", next, nil, nil
	}
	previous, err := readBackupIndex(candidate.ID)
	if err != nil || previous.Root != site.Root {
		return "full", "", next, nil, nil
	}
	chain, err := backupChain(candidate)
	// Periodic full checkpoints bound restore time and retention overhead.
	if err != nil || len(chain) >= 7 {
		return "full", "", next, nil, nil
	}
	for _, part := range chain {
		if !part.Local && part.Remote == "" || part.Local && !fileExists(part.File) && part.Remote == "" {
			return "full", "", next, nil, nil
		}
	}
	next.Changed = map[string]bool{".": true}
	for name, st := range next.Files {
		if old, ok := previous.Files[name]; !ok || old != st {
			next.Changed[name] = true
		}
	}
	var deleted []string
	for name := range previous.Files {
		if _, ok := next.Files[name]; !ok {
			deleted = append(deleted, name)
		}
	}
	sort.Strings(deleted)
	return "incremental", candidate.ID, next, deleted, nil
}
func writeBackupArchive(file, tmp, root string, index *backupFileIndex, parent string, boundary ...string) (err error) {
	var siteRoot *os.Root
	if index != nil {
		siteRoot, err = openBackupSiteRoot(root, boundary)
		if err != nil {
			return err
		}
		defer siteRoot.Close()
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	defer func() {
		for _, e := range []error{tw.Close(), gz.Close(), f.Close()} {
			if err == nil {
				err = e
			}
		}
	}()
	add := func(path, name string, expected *backupFileState) error {
		var info os.FileInfo
		var err error
		readPath := path
		if expected != nil {
			readPath, err = filepath.Rel(root, path)
			if err != nil {
				return err
			}
			info, err = siteRoot.Lstat(readPath)
		} else {
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if expected != nil {
				link, err = siteRoot.Readlink(readPath)
			} else {
				link, err = os.Readlink(path)
			}
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = name
		if expected != nil && (uint32(info.Mode()) != expected.Mode || info.Size() != expected.Size || info.ModTime().UnixNano() != expected.Modified || link != expected.Link) {
			return errf("Файл изменился во время копирования: %s. Повторите копирование", name)
		}
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		var source *os.File
		if expected != nil {
			source, err = siteRoot.Open(readPath)
		} else {
			source, err = os.Open(path)
		}
		if err != nil {
			return err
		}
		defer source.Close()
		opened, e := source.Stat()
		if e != nil || !opened.Mode().IsRegular() {
			return errf("Файл изменился во время копирования")
		}
		h := sha256.New()
		if _, err = io.Copy(tw, io.TeeReader(source, h)); err != nil {
			return err
		}
		if expected != nil && hex.EncodeToString(h.Sum(nil)) != expected.Hash {
			return errf("Файл изменился во время копирования: %s. Повторите копирование", name)
		}
		return nil
	}
	// Manifest first: readers need not unpack website files to inspect settings.
	if err = add(filepath.Join(tmp, "meta.json"), "meta.json", nil); err != nil {
		return err
	}
	if err = filepath.Walk(filepath.Join(tmp, "db"), func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		name, e := filepath.Rel(tmp, path)
		if e != nil {
			return e
		}
		return add(path, filepath.ToSlash(name), nil)
	}); err != nil {
		return err
	}
	if index != nil {
		names := make([]string, 0, len(index.Changed))
		for name := range index.Changed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			st := index.Files[name]
			archiveName := "files"
			if name != "." {
				archiveName += "/" + name
			}
			if err = add(filepath.Join(root, filepath.FromSlash(name)), archiveName, &st); err != nil {
				return err
			}
		}
	}
	return nil
}
func backupChain(b *Backup) ([]*Backup, error) {
	mu.Lock()
	defer mu.Unlock()
	byID := map[string]*Backup{}
	for _, v := range store.Backups {
		copy := *v
		byID[v.ID] = &copy
	}
	var reversed []*Backup
	seen := map[string]bool{}
	for b != nil {
		if seen[b.ID] || len(reversed) > 128 {
			return nil, errf("Повреждена цепочка резервных копий")
		}
		seen[b.ID] = true
		copy := *b
		reversed = append(reversed, &copy)
		if b.ParentID == "" {
			break
		}
		parent := byID[b.ParentID]
		if parent == nil || parent.Site != b.Site || parent.Kind != b.Kind {
			return nil, errf("Не найдена необходимая копия %s", b.ParentID)
		}
		b = parent
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}
func backupSafeName(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	clean := filepath.Clean(filepath.FromSlash(name))
	if name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errf("Небезопасный путь в копии")
	}
	return clean, nil
}
func ensureBackupParents(root, relative string) error {
	current := root
	parts := strings.Split(filepath.Dir(relative), string(os.PathSeparator))
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errf("Небезопасный родительский путь в копии")
		}
	}
	return nil
}
func unpackBackupPart(b *Backup, dest string) error { return unpackBackupPartProgress(b, dest, nil) }

// countingReader считает прочитанные байты сжатого архива: по ним видно, какая часть копии уже распакована.
type countingReader struct {
	r    io.Reader
	n    int64
	done func(n int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.done != nil && n > 0 {
		c.done(c.n)
	}
	return n, err
}

// unpackBackupPartProgress — unpackBackupPart с отчётом о ходе: progress(прочитано, всего) в байтах архива.
func unpackBackupPartProgress(b *Backup, dest string, progress func(done, total int64)) error {
	file, temporary, err := fetchBackup(b)
	if err != nil {
		return err
	}
	if temporary {
		defer os.Remove(P(file))
	}
	f, err := os.Open(P(file))
	if err != nil {
		return err
	}
	defer f.Close()
	var src io.Reader = f
	if progress != nil {
		if st, err := f.Stat(); err == nil && st.Size() > 0 {
			total := st.Size()
			src = &countingReader{r: f, done: func(n int64) { progress(n, total) }}
		}
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	manifestSeen := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name, err := backupSafeName(h.Name)
		if err != nil {
			return err
		}
		if name == "meta.json" {
			if manifestSeen || h.Typeflag != tar.TypeReg || h.Size > 64<<20 {
				return errf("Некорректный манифест копии")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if b.ManifestHash != "" {
				sum := sha256.Sum256(data)
				if hex.EncodeToString(sum[:]) != b.ManifestHash {
					return errf("Манифест архива изменился после подключения. Восстановление отменено")
				}
			}
			m, err := decodeBackupMetadata(data)
			if err != nil {
				return err
			}
			if m.ParentID != b.ParentID {
				return errf("Цепочка копии не совпадает с манифестом")
			}
			if m.Version >= 2 && m.Domain != b.Site {
				return errf("Домен копии не совпадает с манифестом")
			}
			if b.ParentID == "" {
				if err = os.RemoveAll(filepath.Join(dest, "files")); err != nil {
					return err
				}
			}
			if err = os.RemoveAll(filepath.Join(dest, "db")); err != nil {
				return err
			}
			for _, deleted := range m.Deleted {
				rel, e := backupSafeName(deleted)
				if e != nil || rel == "." {
					return errf("Некорректный список удалённых файлов")
				}
				rel = filepath.Join("files", rel)
				// Deleted descendants of a deleted directory need no further action.
				if _, e = os.Lstat(filepath.Join(dest, rel)); os.IsNotExist(e) {
					continue
				}
				if e = ensureBackupParents(dest, rel); e != nil {
					return e
				}
				if e = os.RemoveAll(filepath.Join(dest, rel)); e != nil {
					return e
				}
			}
			if err = os.WriteFile(filepath.Join(dest, "meta.json"), data, 0600); err != nil {
				return err
			}
			manifestSeen = true
			continue
		}
		if !manifestSeen {
			return errf("В копии отсутствует начальный манифест")
		}
		if name != "files" && name != "db" && !strings.HasPrefix(name, "files/") && !strings.HasPrefix(name, "db/") {
			return errf("Неизвестный раздел архива")
		}
		if (name == "files" || name == "db") && h.Typeflag != tar.TypeDir {
			return errf("Корневой раздел копии должен быть каталогом")
		}
		if strings.HasPrefix(name, "db/") && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return errf("Недопустимая ссылка в дампах базы")
		}
		if err = ensureBackupParents(dest, name); err != nil {
			return err
		}
		path := filepath.Join(dest, name)
		info, statErr := os.Lstat(path)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if h.Typeflag == tar.TypeDir {
			if info != nil && !info.IsDir() {
				if err = os.RemoveAll(path); err != nil {
					return err
				}
			}
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
			// Restore original directory permissions. The panel runs as root.
			if err = os.Chmod(path, os.FileMode(h.Mode)&0777); err != nil {
				return err
			}
			continue
		}
		if info != nil {
			if err = os.RemoveAll(path); err != nil {
				return err
			}
		}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
			if e != nil {
				return e
			}
			_, e = io.Copy(out, tr)
			closeErr := out.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			if err = os.Chtimes(path, h.ModTime, h.ModTime); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err = os.Symlink(h.Linkname, path); err != nil {
				return err
			}
		case tar.TypeLink:
			target, e := backupSafeName(h.Linkname)
			if e != nil {
				return e
			}
			if !strings.HasPrefix(target, "files/") {
				return errf("Некорректная жёсткая ссылка в копии")
			}
			if e = ensureBackupParents(dest, target); e != nil {
				return e
			}
			fi, e := os.Lstat(filepath.Join(dest, target))
			if e != nil || !fi.Mode().IsRegular() {
				return errf("Нет файла для жёсткой ссылки")
			}
			if e = os.Link(filepath.Join(dest, target), path); e != nil {
				return e
			}
		default:
			return fmt.Errorf("Неподдерживаемый тип файла в архиве: %s", name)
		}
	}
	// Read through the gzip trailer: tar EOF alone does not verify its checksum.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	if !manifestSeen {
		return errf("В копии нет манифеста")
	}
	return nil
}
