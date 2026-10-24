// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The worker has no root privileges; rooted handles additionally keep writes in
// the site's system user even when FTP or PHP swaps a directory during restoration.
func copyRestoredFiles(home, dest, source string) error {
	dstRoot, e := os.OpenRoot(home)
	if e != nil {
		return e
	}
	defer dstRoot.Close()
	rel, e := filepath.Rel(home, dest)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return forbidden()
	}
	dst, e := dstRoot.OpenRoot(rel)
	if e != nil {
		return e
	}
	defer dst.Close()
	src, e := os.OpenRoot(source)
	if e != nil {
		return e
	}
	defer src.Close()
	f, e := dst.Open(".")
	if e != nil {
		return e
	}
	entries, e := f.ReadDir(-1)
	f.Close()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = dst.RemoveAll(entry.Name()); e != nil {
			return e
		}
	}
	return copyRootTree(src, dst, ".")
}

// copyRestoredPath заменяет в папке сайта один файл или папку rel содержимым копии.
func copyRestoredPath(home, dest, source, rel string) error {
	clean, e := backupSafeName(rel)
	if e != nil || clean == "." {
		return forbidden()
	}
	clean = filepath.ToSlash(clean)
	dstRoot, e := os.OpenRoot(home)
	if e != nil {
		return e
	}
	defer dstRoot.Close()
	siteRel, e := filepath.Rel(home, dest)
	if e != nil || siteRel == "." || siteRel == ".." || strings.HasPrefix(siteRel, "../") {
		return forbidden()
	}
	dst, e := dstRoot.OpenRoot(siteRel)
	if e != nil {
		return e
	}
	defer dst.Close()
	src, e := os.OpenRoot(source)
	if e != nil {
		return e
	}
	defer src.Close()
	if _, e = src.Lstat(clean); e != nil {
		return backupMissing(clean)
	}
	if parent := filepath.Dir(clean); parent != "." {
		if e = dst.MkdirAll(parent, 0750); e != nil {
			return e
		}
	}
	if e = dst.RemoveAll(clean); e != nil {
		return e
	}
	return copyRootTree(src, dst, clean)
}

func copyRootTree(src, dst *os.Root, start string) error {
	return fs.WalkDir(src.FS(), start, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		info, err := src.Lstat(name)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return dst.MkdirAll(name, 0750)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := src.Readlink(name)
			if e != nil {
				return e
			}
			return dst.Symlink(link, name)
		}
		if !info.Mode().IsRegular() {
			return errf("Недопустимый тип файла в копии")
		}
		from, e := src.Open(name)
		if e != nil {
			return e
		}
		defer from.Close()
		st, e := from.Stat()
		if e != nil || !st.Mode().IsRegular() {
			return errf("Файл в копии изменился")
		}
		to, e := dst.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0777)
		if e != nil {
			return e
		}
		_, e = io.Copy(to, from)
		ce := to.Close()
		if e != nil {
			return e
		}
		return ce
	})
}
func restoreSiteFiles(site *Site, source string) error {
	return runRestoreWorker(site, source, "")
}

func restoreSitePath(site *Site, source, rel string) error {
	return runRestoreWorker(site, source, rel)
}

// runRestoreWorker копирует файлы от имени владельца сайта; rel == "" — весь сайт.
func runRestoreWorker(site *Site, source, rel string) error {
	if cfg.Dry {
		if rel != "" {
			return copyRestoredPath(P(userHome(site.User)), P(site.Root), P(source), rel)
		}
		return copyRestoredFiles(P(userHome(site.User)), P(site.Root), P(source))
	}
	// -h (не следовать по ссылкам) есть и в GNU chown, и во FreeBSD; --no-dereference только в GNU
	if _, e := run("chown", "-R", "-h", site.User+":"+site.User, source); e != nil {
		return e
	}
	// Владелец сайта должен пройти к распакованной копии: папка копий и временная папка — только
	// проход (0711), сами архивы и служебные папки остаются закрытыми.
	for _, dir := range []string{backupDir, filepath.Dir(source)} {
		if e := os.Chmod(dir, 0711); e != nil {
			return e
		}
	}
	// Во FreeBSD /var/backups закрыт для всех, кроме root (0750): добавляем проход, иначе владелец сайта
	// не дойдёт до распакованной копии.
	if parent := filepath.Dir(backupDir); parent != "/" {
		if st, e := os.Stat(parent); e == nil && st.Mode().Perm()&0001 == 0 {
			if e = os.Chmod(parent, st.Mode().Perm()|0001); e != nil {
				return e
			}
		}
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	args := []string{userHome(site.User), site.Root, source}
	if rel != "" {
		args = append(args, rel)
	}
	body, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd, e := asUserCommand(ctx, site.User, executable, "restore-files-worker")
	if e != nil {
		return e
	}
	cmd.Env = []string{"PATH=" + systemPath(""), "LANG=C.UTF-8"}
	cmd.Stdin = bytes.NewReader(body)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("восстановление файлов: %v: %s", e, out)
	}
	return nil
}
func restoreFilesWorker() {
	var paths []string
	if e := json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&paths); e != nil || (len(paths) != 3 && len(paths) != 4) {
		fmt.Fprintln(os.Stderr, "invalid restore request")
		os.Exit(1)
	}
	copyFn := func() error { return copyRestoredFiles(paths[0], paths[1], paths[2]) }
	if len(paths) == 4 {
		copyFn = func() error { return copyRestoredPath(paths[0], paths[1], paths[2], paths[3]) }
	}
	if e := copyFn(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
