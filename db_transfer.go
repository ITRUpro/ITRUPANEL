// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Импорт и экспорт баз данных из раздела «Базы данных».

const dbDumpLimit = 50 << 30   // распакованный дамп
const dbUploadLimit = 20 << 30 // загружаемый файл

func dbTransferDir() (string, error) {
	dir := filepath.Join(cfg.Home, "var", "db-transfer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, "job-")
}

func copySQL(dst string, src io.Reader) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	limited := &io.LimitedReader{R: src, N: dbDumpLimit + 1}
	if _, err = io.Copy(f, limited); err == nil && limited.N <= 0 {
		err = errf("Дамп больше 50 ГБ")
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// prepareSQLDump распаковывает .sql, .sql.gz, .gz или .zip (первый .sql внутри) в dst.
func prepareSQLDump(src io.Reader, name, dst string) error {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		tmp := dst + ".zip"
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(src, dbUploadLimit))
		if ce := f.Close(); err == nil {
			err = ce
		}
		defer os.Remove(tmp)
		if err != nil {
			return err
		}
		z, err := zip.OpenReader(tmp)
		if err != nil {
			return errf("Не удалось открыть ZIP-архив")
		}
		defer z.Close()
		var pick *zip.File
		for _, e := range z.File {
			n := strings.ToLower(e.Name)
			if !e.FileInfo().IsDir() && (strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sql.gz")) {
				pick = e
				break
			}
		}
		if pick == nil {
			return errf("В ZIP-архиве нет файла .sql")
		}
		rc, err := pick.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return prepareSQLDump(rc, path.Base(pick.Name), dst)
	case strings.HasSuffix(lower, ".gz"):
		gz, err := gzip.NewReader(src)
		if err != nil {
			return errf("Файл .gz повреждён или не является архивом gzip")
		}
		defer gz.Close()
		return copySQL(dst, gz)
	case strings.HasSuffix(lower, ".sql"):
		return copySQL(dst, src)
	}
	return errf("Поддерживаются дампы .sql, .sql.gz и .zip")
}

// dbDumpTo сохраняет базу в файл (без ограничения 10 минут у обычных команд).
func dbDumpTo(server, db, out string) error {
	if cfg.Dry {
		// Демо и тесты: команды не выполняются, но копия базы должна содержать дамп.
		return os.WriteFile(P(out), []byte("-- ITRUPANEL demo dump: "+db+"\n"), 0600)
	}
	if o, err := runTimeout(3*time.Hour, "", "bash", "-c", dumpCmd(server, db, out)); err != nil {
		return errf("Не удалось сохранить базу: %s", dockerErr(o, err))
	}
	return nil
}

// dbImportAsRoot — импорт администратора: дамп выполняется с правами администратора СУБД.
func dbImportAsRoot(server, db, in string) error {
	if o, err := runTimeout(3*time.Hour, "", "bash", "-c", importCmd(server, db, in)); err != nil {
		return errf("Ошибка импорта: %s", dockerErr(o, err))
	}
	return nil
}

// dbImportJob готовит дамп и загружает его в базу в фоновой задаче; src закрывается здесь.
func dbImportJob(owner string, d DB, name, dir string, open func() (io.ReadCloser, error)) string {
	server, db := d.Server, d.Name
	return startJob("db:import:"+db, "Импорт базы "+db, owner, func(j *Job) (any, error) {
		defer os.RemoveAll(dir)
		jobStage(j, "Подготовка дампа "+path.Base(name))
		src, err := open()
		if err != nil {
			return nil, err
		}
		sqlPath := filepath.Join(dir, "import.sql")
		err = prepareSQLDump(src, name, sqlPath)
		src.Close()
		if err != nil {
			return nil, err
		}
		if st, e := os.Stat(sqlPath); e == nil {
			j.Write([]byte(fmt.Sprintf("Размер SQL: %s\n", humanBytes(st.Size()))))
		}
		jobStage(j, "Загрузка в базу "+db)
		err = dbImportAsRoot(server, db, sqlPath)
		if err != nil {
			return nil, err
		}
		return map[string]string{"info": "База " + db + " загружена из дампа"}, nil
	})
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d Б", n)
	}
	v, i := float64(n), 0
	for v >= u && i < 4 {
		v /= u
		i++
	}
	return fmt.Sprintf("%.1f %s", v, []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}[i])
}

// serveDBExport сохраняет базу во временный файл и отдаёт её сжатой в .sql.gz.
func serveDBExport(w http.ResponseWriter, server, db string) {
	dir, err := dbTransferDir()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "dump.sql")
	if cfg.Dry {
		os.WriteFile(out, []byte("-- ITRUPANEL demo dump of "+db+"\n"), 0600)
	} else if err := dbDumpTo(server, db, out); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	f, err := os.Open(out)
	if err != nil {
		http.Error(w, "Дамп не создан", 500)
		return
	}
	defer f.Close()
	name := fmt.Sprintf("%s_%s.sql.gz", db, time.Now().Format("20060102-1504"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	gz := gzip.NewWriter(w)
	io.Copy(gz, f)
	gz.Close()
}

// dbUploadPart принимает файл дампа из multipart-запроса во временную папку.
func dbUploadPart(r *http.Request, dir string) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", errf("Неверный запрос загрузки")
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", errf("Файл не передан")
		}
		if err != nil {
			return "", err
		}
		if part.FileName() == "" {
			continue
		}
		return saveDumpPart(part, dir)
	}
}

func saveDumpPart(part *multipart.Part, dir string) (string, error) {
	name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
	if name == "." || name == "/" || name == "" || strings.HasPrefix(name, ".") {
		name = "dump.sql"
	}
	dst := filepath.Join(dir, "upload-"+name)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(part, dbUploadLimit+1))
	if ce := f.Close(); err == nil {
		err = ce
	}
	if err == nil && n > dbUploadLimit {
		err = errf("Файл больше 20 ГБ")
	}
	return dst, err
}

func dbAdminFind(name string) (*DB, error) {
	mu.Lock()
	defer mu.Unlock()
	d := findDB(name)
	if d == nil {
		return nil, errf("База %s не найдена", name)
	}
	c := *d
	return &c, nil
}

func dbTransferLog(r *http.Request, action, db string, err error) {
	s := getSession(r)
	if s == nil {
		return
	}
	e := OpEntry{Actor: s.Actor, Time: time.Now(), User: s.User, IP: s.IP, Action: action, Object: db, Status: "ok"}
	if err != nil {
		e.Status, e.Error = "error", err.Error()
	}
	appendJSONL("ops.jsonl", e)
}

func writeUploadResult(w http.ResponseWriter, res any, err error) {
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "data": res})
}

func handleDBExport(w http.ResponseWriter, r *http.Request) {
	d, err := dbAdminFind(r.URL.Query().Get("name"))
	dbTransferLog(r, "База данных: скачивание дампа", r.URL.Query().Get("name"), err)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	serveDBExport(w, d.Server, d.Name)
}

func handleDBImportUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Itrupanel") != "1" {
		http.Error(w, "bad request", 400)
		return
	}
	d, err := dbAdminFind(r.URL.Query().Get("name"))
	if err != nil {
		writeUploadResult(w, nil, err)
		return
	}
	res, err := dbStartUploadImport(r, getSession(r).User, d)
	writeUploadResult(w, res, err)
}

func dbStartUploadImport(r *http.Request, owner string, d *DB) (any, error) {
	dir, err := dbTransferDir()
	if err != nil {
		return nil, err
	}
	file, err := dbUploadPart(r, dir)
	dbTransferLog(r, "База данных: импорт дампа", d.Name, err)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	name := strings.TrimPrefix(filepath.Base(file), "upload-")
	id := dbImportJob(owner, *d, name, dir, func() (io.ReadCloser, error) { return os.Open(file) })
	return map[string]string{"job": id}, nil
}

func registerDBTransfer() {
	apiOp("db.import", "База данных: импорт дампа с сервера", func(q *Req) (any, error) {
		var in struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		d, err := dbAdminFind(in.Name)
		if err != nil {
			return nil, err
		}
		p := filepath.Clean(strings.TrimSpace(in.Path))
		if !filepath.IsAbs(p) {
			return nil, errf("Укажите полный путь к файлу дампа, например /root/site.sql.gz")
		}
		if st, err := os.Stat(P(p)); err != nil || !st.Mode().IsRegular() {
			return nil, errf("Файл %s не найден", p)
		}
		dir, err := dbTransferDir()
		if err != nil {
			return nil, err
		}
		return map[string]string{"job": dbImportJob(q.S.User, *d, p, dir, func() (io.ReadCloser, error) { return os.Open(P(p)) })}, nil
	})
}
