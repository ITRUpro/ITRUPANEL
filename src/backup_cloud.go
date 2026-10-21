// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Облачные хранилища резервных копий. Общий слой над четырьмя видами хранилищ:
// S3-совместимые сервисы, WebDAV (Яндекс.Диск, Облако Mail.ru, любой WebDAV), Dropbox и Google Drive.
// Копия может лежать только в облаке («s3», «webdav», …) или и на сервере, и в облаке («local_s3», …).

const (
	cloudS3      = "s3"
	cloudWebDAV  = "webdav"
	cloudDropbox = "dropbox"
	cloudGDrive  = "gdrive"
)

var cloudKinds = []string{cloudS3, cloudWebDAV, cloudDropbox, cloudGDrive}

// Идентификатор хранилища в Backup.RemoteSource: по приставке видно, к какому виду относится копия.
var cloudSourcePrefix = map[string]string{cloudS3: "s3-", cloudWebDAV: "dav-", cloudDropbox: "dbx-", cloudGDrive: "gdr-"}

var cloudTitle = map[string]string{cloudS3: "облако S3", cloudWebDAV: "WebDAV", cloudDropbox: "Dropbox", cloudGDrive: "Google Drive"}

// cloudFileName — допустимое имя файла копии в облаке (у всех видов хранилищ одинаковое).
var cloudFileName = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,128}$`)

// cloudFolder — папка в облаке: латиница, цифры и «._-/»; пустая строка — корень.
var cloudFolder = regexp.MustCompile(`^[A-Za-z0-9._\-/]*$`)

// cloudKindOfTarget возвращает вид хранилища для места хранения вида «s3» или «local_dropbox»; пусто — не облако.
func cloudKindOfTarget(t string) string {
	t = strings.TrimPrefix(t, "local_")
	for _, k := range cloudKinds {
		if t == k {
			return k
		}
	}
	return ""
}

func backupTargetIsCloud(t string) bool { return cloudKindOfTarget(t) != "" }

// cloudTargetKeepsLocal — «здесь и в облаке»: локальный файл остаётся.
func cloudTargetKeepsLocal(t string) bool { return strings.HasPrefix(t, "local_") }

func cloudKindOfSource(src string) string {
	for k, p := range cloudSourcePrefix {
		if strings.HasPrefix(src, p) {
			return k
		}
	}
	return ""
}

func backupIsCloudSource(src string) bool { return cloudKindOfSource(src) != "" }

// cloudSourceID строит стабильный идентификатор хранилища из его отличительных частей.
func cloudSourceID(kind string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return cloudSourcePrefix[kind] + fmt.Sprintf("%x", sum[:8])
}

type cloudObject struct {
	Name string // имя файла копии
	Key  string // что хранится в Backup.Remote и передаётся в Download, Head и Delete
}

type cloudStore interface {
	Kind() string
	Source() string
	Test(ctx context.Context) error
	Upload(ctx context.Context, file, name string) (key string, err error)
	Download(ctx context.Context, key, dst string) error
	Head(ctx context.Context, key string, n int64) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]cloudObject, error)
}

// s3Store подключает S3-клиент к общему интерфейсу.
type s3Store struct{ *s3Client }

func (s s3Store) Kind() string   { return cloudS3 }
func (s s3Store) Source() string { return backupS3Source(s.c) }
func (s s3Store) Upload(ctx context.Context, file, name string) (string, error) {
	if err := s.s3Client.Upload(ctx, file, name); err != nil {
		return "", err
	}
	return s.keyFor(name), nil
}
func (s s3Store) List(ctx context.Context) ([]cloudObject, error) {
	names, err := s.s3Client.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cloudObject, 0, len(names))
	for _, n := range names {
		out = append(out, cloudObject{Name: n, Key: s.keyFor(n)})
	}
	return out, nil
}

// newCloudStore создаёт клиента по сохранённым настройкам данного вида хранилища.
func newCloudStore(kind string) (cloudStore, error) {
	mu.Lock()
	st := store.Settings
	mu.Unlock()
	return cloudStoreFromSettings(kind, st)
}

// cloudStoreFromSettings не трогает mu: им можно пользоваться, держа блокировку.
func cloudStoreFromSettings(kind string, st Settings) (cloudStore, error) {
	switch kind {
	case cloudS3:
		c, err := newS3Client(st.BackupS3)
		if err != nil {
			return nil, errf("Облако не настроено: подключите хранилище, где лежит эта копия")
		}
		return s3Store{c}, nil
	case cloudWebDAV:
		return newDAVStore(st.BackupDAV)
	case cloudDropbox:
		return newDropboxStore(st.BackupDropbox)
	case cloudGDrive:
		return newGDriveStore(st.BackupGDrive)
	}
	return nil, errf("Неизвестный вид облачного хранилища")
}

// cloudHTTP — клиент для облачных API. Перенаправления разрешены: у API и WebDAV они обычны,
// а заголовок Authorization Go не пересылает на другой домен.
func cloudHTTP(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = insecureTLSConfig()
	}
	return &http.Client{Transport: tr}
}

// cloudRetry повторяет операцию при временных ошибках сети и сервера (до трёх попыток).
func cloudRetry(ctx context.Context, f func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
		}
		if err = f(); err == nil {
			return nil
		}
		if _, permanent := err.(cloudPermanent); permanent || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// cloudPermanent — ошибка, повтор которой бессмыслен (неверный ключ, нет места, нет доступа).
type cloudPermanent struct{ error }

func (e cloudPermanent) Unwrap() error { return e.error }

func permanent(err error) error {
	if err == nil {
		return nil
	}
	return cloudPermanent{err}
}

// ---------- интеграция с резервными копиями ----------

func backupCloudTarget() string {
	mu.Lock()
	defer mu.Unlock()
	if store.Settings.BackupTarget == "" {
		return "local"
	}
	return store.Settings.BackupTarget
}

// uploadBackupCloud отправляет копию в облако; при месте хранения «только в облаке» локальный файл удаляется.
func uploadBackupCloud(b *Backup, target string) error {
	cl, err := newCloudStore(cloudKindOfTarget(target))
	if err != nil {
		return err
	}
	name := path.Base(b.File)
	if !cloudFileName.MatchString(name) {
		return errf("Облако: недопустимое имя файла копии")
	}
	key, err := cl.Upload(context.Background(), P(b.File), name)
	if err != nil {
		return err
	}
	mu.Lock()
	b.RemoteSource = cl.Source()
	b.Remote = key
	if !cloudTargetKeepsLocal(target) {
		os.Remove(P(b.File))
		b.Local = false
	}
	save()
	mu.Unlock()
	return nil
}

// cloudForBackup возвращает клиента для копии, лежащей в облаке, если подключено то же хранилище.
func cloudForBackup(b *Backup) (cloudStore, error) {
	kind := cloudKindOfSource(b.RemoteSource)
	cl, err := newCloudStore(kind)
	if err != nil {
		return nil, errf("Облако не настроено: подключите хранилище, где лежит эта копия")
	}
	if b.RemoteSource != cl.Source() {
		return nil, errf("Подключите исходное облачное хранилище этой копии")
	}
	return cl, nil
}

func fetchBackupCloud(b *Backup) (string, bool, error) {
	cl, err := cloudForBackup(b)
	if err != nil {
		return "", false, err
	}
	tmp := backupDir + "/.dl-" + randToken(4) + ".tar.gz"
	if err := cl.Download(context.Background(), b.Remote, P(tmp)); err != nil {
		return "", false, err
	}
	return tmp, true, nil
}

func deleteRemoteCloud(b *Backup) {
	cl, err := cloudForBackup(b)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cl.Delete(ctx, b.Remote)
}

// scanBackupCloud ищет архивы ITRUPANEL в папке облака и добавляет их в список (как поиск на FTP).
func scanBackupCloud(j *Job, kind string) (any, error) {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	cl, err := newCloudStore(kind)
	if err != nil {
		return nil, errf("Сначала сохраните настройки облака")
	}
	j.progress("Чтение списка копий в облаке")
	all, err := cl.List(j.ctx)
	if err != nil {
		return nil, err
	}
	var found []cloudObject
	seen := map[string]bool{}
	for _, o := range all {
		if seen[o.Name] {
			continue
		}
		if backupArchiveName.MatchString(o.Name) || backupDBArchiveName.MatchString(o.Name) {
			seen[o.Name] = true
			found = append(found, o)
		}
	}
	if len(found) > 500 {
		return nil, errf("В папке больше 500 архивов; разделите их по папкам")
	}
	source := cl.Source()
	rows := []*Backup{}
	skipped := 0
	deadline := time.Now().Add(20 * time.Minute)
	for i, o := range found {
		if time.Now().After(deadline) {
			return nil, errf("Превышено время поиска. Разделите архивы по папкам")
		}
		j.progress(fmt.Sprintf("Проверка манифеста %d/%d: %s", i+1, len(found), o.Name))
		rc, err := cl.Head(j.ctx, o.Key, 2<<20)
		if err != nil {
			skipped++
			j.progress("Пропущен архив: " + o.Name + " — недоступен")
			continue
		}
		b, err := inspectBackupManifestAt(rc, o.Name, o.Key, source)
		rc.Close()
		if err != nil {
			skipped++
			j.progress("Пропущен архив: " + o.Name + " — манифест недоступен или формат не поддерживается")
			continue
		}
		rows = append(rows, b)
	}
	if now, err := newCloudStore(kind); err != nil || now.Source() != source {
		return nil, errf("Настройки облака изменились во время поиска. Повторите поиск")
	}
	added, existing, err := mergeDiscoveredBackups(rows)
	if err != nil {
		return nil, err
	}
	info := fmt.Sprintf("Добавлено: %d. Уже в списке: %d. Пропущено: %d. Проверены манифесты; содержимое архивов проверяется при восстановлении.", added, existing, skipped)
	j.progress(info)
	return map[string]any{"info": info, "added": added, "existing": existing, "skipped": skipped}, nil
}

// scanBackupS3 оставлен для переноса данных и тестов: поиск в S3-облаке.
func scanBackupS3(j *Job) (any, error) { return scanBackupCloud(j, cloudS3) }

// cloudTokenMu защищает кэш токенов доступа (Dropbox и Google Drive).
var cloudTokenMu sync.Mutex

func insecureTLSConfig() *tls.Config { return &tls.Config{InsecureSkipVerify: true} }
