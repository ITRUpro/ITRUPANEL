// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Хранение копий по WebDAV: Яндекс.Диск, Облако Mail.ru и любой другой сервер WebDAV (Nextcloud, ownCloud, …).
// Для Яндекс.Диска и Облака Mail.ru вместо основного пароля используется пароль приложения.

type BackupWebDAV struct {
	Provider string `json:"provider"` // yandex | mailru | custom
	URL      string `json:"url"`      // https://webdav.yandex.ru
	User     string `json:"user"`
	Pass     string `json:"pass"`
	Dir      string `json:"dir"`      // папка в хранилище, например itrupanel
	Insecure bool   `json:"insecure"` // не проверять сертификат (только для своего сервера)
}

var davProviderURL = map[string]string{
	"yandex": "https://webdav.yandex.ru",
	"mailru": "https://webdav.cloud.mail.ru",
}

// davNormalize проверяет и приводит настройки к виду, пригодному для запросов.
func davNormalize(c BackupWebDAV) (BackupWebDAV, error) {
	c.Provider = strings.TrimSpace(c.Provider)
	if c.Provider != "yandex" && c.Provider != "mailru" {
		c.Provider = "custom"
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if u, ok := davProviderURL[c.Provider]; ok {
		c.URL, c.Insecure = u, false
	}
	if c.URL != "" && !strings.Contains(c.URL, "://") {
		c.URL = "https://" + c.URL
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.User != nil {
		return c, errf("Адрес WebDAV: укажите адрес вида https://dav.example.com")
	}
	c.URL = strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/")
	c.User = strings.TrimSpace(c.User)
	if c.User == "" || c.Pass == "" {
		return c, errf("Укажите логин и пароль приложения")
	}
	if strings.ContainsAny(c.User+c.Pass, "\r\n") {
		return c, errf("Логин и пароль не должны содержать переводов строк")
	}
	c.Dir = strings.Trim(strings.TrimSpace(c.Dir), "/")
	if !cloudFolder.MatchString(c.Dir) || strings.Contains(c.Dir, "//") || strings.Contains(c.Dir, "..") || len(c.Dir) > 200 {
		return c, errf("Папка: только латиница, цифры и символы . _ - /")
	}
	return c, nil
}

type davStore struct {
	c  BackupWebDAV
	hc *http.Client
}

func newDAVStore(c BackupWebDAV) (cloudStore, error) {
	n, err := davNormalize(c)
	if err != nil {
		return nil, errf("WebDAV не настроен: подключите хранилище, где лежит эта копия")
	}
	return &davStore{c: n, hc: cloudHTTP1(n.Insecure)}, nil
}

func (d *davStore) Kind() string { return cloudWebDAV }
func (d *davStore) Source() string {
	return cloudSourceID(cloudWebDAV, strings.ToLower(d.c.URL), d.c.User, d.c.Dir)
}

// folderPath — путь папки на сервере вида /itrupanel/ (корень — «/»).
func (d *davStore) folderPath() string {
	if d.c.Dir == "" {
		return "/"
	}
	return "/" + d.c.Dir + "/"
}

// keyFor — путь файла на сервере; он хранится в Backup.Remote.
func (d *davStore) keyFor(name string) string { return d.folderPath() + name }

func (d *davStore) urlOf(p string) string {
	u, _ := url.Parse(d.c.URL)
	base := strings.TrimRight(u.EscapedPath(), "/")
	return u.Scheme + "://" + u.Host + base + (&url.URL{Path: p}).EscapedPath()
}

func (d *davStore) do(ctx context.Context, method, p string, body io.Reader, size int64, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.urlOf(p), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		if f, ok := body.(*os.File); ok {
			req.GetBody = func() (io.ReadCloser, error) {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return nil, err
				}
				return io.NopCloser(f), nil
			}
		}
	}
	req.SetBasicAuth(d.c.User, d.c.Pass)
	req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := d.hc.Do(req)
	if err != nil {
		return nil, errf("WebDAV: %s", firstLine(err.Error()))
	}
	return res, nil
}

// davBodyNote возвращает короткое пояснение из тела ответа (без разметки): серверы часто пишут туда причину ошибки.
func davBodyNote(b []byte) string {
	t := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(b), " ")
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > 160 {
		t = t[:160] + "…"
	}
	return t
}

// davFail переводит ответ сервера в понятное сообщение; тело читается и закрывается.
func davFail(res *http.Response, action string) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	var msg string
	switch res.StatusCode {
	case 401:
		msg = "неверный логин или пароль (для Яндекс.Диска и Облака Mail.ru нужен пароль приложения)"
	case 403:
		msg = "доступ запрещён: проверьте права и пароль приложения"
	case 404:
		msg = "не найдено"
	case 405, 409:
		msg = "нет такой папки или операция не разрешена"
	case 413:
		msg = "файл слишком большой для этого хранилища"
	case 423:
		msg = "файл заблокирован на сервере"
	case 429:
		msg = "слишком много запросов, повторите позже"
	case 507:
		msg = "в хранилище не хватает места"
	default:
		msg = res.Status
		if note := davBodyNote(body); note != "" {
			msg += " (" + note + ")"
		}
	}
	err := errf("WebDAV (%s): %s", action, msg)
	switch res.StatusCode {
	case 401, 403, 404, 405, 409, 413, 507:
		return permanent(err)
	}
	return err
}

// davExists проверяет, есть ли папка: PROPFIND надёжнее, чем MKCOL на существующую папку (серверы отвечают на неё по-разному).
func (d *davStore) davExists(ctx context.Context, p string) (bool, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>`
	res, err := d.do(ctx, "PROPFIND", p, strings.NewReader(body), int64(len(body)), map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"})
	if err != nil {
		return false, err
	}
	if res.StatusCode == 207 || res.StatusCode == 200 || res.StatusCode == 404 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return res.StatusCode != 404, nil
	}
	return false, davFail(res, "проверка папки")
}

// mkdirs создаёт папку и все родительские. MKCOL отправляется только для тех папок, которых нет;
// если сервер ответил на MKCOL ошибкой, но папка появилась (создана параллельно), это не ошибка.
func (d *davStore) mkdirs(ctx context.Context) error {
	if d.c.Dir == "" {
		return nil
	}
	cur := ""
	for _, seg := range strings.Split(d.c.Dir, "/") {
		cur += "/" + seg
		if ok, err := d.davExists(ctx, cur+"/"); err == nil && ok {
			continue
		}
		res, err := d.do(ctx, "MKCOL", cur+"/", nil, 0, nil)
		if err != nil {
			return err
		}
		code := res.StatusCode
		if code == 201 || code == 200 || code == 405 || code == 301 || code == 302 {
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			continue
		}
		fail := davFail(res, "создание папки")
		if ok, err := d.davExists(ctx, cur+"/"); err == nil && ok {
			continue
		}
		return fail
	}
	return nil
}

// Test проверяет все шаги, которые понадобятся копии: вход, папка, запись небольшого и крупного файла, чтение, удаление.
// При сбое в сообщении перечислены ответы сервера по шагам: по ним видно, на чём именно остановилось.
func (d *davStore) Test(ctx context.Context) error {
	var log []string
	step := func(name string, err error) error {
		if err != nil {
			log = append(log, name+": "+firstLine(err.Error()))
			return errf("WebDAV: сбой на шаге «%s»: %s. Ход проверки: %s", name, firstLine(err.Error()), strings.Join(log, "; "))
		}
		log = append(log, name+": ok")
		return nil
	}
	if err := step("вход", func() error { _, e := d.davExists(ctx, "/"); return e }()); err != nil {
		return err
	}
	if err := step("папка", d.mkdirs(ctx)); err != nil {
		return err
	}
	name := "itrupanel-check-" + randToken(4) + ".tmp"
	key := d.keyFor(name)
	put := func(size int) error {
		body := bytes.Repeat([]byte("x"), size)
		headers := map[string]string{}
		if size > 1<<20 {
			headers["Expect"] = "100-continue"
		}
		res, err := d.do(ctx, "PUT", key, bytes.NewReader(body), int64(size), headers)
		if err != nil {
			return err
		}
		if res.StatusCode != 200 && res.StatusCode != 201 && res.StatusCode != 204 {
			return davFail(res, "запись")
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return nil
	}
	defer d.Delete(context.Background(), key)
	if err := step("запись 64 Б", put(64)); err != nil {
		return err
	}
	if err := step("запись 2 МБ", put(2<<20)); err != nil {
		return err
	}
	if err := step("чтение", func() error {
		rc, err := d.Head(ctx, key, 16)
		if err != nil {
			return err
		}
		defer rc.Close()
		got, _ := io.ReadAll(rc)
		if len(got) == 0 {
			return errf("пустой ответ")
		}
		return nil
	}()); err != nil {
		return err
	}
	return step("удаление", d.Delete(ctx, key))
}

type davEntry struct {
	Name   string
	Dir    bool
	Length int64
}

// propfind возвращает содержимое папки (глубина 1) или сведения об одном объекте (глубина 0).
func (d *davStore) propfind(ctx context.Context, p string, depth int) ([]davEntry, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/></d:prop></d:propfind>`
	res, err := d.do(ctx, "PROPFIND", p, strings.NewReader(body), int64(len(body)), map[string]string{"Depth": fmt.Sprint(depth), "Content-Type": "application/xml; charset=utf-8"})
	if err != nil {
		return nil, err
	}
	if res.StatusCode == 404 {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return nil, nil
	}
	if res.StatusCode != 207 && res.StatusCode != 200 {
		return nil, davFail(res, "чтение списка")
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	res.Body.Close()
	var ms struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Type struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length string `xml:"getcontentlength"`
			} `xml:"propstat>prop"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, errf("WebDAV: непонятный ответ хранилища на запрос списка")
	}
	self := strings.Trim(p, "/")
	var out []davEntry
	for _, r := range ms.Responses {
		href := r.Href
		if u, err := url.Parse(href); err == nil {
			href = u.Path
		}
		href = strings.Trim(href, "/")
		base := strings.Trim(func() string { u, _ := url.Parse(d.c.URL); return u.Path }(), "/")
		if base != "" {
			href = strings.TrimPrefix(href, base)
			href = strings.Trim(href, "/")
		}
		e := davEntry{Name: href}
		if i := strings.LastIndex(href, "/"); i >= 0 {
			e.Name = href[i+1:]
		}
		for _, pr := range r.Props {
			if pr.Type.Collection != nil {
				e.Dir = true
			}
			if pr.Length != "" {
				fmt.Sscan(pr.Length, &e.Length)
			}
		}
		if depth == 1 && href == self {
			continue // сама папка
		}
		out = append(out, e)
	}
	return out, nil
}

func (d *davStore) Upload(ctx context.Context, file, name string) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	key := d.keyFor(name)
	// Как у curl -T и davfs2: без Content-Type, а для больших файлов с «Expect: 100-continue» — сервер может отказать
	// до передачи данных (нет места, нет прав, ошибка на его стороне), и панель не будет зря гнать гигабайты.
	headers := map[string]string{}
	if st.Size() > 1<<20 {
		headers["Expect"] = "100-continue"
	}
	put := func() (*http.Response, error) {
		f, err := os.Open(file)
		if err != nil {
			return nil, permanent(err)
		}
		defer f.Close()
		return d.do(ctx, "PUT", key, f, st.Size(), headers)
	}
	err = cloudRetryN(ctx, 4, func() error {
		// Папка обычно уже есть, поэтому сначала пишем файл, а создаём папки только если сервер сказал, что их нет.
		res, err := put()
		if err != nil {
			return err
		}
		if res.StatusCode == 404 || res.StatusCode == 409 || res.StatusCode == 405 {
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			if err := d.mkdirs(ctx); err != nil {
				return err
			}
			if res, err = put(); err != nil {
				return err
			}
		}
		if res.StatusCode != 200 && res.StatusCode != 201 && res.StatusCode != 204 {
			return davFail(res, "загрузка")
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return nil
	})
	if err != nil {
		return "", err
	}
	// Сверка размера: сервер мог принять файл не целиком.
	if es, err := d.propfind(ctx, key, 0); err == nil && len(es) == 1 && es[0].Length > 0 && es[0].Length != st.Size() {
		d.Delete(ctx, key)
		return "", errf("WebDAV: размер файла в хранилище (%d) не совпал с исходным (%d)", es[0].Length, st.Size())
	}
	return key, nil
}

func (d *davStore) Download(ctx context.Context, key, dst string) error {
	res, err := d.do(ctx, "GET", key, nil, 0, nil)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return davFail(res, "скачивание")
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
		return errf("WebDAV (скачивание): %s", firstLine(err.Error()))
	}
	return nil
}

func (d *davStore) Head(ctx context.Context, key string, n int64) (io.ReadCloser, error) {
	res, err := d.do(ctx, "GET", key, nil, 0, map[string]string{"Range": fmt.Sprintf("bytes=0-%d", n-1)})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 && res.StatusCode != 206 {
		return nil, davFail(res, "чтение")
	}
	// Если сервер не поддерживает Range и отдаёт весь файл, читаем только начало и закрываем соединение.
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, n), res.Body}, nil
}

func (d *davStore) Delete(ctx context.Context, key string) error {
	res, err := d.do(ctx, "DELETE", key, nil, 0, nil)
	if err != nil {
		return err
	}
	if res.StatusCode == 404 || (res.StatusCode >= 200 && res.StatusCode < 300) {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		return nil
	}
	return davFail(res, "удаление")
}

func (d *davStore) List(ctx context.Context) ([]cloudObject, error) {
	es, err := d.propfind(ctx, d.folderPath(), 1)
	if err != nil {
		return nil, err
	}
	var out []cloudObject
	for _, e := range es {
		if e.Dir || e.Name == "" || !cloudFileName.MatchString(e.Name) {
			continue
		}
		out = append(out, cloudObject{Name: e.Name, Key: d.keyFor(e.Name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
