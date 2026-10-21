// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Корзина менеджера файлов (как в aaPanel): удалённые файлы и папки переносятся в recycleDir,
// откуда их можно вернуть на прежнее место или удалить навсегда. Папка лежит в /var/www — на той
// же файловой системе, что и сайты, поэтому перенос мгновенный; доступ к ней есть только у root.

const recycleDir = "/var/www/.itrupanel-recycle"

type RecycleItem struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"` // откуда удалён
	Name    string    `json:"name"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Deleted time.Time `json:"deleted"`
}

var recycleMu sync.Mutex

func recycleIndexPath() string { return recycleDir + "/.index.json" }

func recycleLoad() []*RecycleItem {
	var items []*RecycleItem
	data, err := os.ReadFile(P(recycleIndexPath()))
	if err == nil {
		json.Unmarshal(data, &items)
	}
	return items
}

func recycleSave(items []*RecycleItem) error {
	if err := os.MkdirAll(P(recycleDir), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(items, "", " ")
	return writeFile(recycleIndexPath(), data, 0600)
}

func recycleEnabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return !store.Settings.RecycleOff
}

// inRecycle — путь внутри корзины: такие файлы удаляются сразу и навсегда.
func inRecycle(p string) bool { return p == recycleDir || strings.HasPrefix(p, recycleDir+"/") }

// recycleMove переносит файл или папку в корзину. Если корзина на другой файловой системе,
// данные копируются (mv), это дольше, но результат тот же.
func recycleMove(p string) error {
	info, err := os.Lstat(P(p))
	if err != nil {
		return errf("%s не найден", p)
	}
	recycleMu.Lock()
	defer recycleMu.Unlock()
	if err := os.MkdirAll(P(recycleDir), 0700); err != nil {
		return err
	}
	os.Chmod(P(recycleDir), 0700)
	item := &RecycleItem{ID: time.Now().Format("20060102-150405") + "-" + randToken(4), Path: p, Name: path.Base(p), Dir: info.IsDir(), Deleted: time.Now()}
	item.Size = info.Size()
	if info.IsDir() {
		item.Size = duBytes([]string{p})[P(p)]
	}
	dst := recycleDir + "/" + item.ID
	if err := os.Rename(P(p), P(dst)); err != nil {
		if _, e := run("mv", "-T", "--", P(p), P(dst)); e != nil {
			return errf("Не удалось перенести %s в корзину: %s", p, firstLine(e.Error()))
		}
	}
	items := append(recycleLoad(), item)
	return recycleSave(items)
}

func recycleFind(items []*RecycleItem, id string) (int, *RecycleItem) {
	for i, it := range items {
		if it.ID == id {
			return i, it
		}
	}
	return -1, nil
}

// recycleRestore возвращает объекты на прежние места. Если там уже есть файл с тем же именем,
// объект восстанавливается рядом с пометкой «(восстановлен …)», ничего не перезаписывается.
func recycleRestore(ids []string) ([]string, error) {
	recycleMu.Lock()
	defer recycleMu.Unlock()
	items := recycleLoad()
	var restored []string
	for _, id := range ids {
		i, it := recycleFind(items, id)
		if it == nil {
			continue
		}
		dst := it.Path
		if _, err := os.Lstat(P(dst)); err == nil {
			ext := path.Ext(it.Name)
			if it.Dir {
				ext = ""
			}
			dst = strings.TrimSuffix(it.Path, ext) + " (восстановлен " + time.Now().Format("2006-01-02 15-04") + ")" + ext
		}
		if err := os.MkdirAll(P(path.Dir(dst)), 0755); err != nil {
			return restored, err
		}
		src := recycleDir + "/" + filepath.Base(it.ID)
		if err := os.Rename(P(src), P(dst)); err != nil {
			if _, e := run("mv", "-T", "--", P(src), P(dst)); e != nil {
				return restored, errf("Не удалось восстановить %s: %s", it.Path, firstLine(e.Error()))
			}
		}
		relabelWeb(dst)
		restored = append(restored, dst)
		items = append(items[:i], items[i+1:]...)
	}
	return restored, recycleSave(items)
}

// recycleDelete удаляет объекты из корзины навсегда.
func recycleDelete(ids []string) error {
	recycleMu.Lock()
	defer recycleMu.Unlock()
	items := recycleLoad()
	for _, id := range ids {
		i, it := recycleFind(items, id)
		if it == nil {
			continue
		}
		if err := os.RemoveAll(P(recycleDir + "/" + filepath.Base(it.ID))); err != nil {
			return err
		}
		items = append(items[:i], items[i+1:]...)
	}
	return recycleSave(items)
}

func recycleEmpty() error {
	recycleMu.Lock()
	defer recycleMu.Unlock()
	entries, _ := os.ReadDir(P(recycleDir))
	for _, e := range entries {
		if e.Name() != ".index.json" {
			if err := os.RemoveAll(P(recycleDir + "/" + e.Name())); err != nil {
				return err
			}
		}
	}
	return recycleSave(nil)
}

func registerRecycle() {
	api("files.recycle.list", func(q *Req) (any, error) {
		recycleMu.Lock()
		all := recycleLoad()
		recycleMu.Unlock()
		// Записи, чьи файлы удалили вручную, не показываются.
		items := []*RecycleItem{}
		for _, it := range all {
			if _, err := os.Lstat(P(recycleDir + "/" + filepath.Base(it.ID))); err == nil {
				items = append(items, it)
			}
		}
		sort.Slice(items, func(i, k int) bool { return items[i].Deleted.After(items[k].Deleted) })
		var total int64
		for _, it := range items {
			total += it.Size
		}
		return map[string]any{"items": items, "total": total, "enabled": recycleEnabled(), "dir": recycleDir}, nil
	})
	apiOp("files.recycle.restore", "Корзина: восстановление", func(q *Req) (any, error) {
		var in struct{ IDs []string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		restored, err := recycleRestore(in.IDs)
		if err != nil {
			return nil, err
		}
		return map[string]any{"restored": restored, "info": fmt.Sprintf("Восстановлено: %d", len(restored))}, nil
	})
	apiOp("files.recycle.delete", "Корзина: удаление навсегда", func(q *Req) (any, error) {
		var in struct{ IDs []string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, recycleDelete(in.IDs)
	})
	apiOp("files.recycle.empty", "Корзина: очистка", func(q *Req) (any, error) {
		return nil, recycleEmpty()
	})
	apiOp("files.recycle.settings", "Корзина: настройка", func(q *Req) (any, error) {
		var in struct{ Enabled bool }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		store.Settings.RecycleOff = !in.Enabled
		return nil, saveChecked()
	})
}
