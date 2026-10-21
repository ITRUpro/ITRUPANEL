// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

var backupArchiveName = regexp.MustCompile(`^([a-z0-9][a-z0-9.-]*)_([0-9]{8}-[0-9]{6}-[a-f0-9]+)\.tar\.gz$`)
var backupIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-f0-9]+$`)

// A backup remains tied to its source server even if storage settings change.
// Passwords are deliberately excluded: credential rotation must remain possible.
func backupFTPSource(f BackupFTP) string {
	port := f.Port
	if port == 0 {
		port = 21
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d/%s/%t", strings.ToLower(f.Host), port, f.User, f.TLS)))
	return hex.EncodeToString(sum[:])
}
func backupRemoteURL(f BackupFTP, remote string) string {
	port := f.Port
	if port == 0 {
		port = 21
	}
	return fmt.Sprintf("ftp://%s:%d%s", f.Host, port, (&url.URL{Path: remote}).EscapedPath())
}

// Stream bounded data instead of downloading every archive to the panel disk.
// Metadata inspection intentionally stops the transfer after the manifest.
func inspectFTP(f BackupFTP, remote string, listing bool, inspect func(io.Reader) error) error {
	if cfg.Dry {
		return errf("Поиск копий на FTP доступен на установленной панели")
	}
	netrc, err := os.CreateTemp("", "itru-discovery-netrc-")
	if err != nil {
		return err
	}
	defer os.Remove(netrc.Name())
	_, err = fmt.Fprintf(netrc, "machine %s login %s password %s\n", f.Host, f.User, f.Pass)
	closeErr := netrc.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"--silent", "--fail", "--netrc-file", netrc.Name(), "--connect-timeout", "15", "--max-time", "55", "--ftp-pasv", "--proto", "=ftp"}
	if f.TLS {
		args = append(args, "--ssl-reqd", "--insecure")
	}
	if listing {
		args = append(args, "--list-only")
	}
	args = append(args, "--url", backupRemoteURL(f, remote))
	cmd := exec.CommandContext(ctx, "curl", args...)
	stream, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// curl errors can contain credentials or server-controlled messages; do not log them.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return err
	}
	err = inspect(stream)
	if !listing || err != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if listing && waitErr != nil {
		return errf("Не удалось прочитать папку FTP. Проверьте подключение")
	}
	return nil
}

func discoverFTPNames(r io.Reader) ([]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errf("Слишком большой список FTP; выберите отдельную папку с копиями")
	}
	seen := map[string]bool{}
	names := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSuffix(line, "\r")
		if !backupArchiveName.MatchString(name) && !backupDBArchiveName.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) > 500 {
			return nil, errf("В папке больше 500 архивов; разделите их по папкам")
		}
	}
	sort.Strings(names)
	return names, nil
}

func inspectBackupManifest(r io.Reader, name string, f BackupFTP) (*Backup, error) {
	return inspectBackupManifestAt(r, name, path.Join("/", f.Dir, name), backupFTPSource(f))
}

// inspectBackupManifestAt читает манифест архива; remote — путь или ключ файла в хранилище, source — идентификатор хранилища.
func inspectBackupManifestAt(r io.Reader, name, remote, source string) (*Backup, error) {
	parts, dbName := backupArchiveName.FindStringSubmatch(name), ""
	if p := backupDBArchiveName.FindStringSubmatch(name); p != nil {
		parts, dbName = []string{p[0], "", p[2]}, p[1]
	}
	if parts == nil {
		return nil, errf("Неверное имя архива")
	}
	// Current portable format puts meta.json first. No website data is extracted.
	gz, err := gzip.NewReader(io.LimitReader(r, 2<<20))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, 2<<20))
	h, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if strings.TrimPrefix(h.Name, "./") != "meta.json" || h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > 1<<20 {
		return nil, errf("Архив не содержит начальный манифест ITRUPANEL")
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, err
	}
	m, err := decodeBackupMetadata(data)
	if err != nil {
		return nil, err
	}
	if m.Version != 2 || m.Created.IsZero() {
		return nil, errf("Неверный формат или домен копии")
	}
	switch m.Kind {
	case "database":
		// Копия одной базы: db-<имя>_<id>.tar.gz, в манифесте ровно эта база.
		if dbName == "" || m.Domain != "" || m.ParentID != "" || len(m.DBs) != 1 || m.DBs[0] != dbName {
			return nil, errf("Неверный формат копии базы")
		}
	case "panel":
		if parts[1] != "panel" || m.Domain != "" || m.ParentID != "" || m.Panel == nil {
			return nil, errf("Неверный формат копии настроек панели")
		}
	case "full", "files", "db", "settings":
		if dbName != "" || m.Domain != parts[1] {
			return nil, errf("Неверный формат или домен копии")
		}
		if _, err = toASCII(m.Domain); err != nil {
			return nil, err
		}
	default:
		return nil, errf("Неизвестное содержимое копии")
	}
	if m.ParentID != "" && (!backupIDPattern.MatchString(m.ParentID) || m.ParentID == parts[2] || (m.Kind != "full" && m.Kind != "files")) {
		return nil, errf("Неверная цепочка копии")
	}
	for _, name := range m.Deleted {
		if rel, e := backupSafeName(name); e != nil || rel == "." {
			return nil, errf("Неверный список удалённых файлов")
		}
	}
	sum := sha256.Sum256(data)
	mode := "full"
	if m.ParentID != "" {
		mode = "incremental"
	}
	return &Backup{ID: parts[2], Site: m.Domain, Kind: m.Kind, DB: dbName, Created: m.Created, Mode: mode, ParentID: m.ParentID,
		File: backupDir + "/" + name, Remote: remote, RemoteSource: source, Imported: true,
		ManifestHash: hex.EncodeToString(sum[:]), SiteSettings: m.Site != nil && m.CustomNginx != nil, PanelSettings: m.Panel != nil}, nil
}

// Imported copies are manual, so automatic retention never deletes the old collection.
func mergeDiscoveredBackups(rows []*Backup) (int, int, error) {
	mu.Lock()
	defer mu.Unlock()
	next := append([]*Backup(nil), store.Backups...)
	byID := map[string]*Backup{}
	for _, b := range next {
		byID[b.ID] = b
	}
	added, existing := 0, 0
	for _, b := range rows {
		if old := byID[b.ID]; old != nil {
			if old.Site != b.Site || old.Remote != b.Remote || (old.RemoteSource != "" && old.RemoteSource != b.RemoteSource) || (old.ManifestHash != "" && old.ManifestHash != b.ManifestHash) {
				return 0, 0, errf("Конфликт архива %s: такой ID уже относится к другой копии", b.ID)
			}
			existing++
			continue
		}
		byID[b.ID] = b
		next = append(next, b)
		added++
	}
	previous := store.Backups
	store.Backups = next
	if err := saveChecked(); err != nil {
		store.Backups = previous
		return 0, 0, err
	}
	return added, existing, nil
}

func scanBackupFTP(j *Job) (any, error) {
	backupWorkMu.Lock()
	defer backupWorkMu.Unlock()
	f, _ := backupFTPSettings()
	if f.Host == "" || f.User == "" {
		return nil, errf("Сначала сохраните настройки FTP")
	}
	j.progress("Чтение списка копий на FTP")
	var names []string
	err := inspectFTP(f, "/"+strings.Trim(f.Dir, "/")+"/", true, func(r io.Reader) error { var e error; names, e = discoverFTPNames(r); return e })
	if err != nil {
		return nil, err
	}
	rows := []*Backup{}
	skipped := 0
	deadline := time.Now().Add(20 * time.Minute)
	for i, name := range names {
		if time.Now().After(deadline) {
			return nil, errf("Превышено время поиска. Разделите архивы по папкам")
		}
		j.progress(fmt.Sprintf("Проверка манифеста %d/%d: %s", i+1, len(names), name))
		var b *Backup
		err = inspectFTP(f, path.Join("/", f.Dir, name), false, func(r io.Reader) error { var e error; b, e = inspectBackupManifest(r, name, f); return e })
		if err != nil {
			skipped++
			j.progress("Пропущен архив: " + name + " — манифест недоступен или формат не поддерживается")
			continue
		}
		rows = append(rows, b)
	}
	current, _ := backupFTPSettings()
	if current != f {
		return nil, errf("Настройки FTP изменились во время поиска. Повторите поиск")
	}
	added, existing, err := mergeDiscoveredBackups(rows)
	if err != nil {
		return nil, err
	}
	info := fmt.Sprintf("Добавлено: %d. Уже в списке: %d. Пропущено: %d. Проверены манифесты; содержимое архивов проверяется при восстановлении.", added, existing, skipped)
	j.progress(info)
	return map[string]any{"info": info, "added": added, "existing": existing, "skipped": skipped}, nil
}

// Caller holds mu. Checks the whole chain, including source compatibility.
func backupRestoreIssue(b *Backup) string {
	switch {
	case b.Kind == "database":
		if findDB(b.DB) == nil {
			return "Сначала создайте базу " + b.DB + " в разделе «Базы данных»"
		}
	case b.Kind == "panel":
	case findSite(b.Site) == nil:
		return "Сначала создайте сайт с этим доменом на новой панели"
	}
	byID := map[string]*Backup{}
	for _, v := range store.Backups {
		byID[v.ID] = v
	}
	seen := map[string]bool{}
	for b != nil {
		if seen[b.ID] || len(seen) > 128 {
			return "Повреждена цепочка копий"
		}
		seen[b.ID] = true
		if !b.Local || !fileExists(b.File) {
			if b.Remote == "" {
				return "Файл копии недоступен"
			}
			if msg := backupSourceMismatch(b); msg != "" {
				return msg
			}
		}
		if b.ParentID == "" {
			return ""
		}
		p := byID[b.ParentID]
		if p == nil || p.Site != b.Site || p.Kind != b.Kind {
			return "Не хватает части цепочки: " + b.ParentID
		}
		b = p
	}
	return ""
}

// backupSourceMismatch сравнивает хранилище копии с подключённым сейчас (FTP или облако); пустая строка — совпадает.
// Вызывающий держит mu.
func backupSourceMismatch(b *Backup) string {
	if b.RemoteSource == "" {
		return ""
	}
	if backupIsS3Source(b.RemoteSource) {
		if c, err := s3Normalize(store.Settings.BackupS3); err != nil || b.RemoteSource != backupS3Source(c) {
			return "Подключите исходное облачное хранилище этой копии"
		}
		return ""
	}
	if b.RemoteSource != backupFTPSource(store.Settings.BackupFTP) {
		return "Подключите исходный FTP-сервер этой копии"
	}
	return ""
}
