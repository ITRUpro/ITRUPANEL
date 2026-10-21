// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const releaseEndpoint = "https://itrupanel.com/panel/latest.json"
const releasePublicKey = "22b0819f8689c898d6de2a70441d26bc01a6ddbf0d48c6e57afa4b5a127baf75"
const maxReleaseSize int64 = 128 << 20

var updateGate sync.RWMutex
var updateCache struct {
	sync.Mutex
	home    string
	checked time.Time
	release *PanelRelease
	err     string
}

type PanelRelease struct {
	Schema         int      `json:"schema"`
	Version        string   `json:"version"`
	MinimumVersion string   `json:"minimum_version"`
	Released       string   `json:"released"`
	Notes          []string `json:"notes"`
	NotesEN        []string `json:"notes_en,omitempty"`
	URL            string   `json:"url"`
	SHA256         string   `json:"sha256"`
	Size           int64    `json:"size"`
	Envelope       []byte   `json:"-"`
}
type PanelUpdateRun struct {
	ID         string    `json:"id"`
	Version    string    `json:"version"`
	State      string    `json:"state"`
	Stage      string    `json:"stage"`
	Unit       string    `json:"unit"`
	Started    time.Time `json:"started"`
	Updated    time.Time `json:"updated"`
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"`
	Log        string    `json:"log"`
	Error      string    `json:"error,omitempty"`
	Auto       bool      `json:"auto,omitempty"` // запущено автоматически по расписанию
}

var releaseVersionRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

func compareVersions(a, b string) (int, error) {
	if !releaseVersionRE.MatchString(a) || !releaseVersionRE.MatchString(b) {
		return 0, errf("Некорректная версия")
	}
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		u, _ := strconv.Atoi(x[i])
		v, _ := strconv.Atoi(y[i])
		if u > v {
			return 1, nil
		}
		if u < v {
			return -1, nil
		}
	}
	return 0, nil
}
func verifyPanelRelease(data []byte, key ed25519.PublicKey) (*PanelRelease, error) {
	if len(data) > 65536 {
		return nil, errf("Описание обновления слишком велико")
	}
	var envelope struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if e := json.Unmarshal(data, &envelope); e != nil {
		return nil, e
	}
	payload, e := base64.StdEncoding.DecodeString(envelope.Payload)
	if e != nil {
		return nil, e
	}
	sig, e := base64.StdEncoding.DecodeString(envelope.Signature)
	if e != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, sig) {
		return nil, errf("Подпись обновления не прошла проверку")
	}
	var r PanelRelease
	if e = json.Unmarshal(payload, &r); e != nil {
		return nil, e
	}
	if r.Schema != 1 || !releaseVersionRE.MatchString(r.Version) || !releaseVersionRE.MatchString(r.MinimumVersion) {
		return nil, errf("Неподдерживаемое описание выпуска")
	}
	if r.URL != "https://itrupanel.com/panel/itrupanel-full-"+r.Version+".zip" {
		return nil, errf("Неожиданный адрес архива")
	}
	digest, e := hex.DecodeString(r.SHA256)
	if e != nil || len(digest) != sha256.Size || r.SHA256 != strings.ToLower(r.SHA256) {
		return nil, errf("Неверная контрольная сумма выпуска")
	}
	if r.Size <= 0 || r.Size > maxReleaseSize {
		return nil, errf("Недопустимый размер выпуска")
	}
	if _, e = time.Parse("2006-01-02", r.Released); e != nil {
		return nil, e
	}
	if len(r.Notes) > 40 || len(r.NotesEN) > 40 {
		return nil, errf("Слишком длинное описание")
	}
	for _, s := range append(append([]string{}, r.Notes...), r.NotesEN...) {
		if len(s) > 2000 {
			return nil, errf("Слишком длинное описание")
		}
	}
	r.Envelope = append([]byte{}, data...)
	return &r, nil
}
func releaseHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "itrupanel.com" || len(via) > 2 {
			return errf("Перенаправление обновления запрещено")
		}
		return nil
	}}
}
func fetchPanelRelease(ctx context.Context) (*PanelRelease, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", releaseEndpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Cache-Control", "no-cache")
	res, e := releaseHTTPClient(12 * time.Second).Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errf("Сервер обновлений: HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil {
		return nil, e
	}
	key, _ := hex.DecodeString(releasePublicKey)
	return verifyPanelRelease(b, key)
}
func cachedPanelRelease(force bool) (*PanelRelease, time.Time, string) {
	updateCache.Lock()
	defer updateCache.Unlock()
	if updateCache.home != cfg.Home {
		updateCache.home = cfg.Home
		updateCache.checked = time.Time{}
		updateCache.release = nil
		updateCache.err = ""
	}
	if cfg.Dry {
		return nil, time.Time{}, ""
	}
	age := time.Since(updateCache.checked)
	if updateCache.checked.IsZero() || age >= 15*time.Minute || (force && age >= 30*time.Second) {
		r, e := fetchPanelRelease(context.Background())
		reportPanelActive()
		updateCache.checked = time.Now()
		updateCache.err = ""
		if e != nil {
			updateCache.err = e.Error()
		} else {
			updateCache.release = r
		}
	}
	return updateCache.release, updateCache.checked, updateCache.err
}
func updateStatusPath() string { return filepath.Join(cfg.Home, "var", "panel-update.json") }
func writeUpdateJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".update-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func readPanelUpdate() (*PanelUpdateRun, error) {
	b, e := os.ReadFile(updateStatusPath())
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var r PanelUpdateRun
	if e = json.Unmarshal(b, &r); e != nil {
		return nil, e
	}
	return &r, nil
}
func runningPanelUpdate(r *PanelUpdateRun) bool {
	return r != nil && (r.State == "queued" || r.State == "running" || r.State == "ready")
}
func updateApplyPath(id string) string {
	return filepath.Join(cfg.Home, "var", "panel-updates", id, "apply")
}
func panelUpdating() bool {
	r, e := readPanelUpdate()
	if e != nil {
		return true
	}
	if r != nil && r.State == "ready" {
		_, e = os.Stat(updateApplyPath(r.ID))
		return !os.IsNotExist(e)
	}
	return runningPanelUpdate(r)
}
func updateSupportReason() string {
	if cfg.Dry {
		return "Демонстрация: установка обновлений отключена"
	}
	if runtime.GOOS != "linux" || !osInfo.Systemd {
		return "Для обновления из панели нужен Linux с systemd"
	}
	if cfg.Home != "/usr/local/itrupanel" || os.Geteuid() != 0 {
		return "Нужна стандартная установка /usr/local/itrupanel, запущенная от root"
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "Архитектура не поддерживается"
	}
	return ""
}
func reconcilePanelUpdate() *PanelUpdateRun {
	r, e := readPanelUpdate()
	if e != nil {
		return &PanelUpdateRun{State: "error", Error: "Не удалось прочитать журнал обновления: " + e.Error()}
	}
	if runningPanelUpdate(r) && time.Since(r.Updated) > 90*time.Second && regexp.MustCompile(`^itrupanel-online-update-[a-f0-9]{24}$`).MatchString(r.Unit) {
		out, e := runTimeout(5*time.Second, "", "systemctl", "show", r.Unit, "--property=ActiveState", "--property=LoadState")
		if (e == nil && (strings.Contains(out, "ActiveState=inactive") || strings.Contains(out, "ActiveState=failed"))) || strings.Contains(out, "LoadState=not-found") {
			fresh, readErr := readPanelUpdate()
			if readErr != nil || fresh == nil || fresh.ID != r.ID || !runningPanelUpdate(fresh) || !fresh.Updated.Equal(r.Updated) {
				return fresh
			}
			r.State = "error"
			r.Stage = "Обновление прервано"
			r.Error = "Служба обновления остановлена. Проверьте журнал; затем можно повторить установку."
			r.Updated = time.Now()
			writeUpdateJSON(updateStatusPath(), r)
		}
	}
	return r
}
func panelUpdateInfo(force bool) map[string]any {
	r, checked, errText := cachedPanelRelease(force)
	available := false
	reason := updateSupportReason()
	if r != nil {
		cmp, e := compareVersions(r.Version, Version)
		available = e == nil && cmp > 0
		if cmp, e = compareVersions(Version, r.MinimumVersion); e != nil || cmp < 0 {
			reason = "Для этой версии требуется промежуточное обновление: " + r.MinimumVersion
		}
	}
	return map[string]any{"current": Version, "release": r, "available": available, "checked": checked, "error": errText, "unsupported": reason, "run": reconcilePanelUpdate()}
}
func copyUpdateFile(src, dst string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
func startPanelUpdate(version, sha string) (*PanelUpdateRun, error) {
	return startPanelUpdateMode(version, sha, false)
}

// startPanelUpdateAuto запускает обновление по расписанию; в остальном оно не отличается от ручного.
func startPanelUpdateAuto(version, sha string) (*PanelUpdateRun, error) {
	return startPanelUpdateMode(version, sha, true)
}

func startPanelUpdateMode(version, sha string, auto bool) (*PanelUpdateRun, error) {
	if reason := updateSupportReason(); reason != "" {
		return nil, errf("%s", reason)
	}
	r, _, errText := cachedPanelRelease(true)
	if errText != "" {
		return nil, errf("Проверка обновлений: %s", errText)
	}
	if r == nil || r.Version != version || r.SHA256 != sha {
		return nil, errf("Выпуск изменился. Проверьте обновления ещё раз")
	}
	cmp, e := compareVersions(r.Version, Version)
	if e != nil || cmp <= 0 {
		return nil, errf("Новая версия не требуется")
	}
	cmp, e = compareVersions(Version, r.MinimumVersion)
	if e != nil || cmp < 0 {
		return nil, errf("Сначала обновите панель до %s", r.MinimumVersion)
	}
	if !updateGate.TryLock() {
		return nil, errf("Дождитесь завершения текущих операций или закройте Shell-клиент")
	}
	defer updateGate.Unlock()
	if runningPanelUpdate(reconcilePanelUpdate()) {
		return nil, errf("Обновление уже выполняется")
	}
	jobsMu.Lock()
	busy := false
	for _, j := range jobs {
		if j.Status == "running" {
			busy = true
		}
	}
	jobsMu.Unlock()
	if busy {
		return nil, errf("Дождитесь завершения фоновых задач панели")
	}
	id := randToken(12)
	dir := filepath.Join(cfg.Home, "var", "panel-updates", id)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	if e = copyUpdateFile(exe, filepath.Join(dir, "worker"), 0700); e != nil {
		return nil, e
	}
	if e = os.WriteFile(filepath.Join(dir, "release.json"), r.Envelope, 0600); e != nil {
		return nil, e
	}
	job := &PanelUpdateRun{ID: id, Version: r.Version, State: "queued", Stage: "Запуск службы обновления", Unit: "itrupanel-online-update-" + id, Started: time.Now(), Updated: time.Now(), Total: r.Size, Auto: auto}
	if e = writeUpdateJSON(updateStatusPath(), job); e != nil {
		return nil, e
	}
	_, e = runTimeout(15*time.Second, "", "systemd-run", "--unit="+job.Unit, "--description=ITRUPANEL update to "+r.Version, "--property=Type=exec", "--property=UMask=0077", filepath.Join(dir, "worker"), "update-worker", id)
	if e != nil {
		job.State = "error"
		job.Error = e.Error()
		job.Stage = "Служба обновления не запущена"
		job.Updated = time.Now()
		writeUpdateJSON(updateStatusPath(), job)
		return nil, e
	}
	cleanup = false
	return job, nil
}

type panelUpdateWriter struct {
	sync.Mutex
	r            *PanelUpdateRun
	lastProgress time.Time
}

func (w *panelUpdateWriter) save() error {
	w.r.Updated = time.Now()
	return writeUpdateJSON(updateStatusPath(), w.r)
}
func (w *panelUpdateWriter) stage(s string) error {
	w.Lock()
	defer w.Unlock()
	w.r.Stage = s
	w.r.Log = tailLog(w.r.Log + "\n" + s + "\n")
	return w.save()
}
func (w *panelUpdateWriter) Write(b []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	w.r.Log = tailLog(w.r.Log + string(b))
	return len(b), w.save()
}
func (w *panelUpdateWriter) download(n int) {
	w.Lock()
	defer w.Unlock()
	w.r.Downloaded += int64(n)
	if time.Since(w.lastProgress) > 250*time.Millisecond {
		w.save()
		w.lastProgress = time.Now()
	}
}

type updateProgressWriter struct {
	w io.Writer
	p *panelUpdateWriter
}

func (w updateProgressWriter) Write(b []byte) (int, error) {
	n, e := w.w.Write(b)
	w.p.download(n)
	return n, e
}
func downloadPanelArchive(ctx context.Context, client *http.Client, r *PanelRelease, path string, progress *panelUpdateWriter) error {
	req, e := http.NewRequestWithContext(ctx, "GET", r.URL, nil)
	if e != nil {
		return e
	}
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errf("Архив: HTTP %d", res.StatusCode)
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	sum := sha256.New()
	var dst io.Writer = io.MultiWriter(f, sum)
	if progress != nil {
		dst = updateProgressWriter{dst, progress}
	}
	n, e := io.Copy(dst, io.LimitReader(res.Body, r.Size+1))
	if e != nil {
		return e
	}
	if progress != nil {
		progress.Lock()
		saveErr := progress.save()
		progress.Unlock()
		if saveErr != nil {
			return saveErr
		}
	}
	if n != r.Size || hex.EncodeToString(sum.Sum(nil)) != r.SHA256 {
		return errf("Архив повреждён: размер или SHA-256 не совпадает")
	}
	return nil
}
func unpackPanelRelease(archive, dir, arch string) error {
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer z.Close()
	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		name := f.Name
		clean := filepath.ToSlash(filepath.Clean(name))
		if !strings.HasPrefix(name, "itrupanel/") || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "/../") || clean != strings.TrimSuffix(name, "/") || seen[clean] || f.Mode()&os.ModeSymlink != 0 {
			return errf("Небезопасный путь в архиве")
		}
		seen[clean] = true
		if f.UncompressedSize64 > 512<<20 {
			return errf("Файл слишком велик")
		}
		total += f.UncompressedSize64
		if total > 512<<20 || len(seen) > 10000 {
			return errf("Распакованный архив слишком велик")
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0700); e != nil {
				return e
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return errf("Неподдерживаемый тип файла")
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		ce := out.Close()
		in.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	for _, name := range []string{"itrupanel/update.sh", "itrupanel/bin/itrupanel-linux-" + arch} {
		if !seen[name] {
			return errf("В архиве отсутствует %s", name)
		}
	}
	return os.Chmod(filepath.Join(dir, "itrupanel", "bin", "itrupanel-linux-"+arch), 0700)
}
func runPanelUpdateWorker(id string) (err error) {
	if os.Geteuid() != 0 || runtime.GOOS != "linux" || !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(id) {
		return errf("Неверный запуск службы обновления")
	}
	dir := filepath.Join(cfg.Home, "var", "panel-updates", id)
	b, e := os.ReadFile(filepath.Join(dir, "release.json"))
	if e != nil {
		return e
	}
	key, _ := hex.DecodeString(releasePublicKey)
	r, e := verifyPanelRelease(b, key)
	if e != nil {
		return e
	}
	status, e := readPanelUpdate()
	if e != nil || status == nil || status.ID != id {
		return errf("Задание обновления не найдено")
	}
	lock, e := os.OpenFile("/run/lock/itrupanel-online-update.lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errf("Другая служба обновления работает")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	w := &panelUpdateWriter{r: status}
	w.r.State = "running"
	if e = w.stage("Скачивание полной сборки " + r.Version); e != nil {
		return e
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				w.Lock()
				w.save()
				w.Unlock()
			}
		}
	}()
	defer func() {
		w.Lock()
		defer w.Unlock()
		if err != nil {
			w.r.State = "error"
			w.r.Stage = "Обновление не завершено"
			w.r.Error = err.Error()
			w.r.Log = tailLog(w.r.Log + "\n" + err.Error())
		} else {
			w.r.State = "done"
			w.r.Stage = "Обновлено до " + r.Version
		}
		w.save()
		os.Remove(filepath.Join(dir, "worker"))
	}()
	work, e := os.MkdirTemp("/var/tmp", "itrupanel-online-*")
	if e != nil {
		return e
	}
	defer os.RemoveAll(work)
	var disk syscall.Statfs_t
	if e = syscall.Statfs(work, &disk); e != nil {
		return e
	}
	if uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(r.Size*4+(64<<20)) {
		return errf("Недостаточно свободного места в /var/tmp")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if e = downloadPanelArchive(ctx, releaseHTTPClient(10*time.Minute), r, filepath.Join(work, "release.zip"), w); e != nil {
		return e
	}
	if e = w.stage("Подпись и SHA-256 проверены. Распаковка"); e != nil {
		return e
	}
	if e = unpackPanelRelease(filepath.Join(work, "release.zip"), work, runtime.GOARCH); e != nil {
		return e
	}
	bin := filepath.Join(work, "itrupanel", "bin", "itrupanel-linux-"+runtime.GOARCH)
	versionCtx, versionCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer versionCancel()
	out, e := exec.CommandContext(versionCtx, bin, "version").CombinedOutput()
	if e != nil || strings.TrimSpace(string(out)) != r.Version {
		return errf("Версия программы не совпадает с выпуском")
	}
	// Keep the current panel running until an authenticated administrator applies
	// this exact prepared release. No installed files or settings change yet.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer waitCancel()
	if e = waitPanelUpdateApply(waitCtx, w); e != nil {
		return e
	}
	if e = w.stage("Установка, миграции и перезапуск панели"); e != nil {
		return e
	}
	command := exec.Command("/bin/bash", filepath.Join(work, "itrupanel", "update.sh"))
	command.Stdout = w
	command.Stderr = w
	if e = command.Run(); e != nil {
		return fmt.Errorf("Установщик завершился с ошибкой: %w. Подробности в журнале", e)
	}
	return nil
}

// Only the worker writes status; the API sends an atomic, run-scoped signal.
func applyPanelUpdate(id string) (*PanelUpdateRun, error) {
	if reason := updateSupportReason(); reason != "" {
		return nil, errf("%s", reason)
	}
	if !updateGate.TryLock() {
		return nil, errf("Дождитесь завершения текущих операций или закройте Shell-клиент")
	}
	defer updateGate.Unlock()
	return signalPanelUpdateApply(id)
}
func signalPanelUpdateApply(id string) (*PanelUpdateRun, error) {
	r := reconcilePanelUpdate()
	if r == nil || r.ID != id || !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(id) {
		return nil, errf("Задание обновления не найдено")
	}
	// A retry after a lost response must not launch a second installer.
	if r.State == "running" {
		if _, e := os.Stat(updateApplyPath(id)); e == nil {
			return r, nil
		}
	}
	if r.State != "ready" {
		return nil, errf("Обновление ещё не готово к применению")
	}
	jobsMu.Lock()
	busy := false
	for _, j := range jobs {
		if j.Status == "running" {
			busy = true
		}
	}
	jobsMu.Unlock()
	if busy {
		return nil, errf("Дождитесь завершения фоновых задач панели")
	}
	if e := writeUpdateJSON(updateApplyPath(id), map[string]string{"id": id}); e != nil {
		return nil, e
	}
	return r, nil
}
func waitPanelUpdateApply(ctx context.Context, w *panelUpdateWriter) error {
	w.Lock()
	w.r.State = "ready"
	w.r.Stage = "Обновление подготовлено. Нажмите «Перезапустить панель и применить»"
	w.r.Log = tailLog(w.r.Log + "\n" + w.r.Stage + "\n")
	e := w.save()
	id := w.r.ID
	w.Unlock()
	if e != nil {
		return e
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errf("Время ожидания применения истекло. Подготовьте обновление повторно")
		case <-ticker.C:
			b, e := os.ReadFile(updateApplyPath(id))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			var signal struct{ ID string }
			if json.Unmarshal(b, &signal) != nil || signal.ID != id {
				return errf("Неверное подтверждение обновления")
			}
			w.Lock()
			w.r.State = "running"
			w.r.Stage = "Перезапуск панели и применение обновления"
			e = w.save()
			w.Unlock()
			return e
		}
	}
}

// A local Unix socket avoids disabling TLS verification for self-signed panel certificates.
func startPanelHealth() error {
	path := filepath.Join(cfg.Home, "var", "panel-health.sock")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	os.Remove(path)
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return e
	}
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, Version)
	}))
	return nil
}
func checkPanelHealth() error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(cfg.Home, "var", "panel-health.sock"))
	}}}
	res, e := client.Get("http://localhost/health")
	if e != nil {
		return e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 100))
	if e != nil {
		return e
	}
	if res.StatusCode != 200 || strings.TrimSpace(string(b)) != Version {
		return errf("Панель ещё не запустила новую версию")
	}
	fmt.Println(Version)
	return nil
}
func registerPanelUpdates() {
	apiOp("updates.apply", "Применение обновления панели", func(q *Req) (any, error) {
		var in struct{ ID string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return applyPanelUpdate(in.ID)
	})
	api("updates.info", func(q *Req) (any, error) {
		var in struct{ Force bool }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return panelUpdateInfo(in.Force), nil
	})
	api("updates.status", func(q *Req) (any, error) {
		return map[string]any{"current": Version, "run": reconcilePanelUpdate()}, nil
	})
	apiOp("updates.start", "Обновление панели", func(q *Req) (any, error) {
		var in struct{ Version, SHA256 string }
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return startPanelUpdate(in.Version, in.SHA256)
	})
}
func withUpdateGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		updateGate.RLock()
		defer updateGate.RUnlock()
		if panelUpdating() {
			http.Error(w, "Выполняется обновление панели. Повторите после завершения.", http.StatusServiceUnavailable)
			return
		}
		next(w, r)
	}
}
