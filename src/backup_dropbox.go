// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Хранение копий в Dropbox (API v2). Приложение создаёт сам владелец аккаунта в Dropbox App Console
// (тип доступа «App folder»), поэтому панель видит только свою папку. Доступ выдаётся один раз через
// OAuth 2 с PKCE; панель хранит долгоживущий refresh-токен и сама получает короткие токены доступа.

type BackupDropbox struct {
	AppKey       string `json:"app_key"`
	AppSecret    string `json:"app_secret"` // необязательно: при PKCE не нужен
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"` // идентификатор аккаунта: отличает хранилище при повторной авторизации
	Account      string `json:"account"`    // почта аккаунта для показа в панели
	Dir          string `json:"dir"`
}

// Адреса API; переменные, чтобы тесты могли подставить свой сервер.
var (
	dropboxAPI     = "https://api.dropboxapi.com"
	dropboxContent = "https://content.dropboxapi.com"
	dropboxAuthURL = "https://www.dropbox.com/oauth2/authorize"
	dropboxChunk   = int64(32 << 20)
	dropboxSingle  = int64(120 << 20) // до этого размера файл уходит одним запросом
)

var reDropboxKey = regexp.MustCompile(`^[A-Za-z0-9]{6,40}$`)

func dropboxNormalize(c BackupDropbox) (BackupDropbox, error) {
	c.AppKey = strings.TrimSpace(c.AppKey)
	c.AppSecret = strings.TrimSpace(c.AppSecret)
	c.RefreshToken = strings.TrimSpace(c.RefreshToken)
	if !reDropboxKey.MatchString(c.AppKey) {
		return c, errf("Укажите ключ приложения Dropbox (App key)")
	}
	if c.AppSecret != "" && !reDropboxKey.MatchString(c.AppSecret) {
		return c, errf("Секрет приложения Dropbox указан неверно")
	}
	if c.RefreshToken == "" {
		return c, errf("Dropbox не подключён: нажмите «Подключить Dropbox» и введите код")
	}
	c.Dir = strings.Trim(strings.TrimSpace(c.Dir), "/")
	if !cloudFolder.MatchString(c.Dir) || strings.Contains(c.Dir, "//") || strings.Contains(c.Dir, "..") || len(c.Dir) > 200 {
		return c, errf("Папка: только латиница, цифры и символы . _ - /")
	}
	return c, nil
}

// dropboxToken хранит короткий токен доступа и срок его жизни.
type dropboxToken struct {
	token   string
	expires time.Time
}

var (
	dropboxTokens   = map[string]dropboxToken{}
	dropboxTokensMu sync.Mutex
)

type dropboxStore struct {
	c  BackupDropbox
	hc *http.Client
}

func newDropboxStore(c BackupDropbox) (cloudStore, error) {
	n, err := dropboxNormalize(c)
	if err != nil {
		return nil, errf("Dropbox не настроен: подключите хранилище, где лежит эта копия")
	}
	return &dropboxStore{c: n, hc: cloudHTTP(false)}, nil
}

func (d *dropboxStore) Kind() string { return cloudDropbox }
func (d *dropboxStore) Source() string {
	id := d.c.AccountID
	if id == "" {
		id = d.c.AppKey
	}
	return cloudSourceID(cloudDropbox, id, d.c.Dir)
}

func (d *dropboxStore) folder() string {
	if d.c.Dir == "" {
		return ""
	}
	return "/" + d.c.Dir
}
func (d *dropboxStore) keyFor(name string) string { return d.folder() + "/" + name }

// accessToken возвращает действующий токен доступа, при необходимости обновляя его по refresh-токену.
func (d *dropboxStore) accessToken(ctx context.Context) (string, error) {
	cacheKey := d.c.AppKey + "\x00" + d.c.RefreshToken
	dropboxTokensMu.Lock()
	t, ok := dropboxTokens[cacheKey]
	dropboxTokensMu.Unlock()
	if ok && time.Until(t.expires) > time.Minute {
		return t.token, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {d.c.RefreshToken}, "client_id": {d.c.AppKey}}
	if d.c.AppSecret != "" {
		form.Set("client_secret", d.c.AppSecret)
	}
	var out struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := d.formPost(ctx, dropboxAPI+"/oauth2/token", form, &out); err != nil {
		return "", err
	}
	if out.Access == "" {
		if out.Error == "invalid_grant" {
			return "", permanent(errf("Dropbox: доступ отозван или истёк. Подключите Dropbox заново"))
		}
		return "", errf("Dropbox: не удалось получить токен доступа (%s)", firstLine(out.Error+" "+out.Desc))
	}
	if out.Expires <= 0 {
		out.Expires = 3600
	}
	dropboxTokensMu.Lock()
	dropboxTokens[cacheKey] = dropboxToken{out.Access, time.Now().Add(time.Duration(out.Expires) * time.Second)}
	dropboxTokensMu.Unlock()
	return out.Access, nil
}

func (d *dropboxStore) formPost(ctx context.Context, u string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.hc.Do(req)
	if err != nil {
		return errf("Dropbox: %s", firstLine(err.Error()))
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(data, out); err != nil {
		return errf("Dropbox: непонятный ответ сервера (%s)", res.Status)
	}
	return nil
}

// dropboxErrorText переводит ответ API с ошибкой в понятное сообщение.
func dropboxErrorText(status int, body []byte) (string, bool) {
	text := string(body)
	var e struct {
		Summary string `json:"error_summary"`
	}
	if json.Unmarshal(body, &e) == nil && e.Summary != "" {
		text = e.Summary
	}
	switch {
	case status == 401:
		return "токен доступа недействителен (повторная попытка получит новый токен)", false
	case strings.Contains(text, "insufficient_space"):
		return "в Dropbox не хватает места", true
	case strings.Contains(text, "not_found"):
		return "не найдено", true
	case strings.Contains(text, "malformed_path") || strings.Contains(text, "restricted_content"):
		return "недопустимый путь или имя файла", true
	case status == 429:
		return "слишком много запросов, повторите позже", false
	}
	return firstLine(text), status >= 400 && status < 500 && status != 408 && status != 429
}

// call выполняет запрос к API. Для content-запросов параметры передаются в заголовке Dropbox-API-Arg.
func (d *dropboxStore) call(ctx context.Context, host, endpoint string, arg any, body io.Reader, size int64, headers map[string]string, action string) (*http.Response, error) {
	tok, err := d.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader = body
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/2/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	if host == dropboxContent {
		b, _ := json.Marshal(arg)
		// Заголовок должен быть ASCII: непечатные и не-ASCII символы записываются как \uXXXX.
		var esc strings.Builder
		for _, r := range string(b) {
			if r > 126 {
				fmt.Fprintf(&esc, `\u%04x`, r)
			} else {
				esc.WriteRune(r)
			}
		}
		req.Header.Set("Dropbox-API-Arg", esc.String())
		req.Header.Set("Content-Type", "application/octet-stream")
	} else {
		if arg == nil {
			rdr = nil
		} else {
			b, _ := json.Marshal(arg)
			rdr = bytes.NewReader(b)
			size = int64(len(b))
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if rdr != nil {
		req.Body = io.NopCloser(rdr)
		req.ContentLength = size
		if br, ok := rdr.(*bytes.Reader); ok {
			req.GetBody = func() (io.ReadCloser, error) { br.Seek(0, io.SeekStart); return io.NopCloser(br), nil }
		}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := d.hc.Do(req)
	if err != nil {
		return nil, errf("Dropbox: %s", firstLine(err.Error()))
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res, nil
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	if res.StatusCode == 401 {
		dropboxTokensMu.Lock()
		delete(dropboxTokens, d.c.AppKey+"\x00"+d.c.RefreshToken)
		dropboxTokensMu.Unlock()
	}
	if res.StatusCode == 429 {
		if s, _ := strconv.Atoi(res.Header.Get("Retry-After")); s > 0 && s <= 60 {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(s) * time.Second):
			}
		}
	}
	msg, perm := dropboxErrorText(res.StatusCode, data)
	err = errf("Dropbox (%s): %s", action, msg)
	if perm {
		return nil, permanent(err)
	}
	return nil, err
}

// Test проверяет доступ чтением папки; отсутствие папки не ошибка (она создаётся при первой загрузке).
// Сведения об аккаунте не запрашиваются: для них нужно лишнее разрешение account_info.read.
func (d *dropboxStore) Test(ctx context.Context) error {
	res, err := d.call(ctx, dropboxAPI, "files/list_folder", map[string]any{"path": d.folder(), "limit": 1}, nil, 0, nil, "проверка")
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

// Upload загружает файл: небольшой — одним запросом, большой — сессией по частям.
func (d *dropboxStore) Upload(ctx context.Context, file, name string) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	key := d.keyFor(name)
	commit := map[string]any{"path": key, "mode": "overwrite", "mute": true, "autorename": false}
	size := st.Size()
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if size <= dropboxSingle {
		err := cloudRetry(ctx, func() error {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return permanent(err)
			}
			res, err := d.call(ctx, dropboxContent, "files/upload", commit, io.LimitReader(f, size), size, nil, "загрузка")
			if err != nil {
				return err
			}
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			return nil
		})
		return key, err
	}
	var session string
	var offset int64
	finished := false
	for offset < size {
		n := dropboxChunk
		if size-offset < n {
			n = size - offset
		}
		last := offset+n >= size
		err := cloudRetry(ctx, func() error {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return permanent(err)
			}
			body := io.LimitReader(f, n)
			var res *http.Response
			var err error
			switch {
			case session == "":
				res, err = d.call(ctx, dropboxContent, "files/upload_session/start", map[string]any{"close": false}, body, n, nil, "загрузка")
			case last && session != "":
				res, err = d.call(ctx, dropboxContent, "files/upload_session/finish", map[string]any{"cursor": map[string]any{"session_id": session, "offset": offset}, "commit": commit}, body, n, nil, "загрузка")
				if err == nil {
					finished = true
				}
			default:
				res, err = d.call(ctx, dropboxContent, "files/upload_session/append_v2", map[string]any{"cursor": map[string]any{"session_id": session, "offset": offset}, "close": false}, body, n, nil, "загрузка")
			}
			if err != nil {
				return err
			}
			defer res.Body.Close()
			data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			if session == "" {
				var out struct {
					ID string `json:"session_id"`
				}
				if json.Unmarshal(data, &out) != nil || out.ID == "" {
					return errf("Dropbox: сервер не вернул номер сессии загрузки")
				}
				session = out.ID
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		offset += n
	}
	if size == 0 {
		return "", errf("Dropbox: пустой файл копии")
	}
	if !finished {
		// Весь файл ушёл одной частью через start: сессию нужно закрыть.
		err := cloudRetry(ctx, func() error {
			res, err := d.call(ctx, dropboxContent, "files/upload_session/finish", map[string]any{"cursor": map[string]any{"session_id": session, "offset": size}, "commit": commit}, bytes.NewReader(nil), 0, nil, "загрузка")
			if err != nil {
				return err
			}
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return key, nil
}

func (d *dropboxStore) Download(ctx context.Context, key, dst string) error {
	res, err := d.call(ctx, dropboxContent, "files/download", map[string]any{"path": key}, nil, 0, nil, "скачивание")
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
		return errf("Dropbox (скачивание): %s", firstLine(err.Error()))
	}
	return nil
}

func (d *dropboxStore) Head(ctx context.Context, key string, n int64) (io.ReadCloser, error) {
	res, err := d.call(ctx, dropboxContent, "files/download", map[string]any{"path": key}, nil, 0, map[string]string{"Range": fmt.Sprintf("bytes=0-%d", n-1)}, "чтение")
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, n), res.Body}, nil
}

func (d *dropboxStore) Delete(ctx context.Context, key string) error {
	res, err := d.call(ctx, dropboxAPI, "files/delete_v2", map[string]any{"path": key}, nil, 0, nil, "удаление")
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

func (d *dropboxStore) List(ctx context.Context) ([]cloudObject, error) {
	var out []cloudObject
	endpoint, arg := "files/list_folder", any(map[string]any{"path": d.folder(), "limit": 2000})
	for page := 0; page < 200; page++ {
		res, err := d.call(ctx, dropboxAPI, endpoint, arg, nil, 0, nil, "список копий")
		if err != nil {
			if strings.Contains(err.Error(), "не найдено") {
				return nil, nil
			}
			return nil, err
		}
		data, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		res.Body.Close()
		var l struct {
			Entries []struct {
				Tag  string `json:".tag"`
				Name string `json:"name"`
			} `json:"entries"`
			Cursor  string `json:"cursor"`
			HasMore bool   `json:"has_more"`
		}
		if err := json.Unmarshal(data, &l); err != nil {
			return nil, errf("Dropbox: непонятный ответ на запрос списка")
		}
		for _, e := range l.Entries {
			if e.Tag == "file" && cloudFileName.MatchString(e.Name) {
				out = append(out, cloudObject{Name: e.Name, Key: d.keyFor(e.Name)})
			}
		}
		if !l.HasMore || l.Cursor == "" {
			break
		}
		endpoint, arg = "files/list_folder/continue", map[string]any{"cursor": l.Cursor}
	}
	return out, nil
}

// ---------- первое подключение (OAuth 2 с PKCE) ----------

type dropboxPending struct {
	verifier  string
	appKey    string
	appSecret string
	created   time.Time
}

var (
	dropboxPendingMu sync.Mutex
	dropboxPendingBy = map[string]dropboxPending{}
)

// dropboxStart готовит ссылку для разрешения доступа. Код подтверждения Dropbox показывает на своей странице.
func dropboxStart(appKey, appSecret string) (state, link string, err error) {
	appKey, appSecret = strings.TrimSpace(appKey), strings.TrimSpace(appSecret)
	if !reDropboxKey.MatchString(appKey) {
		return "", "", errf("Укажите ключ приложения Dropbox (App key)")
	}
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state = randToken(12)
	dropboxPendingMu.Lock()
	for k, v := range dropboxPendingBy {
		if time.Since(v.created) > 30*time.Minute {
			delete(dropboxPendingBy, k)
		}
	}
	dropboxPendingBy[state] = dropboxPending{verifier, appKey, appSecret, time.Now()}
	dropboxPendingMu.Unlock()
	q := url.Values{"client_id": {appKey}, "response_type": {"code"}, "token_access_type": {"offline"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return state, dropboxAuthURL + "?" + q.Encode(), nil
}

// dropboxFinish обменивает код на refresh-токен и сведения об аккаунте.
func dropboxFinish(ctx context.Context, state, code string) (BackupDropbox, error) {
	dropboxPendingMu.Lock()
	p, ok := dropboxPendingBy[state]
	dropboxPendingMu.Unlock()
	code = strings.TrimSpace(code)
	if !ok || time.Since(p.created) > 30*time.Minute {
		return BackupDropbox{}, errf("Подключение устарело: нажмите «Подключить Dropbox» ещё раз")
	}
	if code == "" || len(code) > 200 {
		return BackupDropbox{}, errf("Введите код, который показал Dropbox")
	}
	d := &dropboxStore{hc: cloudHTTP(false)}
	form := url.Values{"code": {code}, "grant_type": {"authorization_code"}, "client_id": {p.appKey}, "code_verifier": {p.verifier}}
	if p.appSecret != "" {
		form.Set("client_secret", p.appSecret)
	}
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Account string `json:"account_id"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := d.formPost(ctx, dropboxAPI+"/oauth2/token", form, &out); err != nil {
		return BackupDropbox{}, err
	}
	if out.Refresh == "" {
		return BackupDropbox{}, errf("Dropbox не принял код (%s). Получите новый код и повторите", firstLine(out.Error+" "+out.Desc))
	}
	dropboxPendingMu.Lock()
	delete(dropboxPendingBy, state)
	dropboxPendingMu.Unlock()
	res := BackupDropbox{AppKey: p.appKey, AppSecret: p.appSecret, RefreshToken: out.Refresh, AccountID: out.Account}
	// Почту аккаунта показываем в панели, чтобы было видно, к какому аккаунту подключились.
	cl := &dropboxStore{c: res, hc: d.hc}
	dropboxTokensMu.Lock()
	dropboxTokens[res.AppKey+"\x00"+res.RefreshToken] = dropboxToken{out.Access, time.Now().Add(3 * time.Hour)}
	dropboxTokensMu.Unlock()
	if r, err := cl.call(ctx, dropboxAPI, "users/get_current_account", nil, nil, 0, nil, "проверка"); err == nil {
		var acc struct {
			Email string `json:"email"`
			ID    string `json:"account_id"`
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body.Close()
		if json.Unmarshal(data, &acc) == nil {
			res.Account = acc.Email
			if acc.ID != "" {
				res.AccountID = acc.ID
			}
		}
	}
	return res, nil
}
