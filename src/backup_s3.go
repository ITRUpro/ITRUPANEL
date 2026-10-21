// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Облачное хранилище резервных копий: любой S3-совместимый сервис (Amazon S3, Yandex Object Storage,
// Cloudflare R2, Wasabi, DigitalOcean Spaces, MinIO и другие). Запросы подписываются по AWS Signature V4
// без внешних библиотек; большие файлы загружаются частями.

type BackupS3 struct {
	Endpoint  string `json:"endpoint"` // https://s3.amazonaws.com
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Prefix    string `json:"prefix"`     // папка в хранилище
	PathStyle bool   `json:"path_style"` // адрес вида endpoint/bucket/key вместо bucket.endpoint/key
	Insecure  bool   `json:"insecure"`   // не проверять сертификат (самоподписанный)
}

var (
	reS3Bucket = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$`)
	reS3Region = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}$`)
	reS3Prefix = regexp.MustCompile(`^[A-Za-z0-9._\-/]*$`)
	reS3Key    = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,128}$`)
)

// s3PartSize — размер части при загрузке частями; переменная, чтобы тесты могли уменьшить её.
var s3PartSize int64 = 32 << 20

// s3Normalize проверяет и приводит настройки к виду, пригодному для запросов.
func s3Normalize(c BackupS3) (BackupS3, error) {
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if c.Endpoint != "" && !strings.Contains(c.Endpoint, "://") {
		c.Endpoint = "https://" + c.Endpoint
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return c, errf("Адрес хранилища: укажите адрес вида https://s3.example.com без пути")
	}
	c.Endpoint = u.Scheme + "://" + u.Host
	if c.Region = strings.TrimSpace(c.Region); c.Region == "" {
		c.Region = "us-east-1"
	}
	if !reS3Region.MatchString(c.Region) {
		return c, errf("Регион: латинские буквы, цифры и дефис, например us-east-1")
	}
	if c.Bucket = strings.TrimSpace(c.Bucket); !reS3Bucket.MatchString(c.Bucket) {
		return c, errf("Имя бакета: латинские буквы, цифры, точка, дефис и подчёркивание (2–63 символа)")
	}
	c.AccessKey, c.SecretKey = strings.TrimSpace(c.AccessKey), strings.TrimSpace(c.SecretKey)
	if c.AccessKey == "" || c.SecretKey == "" || strings.ContainsAny(c.AccessKey+c.SecretKey, " \r\n\t") {
		return c, errf("Укажите ключ доступа и секретный ключ без пробелов")
	}
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if !reS3Prefix.MatchString(c.Prefix) || strings.Contains(c.Prefix, "..") || strings.Contains(c.Prefix, "//") {
		return c, errf("Папка в хранилище: латинские буквы, цифры, точка, дефис, подчёркивание и «/»")
	}
	return c, nil
}

// backupS3Source — идентификатор хранилища для копий: привязывает копию к бакету и папке, но не к ключам доступа,
// чтобы их можно было сменить.
func backupS3Source(c BackupS3) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", strings.ToLower(strings.TrimRight(c.Endpoint, "/")), c.Bucket, strings.Trim(c.Prefix, "/"))))
	return "s3-" + hex.EncodeToString(sum[:16])
}

func backupIsS3Source(src string) bool { return strings.HasPrefix(src, "s3-") }

// ---------- подпись AWS Signature V4 ----------

func s3Escape(s string, keepSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~', c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func s3Query(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, s3Escape(k, false)+"="+s3Escape(v, false))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// s3Sign подписывает запрос: добавляет x-amz-date, x-amz-content-sha256 и Authorization.
func s3Sign(req *http.Request, c BackupS3, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "range" || strings.HasPrefix(lk, "x-amz-") {
			headers[lk] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")
	uri := req.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	query, _ := url.ParseQuery(req.URL.RawQuery)
	canonical := strings.Join([]string{req.Method, uri, s3Query(query), canonicalHeaders.String(), signed, payloadHash}, "\n")
	scope := date + "/" + c.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+c.SecretKey), date)
	key = hmacSHA256(key, c.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// ---------- клиент ----------

type s3Client struct {
	c    BackupS3
	http *http.Client
	now  func() time.Time
}

func newS3Client(c BackupS3) (*s3Client, error) {
	c, err := s3Normalize(c)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: c.Insecure}, ResponseHeaderTimeout: 2 * time.Minute, IdleConnTimeout: 30 * time.Second}
	return &s3Client{c: c, now: time.Now, http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *s3Client) keyFor(name string) string {
	if s.c.Prefix == "" {
		return name
	}
	return s.c.Prefix + "/" + name
}

// target возвращает адрес запроса к объекту (или к бакету, если key пуст).
func (s *s3Client) target(key string, q url.Values) string {
	u, _ := url.Parse(s.c.Endpoint)
	host, p := u.Host, "/"+s3Escape(key, true)
	if s.c.PathStyle {
		p = "/" + s.c.Bucket + "/" + s3Escape(key, true)
		if key == "" {
			p = "/" + s.c.Bucket
		}
	} else {
		host = s.c.Bucket + "." + u.Host
		if key == "" {
			p = "/"
		}
	}
	out := u.Scheme + "://" + host + p
	if len(q) > 0 {
		out += "?" + s3Query(q)
	}
	return out
}

type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// s3Fail превращает ответ с ошибкой в понятное сообщение.
func s3Fail(res *http.Response, action string) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var e s3Error
	_ = xml.Unmarshal(body, &e)
	var hint string
	switch e.Code {
	case "AccessDenied", "Forbidden":
		hint = "доступ запрещён: проверьте права ключа на этот бакет"
	case "InvalidAccessKeyId":
		hint = "неверный ключ доступа"
	case "SignatureDoesNotMatch":
		hint = "неверный секретный ключ, регион или адрес хранилища"
	case "NoSuchBucket":
		hint = "бакет не найден: создайте его в личном кабинете облака"
	case "RequestTimeTooSkewed":
		hint = "время на сервере сильно отличается от времени облака: проверьте дату и время сервера"
	case "PermanentRedirect", "AuthorizationHeaderMalformed", "IllegalLocationConstraintException":
		hint = "неверный регион или адрес хранилища"
	case "SlowDown", "ServiceUnavailable", "InternalError":
		hint = "хранилище временно недоступно, повторите позже"
	}
	switch {
	case hint == "" && res.StatusCode >= 300 && res.StatusCode < 400:
		hint = "хранилище перенаправляет запрос: проверьте регион и адрес"
	case hint == "" && res.StatusCode == 403:
		hint = "доступ запрещён"
	case hint == "" && res.StatusCode == 404:
		hint = "не найдено: проверьте имя бакета и папку"
	case hint == "":
		hint = strings.TrimSpace(e.Code + " " + e.Message)
		if hint == "" {
			hint = "ответ " + res.Status
		}
	}
	return errf("Облако (%s): %s", action, hint)
}

// do выполняет подписанный запрос. body может быть nil.
func (s *s3Client) do(ctx context.Context, method, key string, q url.Values, body []byte, extra map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.target(key, q), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(len(body))
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	s3Sign(req, s.c, sha256Hex(body), s.now())
	res, err := s.http.Do(req)
	if err != nil {
		return nil, errf("Облако: нет связи с хранилищем: %s", firstLine(err.Error()))
	}
	return res, nil
}

func (s *s3Client) must(ctx context.Context, method, key string, q url.Values, body []byte, extra map[string]string, action string) (*http.Response, error) {
	res, err := s.do(ctx, method, key, q, body, extra)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		defer res.Body.Close()
		return nil, s3Fail(res, action)
	}
	return res, nil
}

func (s *s3Client) retry(ctx context.Context, f func() error) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = f(); err == nil || ctx.Err() != nil {
			return err
		}
		time.Sleep(time.Duration(i+1) * 2 * time.Second)
	}
	return err
}

// Test проверяет доступ к бакету и папке.
func (s *s3Client) Test(ctx context.Context) error {
	q := url.Values{"list-type": {"2"}, "max-keys": {"1"}}
	if s.c.Prefix != "" {
		q.Set("prefix", s.c.Prefix+"/")
	}
	res, err := s.must(ctx, "GET", "", q, nil, nil, "проверка")
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// Upload загружает файл в объект name (в папке Prefix); большие файлы — частями.
func (s *s3Client) Upload(ctx context.Context, file, name string) error {
	st, err := os.Stat(file)
	if err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	key := s.keyFor(name)
	size := st.Size()
	if size <= s3PartSize {
		data, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		return s.retry(ctx, func() error {
			res, err := s.must(ctx, "PUT", key, nil, data, nil, "загрузка")
			if err == nil {
				res.Body.Close()
			}
			return err
		})
	}
	part := s3PartSize
	if size/part > 9000 {
		part = (size/9000/(1<<20) + 1) << 20
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	res, err := s.must(ctx, "POST", key, url.Values{"uploads": {""}}, nil, nil, "начало загрузки")
	if err != nil {
		return err
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	if err := xml.Unmarshal(data, &init); err != nil || init.UploadID == "" {
		return errf("Облако: хранилище не вернуло номер загрузки")
	}
	abort := func() {
		c2, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if r, e := s.do(c2, "DELETE", key, url.Values{"uploadId": {init.UploadID}}, nil, nil); e == nil {
			r.Body.Close()
		}
	}
	type donePart struct {
		N    int
		ETag string
	}
	var done []donePart
	buf := make([]byte, part)
	for n := 1; ; n++ {
		read, rerr := io.ReadFull(f, buf)
		if read == 0 && (rerr == io.EOF || rerr == io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
			abort()
			return rerr
		}
		chunk := buf[:read]
		var etag string
		if err := s.retry(ctx, func() error {
			pctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			r, e := s.must(pctx, "PUT", key, url.Values{"partNumber": {fmt.Sprint(n)}, "uploadId": {init.UploadID}}, chunk, nil, fmt.Sprintf("загрузка части %d", n))
			if e != nil {
				return e
			}
			etag = r.Header.Get("ETag")
			r.Body.Close()
			return nil
		}); err != nil {
			abort()
			return err
		}
		done = append(done, donePart{n, etag})
		if rerr == io.ErrUnexpectedEOF || rerr == io.EOF {
			break
		}
	}
	var body bytes.Buffer
	body.WriteString("<CompleteMultipartUpload>")
	for _, p := range done {
		fmt.Fprintf(&body, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", p.N, strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(p.ETag))
	}
	body.WriteString("</CompleteMultipartUpload>")
	res, err = s.must(ctx, "POST", key, url.Values{"uploadId": {init.UploadID}}, body.Bytes(), nil, "завершение загрузки")
	if err != nil {
		abort()
		return err
	}
	// Хранилище может ответить 200 с ошибкой в теле.
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	if bytes.Contains(out, []byte("<Error>")) {
		abort()
		return errf("Облако (завершение загрузки): %s", firstLine(string(out)))
	}
	return nil
}

// Download сохраняет объект в файл dst.
func (s *s3Client) Download(ctx context.Context, key, dst string) error {
	res, err := s.must(ctx, "GET", key, nil, nil, nil, "скачивание")
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
		return errf("Облако (скачивание): %s", firstLine(err.Error()))
	}
	return nil
}

// Head возвращает начало объекта (до n байт), например манифест архива.
func (s *s3Client) Head(ctx context.Context, key string, n int64) (io.ReadCloser, error) {
	res, err := s.must(ctx, "GET", key, nil, nil, map[string]string{"Range": fmt.Sprintf("bytes=0-%d", n-1)}, "чтение")
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, n), res.Body}, nil
}

func (s *s3Client) Delete(ctx context.Context, key string) error {
	res, err := s.do(ctx, "DELETE", key, nil, nil, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 || (res.StatusCode >= 200 && res.StatusCode < 300) {
		return nil
	}
	return s3Fail(res, "удаление")
}

// List возвращает имена объектов, лежащих прямо в папке Prefix (без вложенных папок).
func (s *s3Client) List(ctx context.Context) ([]string, error) {
	prefix := ""
	if s.c.Prefix != "" {
		prefix = s.c.Prefix + "/"
	}
	var names []string
	token := ""
	for page := 0; page < 200; page++ {
		q := url.Values{"list-type": {"2"}, "delimiter": {"/"}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		res, err := s.must(ctx, "GET", "", q, nil, nil, "список копий")
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		var out struct {
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
			Contents  []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
		}
		if err := xml.Unmarshal(data, &out); err != nil {
			return nil, errf("Облако: непонятный ответ хранилища на запрос списка")
		}
		for _, c := range out.Contents {
			if n := strings.TrimPrefix(c.Key, prefix); n != "" && !strings.Contains(n, "/") {
				names = append(names, n)
			}
		}
		if !out.Truncated || out.Next == "" {
			break
		}
		token = out.Next
	}
	sort.Strings(names)
	return names, nil
}

// ---------- интеграция с резервными копиями ----------

// backupS3Settings возвращает сохранённые настройки облака и место хранения.
func backupS3Settings() (BackupS3, string) {
	mu.Lock()
	defer mu.Unlock()
	t := store.Settings.BackupTarget
	if t == "" {
		t = "local"
	}
	return store.Settings.BackupS3, t
}

func backupTargetIsS3(t string) bool { return t == "s3" || t == "local_s3" }
