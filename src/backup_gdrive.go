// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Хранение копий в Google Drive (API v3). Владелец аккаунта создаёт в Google Cloud свой OAuth-клиент
// (тип «TV и устройства с ограниченным вводом»); панель получает доступ с правом drive.file: она видит
// только созданные ею файлы и папки, а не весь диск. Доступ выдаётся один раз по коду на google.com/device.

type BackupGDrive struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Account      string `json:"account"` // почта аккаунта: отличает хранилище при повторной авторизации
	Dir          string `json:"dir"`
}

// Адреса API; переменные, чтобы тесты могли подставить свой сервер.
var (
	googleAuth      = "https://oauth2.googleapis.com"
	googleAPI       = "https://www.googleapis.com/drive/v3"
	googleUpload    = "https://www.googleapis.com/upload/drive/v3"
	googleScope     = "https://www.googleapis.com/auth/drive.file"
	gdriveChunk     = int64(32 << 20) // кратно 256 КиБ, как требует Google
	gdriveFolderMIM = "application/vnd.google-apps.folder"
)

func gdriveNormalize(c BackupGDrive) (BackupGDrive, error) {
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.ClientSecret = strings.TrimSpace(c.ClientSecret)
	c.RefreshToken = strings.TrimSpace(c.RefreshToken)
	if c.ClientID == "" || len(c.ClientID) > 200 || strings.ContainsAny(c.ClientID, " \r\n\t") {
		return c, errf("Укажите идентификатор клиента Google (Client ID)")
	}
	if c.ClientSecret == "" || strings.ContainsAny(c.ClientSecret, " \r\n\t") {
		return c, errf("Укажите секрет клиента Google (Client secret)")
	}
	if c.RefreshToken == "" {
		return c, errf("Google Drive не подключён: нажмите «Подключить Google Drive» и введите код на google.com/device")
	}
	c.Dir = strings.Trim(strings.TrimSpace(c.Dir), "/")
	if !cloudFolder.MatchString(c.Dir) || strings.Contains(c.Dir, "//") || strings.Contains(c.Dir, "..") || len(c.Dir) > 200 {
		return c, errf("Папка: только латиница, цифры и символы . _ - /")
	}
	return c, nil
}

type gdriveToken struct {
	token   string
	expires time.Time
}

var (
	gdriveTokens   = map[string]gdriveToken{}
	gdriveTokensMu sync.Mutex
)

type gdriveStore struct {
	c        BackupGDrive
	hc       *http.Client
	folderID string // кэш идентификатора папки
}

func newGDriveStore(c BackupGDrive) (cloudStore, error) {
	n, err := gdriveNormalize(c)
	if err != nil {
		return nil, errf("Google Drive не настроен: подключите хранилище, где лежит эта копия")
	}
	return &gdriveStore{c: n, hc: cloudHTTP(false)}, nil
}

func (g *gdriveStore) Kind() string { return cloudGDrive }
func (g *gdriveStore) Source() string {
	id := g.c.Account
	if id == "" {
		id = g.c.ClientID
	}
	return cloudSourceID(cloudGDrive, strings.ToLower(id), g.c.Dir)
}

func (g *gdriveStore) formPost(ctx context.Context, u string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.hc.Do(req)
	if err != nil {
		return 0, errf("Google Drive: %s", firstLine(err.Error()))
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(data, out); err != nil {
		return res.StatusCode, errf("Google Drive: непонятный ответ сервера (%s)", res.Status)
	}
	return res.StatusCode, nil
}

func (g *gdriveStore) accessToken(ctx context.Context) (string, error) {
	cacheKey := g.c.ClientID + "\x00" + g.c.RefreshToken
	gdriveTokensMu.Lock()
	t, ok := gdriveTokens[cacheKey]
	gdriveTokensMu.Unlock()
	if ok && time.Until(t.expires) > time.Minute {
		return t.token, nil
	}
	var out struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	_, err := g.formPost(ctx, googleAuth+"/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {g.c.RefreshToken}, "client_id": {g.c.ClientID}, "client_secret": {g.c.ClientSecret}}, &out)
	if err != nil {
		return "", err
	}
	if out.Access == "" {
		if out.Error == "invalid_grant" {
			return "", permanent(errf("Google Drive: доступ отозван или истёк. Если приложение в Google Cloud в режиме «Testing», доступ действует 7 дней — переведите его в «In production». Подключите Google Drive заново"))
		}
		return "", permanent(errf("Google Drive: не удалось получить токен доступа (%s)", firstLine(out.Error+" "+out.Desc)))
	}
	if out.Expires <= 0 {
		out.Expires = 3600
	}
	gdriveTokensMu.Lock()
	gdriveTokens[cacheKey] = gdriveToken{out.Access, time.Now().Add(time.Duration(out.Expires) * time.Second)}
	gdriveTokensMu.Unlock()
	return out.Access, nil
}

func gdriveErrorText(status int, body []byte) (string, bool) {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	text := string(body)
	reason := ""
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		text = e.Error.Message
		if len(e.Error.Errors) > 0 {
			reason = e.Error.Errors[0].Reason
		}
	}
	switch {
	case status == 401:
		return "токен доступа недействителен (повторная попытка получит новый токен)", false
	case reason == "storageQuotaExceeded" || reason == "quotaExceeded":
		return "в Google Drive не хватает места", true
	case reason == "rateLimitExceeded" || reason == "userRateLimitExceeded" || status == 429:
		return "слишком много запросов, повторите позже", false
	case status == 403 && (reason == "accessNotConfigured" || strings.Contains(text, "has not been used")):
		return "в проекте Google Cloud не включён Google Drive API: включите его в разделе «API и сервисы»", true
	case status == 403:
		return "доступ запрещён: " + firstLine(text), true
	case status == 404:
		return "не найдено", true
	}
	return firstLine(text), status >= 400 && status < 500 && status != 408 && status != 429
}

// call выполняет запрос к API с токеном доступа; успешный ответ возвращается открытым.
func (g *gdriveStore) call(ctx context.Context, method, u string, body io.Reader, size int64, headers map[string]string, action string) (*http.Response, error) {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := g.hc.Do(req)
	if err != nil {
		return nil, errf("Google Drive: %s", firstLine(err.Error()))
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 || res.StatusCode == 308 {
		return res, nil
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	if res.StatusCode == 401 {
		gdriveTokensMu.Lock()
		delete(gdriveTokens, g.c.ClientID+"\x00"+g.c.RefreshToken)
		gdriveTokensMu.Unlock()
	}
	if res.StatusCode == 429 {
		if s, _ := strconv.Atoi(res.Header.Get("Retry-After")); s > 0 && s <= 60 {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(s) * time.Second):
			}
		}
	}
	msg, perm := gdriveErrorText(res.StatusCode, data)
	err = errf("Google Drive (%s): %s", action, msg)
	if perm {
		return nil, permanent(err)
	}
	return nil, err
}

func (g *gdriveStore) jsonCall(ctx context.Context, method, u string, in, out any, action string) error {
	var body io.Reader
	var size int64
	headers := map[string]string{}
	if in != nil {
		b, _ := json.Marshal(in)
		body, size = bytes.NewReader(b), int64(len(b))
		headers["Content-Type"] = "application/json; charset=UTF-8"
	}
	res, err := g.call(ctx, method, u, body, size, headers, action)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errf("Google Drive: непонятный ответ сервера")
		}
	}
	return nil
}

func gdriveQ(s string) string { return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) }

// ensureFolder находит (или создаёт) папку Dir; вложенные папки создаются по очереди. Результат кэшируется.
func (g *gdriveStore) ensureFolder(ctx context.Context, create bool) (string, error) {
	if g.folderID != "" {
		return g.folderID, nil
	}
	parent := "root"
	if g.c.Dir == "" {
		g.folderID = parent
		return parent, nil
	}
	for _, seg := range strings.Split(g.c.Dir, "/") {
		q := fmt.Sprintf("name='%s' and mimeType='%s' and '%s' in parents and trashed=false", gdriveQ(seg), gdriveFolderMIM, parent)
		var out struct {
			Files []struct {
				ID string `json:"id"`
			} `json:"files"`
		}
		if err := g.jsonCall(ctx, "GET", googleAPI+"/files?"+url.Values{"q": {q}, "fields": {"files(id)"}, "pageSize": {"1"}, "spaces": {"drive"}}.Encode(), nil, &out, "поиск папки"); err != nil {
			return "", err
		}
		if len(out.Files) > 0 {
			parent = out.Files[0].ID
			continue
		}
		if !create {
			return "", nil
		}
		var made struct {
			ID string `json:"id"`
		}
		if err := g.jsonCall(ctx, "POST", googleAPI+"/files?fields=id", map[string]any{"name": seg, "mimeType": gdriveFolderMIM, "parents": []string{parent}}, &made, "создание папки"); err != nil {
			return "", err
		}
		if made.ID == "" {
			return "", errf("Google Drive: не удалось создать папку")
		}
		parent = made.ID
	}
	g.folderID = parent
	return parent, nil
}

func (g *gdriveStore) Test(ctx context.Context) error {
	_, err := g.ensureFolder(ctx, true)
	return err
}

// Upload загружает файл возобновляемой загрузкой частями; ключом копии служит идентификатор файла.
func (g *gdriveStore) Upload(ctx context.Context, file, name string) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	size := st.Size()
	if size == 0 {
		return "", errf("Google Drive: пустой файл копии")
	}
	folder, err := g.ensureFolder(ctx, true)
	if err != nil {
		return "", err
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Если файл с таким именем уже есть (повторная загрузка), он будет заменён после успешной загрузки.
	old, _ := g.find(ctx, folder, name)
	var id string
	err = cloudRetry(ctx, func() error {
		meta, _ := json.Marshal(map[string]any{"name": name, "parents": []string{folder}})
		res, err := g.call(ctx, "POST", googleUpload+"/files?uploadType=resumable&fields=id", bytes.NewReader(meta), int64(len(meta)), map[string]string{
			"Content-Type": "application/json; charset=UTF-8", "X-Upload-Content-Type": "application/gzip", "X-Upload-Content-Length": strconv.FormatInt(size, 10)}, "загрузка")
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		session := res.Header.Get("Location")
		if session == "" {
			return errf("Google Drive: сервер не вернул адрес загрузки")
		}
		var offset int64
		for offset < size {
			n := gdriveChunk
			if size-offset < n {
				n = size - offset
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return permanent(err)
			}
			r, err := g.call(ctx, "PUT", session, io.LimitReader(f, n), n, map[string]string{"Content-Range": fmt.Sprintf("bytes %d-%d/%d", offset, offset+n-1, size)}, "загрузка")
			if err != nil {
				return err
			}
			data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body.Close()
			if r.StatusCode == 308 {
				// Сервер сообщает, сколько байт принято: «Range: bytes=0-N».
				var got int64 = -1
				if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=0-") {
					if v, e := strconv.ParseInt(strings.TrimPrefix(rg, "bytes=0-"), 10, 64); e == nil {
						got = v
					}
				}
				if got < offset {
					return errf("Google Drive: сервер принял не все данные части")
				}
				offset = got + 1
				continue
			}
			offset += n
			if r.StatusCode == 200 || r.StatusCode == 201 {
				var out struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(data, &out) != nil || out.ID == "" {
					return errf("Google Drive: сервер не вернул идентификатор файла")
				}
				id = out.ID
			}
		}
		if id == "" {
			return errf("Google Drive: загрузка не завершена")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if old != "" && old != id {
		g.Delete(ctx, old)
	}
	return id, nil
}

// find возвращает идентификатор файла с данным именем в папке.
func (g *gdriveStore) find(ctx context.Context, folder, name string) (string, error) {
	q := fmt.Sprintf("name='%s' and '%s' in parents and trashed=false and mimeType!='%s'", gdriveQ(name), folder, gdriveFolderMIM)
	var out struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := g.jsonCall(ctx, "GET", googleAPI+"/files?"+url.Values{"q": {q}, "fields": {"files(id)"}, "pageSize": {"1"}}.Encode(), nil, &out, "поиск файла"); err != nil {
		return "", err
	}
	if len(out.Files) == 0 {
		return "", nil
	}
	return out.Files[0].ID, nil
}

func (g *gdriveStore) Download(ctx context.Context, key, dst string) error {
	res, err := g.call(ctx, "GET", googleAPI+"/files/"+url.PathEscape(key)+"?alt=media", nil, 0, nil, "скачивание")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, res.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && res.ContentLength >= 0 && n != res.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		os.Remove(dst)
		return errf("Google Drive (скачивание): %s", firstLine(err.Error()))
	}
	return nil
}

func (g *gdriveStore) Head(ctx context.Context, key string, n int64) (io.ReadCloser, error) {
	res, err := g.call(ctx, "GET", googleAPI+"/files/"+url.PathEscape(key)+"?alt=media", nil, 0, map[string]string{"Range": fmt.Sprintf("bytes=0-%d", n-1)}, "чтение")
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, n), res.Body}, nil
}

func (g *gdriveStore) Delete(ctx context.Context, key string) error {
	res, err := g.call(ctx, "DELETE", googleAPI+"/files/"+url.PathEscape(key), nil, 0, nil, "удаление")
	if err != nil {
		if strings.Contains(err.Error(), "не найдено") {
			return nil
		}
		return err
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	return nil
}

func (g *gdriveStore) List(ctx context.Context) ([]cloudObject, error) {
	folder, err := g.ensureFolder(ctx, false)
	if err != nil || folder == "" {
		return nil, err
	}
	var out []cloudObject
	token := ""
	for page := 0; page < 200; page++ {
		q := fmt.Sprintf("'%s' in parents and trashed=false and mimeType!='%s'", folder, gdriveFolderMIM)
		v := url.Values{"q": {q}, "fields": {"nextPageToken,files(id,name)"}, "pageSize": {"1000"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var l struct {
			Files []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"files"`
			Next string `json:"nextPageToken"`
		}
		if err := g.jsonCall(ctx, "GET", googleAPI+"/files?"+v.Encode(), nil, &l, "список копий"); err != nil {
			return nil, err
		}
		for _, f := range l.Files {
			if cloudFileName.MatchString(f.Name) {
				out = append(out, cloudObject{Name: f.Name, Key: f.ID})
			}
		}
		if l.Next == "" {
			break
		}
		token = l.Next
	}
	return out, nil
}

// ---------- первое подключение (код на google.com/device) ----------

type gdrivePending struct {
	clientID, clientSecret, deviceCode string
	interval                           int
	created                            time.Time
}

var (
	gdrivePendingMu sync.Mutex
	gdrivePendingBy = map[string]gdrivePending{}
)

// gdriveStart запрашивает у Google код для ввода пользователем.
func gdriveStart(ctx context.Context, clientID, clientSecret string) (state string, info map[string]any, err error) {
	clientID, clientSecret = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" {
		return "", nil, errf("Укажите идентификатор и секрет клиента Google")
	}
	g := &gdriveStore{hc: cloudHTTP(false)}
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URL        string `json:"verification_url"`
		Expires    int    `json:"expires_in"`
		Interval   int    `json:"interval"`
		Error      string `json:"error"`
		Desc       string `json:"error_description"`
	}
	if _, err := g.formPost(ctx, googleAuth+"/device/code", url.Values{"client_id": {clientID}, "scope": {googleScope}}, &out); err != nil {
		return "", nil, err
	}
	if out.DeviceCode == "" {
		return "", nil, errf("Google не выдал код (%s). Проверьте, что тип клиента — «TV и устройства с ограниченным вводом»", firstLine(out.Error+" "+out.Desc))
	}
	if out.Interval < 3 {
		out.Interval = 5
	}
	state = randToken(12)
	gdrivePendingMu.Lock()
	for k, v := range gdrivePendingBy {
		if time.Since(v.created) > time.Hour {
			delete(gdrivePendingBy, k)
		}
	}
	gdrivePendingBy[state] = gdrivePending{clientID, clientSecret, out.DeviceCode, out.Interval, time.Now()}
	gdrivePendingMu.Unlock()
	return state, map[string]any{"user_code": out.UserCode, "url": out.URL, "interval": out.Interval, "expires": out.Expires}, nil
}

// gdriveFinish проверяет, ввёл ли пользователь код. Пока доступ не выдан, возвращает (nil, true, nil).
func gdriveFinish(ctx context.Context, state string) (res *BackupGDrive, pending bool, err error) {
	gdrivePendingMu.Lock()
	p, ok := gdrivePendingBy[state]
	gdrivePendingMu.Unlock()
	if !ok {
		return nil, false, errf("Подключение устарело: нажмите «Подключить Google Drive» ещё раз")
	}
	g := &gdriveStore{hc: cloudHTTP(false)}
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if _, err := g.formPost(ctx, googleAuth+"/token", url.Values{"client_id": {p.clientID}, "client_secret": {p.clientSecret}, "device_code": {p.deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &out); err != nil {
		return nil, false, err
	}
	switch out.Error {
	case "":
	case "authorization_pending", "slow_down":
		return nil, true, nil
	case "access_denied":
		gdrivePendingMu.Lock()
		delete(gdrivePendingBy, state)
		gdrivePendingMu.Unlock()
		return nil, false, errf("Доступ не разрешён. Нажмите «Подключить Google Drive» и разрешите доступ")
	case "expired_token", "invalid_grant":
		gdrivePendingMu.Lock()
		delete(gdrivePendingBy, state)
		gdrivePendingMu.Unlock()
		return nil, false, errf("Код истёк. Нажмите «Подключить Google Drive» ещё раз")
	default:
		return nil, false, errf("Google: %s", firstLine(out.Error+" "+out.Desc))
	}
	if out.Refresh == "" {
		return nil, false, errf("Google не выдал постоянный доступ. Отзовите доступ приложения в аккаунте Google и подключитесь снова")
	}
	gdrivePendingMu.Lock()
	delete(gdrivePendingBy, state)
	gdrivePendingMu.Unlock()
	r := &BackupGDrive{ClientID: p.clientID, ClientSecret: p.clientSecret, RefreshToken: out.Refresh}
	if out.Expires <= 0 {
		out.Expires = 3600
	}
	gdriveTokensMu.Lock()
	gdriveTokens[r.ClientID+"\x00"+r.RefreshToken] = gdriveToken{out.Access, time.Now().Add(time.Duration(out.Expires) * time.Second)}
	gdriveTokensMu.Unlock()
	cl := &gdriveStore{c: *r, hc: g.hc}
	var about struct {
		User struct {
			Email string `json:"emailAddress"`
			ID    string `json:"permissionId"`
		} `json:"user"`
	}
	if err := cl.jsonCall(ctx, "GET", googleAPI+"/about?fields=user(emailAddress,permissionId)", nil, &about, "проверка"); err == nil {
		r.Account = about.User.Email
		if r.Account == "" {
			r.Account = about.User.ID
		}
	}
	return r, false, nil
}
