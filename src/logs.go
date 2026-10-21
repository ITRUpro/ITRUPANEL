// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// tailJSONL читает последние строки JSONL-журнала (с конца, не более limit записей).
func tailJSONL[T any](name string, limit int, filter func(*T) bool) []T {
	out := []T{}
	for _, file := range []string{name, name + ".1"} {
		lines := tailLines(filepath.Join(cfg.Home, "var", file), 50000)
		for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
			var v T
			if json.Unmarshal([]byte(lines[i]), &v) == nil && (filter == nil || filter(&v)) {
				out = append(out, v)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

// tailLines — последние n строк файла (читает не больше 8 МБ с конца).
func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, _ := f.Stat()
	const maxRead = 8 << 20
	if fi.Size() > maxRead {
		f.Seek(fi.Size()-maxRead, io.SeekStart)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var lines []string
	first := fi.Size() > maxRead
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		lines = append(lines, sc.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// addNotice добавляет уведомление (под mu). key — чтобы не дублировать одно и то же.
func addNotice(level, key, text string) {
	if key != "" {
		for _, n := range store.Notices {
			if n.Key == key && !n.Read {
				return
			}
		}
	}
	store.Notices = append(store.Notices, &Notice{ID: randToken(6), Time: time.Now(), Level: level, Text: text, Key: key})
	if len(store.Notices) > 300 {
		store.Notices = store.Notices[len(store.Notices)-300:]
	}
}

func registerLogs() {
	api("logs.ops", func(q *Req) (any, error) {
		var in struct {
			Q      string
			Errors bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ql := strings.ToLower(in.Q)
		return tailJSONL("ops.jsonl", 500, func(e *OpEntry) bool {
			if in.Errors && e.Status != "error" {
				return false
			}
			return ql == "" || strings.Contains(strings.ToLower(e.Action+" "+e.Object+" "+e.Error+" "+e.IP), ql)
		}), nil
	})
	api("logs.visits", func(q *Req) (any, error) {
		return tailJSONL[VisitEntry]("visits.jsonl", 500, nil), nil
	})
	api("logs.www.files", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]any{}
		for _, s := range store.Sites {
			u := findUser(s.User)
			if u == nil {
				continue
			}
			for _, kind := range []string{"access", "error", "php"} {
				p := u.Home + "/data/logs/" + s.Domain + "." + kind + ".log"
				var size int64
				var mt time.Time
				if fi, err := os.Stat(P(p)); err == nil {
					size, mt = fi.Size(), fi.ModTime()
				}
				out = append(out, map[string]any{"site": s.Domain, "kind": kind, "path": p, "size": size, "mtime": mt})
			}
		}
		return out, nil
	})
	api("logs.www.read", func(q *Req) (any, error) {
		var in struct {
			Path  string
			Lines int
			Q     string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		// разрешены только журналы сайтов панели
		ok := false
		mu.Lock()
		for _, s := range store.Sites {
			if u := findUser(s.User); u != nil {
				for _, kind := range []string{"access", "error", "php"} {
					ok = ok || in.Path == u.Home+"/data/logs/"+s.Domain+"."+kind+".log"
				}
			}
		}
		mu.Unlock()
		if !ok {
			return nil, errf("Нет такого журнала")
		}
		if in.Lines <= 0 || in.Lines > 5000 {
			in.Lines = 300
		}
		lines := tailLines(P(in.Path), 20000)
		out := []string{} // пустой журнал или поиск без совпадений — массив, а не null
		ql := strings.ToLower(in.Q)
		for i := len(lines) - 1; i >= 0 && len(out) < in.Lines; i-- {
			if ql == "" || strings.Contains(strings.ToLower(lines[i]), ql) {
				out = append(out, lines[i])
			}
		}
		return out, nil
	})
	api("notices.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		rows := append([]*Notice{}, store.Notices...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Time.After(rows[j].Time) })
		return rows, nil
	})
	api("notices.read", func(q *Req) (any, error) {
		var in struct {
			ID  string
			All bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, n := range store.Notices {
			if in.All || n.ID == in.ID {
				n.Read = true
			}
		}
		save()
		return nil, nil
	})
	api("notices.clear", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		store.Notices = nil
		save()
		return nil, nil
	})
	api("notices.settings", func(q *Req) (any, error) {
		var in struct {
			Email    string
			DiskWarn int `json:"disk_warn"`
			Save     bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if in.Save {
			if in.Email != "" && !reEmail.MatchString(in.Email) {
				return nil, errf("Неверный e-mail")
			}
			if in.DiskWarn < 50 || in.DiskWarn > 99 {
				return nil, errf("Порог диска от 50 до 99%%")
			}
			store.Settings.Email, store.Settings.DiskWarn = in.Email, in.DiskWarn
			save()
		}
		return map[string]any{"email": store.Settings.Email, "disk_warn": store.Settings.DiskWarn}, nil
	})
}
