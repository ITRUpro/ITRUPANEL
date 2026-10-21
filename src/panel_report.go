// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// При каждой проверке обновлений панель сообщает itrupanel.com случайный ID установки и версию
// (для отчёта «где стоит панель»). Домены, пароли и настройки не отправляются; IP и страну сайт
// видит по соединению. Отключение — файл etc/installation-stat-disabled (его же создаёт
// ITRUPANEL_INSTALL_STATS=0 при установке).

const panelReportEndpoint = "https://itrupanel.com/install-stats/api.php"

var reStatID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func statDir() string { return filepath.Join(cfg.Home, "etc") }

func panelReportDisabled() bool {
	_, err := os.Stat(filepath.Join(statDir(), "installation-stat-disabled"))
	return err == nil
}

// installationStatID — ID из установщика или новый для панелей, установленных до статистики.
func installationStatID() (string, error) {
	path := filepath.Join(statDir(), "installation-stat-id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); reStatID.MatchString(id) {
			return id, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		return "", err
	}
	return id, nil
}

func sendPanelReport(ctx context.Context, client *http.Client, endpoint string) error {
	if panelReportDisabled() {
		return nil
	}
	id, err := installationStatID()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"schema": 1, "event": "active", "install_id": id, "version": Version})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode != 200 {
		return errf("HTTP %d", res.StatusCode)
	}
	return nil
}

// reportPanelActive вызывается при проверке обновлений и не задерживает её.
func reportPanelActive() {
	if cfg.Dry {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sendPanelReport(ctx, releaseHTTPClient(10*time.Second), panelReportEndpoint)
	}()
}
