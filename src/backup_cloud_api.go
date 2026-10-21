// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"strings"
	"time"
)

// Первое подключение Dropbox и Google Drive: владелец аккаунта разрешает доступ на сайте сервиса,
// а панель получает постоянный (refresh) токен и хранит его на сервере. В браузер токены не отдаются.

func registerBackupCloud() {
	apiOp("backups.cloud.oauth.start", "Резервные копии: подключение облака", func(q *Req) (any, error) {
		var in struct {
			Kind         string `json:"kind"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		oldDBX, oldGDR := store.Settings.BackupDropbox, store.Settings.BackupGDrive
		mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		switch in.Kind {
		case cloudDropbox:
			if strings.Trim(in.ClientSecret, "•") == "" && in.ClientID == oldDBX.AppKey {
				in.ClientSecret = oldDBX.AppSecret
			}
			state, link, err := dropboxStart(in.ClientID, in.ClientSecret)
			if err != nil {
				return nil, err
			}
			return map[string]any{"state": state, "url": link}, nil
		case cloudGDrive:
			if strings.Trim(in.ClientSecret, "•") == "" && in.ClientID == oldGDR.ClientID {
				in.ClientSecret = oldGDR.ClientSecret
			}
			state, info, err := gdriveStart(ctx, in.ClientID, in.ClientSecret)
			if err != nil {
				return nil, err
			}
			info["state"] = state
			return info, nil
		}
		return nil, errf("Неизвестный вид облачного хранилища")
	})
	apiOp("backups.cloud.oauth.finish", "Резервные копии: завершение подключения облака", func(q *Req) (any, error) {
		var in struct {
			Kind  string `json:"kind"`
			State string `json:"state"`
			Code  string `json:"code"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		switch in.Kind {
		case cloudDropbox:
			c, err := dropboxFinish(ctx, in.State, in.Code)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			old := store.Settings.BackupDropbox
			if old.Dir == "" {
				old.Dir = "itrupanel"
			}
			c.Dir = old.Dir
			store.Settings.BackupDropbox = c
			save()
			mu.Unlock()
			return map[string]any{"connected": true, "account": c.Account}, nil
		case cloudGDrive:
			c, pending, err := gdriveFinish(ctx, in.State)
			if err != nil {
				return nil, err
			}
			if pending {
				return map[string]any{"pending": true}, nil
			}
			mu.Lock()
			c.Dir = store.Settings.BackupGDrive.Dir
			if c.Dir == "" {
				c.Dir = "itrupanel"
			}
			store.Settings.BackupGDrive = *c
			save()
			mu.Unlock()
			return map[string]any{"connected": true, "account": c.Account}, nil
		}
		return nil, errf("Неизвестный вид облачного хранилища")
	})
}
