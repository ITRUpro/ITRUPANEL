// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Job struct {
	ID      string    `json:"id"`
	Key     string    `json:"key"`
	Owner   string    `json:"owner"`
	Title   string    `json:"title"`
	Status  string    `json:"status"`
	Stage   string    `json:"stage"`
	Log     string    `json:"log"`
	Error   string    `json:"error,omitempty"`
	Result  any       `json:"result,omitempty"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	// Остановить можно только задачи, прерывание которых ничего не ломает (не установка пакетов).
	Cancelable bool `json:"cancelable,omitempty"`
	ctx        context.Context
	cancel     context.CancelFunc
}

var cancelableJobs = []string{"import:analyze", "import:run", "av:scan", "av:update", "av:cure", "backup:extract:", "backup:scan:", "backups:ftp:discover", "docker:pull:"}

func jobCancelable(key string) bool {
	for _, p := range cancelableJobs {
		if key == p || strings.HasSuffix(p, ":") && strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// jobStopped — задачу остановили; длинные циклы проверяют это между шагами.
func jobStopped(j *Job) bool { return j != nil && j.ctx != nil && j.ctx.Err() != nil }

var jobsMu sync.Mutex
var jobs = map[string]*Job{}

func saveJobsLocked() {
	b, err := json.Marshal(jobs)
	if err != nil {
		return
	}
	p := filepath.Join(cfg.Home, "var", "jobs.json")
	if err = os.WriteFile(p+".tmp", b, 0600); err == nil {
		err = os.Rename(p+".tmp", p)
	}
	if err != nil {
		log.Printf("Не удалось сохранить журнал задач: %v", err)
	}
}
func loadJobs() {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	b, err := os.ReadFile(filepath.Join(cfg.Home, "var", "jobs.json"))
	if err != nil {
		return
	}
	if json.Unmarshal(b, &jobs) != nil {
		jobs = map[string]*Job{}
		return
	}
	for _, j := range jobs {
		if j.Status == "running" {
			j.Status = "error"
			j.Error = "Панель была перезапущена. Проверьте состояние службы и повторите операцию."
			j.Updated = time.Now()
		}
	}
	saveJobsLocked()
}
func (j *Job) progress(stage string) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	j.Stage = stage
	j.Updated = time.Now()
	j.Log = tailLog(j.Log + "\n[" + time.Now().Format("15:04:05") + "] " + stage + "\n")
	saveJobsLocked()
}
func tailLog(s string) string {
	if len(s) > 64000 {
		return s[len(s)-64000:]
	}
	return s
}
func (j *Job) Write(p []byte) (int, error) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	j.Log = tailLog(j.Log + string(p))
	j.Updated = time.Now()
	return len(p), nil
}
func startJob(key, title, owner string, fn func(*Job) (any, error)) string {
	jobsMu.Lock()
	for _, j := range jobs {
		if j.Key == key && j.Status == "running" {
			id := j.ID
			jobsMu.Unlock()
			return id
		}
	}
	// Keep the newest 40 completed jobs; never evict a running task.
	var done []*Job
	for _, j := range jobs {
		if j.Status != "running" {
			done = append(done, j)
		}
	}
	sort.Slice(done, func(i, k int) bool { return done[i].Started.After(done[k].Started) })
	for i := 39; i < len(done); i++ {
		delete(jobs, done[i].ID)
	}
	j := &Job{ID: randToken(16), Key: key, Owner: owner, Title: title, Status: "running", Stage: "В очереди", Started: time.Now(), Updated: time.Now(), Cancelable: jobCancelable(key)}
	j.ctx, j.cancel = context.WithCancel(context.Background())
	jobs[j.ID] = j
	saveJobsLocked()
	jobsMu.Unlock()
	go func() {
		var result any
		var err error
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("Ошибка задачи: %v", p)
				}
			}()
			result, err = fn(j)
		}()
		stopped := jobStopped(j)
		j.cancel()
		jobsMu.Lock()
		j.Updated = time.Now()
		j.Status = "done"
		j.Stage = "Готово"
		j.Result = result
		if stopped {
			err = errf("Задача остановлена")
		}
		if err != nil {
			j.Status = "error"
			j.Stage = "Операция не выполнена"
			j.Error = err.Error()
			j.Log = tailLog(j.Log + "\n" + err.Error() + "\n")
		}
		saveJobsLocked()
		jobsMu.Unlock()
	}()
	return j.ID
}
func liveCommand(j *Job, limit time.Duration, name string, args ...string) error {
	if j == nil {
		_, err := runTimeout(limit, "", name, args...)
		return err
	}
	if cfg.Dry {
		j.Write([]byte("[demo] " + name + " " + strings.Join(args, " ") + "\n"))
		return nil
	}
	parent := context.Background()
	if j.ctx != nil {
		parent = j.ctx
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8", "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout = j
	cmd.Stderr = j
	if err := cmd.Run(); err != nil {
		if jobStopped(j) {
			return errf("Задача остановлена")
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%s: истекло время ожидания (%s). Подробности в журнале задачи", name, limit)
		}
		return fmt.Errorf("%s: %w. Подробности в журнале задачи", name, err)
	}
	return nil
}
func jobStage(j *Job, s string) {
	if j != nil {
		j.progress(s)
	}
}
func registerJobs() {
	apiOp("jobs.cancel", "Задачи: остановка", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		jobsMu.Lock()
		defer jobsMu.Unlock()
		j := jobs[in.ID]
		if j == nil || j.Owner != q.S.User {
			return nil, errf("Задача не найдена")
		}
		if j.Status != "running" || j.cancel == nil {
			return nil, errf("Задача уже завершена")
		}
		if !j.Cancelable {
			return nil, errf("Эту задачу нельзя остановить: прерывание может оставить установку незавершённой")
		}
		j.cancel()
		j.Stage = "Остановка…"
		j.Updated = time.Now()
		return nil, nil
	})
	api("jobs.list", func(q *Req) (any, error) {
		jobsMu.Lock()
		defer jobsMu.Unlock()
		out := []Job{}
		for _, j := range jobs {
			if j.Owner == q.S.User {
				c := *j
				c.Log = ""
				out = append(out, c)
			}
		}
		sort.Slice(out, func(i, k int) bool { return out[i].Started.After(out[k].Started) })
		return out, nil
	})
	api("jobs.get", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		jobsMu.Lock()
		defer jobsMu.Unlock()
		j := jobs[in.ID]
		if j == nil || j.Owner != q.S.User {
			return nil, errf("Задача не найдена")
		}
		return *j, nil
	})
}

// jobOutput выполняет команду задачи и возвращает её вывод; останавливается вместе с задачей.
func jobOutput(j *Job, limit time.Duration, name string, args ...string) (string, error) {
	if j == nil || j.ctx == nil || cfg.Dry {
		return runTimeout(limit, "", name, args...)
	}
	ctx, cancel := context.WithTimeout(j.ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8")
	out, err := cmd.CombinedOutput()
	if jobStopped(j) {
		return string(out), errf("Задача остановлена")
	}
	return string(out), err
}
