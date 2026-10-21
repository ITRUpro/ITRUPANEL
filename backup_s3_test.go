// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Примеры из документации AWS Signature Version 4 для S3 — независимая проверка подписи.
func TestS3SignAWSExamples(t *testing.T) {
	c := BackupS3{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	empty := sha256Hex(nil)

	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	s3Sign(req, c, empty, now)
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41") || !strings.Contains(got, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("GET с Range: %s", got)
	}

	req, _ = http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?lifecycle", nil)
	s3Sign(req, c, empty, now)
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, "Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543") {
		t.Fatalf("GET ?lifecycle: %s", got)
	}
}

func TestS3Escape(t *testing.T) {
	if got := s3Escape("a b/ф+=~.-_&", true); got != "a%20b/%D1%84%2B%3D~.-_%26" {
		t.Fatalf("keepSlash: %s", got)
	}
	if got := s3Escape("a/b", false); got != "a%2Fb" {
		t.Fatalf("без слэша: %s", got)
	}
	if got := s3Query(url.Values{"prefix": {"a b/"}, "list-type": {"2"}, "uploads": {""}}); got != "list-type=2&prefix=a%20b%2F&uploads=" {
		t.Fatalf("запрос: %s", got)
	}
}

func TestS3NormalizeAndSource(t *testing.T) {
	ok := BackupS3{Endpoint: "s3.example.com/", Bucket: "my-bucket", AccessKey: "k", SecretKey: "s", Prefix: "/itrupanel/main/"}
	n, err := s3Normalize(ok)
	if err != nil || n.Endpoint != "https://s3.example.com" || n.Region != "us-east-1" || n.Prefix != "itrupanel/main" {
		t.Fatalf("нормализация: %+v %v", n, err)
	}
	for name, bad := range map[string]BackupS3{
		"адрес с путём":  {Endpoint: "https://s3.example.com/x", Bucket: "bb", AccessKey: "k", SecretKey: "s"},
		"пустой бакет":   {Endpoint: "https://s3.example.com", Bucket: "", AccessKey: "k", SecretKey: "s"},
		"нет ключей":     {Endpoint: "https://s3.example.com", Bucket: "bb"},
		"пробел в ключе": {Endpoint: "https://s3.example.com", Bucket: "bb", AccessKey: "k k", SecretKey: "s"},
		"папка ..":       {Endpoint: "https://s3.example.com", Bucket: "bb", AccessKey: "k", SecretKey: "s", Prefix: "a/../b"},
		"регион":         {Endpoint: "https://s3.example.com", Bucket: "bb", AccessKey: "k", SecretKey: "s", Region: "us east"},
	} {
		if _, err := s3Normalize(bad); err == nil {
			t.Fatalf("принято: %s", name)
		}
	}
	a, b := backupS3Source(n), backupS3Source(BackupS3{Endpoint: "https://S3.example.com", Bucket: "my-bucket", AccessKey: "other", SecretKey: "other", Prefix: "itrupanel/main"})
	if a != b || !backupIsS3Source(a) {
		t.Fatalf("источник зависит от ключей: %s %s", a, b)
	}
	if backupS3Source(BackupS3{Endpoint: n.Endpoint, Bucket: "other", Prefix: n.Prefix}) == a {
		t.Fatal("разные бакеты дали один источник")
	}
}

// fakeS3 — минимальный S3: проверяет подпись пересчётом и хранит объекты в памяти.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string][]byte
	uploads  map[string]map[int][]byte
	cfg      BackupS3
	pageSize int
	calls    []string
	failPart int // номер части, которая один раз вернёт ошибку 500
	failed   bool
}

func newFakeS3(t *testing.T, cfg BackupS3) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{objects: map[string][]byte{}, uploads: map[string]map[int][]byte{}, cfg: cfg, pageSize: 2}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func s3XMLError(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, msg)
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	// Подпись: пересчитываем её теми же функциями по присланному запросу.
	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+f.cfg.AccessKey+"/") {
		s3XMLError(w, 403, "InvalidAccessKeyId", "bad key")
		return
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
		s3XMLError(w, 400, "XAmzContentSHA256Mismatch", "payload hash")
		return
	}
	check := httptest.NewRequest(r.Method, "http://"+r.Host+r.URL.RequestURI(), nil)
	check.URL.Scheme = "http"
	check.URL.Host = r.Host
	for _, h := range []string{"Range"} {
		if v := r.Header.Get(h); v != "" {
			check.Header.Set(h, v)
		}
	}
	at, _ := time.Parse("20060102T150405Z", r.Header.Get("x-amz-date"))
	s3Sign(check, f.cfg, r.Header.Get("x-amz-content-sha256"), at)
	if check.Header.Get("Authorization") != r.Header.Get("Authorization") {
		s3XMLError(w, 403, "SignatureDoesNotMatch", "signature")
		return
	}
	// path-style: /bucket/key
	p := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(p, "/")
	if bucket != f.cfg.Bucket {
		s3XMLError(w, 404, "NoSuchBucket", "no bucket")
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == "GET" && key == "" && q.Get("list-type") == "2":
		prefix, delim := q.Get("prefix"), q.Get("delimiter")
		var keys []string
		for k := range f.objects {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			if delim != "" && strings.Contains(strings.TrimPrefix(k, prefix), delim) {
				continue // «вложенная папка» не показывается как объект
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		start := 0
		if tok := q.Get("continuation-token"); tok != "" {
			start, _ = strconv.Atoi(tok)
		}
		end := start + f.pageSize
		if mk, e := strconv.Atoi(q.Get("max-keys")); e == nil && mk < f.pageSize {
			end = start + mk
		}
		truncated := end < len(keys)
		if end > len(keys) {
			end = len(keys)
		}
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><ListBucketResult>`)
		fmt.Fprintf(&b, "<IsTruncated>%t</IsTruncated>", truncated)
		if truncated {
			fmt.Fprintf(&b, "<NextContinuationToken>%d</NextContinuationToken>", end)
		}
		for _, k := range keys[start:end] {
			var esc bytes.Buffer
			xml.EscapeText(&esc, []byte(k))
			fmt.Fprintf(&b, "<Contents><Key>%s</Key></Contents>", esc.String())
		}
		b.WriteString("</ListBucketResult>")
		w.Write([]byte(b.String()))
	case r.Method == "PUT" && q.Get("partNumber") != "":
		n, _ := strconv.Atoi(q.Get("partNumber"))
		if n == f.failPart && !f.failed {
			f.failed = true
			s3XMLError(w, 500, "InternalError", "boom")
			return
		}
		parts := f.uploads[q.Get("uploadId")]
		if parts == nil {
			s3XMLError(w, 404, "NoSuchUpload", "no upload")
			return
		}
		parts[n] = body
		w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
	case r.Method == "PUT":
		f.objects[key] = body
	case r.Method == "POST" && q.Has("uploads"):
		id := fmt.Sprintf("up%d", len(f.uploads)+1)
		f.uploads[id] = map[int][]byte{}
		fmt.Fprintf(w, `<?xml version="1.0"?><InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case r.Method == "POST" && q.Get("uploadId") != "":
		parts := f.uploads[q.Get("uploadId")]
		var req struct {
			Parts []struct {
				N    int    `xml:"PartNumber"`
				ETag string `xml:"ETag"`
			} `xml:"Part"`
		}
		if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) != len(parts) {
			s3XMLError(w, 400, "MalformedXML", "bad complete")
			return
		}
		var all []byte
		for i, p := range req.Parts {
			if p.N != i+1 || p.ETag != fmt.Sprintf(`"etag-%d"`, p.N) {
				s3XMLError(w, 400, "InvalidPart", "bad part")
				return
			}
			all = append(all, parts[p.N]...)
		}
		f.objects[key] = all
		delete(f.uploads, q.Get("uploadId"))
		w.Write([]byte(`<?xml version="1.0"?><CompleteMultipartUploadResult><Key>x</Key></CompleteMultipartUploadResult>`))
	case r.Method == "DELETE" && q.Get("uploadId") != "":
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(204)
	case r.Method == "DELETE":
		delete(f.objects, key)
		w.WriteHeader(204)
	case r.Method == "GET":
		data, ok := f.objects[key]
		if !ok {
			s3XMLError(w, 404, "NoSuchKey", "no key")
			return
		}
		if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=0-") {
			end, _ := strconv.Atoi(strings.TrimPrefix(rg, "bytes=0-"))
			if end+1 < len(data) {
				data = data[:end+1]
				w.WriteHeader(206)
			}
		}
		w.Write(data)
	default:
		s3XMLError(w, 400, "InvalidRequest", "unsupported")
	}
}

func s3TestConfig(srv *httptest.Server) BackupS3 {
	return BackupS3{Endpoint: srv.URL, Region: "ru-central1", Bucket: "backups", AccessKey: "AK", SecretKey: "SK", Prefix: "itrupanel/main", PathStyle: true}
}

func TestS3ClientRoundTrip(t *testing.T) {
	cfg := BackupS3{Endpoint: "http://placeholder", Region: "ru-central1", Bucket: "backups", AccessKey: "AK", SecretKey: "SK", Prefix: "itrupanel/main", PathStyle: true}
	fake, srv := newFakeS3(t, cfg)
	cfg = s3TestConfig(srv)
	fake.cfg = cfg
	cl, err := newS3Client(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := cl.Test(ctx); err != nil {
		t.Fatalf("проверка доступа: %v", err)
	}
	// Неверный секретный ключ даёт понятное сообщение.
	bad := cfg
	bad.SecretKey = "WRONG"
	cb, _ := newS3Client(bad)
	if err := cb.Test(ctx); err == nil || !strings.Contains(err.Error(), "неверный секретный ключ") {
		t.Fatalf("неверный ключ: %v", err)
	}
	bad = cfg
	bad.Bucket = "nope"
	cb, _ = newS3Client(bad)
	if err := cb.Test(ctx); err == nil || !strings.Contains(err.Error(), "бакет не найден") {
		t.Fatalf("нет бакета: %v", err)
	}
	bad = cfg
	bad.AccessKey = "OTHER"
	cb, _ = newS3Client(bad)
	if err := cb.Test(ctx); err == nil || !strings.Contains(err.Error(), "неверный ключ доступа") {
		t.Fatalf("неверный ключ доступа: %v", err)
	}

	dir := t.TempDir()
	small := dir + "/small_20261019-120000-aaaaaaaa.tar.gz"
	os.WriteFile(small, []byte("hello cloud"), 0600)
	if err := cl.Upload(ctx, small, "small.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if string(fake.objects["itrupanel/main/small.tar.gz"]) != "hello cloud" {
		t.Fatalf("объект: %q", fake.objects["itrupanel/main/small.tar.gz"])
	}

	// Загрузка частями: 3,5 МБ при части 1 МБ, одна часть один раз получает ошибку 500 и повторяется.
	old := s3PartSize
	s3PartSize = 1 << 20
	defer func() { s3PartSize = old }()
	big := bytes.Repeat([]byte("0123456789abcdef"), (3<<20+512<<10)/16)
	os.WriteFile(dir+"/big.bin", big, 0600)
	fake.failPart = 2
	if err := cl.Upload(ctx, dir+"/big.bin", "big.tar.gz"); err != nil {
		t.Fatalf("загрузка частями: %v", err)
	}
	if !bytes.Equal(fake.objects["itrupanel/main/big.tar.gz"], big) {
		t.Fatalf("собранный объект не совпадает: %d из %d", len(fake.objects["itrupanel/main/big.tar.gz"]), len(big))
	}
	if !fake.failed {
		t.Fatal("повтор части не проверен")
	}
	if len(fake.uploads) != 0 {
		t.Fatalf("осталась незавершённая загрузка: %v", fake.uploads)
	}

	// Скачивание и чтение начала.
	if err := cl.Download(ctx, "itrupanel/main/big.tar.gz", dir+"/back.bin"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dir + "/back.bin"); !bytes.Equal(b, big) {
		t.Fatal("скачанное не совпадает")
	}
	rc, err := cl.Head(ctx, "itrupanel/main/big.tar.gz", 100)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(head, big[:100]) {
		t.Fatalf("начало объекта: %d байт", len(head))
	}
	if err := cl.Download(ctx, "itrupanel/main/нет.tar.gz", dir+"/x"); err == nil {
		t.Fatal("скачивание несуществующего")
	}

	// Список: постранично, вложенные папки и чужие префиксы не попадают.
	fake.objects["itrupanel/main/third.tar.gz"] = []byte("3")
	fake.objects["itrupanel/main/sub/inner.tar.gz"] = []byte("x")
	fake.objects["other/foreign.tar.gz"] = []byte("x")
	names, err := cl.List(ctx)
	if err != nil || strings.Join(names, ",") != "big.tar.gz,small.tar.gz,third.tar.gz" {
		t.Fatalf("список: %v %v", names, err)
	}
	if err := cl.Delete(ctx, "itrupanel/main/third.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Delete(ctx, "itrupanel/main/нет-такого.tar.gz"); err != nil {
		t.Fatalf("удаление несуществующего должно проходить: %v", err)
	}
	if _, ok := fake.objects["itrupanel/main/third.tar.gz"]; ok {
		t.Fatal("объект не удалён")
	}
}

func TestBackupCloudUploadFetchDeleteAndSettings(t *testing.T) {
	testPanel(t)
	cfg := BackupS3{Endpoint: "http://placeholder", Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK", Prefix: "p", PathStyle: true}
	fake, srv := newFakeS3(t, cfg)
	cfg = s3TestConfig(srv)
	cfg.Bucket, cfg.Prefix, cfg.Region = "bk", "p", "us-east-1"
	fake.cfg = cfg
	store.Settings.BackupTarget, store.Settings.BackupS3 = "s3", cfg
	store.Sites = []*Site{{Domain: "example.test"}}
	os.MkdirAll(P(backupDir), 0700)
	name := "example.test_20261019-120000-abcdef12.tar.gz"
	b := &Backup{ID: "20261019-120000-abcdef12", File: backupDir + "/" + name, Site: "example.test", Kind: "files", Local: true}
	os.WriteFile(P(b.File), []byte("archive-bytes"), 0600)
	if err := uploadBackup(b); err != nil {
		t.Fatal(err)
	}
	if b.Remote != "p/"+name || !backupIsS3Source(b.RemoteSource) || b.Local || fileExists(b.File) {
		t.Fatalf("после загрузки в режиме «только облако»: %+v", b)
	}
	if string(fake.objects["p/"+name]) != "archive-bytes" {
		t.Fatal("объект в облаке")
	}
	// Скачивание.
	path, tmp, err := fetchBackup(b)
	if err != nil || !tmp {
		t.Fatalf("скачивание: %v %v", err, tmp)
	}
	if data, _ := os.ReadFile(P(path)); string(data) != "archive-bytes" {
		t.Fatalf("скачано: %q", data)
	}
	// Другое хранилище: копия не должна открываться через чужие настройки.
	mu.Lock()
	store.Backups = []*Backup{b}
	issue := backupRestoreIssue(b)
	mu.Unlock()
	if strings.Contains(issue, "исходное облачное хранилище") {
		t.Fatalf("то же хранилище отклонено: %s", issue)
	}
	other := cfg
	other.Bucket = "another"
	store.Settings.BackupS3 = other
	mu.Lock()
	issue = backupRestoreIssue(b)
	mu.Unlock()
	if !strings.Contains(issue, "исходное облачное хранилище") {
		t.Fatalf("чужое хранилище принято: %q", issue)
	}
	if _, _, err := fetchBackup(b); err == nil {
		t.Fatal("скачивание из чужого хранилища разрешено")
	}
	store.Settings.BackupS3 = cfg
	// Удаление.
	deleteRemote(b)
	if _, ok := fake.objects["p/"+name]; ok {
		t.Fatal("объект остался в облаке")
	}
	// Режим «здесь и в облаке» сохраняет локальный файл.
	store.Settings.BackupTarget = "local_s3"
	b2 := &Backup{ID: "20261019-130000-abcdef12", File: backupDir + "/" + "example.test_20261019-130000-abcdef12.tar.gz", Site: "example.test", Kind: "files", Local: true}
	os.WriteFile(P(b2.File), []byte("two"), 0600)
	if err := uploadBackup(b2); err != nil || !b2.Local || !fileExists(b2.File) || b2.Remote == "" {
		t.Fatalf("local_s3: %v %+v", err, b2)
	}
}

func TestBackupCloudSettingsAPI(t *testing.T) {
	mux := testPanel(t)
	cfg := BackupS3{Endpoint: "http://placeholder", Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK", PathStyle: true}
	fake, srv := newFakeS3(t, cfg)
	cfg.Endpoint = srv.URL
	fake.cfg = cfg
	body := func(secret string, test bool) string {
		return fmt.Sprintf(`{"target":"s3","test":%t,"s3":{"endpoint":%q,"region":"us-east-1","bucket":"bk","access_key":"AK","secret_key":%q,"prefix":"itru","path_style":true},"schedule":{"keep":5,"hour":2}}`, test, srv.URL, secret)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("WRONG", true)); r["error"] == nil || !strings.Contains(fmt.Sprint(r["error"]), "облак") {
		t.Fatalf("неверный ключ принят: %v", r)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("SK", true)); r["ok"] != true || store.Settings.BackupTarget == "s3" {
		t.Fatalf("проверка не должна сохранять: %v target=%q", r, store.Settings.BackupTarget)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("SK", false)); r["ok"] != true {
		t.Fatalf("сохранение: %v", r)
	}
	if store.Settings.BackupTarget != "s3" || store.Settings.BackupS3.SecretKey != "SK" || store.Settings.BackupS3.Prefix != "itru" || store.Settings.BackupKeep != 5 {
		t.Fatalf("настройки: %+v %q", store.Settings.BackupS3, store.Settings.BackupTarget)
	}
	// Секретный ключ в ответе скрыт, а при сохранении с маской остаётся прежним.
	_, g := testAPI(t, mux, "backups.ftp.get", `{}`)
	data := g["data"].(map[string]any)
	s3m := data["s3"].(map[string]any)
	if data["target"] != "s3" || strings.Contains(fmt.Sprint(s3m["secret_key"]), "SK") || s3m["bucket"] != "bk" {
		t.Fatalf("ответ: %v", data)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("••••••••••••", false)); r["ok"] != true || store.Settings.BackupS3.SecretKey != "SK" {
		t.Fatalf("маска затёрла ключ: %v %q", r, store.Settings.BackupS3.SecretKey)
	}
	// Настройки облака входят в копию настроек панели и проходят проверку.
	p := capturePanelBackupSettings(store.Settings)
	if p.BackupS3.Bucket != "bk" || p.validate() != nil {
		t.Fatalf("настройки панели в копии: %+v %v", p.BackupS3, p.validate())
	}
	p.BackupS3.Bucket = ""
	if p.validate() == nil {
		t.Fatal("пустой бакет в копии принят")
	}
}

func TestBackupCloudDiscovery(t *testing.T) {
	testPanel(t)
	cfg := BackupS3{Endpoint: "http://placeholder", Region: "us-east-1", Bucket: "bk", AccessKey: "AK", SecretKey: "SK", Prefix: "old", PathStyle: true}
	fake, srv := newFakeS3(t, cfg)
	cfg.Endpoint = srv.URL
	fake.cfg = cfg
	m := discoveryMeta()
	archive := discoveryArchive(t, m)
	name := "example.test_20261013-120000-aabb.tar.gz"
	fake.objects["old/"+name] = archive
	fake.objects["old/random.txt"] = []byte("not a backup")
	store.Settings.BackupTarget, store.Settings.BackupS3 = "s3", cfg
	j := &Job{ctx: context.Background()}
	res, err := scanBackupS3(j)
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if res.(map[string]any)["added"] != 1 || len(store.Backups) != 1 {
		t.Fatalf("итог поиска: %v, копий %d", res, len(store.Backups))
	}
	b := store.Backups[0]
	if b.Remote != "old/"+name || !backupIsS3Source(b.RemoteSource) || !b.Imported || b.Local {
		t.Fatalf("найденная копия: %+v", b)
	}
	// Повторный поиск ничего не дублирует.
	if res, err = scanBackupS3(j); err != nil || res.(map[string]any)["added"] != 0 || len(store.Backups) != 1 {
		t.Fatalf("повторный поиск: %v %v", res, err)
	}
}
