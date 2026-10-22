// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Хранение резервных копий на внешнем FTP/FTPS (через curl). Пароль передаётся в netrc-файле,
// чтобы не светиться в списке процессов.

func ftpBaseURL(f BackupFTP) string {
	port := f.Port
	if port == 0 {
		port = 21
	}
	dir := "/" + strings.Trim(f.Dir, "/")
	if dir != "/" {
		dir += "/"
	}
	return fmt.Sprintf("ftp://%s:%d%s", f.Host, port, (&url.URL{Path: dir}).EscapedPath())
}

func curlFTP(f BackupFTP, args ...string) (string, error) {
	if f.Host == "" {
		return "", errf("Не указан FTP-сервер")
	}
	netrc, err := os.CreateTemp("", "itru-netrc-")
	if err != nil {
		return "", err
	}
	defer os.Remove(netrc.Name())
	fmt.Fprintf(netrc, "machine %s login %s password %s\n", f.Host, f.User, f.Pass)
	netrc.Close()
	base := []string{"-sS", "--fail", "--netrc-file", netrc.Name(), "--connect-timeout", "20", "--ftp-pasv"}
	if f.TLS {
		base = append(base, "--ssl-reqd", "--insecure")
	}
	return runTimeout(6*time.Hour, "", "curl", append(base, args...)...)
}

func backupFTPSettings() (BackupFTP, string) {
	mu.Lock()
	defer mu.Unlock()
	t := store.Settings.BackupTarget
	if t == "" {
		t = "local"
	}
	return store.Settings.BackupFTP, t
}

// uploadBackup отправляет копию на FTP и при режиме "ftp" удаляет локальный файл.
func uploadBackup(b *Backup) error {
	f, target := backupFTPSettings()
	if target == "local" {
		return nil
	}
	if backupTargetIsCloud(target) {
		return uploadBackupCloud(b, target)
	}
	name := path.Base(b.File)
	if _, err := curlFTP(f, "--ftp-create-dirs", "-T", b.File, ftpBaseURL(f)+name); err != nil {
		return errf("Загрузка на FTP: %s", firstLine(err.Error()))
	}
	mu.Lock()
	b.RemoteSource = backupFTPSource(f)
	b.Remote = "/" + strings.Trim(strings.Trim(f.Dir, "/")+"/"+name, "/")
	if target == "ftp" {
		os.Remove(P(b.File))
		b.Local = false
	}
	save()
	mu.Unlock()
	return nil
}

// fetchBackup скачивает копию с FTP, если локального файла нет. Возвращает путь и признак временного файла.
func fetchBackup(b *Backup) (string, bool, error) {
	if b.Local && fileExists(b.File) {
		return b.File, false, nil
	}
	if b.Remote == "" {
		return "", false, errf("Файл копии не найден")
	}
	if backupIsCloudSource(b.RemoteSource) {
		return fetchBackupCloud(b)
	}
	f, _ := backupFTPSettings()
	if b.RemoteSource != "" && b.RemoteSource != backupFTPSource(f) {
		return "", false, errf("Подключите исходный FTP-сервер этой копии")
	}
	tmp := backupDir + "/.dl-" + randToken(4) + ".tar.gz"
	u := fmt.Sprintf("ftp://%s:%d%s", f.Host, map[bool]int{true: f.Port, false: 21}[f.Port != 0], (&url.URL{Path: b.Remote}).EscapedPath())
	if _, err := curlFTP(f, "-o", tmp, u); err != nil {
		os.Remove(P(tmp))
		return "", false, errf("Скачивание с FTP: %s", firstLine(err.Error()))
	}
	return tmp, true, nil
}

func deleteRemote(b *Backup) {
	if b.Remote == "" {
		return
	}
	if backupIsCloudSource(b.RemoteSource) {
		deleteRemoteCloud(b)
		return
	}
	f, _ := backupFTPSettings()
	if b.RemoteSource != "" && b.RemoteSource != backupFTPSource(f) {
		return
	}
	port := f.Port
	if port == 0 {
		port = 21
	}
	curlFTP(f, "-Q", "DELE "+b.Remote, fmt.Sprintf("ftp://%s:%d/", f.Host, port), "-o", "/dev/null")
}

func registerBackupFTP() {
	apiOp("backups.ftp.discover", "Поиск существующих копий на FTP", func(q *Req) (any, error) {
		f, t := backupFTPSettings()
		if kind := cloudKindOfTarget(t); kind != "" {
			if _, err := newCloudStore(kind); err != nil {
				return nil, errf("Сначала сохраните настройки облака")
			}
			id := startJob("backups:ftp:discover", "Поиск копий в облаке", q.S.User, func(j *Job) (any, error) { return scanBackupCloud(j, kind) })
			return map[string]any{"job": id}, nil
		}
		if f.Host == "" || f.User == "" {
			return nil, errf("Сначала сохраните настройки FTP")
		}
		id := startJob("backups:ftp:discover", "Поиск копий на FTP", q.S.User, scanBackupFTP)
		return map[string]any{"job": id}, nil
	})
	api("backups.ftp.get", func(q *Req) (any, error) {
		f, t := backupFTPSettings()
		f.Pass = strings.Repeat("•", len(f.Pass))
		s3c, _ := backupS3Settings()
		if s3c.SecretKey != "" {
			s3c.SecretKey = strings.Repeat("•", 12)
		}
		mu.Lock()
		dav, dbx, gdr := store.Settings.BackupDAV, store.Settings.BackupDropbox, store.Settings.BackupGDrive
		mu.Unlock()
		dav.Pass = maskSecret(dav.Pass)
		dbx.AppSecret, dbx.RefreshToken = maskSecret(dbx.AppSecret), maskSecret(dbx.RefreshToken)
		gdr.ClientSecret, gdr.RefreshToken = maskSecret(gdr.ClientSecret), maskSecret(gdr.RefreshToken)
		return map[string]any{"target": t, "ftp": f, "s3": s3c, "webdav": dav, "dropbox": dbx, "gdrive": gdr}, nil
	})
	apiOp("backups.ftp.save", "Резервные копии: место хранения", func(q *Req) (any, error) {
		var in struct {
			Target   string
			FTP      BackupFTP
			S3       BackupS3
			WebDAV   BackupWebDAV
			Dropbox  BackupDropbox
			GDrive   BackupGDrive
			Test     bool
			Schedule *struct {
				Mode          *string `json:"mode"`
				Daily         bool
				SiteSettings  *bool `json:"site_settings"`
				PanelSettings *bool `json:"panel_settings"`
				Databases     *bool `json:"databases"`
				Keep          int
				Hour          int
			}
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Target != "local" && in.Target != "ftp" && in.Target != "both" && !backupTargetIsCloud(in.Target) {
			return nil, errf("Неверное место хранения")
		}
		if in.Schedule != nil {
			if in.Schedule.Mode != nil && *in.Schedule.Mode != "full" && *in.Schedule.Mode != "incremental" {
				return nil, errf("Неверный режим архивации")
			}
			if in.Schedule.Keep < 1 || in.Schedule.Keep > 60 {
				return nil, errf("Хранить от 1 до 60 копий")
			}
			if in.Schedule.Hour < 0 || in.Schedule.Hour > 23 {
				return nil, errf("Час от 0 до 23")
			}
		}
		old, _ := backupFTPSettings()
		if strings.Trim(in.FTP.Pass, "•") == "" {
			in.FTP.Pass = old.Pass
		}
		oldS3, _ := backupS3Settings()
		if strings.Trim(in.S3.SecretKey, "•") == "" {
			in.S3.SecretKey = oldS3.SecretKey
		}
		mu.Lock()
		oldDAV, oldDBX, oldGDR := store.Settings.BackupDAV, store.Settings.BackupDropbox, store.Settings.BackupGDrive
		mu.Unlock()
		if strings.Trim(in.WebDAV.Pass, "•") == "" {
			in.WebDAV.Pass = oldDAV.Pass
		}
		// Токены Dropbox и Google Drive в браузер не отдаются и не принимаются из него: берём сохранённые.
		in.Dropbox.RefreshToken, in.Dropbox.AccountID, in.Dropbox.Account = oldDBX.RefreshToken, oldDBX.AccountID, oldDBX.Account
		if strings.Trim(in.Dropbox.AppSecret, "•") == "" {
			in.Dropbox.AppSecret = oldDBX.AppSecret
		}
		in.GDrive.RefreshToken, in.GDrive.Account = oldGDR.RefreshToken, oldGDR.Account
		if strings.Trim(in.GDrive.ClientSecret, "•") == "" {
			in.GDrive.ClientSecret = oldGDR.ClientSecret
		}
		var cloud cloudStore
		switch cloudKindOfTarget(in.Target) {
		case cloudS3:
			n, err := s3Normalize(in.S3)
			if err != nil {
				return nil, err
			}
			in.S3 = n
			cl, err := newS3Client(n)
			if err != nil {
				return nil, err
			}
			cloud = s3Store{cl}
		case cloudWebDAV:
			n, err := davNormalize(in.WebDAV)
			if err != nil {
				return nil, err
			}
			in.WebDAV = n
			cloud = &davStore{c: n, hc: cloudHTTP1(n.Insecure)}
		case cloudDropbox:
			n, err := dropboxNormalize(in.Dropbox)
			if err != nil {
				return nil, err
			}
			in.Dropbox = n
			cloud = &dropboxStore{c: n, hc: cloudHTTP(false)}
		case cloudGDrive:
			n, err := gdriveNormalize(in.GDrive)
			if err != nil {
				return nil, err
			}
			in.GDrive = n
			cloud = &gdriveStore{c: n, hc: cloudHTTP(false)}
		}
		if cloud != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := cloud.Test(ctx); err != nil {
				return nil, errf("Не удалось подключиться к облаку: %s", firstLine(err.Error()))
			}
			if in.Test {
				return "Подключение к облаку работает", nil
			}
		}
		in.FTP.Host = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(in.FTP.Host, "ftp://"), "ftps://"))
		if strings.ContainsAny(in.FTP.Host+in.FTP.User+in.FTP.Pass+in.FTP.Dir, " \n\r\t") && (in.Target == "ftp" || in.Target == "both") {
			return nil, errf("Пробелы и переводы строк в параметрах FTP не допускаются")
		}
		if in.Target == "ftp" || in.Target == "both" || (in.Test && !backupTargetIsCloud(in.Target)) {
			if in.FTP.Host == "" || in.FTP.User == "" {
				return nil, errf("Укажите сервер и логин FTP")
			}
			if _, err := curlFTP(in.FTP, "--ftp-create-dirs", "-l", ftpBaseURL(in.FTP)); err != nil {
				return nil, errf("Не удалось подключиться к FTP: %s", firstLine(err.Error()))
			}
		}
		if in.Test {
			return "Подключение работает", nil
		}
		mu.Lock()
		store.Settings.BackupTarget = in.Target
		if in.Target == "ftp" || in.Target == "both" || in.Target == "local" {
			store.Settings.BackupFTP = in.FTP
		}
		switch cloudKindOfTarget(in.Target) {
		case cloudS3:
			store.Settings.BackupS3 = in.S3
		case cloudWebDAV:
			store.Settings.BackupDAV = in.WebDAV
		case cloudDropbox:
			store.Settings.BackupDropbox = in.Dropbox
		case cloudGDrive:
			store.Settings.BackupGDrive = in.GDrive
		}
		if in.Schedule != nil {
			if in.Schedule.Mode != nil {
				store.Settings.BackupMode = *in.Schedule.Mode
			}
			store.Settings.BackupDaily, store.Settings.BackupKeep, store.Settings.BackupHour = in.Schedule.Daily, in.Schedule.Keep, in.Schedule.Hour
			if in.Schedule.SiteSettings != nil {
				store.Settings.BackupSiteSettings = *in.Schedule.SiteSettings
			}
			if in.Schedule.PanelSettings != nil {
				store.Settings.BackupPanelSettings = *in.Schedule.PanelSettings
			}
			if in.Schedule.Databases != nil {
				store.Settings.BackupSkipDBs = !*in.Schedule.Databases
			}
		}
		save()
		mu.Unlock()
		return nil, nil
	})
}

// maskSecret заменяет непустой секрет точками: в браузер значения не отдаются.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return strings.Repeat("•", 12)
}
