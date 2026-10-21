// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Файловый менеджер работает от root по всей ФС. Новые файлы в /var/www/<пользователь>/
// автоматически получают владельца сайта.

func cleanPath(p string) (string, error) {
	if p == "" {
		p = "/var/www"
	}
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", errf("Неверный путь")
	}
	return filepath.Clean(p), nil
}

// selinuxEnabled — на сервере включён SELinux (AlmaLinux, Rocky, CentOS, RHEL), в режиме
// Enforcing или Permissive: метки нужны в обоих, иначе после перехода в Enforcing почта и
// PHP-сайты перестанут работать.
func selinuxEnabled() bool {
	if cfg.Dry || osInfo.Family != "rhel" || !have("restorecon") {
		return false
	}
	_, err := os.Stat("/sys/fs/selinux/enforce")
	return err == nil
}

// relabelWeb восстанавливает метки SELinux файлов сайтов. cp -a и перемещение (mv) сохраняют
// метку источника (например, admin_home_t из /root): nginx такой файл видит, а PHP-FPM прочитать
// не может и отвечает «No input file specified».
func relabelWeb(path string) {
	if !strings.HasPrefix(path, "/var/www/") || !selinuxEnabled() {
		return
	}
	runTimeout(10*time.Minute, "", "restorecon", "-R", path)
}

func fixOwner(path string) {
	if !strings.HasPrefix(path, "/var/www/") {
		return
	}
	defer relabelWeb(path)
	parts := strings.Split(strings.TrimPrefix(path, "/var/www/"), "/")
	mu.Lock()
	u := findUser(parts[0])
	mu.Unlock()
	if u != nil && len(parts) > 1 {
		run("chown", "-R", u.Name+":"+u.Name, path)
	}
}

var uidCache = map[uint32]string{}
var uidCacheMu sync.Mutex

func ownerName(fi os.FileInfo) string {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	uid := st.Uid
	uidCacheMu.Lock()
	defer uidCacheMu.Unlock()
	if n, ok := uidCache[uid]; ok {
		return n
	}
	n := strconv.Itoa(int(uid))
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	uidCache[uid] = n
	return n
}

type fileRow struct {
	Name  string    `json:"name"`
	Dir   bool      `json:"dir"`
	Link  bool      `json:"link"`
	Size  int64     `json:"size"`
	Mode  string    `json:"mode"`
	Owner string    `json:"owner"`
	Mtime time.Time `json:"mtime"`
}

func registerFiles() {
	api("files.list", func(q *Req) (any, error) {
		var in struct{ Path string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := cleanPath(in.Path)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(P(p))
		if err != nil {
			return nil, errf("Не удалось открыть %s: %v", p, err)
		}
		rows := []fileRow{}
		for _, e := range entries {
			fi, err := os.Lstat(P(filepath.Join(p, e.Name())))
			if err != nil {
				continue
			}
			r := fileRow{Name: e.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mode: fmt.Sprintf("%04o", fi.Mode().Perm()), Owner: ownerName(fi), Mtime: fi.ModTime()}
			if fi.Mode()&os.ModeSymlink != 0 {
				r.Link = true
				if st, err := os.Stat(P(filepath.Join(p, e.Name()))); err == nil {
					r.Dir = st.IsDir()
				}
			}
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Dir != rows[j].Dir {
				return rows[i].Dir
			}
			return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
		})
		return map[string]any{"path": p, "rows": rows}, nil
	})
	api("files.read", func(q *Req) (any, error) {
		var in struct{ Path string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := cleanPath(in.Path)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(P(p))
		if err != nil {
			return nil, err
		}
		if fi.Size() > 3<<20 {
			return nil, errf("Файл больше 3 МБ — скачайте его")
		}
		b, _ := os.ReadFile(P(p))
		if !utf8.Valid(b) {
			return nil, errf("Это не текстовый файл")
		}
		return map[string]any{"text": string(b)}, nil
	})
	apiOp("files.write", "Файлы: сохранение", func(q *Req) (any, error) {
		var in struct{ Path, Text string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := cleanPath(in.Path)
		if err != nil {
			return nil, err
		}
		mode := os.FileMode(0644)
		existed := false
		if fi, err := os.Stat(P(p)); err == nil {
			mode, existed = fi.Mode().Perm(), true
		}
		var uid, gid = -1, -1
		if fi, err := os.Stat(P(p)); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(st.Uid), int(st.Gid)
			}
		}
		if err := os.WriteFile(P(p), []byte(in.Text), mode); err != nil {
			return nil, err
		}
		if existed && uid >= 0 {
			os.Chown(P(p), uid, gid)
		} else {
			fixOwner(p)
		}
		return nil, nil
	})
	apiOp("files.mkdir", "Файлы: новая папка", func(q *Req) (any, error) {
		var in struct{ Path, Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Name == "" || strings.ContainsAny(in.Name, "/\x00") {
			return nil, errf("Неверное имя")
		}
		p, err := cleanPath(filepath.Join(in.Path, in.Name))
		if err != nil {
			return nil, err
		}
		if err := os.Mkdir(P(p), 0755); err != nil {
			return nil, err
		}
		fixOwner(p)
		return nil, nil
	})
	apiOp("files.create", "Файлы: новый файл", func(q *Req) (any, error) {
		var in struct{ Path, Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Name == "" || strings.ContainsAny(in.Name, "/\x00") {
			return nil, errf("Неверное имя")
		}
		p, err := cleanPath(filepath.Join(in.Path, in.Name))
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(P(p), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		f.Close()
		fixOwner(p)
		return nil, nil
	})
	apiOp("files.rename", "Файлы: переименование", func(q *Req) (any, error) {
		var in struct{ Path, To string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := cleanPath(in.Path)
		if err != nil {
			return nil, err
		}
		to := in.To
		if !strings.HasPrefix(to, "/") {
			if strings.ContainsRune(to, '/') {
				return nil, errf("Неверное имя")
			}
			to = filepath.Join(filepath.Dir(p), to)
		}
		to, err = cleanPath(to)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(P(to)); err == nil {
			return nil, errf("%s уже существует", to)
		}
		if err := os.Rename(P(p), P(to)); err != nil {
			return nil, err
		}
		relabelWeb(to) // перемещённый файл сохраняет метку SELinux прежней папки
		return nil, nil
	})
	apiOp("files.copy", "Файлы: копирование", func(q *Req) (any, error) {
		var in struct {
			Paths []string
			To    string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		to, err := cleanPath(in.To)
		if err != nil {
			return nil, err
		}
		for _, src := range in.Paths {
			p, err := cleanPath(src)
			if err != nil {
				return nil, err
			}
			if _, err := run("cp", "-a", "--", P(p), P(to)+"/"); err != nil {
				return nil, err
			}
		}
		fixOwner(to)
		return nil, nil
	})
	apiOp("files.delete", "Файлы: удаление", func(q *Req) (any, error) {
		var in struct {
			Paths     []string
			Permanent bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		recycle := !in.Permanent && recycleEnabled()
		for _, x := range in.Paths {
			p, err := cleanPath(x)
			if err != nil {
				return nil, err
			}
			if strings.Count(p, "/") < 2 || p == "/var/www" || p == "/usr/local" || p == recycleDir {
				return nil, errf("Нельзя удалить %s", p)
			}
			// Файлы переносятся в корзину; из самой корзины удаляются навсегда.
			if recycle && !inRecycle(p) {
				if err := recycleMove(p); err != nil {
					return nil, err
				}
				continue
			}
			if err := os.RemoveAll(P(p)); err != nil {
				return nil, err
			}
		}
		if recycle {
			return map[string]string{"info": "Перенесено в корзину"}, nil
		}
		return nil, nil
	})
	apiOp("files.chmod", "Файлы: права", func(q *Req) (any, error) {
		var in struct {
			Paths     []string
			Mode      string
			Recursive bool
			Owner     string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		m, err := strconv.ParseUint(in.Mode, 8, 32)
		if in.Mode != "" && (err != nil || m > 0o7777) {
			return nil, errf("Права в виде 0644")
		}
		if in.Owner != "" && !reName.MatchString(strings.Split(in.Owner, ":")[0]) && in.Owner != "root" {
			return nil, errf("Неверный владелец")
		}
		for _, x := range in.Paths {
			p, err := cleanPath(x)
			if err != nil {
				return nil, err
			}
			args := []string{}
			if in.Recursive {
				args = append(args, "-R")
			}
			if in.Mode != "" {
				if _, err := run("chmod", append(args, in.Mode, P(p))...); err != nil {
					return nil, err
				}
			}
			if in.Owner != "" {
				o := in.Owner
				if !strings.Contains(o, ":") {
					o += ":" + o
				}
				if _, err := run("chown", append(args, o, P(p))...); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	apiOp("files.extract", "Файлы: распаковка", func(q *Req) (any, error) {
		var in struct{ Path string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := cleanPath(in.Path)
		if err != nil {
			return nil, err
		}
		dir := filepath.Dir(p)
		low := strings.ToLower(p)
		switch {
		case strings.HasSuffix(low, ".zip"):
			if !have("unzip") {
				pkgInstall("unzip")
			}
			_, err = run("unzip", "-o", "-q", P(p), "-d", P(dir))
		case strings.HasSuffix(low, ".tar.gz"), strings.HasSuffix(low, ".tgz"), strings.HasSuffix(low, ".tar"), strings.HasSuffix(low, ".tar.bz2"), strings.HasSuffix(low, ".tar.xz"):
			_, err = run("tar", "-xf", P(p), "-C", P(dir))
		case strings.HasSuffix(low, ".gz"):
			_, err = run("gunzip", "-k", P(p))
		default:
			return nil, errf("Поддерживаются zip, tar, tar.gz, tar.bz2, tar.xz, gz")
		}
		if err == nil {
			fixOwner(dir)
		}
		return nil, err
	})
	apiOp("files.compress", "Файлы: архивация", func(q *Req) (any, error) {
		var in struct {
			Paths []string
			Name  string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if len(in.Paths) == 0 {
			return nil, errf("Не выбраны файлы")
		}
		dir := filepath.Dir(filepath.Clean(in.Paths[0]))
		if in.Name == "" {
			in.Name = "archive-" + time.Now().Format("20060102-150405") + ".tar.gz"
		}
		if strings.ContainsAny(in.Name, "/\x00") {
			return nil, errf("Неверное имя архива")
		}
		args := []string{}
		zip := strings.HasSuffix(strings.ToLower(in.Name), ".zip")
		for _, x := range in.Paths {
			p, err := cleanPath(x)
			if err != nil || filepath.Dir(p) != dir {
				return nil, errf("Файлы должны быть в одной папке")
			}
			args = append(args, filepath.Base(p))
		}
		var err error
		if zip {
			if !have("zip") {
				pkgInstall("zip")
			}
			_, err = run("bash", append([]string{"-c", `cd "$1" && shift && zip -rq "$@"`, "itrupanel", P(dir), in.Name}, args...)...)
		} else {
			_, err = run("tar", append([]string{"-czf", P(dir + "/" + in.Name), "-C", P(dir)}, args...)...)
		}
		if err == nil {
			fixOwner(dir + "/" + in.Name)
		}
		return nil, err
	})
}

func handleFileDownload(w http.ResponseWriter, r *http.Request) {
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f, err := os.Open(P(p))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	if fi.IsDir() {
		http.Error(w, "Это папка", 400)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlEscape(filepath.Base(p)))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Itrupanel") != "1" {
		http.Error(w, "bad request", 400)
		return
	}
	dir, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		if part.FileName() == "" {
			continue
		}
		if err := savePart(part, dir); err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		n++
	}
	fixOwner(dir)
	s := getSession(r)
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: s.User, IP: s.IP, Action: "Файлы: загрузка", Object: fmt.Sprintf("%s (%d)", dir, n), Status: "ok"})
	writeJSON(w, 200, map[string]any{"ok": true, "data": n})
}

func savePart(part *multipart.Part, dir string) error {
	name := filepath.Base(filepath.Clean("/" + part.FileName()))
	if name == "/" || name == "." {
		return errf("Неверное имя файла")
	}
	f, err := os.Create(P(filepath.Join(dir, name)))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, part)
	return err
}
