// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
	"time"
)

// Остановить можно только безопасные задачи; остановленная задача завершается с понятной ошибкой.
func TestJobCancel(t *testing.T) {
	mux := testPanel(t)
	started := make(chan struct{})
	id := startJob("av:update", "Обновление баз ClamAV", "admin", func(j *Job) (any, error) {
		close(started)
		<-j.ctx.Done()
		return nil, nil
	})
	<-started
	install := startJob("docker:install", "Установка Docker", "admin", func(j *Job) (any, error) { time.Sleep(300 * time.Millisecond); return nil, nil })
	if _, r := testAPI(t, mux, "jobs.cancel", `{"id":"`+install+`"}`); r["error"] == nil {
		t.Fatal("package installation must not be cancelable")
	}
	if code, r := testAPI(t, mux, "jobs.cancel", `{"id":"`+id+`"}`); code != 200 || r["error"] != nil {
		t.Fatalf("cancel: %v", r)
	}
	for i := 0; i < 50; i++ {
		jobsMu.Lock()
		st, errText := jobs[id].Status, jobs[id].Error
		jobsMu.Unlock()
		if st != "running" {
			if st != "error" || !strings.Contains(errText, "остановлена") {
				t.Fatalf("stopped job: %s %s", st, errText)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not stop")
}
