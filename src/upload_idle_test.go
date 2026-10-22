// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Загрузка, у которой оборвалась связь, не должна держать блокировку обновления: иначе «Обновить»
// отвечает «Дождитесь завершения текущих операций» до тех пор, пока соединение не закроется само.
func TestStalledUploadIsAbortedAndReleasesUpdateGate(t *testing.T) {
	old := uploadIdleTimeout
	uploadIdleTimeout = 300 * time.Millisecond
	defer func() { uploadIdleTimeout = old }()
	done := make(chan error, 1)
	srv := httptest.NewServer(withUpdateGuard(withIdleBody(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		done <- err
	})))
	defer srv.Close()
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() { pw.Write([]byte("first part of the file")) }() // дальше данных нет: связь «зависла»
	req, _ := http.NewRequest("POST", srv.URL, pr)
	req.Header.Set("Content-Type", "application/octet-stream")
	go func() {
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	// Пока загрузка идёт, обновление запустить нельзя.
	time.Sleep(100 * time.Millisecond)
	if updateGate.TryLock() {
		updateGate.Unlock()
		t.Fatal("во время загрузки блокировка обновления свободна")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("зависшая загрузка завершилась без ошибки")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("зависшая загрузка не прервана по таймауту")
	}
	time.Sleep(100 * time.Millisecond)
	if !updateGate.TryLock() {
		t.Fatal("после обрыва загрузки блокировка обновления осталась занятой")
	}
	updateGate.Unlock()
}

// Живая загрузка с паузами короче таймаута проходит целиком.
func TestSlowButAliveUploadIsNotAborted(t *testing.T) {
	old := uploadIdleTimeout
	uploadIdleTimeout = 400 * time.Millisecond
	defer func() { uploadIdleTimeout = old }()
	var got int64
	srv := httptest.NewServer(withIdleBody(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 5; i++ {
			pw.Write([]byte("0123456789"))
			time.Sleep(150 * time.Millisecond)
		}
		pw.Close()
	}()
	res, err := http.Post(srv.URL, "application/octet-stream", pr)
	if err != nil || res.StatusCode != 200 || got != 50 {
		t.Fatalf("медленная живая загрузка: %v, получено %d", err, got)
	}
	res.Body.Close()
}
