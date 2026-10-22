// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// cloudConformance проверяет любой вид хранилища одним набором действий: проверка подключения,
// загрузка маленького и большого файла, начало файла, скачивание, список, удаление.
func cloudConformance(t *testing.T, cl cloudStore) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if err := cl.Test(ctx); err != nil {
		t.Fatalf("проверка подключения: %v", err)
	}
	big := make([]byte, 5<<20+12345)
	rand.Read(big)
	os.WriteFile(dir+"/big", big, 0600)
	os.WriteFile(dir+"/small", []byte("small-archive"), 0600)
	bigKey, err := cl.Upload(ctx, dir+"/big", "big.tar.gz")
	if err != nil {
		t.Fatalf("загрузка большого файла: %v", err)
	}
	smallKey, err := cl.Upload(ctx, dir+"/small", "small.tar.gz")
	if err != nil {
		t.Fatalf("загрузка маленького файла: %v", err)
	}
	// Повторная загрузка под тем же именем заменяет файл, а не дублирует его.
	if _, err := cl.Upload(ctx, dir+"/small", "small.tar.gz"); err != nil {
		t.Fatalf("повторная загрузка: %v", err)
	}
	objs, err := cl.List(ctx)
	if err != nil {
		t.Fatalf("список: %v", err)
	}
	names := map[string]int{}
	for _, o := range objs {
		names[o.Name]++
	}
	if names["big.tar.gz"] != 1 || names["small.tar.gz"] != 1 || len(names) != 2 {
		t.Fatalf("в списке: %v", names)
	}
	if err := cl.Download(ctx, bigKey, dir+"/got"); err != nil {
		t.Fatalf("скачивание: %v", err)
	}
	if got, _ := os.ReadFile(dir + "/got"); !bytes.Equal(got, big) {
		t.Fatalf("скачанный файл отличается: %d и %d байт", len(got), len(big))
	}
	rc, err := cl.Head(ctx, bigKey, 100)
	if err != nil {
		t.Fatalf("начало файла: %v", err)
	}
	head, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(head, big[:100]) {
		t.Fatalf("начало файла: %d байт", len(head))
	}
	if err := cl.Download(ctx, smallKey+"-нет", dir+"/x"); err == nil {
		t.Fatal("скачивание несуществующего файла прошло")
	}
	if _, err := os.Stat(dir + "/x"); err == nil {
		t.Fatal("после неудачного скачивания остался файл")
	}
	if err := cl.Delete(ctx, bigKey); err != nil {
		t.Fatalf("удаление: %v", err)
	}
	if err := cl.Delete(ctx, bigKey); err != nil {
		t.Fatalf("повторное удаление должно проходить: %v", err)
	}
	objs, _ = cl.List(ctx)
	if len(objs) != 1 || objs[0].Name != "small.tar.gz" {
		t.Fatalf("после удаления: %v", objs)
	}
}

// ---------- WebDAV ----------

func newTestDAV(t *testing.T) (*httptest.Server, BackupWebDAV) {
	t.Helper()
	h := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "user@example.test" || p != "app-password" {
			w.WriteHeader(401)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, BackupWebDAV{Provider: "custom", URL: srv.URL, User: "user@example.test", Pass: "app-password", Dir: "itrupanel/main"}
}

func TestWebDAVStoreConformance(t *testing.T) {
	_, cfg := newTestDAV(t)
	cl, err := newDAVStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cloudConformance(t, cl)
}

func TestWebDAVWrongPasswordAndNormalize(t *testing.T) {
	_, cfg := newTestDAV(t)
	cfg.Pass = "wrong"
	cl, _ := newDAVStore(cfg)
	if err := cl.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "пароль") {
		t.Fatalf("неверный пароль: %v", err)
	}
	for _, bad := range []BackupWebDAV{
		{URL: "ftp://x.test", User: "u", Pass: "p"},
		{URL: "https://x.test", User: "", Pass: "p"},
		{URL: "https://x.test", User: "u", Pass: ""},
		{URL: "https://x.test", User: "u", Pass: "p", Dir: "../etc"},
		{URL: "https://x.test", User: "u", Pass: "p", Dir: "a b"},
		{URL: "https://u:p@x.test", User: "u", Pass: "p"},
	} {
		if _, err := davNormalize(bad); err == nil {
			t.Fatalf("принято: %+v", bad)
		}
	}
	// Готовые сервисы подставляют свой адрес и не позволяют отключить проверку сертификата.
	for p, u := range map[string]string{"yandex": "https://webdav.yandex.ru", "mailru": "https://webdav.cloud.mail.ru"} {
		n, err := davNormalize(BackupWebDAV{Provider: p, URL: "http://evil.test", User: "u", Pass: "p", Insecure: true})
		if err != nil || n.URL != u || n.Insecure {
			t.Fatalf("%s: %+v %v", p, n, err)
		}
	}
	a, _ := newDAVStore(BackupWebDAV{URL: "https://x.test", User: "u", Pass: "p", Dir: "a"})
	b, _ := newDAVStore(BackupWebDAV{URL: "https://x.test", User: "u", Pass: "other", Dir: "a"})
	c, _ := newDAVStore(BackupWebDAV{URL: "https://x.test", User: "u", Pass: "p", Dir: "b"})
	if a.Source() != b.Source() || a.Source() == c.Source() || !backupIsCloudSource(a.Source()) || cloudKindOfSource(a.Source()) != cloudWebDAV {
		t.Fatal("идентификатор хранилища")
	}
}

// TestWebDAVQuotaMessage: ответ 507 превращается в понятное сообщение и не повторяется.
func TestWebDAVQuotaMessage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(201)
		case "PUT":
			calls++
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(507)
		}
	}))
	defer srv.Close()
	cl, _ := newDAVStore(BackupWebDAV{URL: srv.URL, User: "u", Pass: "p", Dir: "x"})
	f := t.TempDir() + "/f"
	os.WriteFile(f, []byte("data"), 0600)
	_, err := cl.Upload(context.Background(), f, "a.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "не хватает места") || calls != 1 {
		t.Fatalf("%v, запросов %d", err, calls)
	}
}

func TestBackupWebDAVUploadFetchDeleteDiscovery(t *testing.T) {
	testPanel(t)
	_, cfg := newTestDAV(t)
	store.Settings.BackupTarget, store.Settings.BackupDAV = "webdav", cfg
	store.Sites = []*Site{{Domain: "example.test"}}
	os.MkdirAll(P(backupDir), 0700)
	name := "example.test_20261019-120000-abcdef12.tar.gz"
	b := &Backup{ID: "20261019-120000-abcdef12", File: backupDir + "/" + name, Site: "example.test", Kind: "files", Local: true}
	os.WriteFile(P(b.File), []byte("archive-bytes"), 0600)
	if err := uploadBackup(b); err != nil {
		t.Fatal(err)
	}
	if b.Remote != "/itrupanel/main/"+name || cloudKindOfSource(b.RemoteSource) != cloudWebDAV || b.Local || fileExists(b.File) {
		t.Fatalf("после загрузки в режиме «только облако»: %+v", b)
	}
	path, tmp, err := fetchBackup(b)
	if err != nil || !tmp {
		t.Fatalf("скачивание: %v %v", err, tmp)
	}
	if data, _ := os.ReadFile(P(path)); string(data) != "archive-bytes" {
		t.Fatalf("скачано: %q", data)
	}
	// Копия привязана к своему хранилищу.
	mu.Lock()
	store.Backups = []*Backup{b}
	issue := backupRestoreIssue(b)
	mu.Unlock()
	if strings.Contains(issue, "исходное облачное хранилище") {
		t.Fatalf("то же хранилище отклонено: %s", issue)
	}
	other := cfg
	other.Dir = "another"
	store.Settings.BackupDAV = other
	mu.Lock()
	issue = backupRestoreIssue(b)
	mu.Unlock()
	if !strings.Contains(issue, "исходное облачное хранилище") {
		t.Fatalf("чужое хранилище принято: %q", issue)
	}
	if _, _, err := fetchBackup(b); err == nil {
		t.Fatal("скачивание из чужого хранилища разрешено")
	}
	store.Settings.BackupDAV = cfg
	deleteRemote(b)
	cl, _ := newCloudStore(cloudWebDAV)
	if objs, _ := cl.List(context.Background()); len(objs) != 0 {
		t.Fatalf("файл остался в облаке: %v", objs)
	}
	// «Здесь и в облаке» сохраняет локальный файл.
	store.Settings.BackupTarget = "local_webdav"
	b2 := &Backup{ID: "20261019-130000-abcdef12", File: backupDir + "/example.test_20261019-130000-abcdef12.tar.gz", Site: "example.test", Kind: "files", Local: true}
	os.WriteFile(P(b2.File), []byte("two"), 0600)
	if err := uploadBackup(b2); err != nil || !b2.Local || !fileExists(b2.File) || b2.Remote == "" {
		t.Fatalf("local_webdav: %v %+v", err, b2)
	}
	// Поиск копий в облаке: архивы ITRUPANEL находятся, посторонние файлы пропускаются.
	store.Backups = nil
	m := discoveryMeta()
	archive := discoveryArchive(t, m)
	dname := "example.test_20261013-120000-aabb.tar.gz"
	src := filepathJoinTemp(t, archive)
	cl, _ = newCloudStore(cloudWebDAV)
	if _, err := cl.Upload(context.Background(), src, dname); err != nil {
		t.Fatal(err)
	}
	junk := filepathJoinTemp(t, []byte("not a backup"))
	cl.Upload(context.Background(), junk, "random.txt")
	j := &Job{ctx: context.Background()}
	res, err := scanBackupCloud(j, cloudWebDAV)
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	found := false
	for _, x := range store.Backups {
		if x.Remote == "/itrupanel/main/"+dname && x.Imported && !x.Local && cloudKindOfSource(x.RemoteSource) == cloudWebDAV {
			found = true
		}
	}
	if !found || res.(map[string]any)["added"] != 1 {
		t.Fatalf("итог поиска: %v, копии: %d", res, len(store.Backups))
	}
	if res, err = scanBackupCloud(j, cloudWebDAV); err != nil || res.(map[string]any)["added"] != 0 {
		t.Fatalf("повторный поиск: %v %v", res, err)
	}
}

func filepathJoinTemp(t *testing.T, data []byte) string {
	t.Helper()
	f := t.TempDir() + "/f"
	if err := os.WriteFile(f, data, 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

var _ = fmt.Sprint

// ---------- Dropbox (поддельный сервер по описанию API v2) ----------

type fakeDropbox struct {
	t        *testing.T
	files    map[string][]byte // путь в нижнем регистре → содержимое
	sessions map[string]*bytes.Buffer
	refresh  string
	tokens   int
	verifier string // PKCE: ожидаемый code_challenge для кода «CODE123»
	srv      *httptest.Server
}

func newFakeDropbox(t *testing.T) *fakeDropbox {
	f := &fakeDropbox{t: t, files: map[string][]byte{}, sessions: map[string]*bytes.Buffer{}, refresh: "REFRESH-1"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	oldAPI, oldContent, oldChunk, oldSingle := dropboxAPI, dropboxContent, dropboxChunk, dropboxSingle
	dropboxAPI, dropboxContent = f.srv.URL+"/api", f.srv.URL+"/content"
	dropboxChunk, dropboxSingle = 2<<20, 3<<20
	t.Cleanup(func() {
		dropboxAPI, dropboxContent, dropboxChunk, dropboxSingle = oldAPI, oldContent, oldChunk, oldSingle
	})
	dropboxTokensMu.Lock()
	dropboxTokens = map[string]dropboxToken{}
	dropboxTokensMu.Unlock()
	return f
}

func (f *fakeDropbox) jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (f *fakeDropbox) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/oauth2/token":
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.refresh || r.Form.Get("client_id") != "appkey123" {
				f.jsonOut(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			f.tokens++
			f.jsonOut(w, 200, map[string]any{"access_token": fmt.Sprintf("ACCESS-%d", f.tokens), "expires_in": 14400})
		case "authorization_code":
			if r.Form.Get("code") != "CODE123" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_id") != "appkey123" {
				f.jsonOut(w, 400, map[string]any{"error": "invalid_grant", "error_description": "code is invalid"})
				return
			}
			f.tokens++
			f.jsonOut(w, 200, map[string]any{"access_token": fmt.Sprintf("ACCESS-%d", f.tokens), "refresh_token": f.refresh, "account_id": "dbid:ACC1", "expires_in": 14400})
		}
		return
	}
	if r.Header.Get("Authorization") != fmt.Sprintf("Bearer ACCESS-%d", f.tokens) {
		f.jsonOut(w, 401, map[string]any{"error_summary": "expired_access_token/"})
		return
	}
	var arg map[string]any
	if a := r.Header.Get("Dropbox-API-Arg"); a != "" {
		json.Unmarshal([]byte(a), &arg)
	} else if r.ContentLength > 0 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(r.Body).Decode(&arg)
	}
	path := func(m map[string]any) string { p, _ := m["path"].(string); return strings.ToLower(p) }
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/api/2/users/get_current_account":
		f.jsonOut(w, 200, map[string]any{"account_id": "dbid:ACC1", "email": "me@example.test"})
	case "/content/2/files/upload":
		f.files[path(arg)] = body
		f.jsonOut(w, 200, map[string]any{"name": "x"})
	case "/content/2/files/upload_session/start":
		id := fmt.Sprintf("S%d", len(f.sessions)+1)
		f.sessions[id] = bytes.NewBuffer(append([]byte(nil), body...))
		f.jsonOut(w, 200, map[string]any{"session_id": id})
	case "/content/2/files/upload_session/append_v2", "/content/2/files/upload_session/finish":
		cur, _ := arg["cursor"].(map[string]any)
		s := f.sessions[fmt.Sprint(cur["session_id"])]
		if s == nil || int64(s.Len()) != int64(cur["offset"].(float64)) {
			f.jsonOut(w, 409, map[string]any{"error_summary": "incorrect_offset/"})
			return
		}
		s.Write(body)
		if strings.HasSuffix(r.URL.Path, "finish") {
			commit, _ := arg["commit"].(map[string]any)
			f.files[path(commit)] = s.Bytes()
		}
		f.jsonOut(w, 200, map[string]any{})
	case "/content/2/files/download":
		data, ok := f.files[path(arg)]
		if !ok {
			f.jsonOut(w, 409, map[string]any{"error_summary": "path/not_found/.."})
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			var a, b int
			fmt.Sscanf(rg, "bytes=%d-%d", &a, &b)
			if b >= len(data) {
				b = len(data) - 1
			}
			w.WriteHeader(206)
			w.Write(data[a : b+1])
			return
		}
		w.Write(data)
	case "/api/2/files/list_folder", "/api/2/files/list_folder/continue":
		folder := path(arg)
		start := 0
		if c, ok := arg["cursor"].(string); ok {
			parts := strings.SplitN(c, "|", 2)
			fmt.Sscanf(parts[0], "c%d", &start)
			folder = parts[1]
		}
		var names []string
		for p := range f.files {
			if i := strings.LastIndex(p, "/"); p[:i] == folder {
				names = append(names, p[i+1:])
			}
		}
		if len(names) == 0 && r.URL.Path == "/api/2/files/list_folder" {
			f.jsonOut(w, 409, map[string]any{"error_summary": "path/not_found/.."})
			return
		}
		sortStrings(names)
		end := start + 1 // по одному файлу на страницу, чтобы проверить продолжение списка
		entries := []map[string]any{}
		for _, n := range names[start:min(end, len(names))] {
			entries = append(entries, map[string]any{".tag": "file", "name": n})
		}
		if start == 0 {
			entries = append(entries, map[string]any{".tag": "folder", "name": "sub"})
		}
		f.jsonOut(w, 200, map[string]any{"entries": entries, "cursor": fmt.Sprintf("c%d|%s", end, folder), "has_more": end < len(names)})
	case "/api/2/files/delete_v2":
		if _, ok := f.files[path(arg)]; !ok {
			f.jsonOut(w, 409, map[string]any{"error_summary": "path_lookup/not_found/.."})
			return
		}
		delete(f.files, path(arg))
		f.jsonOut(w, 200, map[string]any{})
	default:
		f.jsonOut(w, 404, map[string]any{"error_summary": "no_endpoint"})
	}
}

func sortStrings(s []string) { sort.Strings(s) }

func TestDropboxStoreConformance(t *testing.T) {
	f := newFakeDropbox(t)
	cl, err := newDropboxStore(BackupDropbox{AppKey: "appkey123", RefreshToken: "REFRESH-1", AccountID: "dbid:ACC1", Dir: "itrupanel/main"})
	if err != nil {
		t.Fatal(err)
	}
	cloudConformance(t, cl)
	if f.tokens != 1 {
		t.Fatalf("токен доступа запрашивался %d раз: он должен кэшироваться", f.tokens)
	}
	// Большой файл ушёл сессией по частям, а не одним запросом.
	if len(f.sessions) == 0 {
		t.Fatal("загрузка частями не использовалась")
	}
}

func TestDropboxTokenExpiryAndRevoked(t *testing.T) {
	f := newFakeDropbox(t)
	cl, _ := newDropboxStore(BackupDropbox{AppKey: "appkey123", RefreshToken: "REFRESH-1", Dir: "x"})
	if err := cl.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Сервер «забыл» токен: первый запрос получает 401, токен сбрасывается, повторная попытка обновляет его.
	f.tokens++
	if err := cl.Test(context.Background()); err == nil {
		t.Fatal("устаревший токен принят")
	}
	if err := cl.Test(context.Background()); err != nil {
		t.Fatalf("после обновления токена: %v", err)
	}
	// Загрузка сама переживает устаревший токен благодаря повторным попыткам.
	f.tokens++
	src := filepathJoinTemp(t, []byte("data"))
	if _, err := cl.Upload(context.Background(), src, "a.tar.gz"); err != nil {
		t.Fatalf("загрузка с устаревшим токеном: %v", err)
	}
	// Отозванный доступ: понятное сообщение.
	bad, _ := newDropboxStore(BackupDropbox{AppKey: "appkey123", RefreshToken: "OLD", Dir: "x"})
	err := bad.Test(context.Background())
	if err == nil || !strings.Contains(err.Error(), "заново") {
		t.Fatalf("отозванный доступ: %v", err)
	}
	for _, c := range []BackupDropbox{{AppKey: "", RefreshToken: "x"}, {AppKey: "appkey123"}, {AppKey: "appkey123", RefreshToken: "x", Dir: "../x"}} {
		if _, err := dropboxNormalize(c); err == nil {
			t.Fatalf("принято: %+v", c)
		}
	}
}

func TestDropboxOAuthAPIFlow(t *testing.T) {
	mux := testPanel(t)
	f := newFakeDropbox(t)
	_, r := testAPI(t, mux, "backups.cloud.oauth.start", `{"kind":"dropbox","client_id":"appkey123"}`)
	d, _ := r["data"].(map[string]any)
	link, _ := d["url"].(string)
	if d == nil || !strings.Contains(link, "client_id=appkey123") || !strings.Contains(link, "code_challenge_method=S256") || !strings.Contains(link, "token_access_type=offline") {
		t.Fatalf("ссылка: %v", r)
	}
	state, _ := d["state"].(string)
	if _, r := testAPI(t, mux, "backups.cloud.oauth.finish", fmt.Sprintf(`{"kind":"dropbox","state":%q,"code":"WRONG"}`, state)); r["error"] == nil {
		t.Fatalf("неверный код принят: %v", r)
	}
	_, r = testAPI(t, mux, "backups.cloud.oauth.finish", fmt.Sprintf(`{"kind":"dropbox","state":%q,"code":"CODE123"}`, state))
	if r["ok"] != true || store.Settings.BackupDropbox.RefreshToken != "REFRESH-1" || store.Settings.BackupDropbox.Account != "me@example.test" || store.Settings.BackupDropbox.Dir != "itrupanel" {
		t.Fatalf("подключение: %v %+v", r, store.Settings.BackupDropbox)
	}
	if strings.Contains(fmt.Sprint(r), "REFRESH-1") {
		t.Fatal("токен попал в ответ браузеру")
	}
	// Настройки: токен скрыт в ответе и не принимается из браузера.
	_, g := testAPI(t, mux, "backups.ftp.get", `{}`)
	dbx := g["data"].(map[string]any)["dropbox"].(map[string]any)
	if strings.Contains(fmt.Sprint(dbx), "REFRESH-1") || dbx["account"] != "me@example.test" {
		t.Fatalf("настройки Dropbox в ответе: %v", dbx)
	}
	_, r = testAPI(t, mux, "backups.ftp.save", `{"target":"local_dropbox","dropbox":{"app_key":"appkey123","refresh_token":"HACK","dir":"itrupanel/main"},"schedule":{"keep":5,"hour":2}}`)
	if r["ok"] != true || store.Settings.BackupTarget != "local_dropbox" || store.Settings.BackupDropbox.RefreshToken != "REFRESH-1" || store.Settings.BackupDropbox.Dir != "itrupanel/main" {
		t.Fatalf("сохранение: %v %+v", r, store.Settings.BackupDropbox)
	}
	if _, ok := f.files["/itrupanel/main"]; ok {
		t.Fatal("лишний файл")
	}
	p := capturePanelBackupSettings(store.Settings)
	if p.validate() != nil || p.BackupDropbox.AppKey != "appkey123" {
		t.Fatalf("настройки панели в копии: %v", p.validate())
	}
}

// ---------- Google Drive (поддельный сервер по описанию API v3) ----------

type fakeDriveFile struct {
	id, name, parent string
	folder           bool
	data             []byte
}

type fakeGDrive struct {
	t        *testing.T
	files    map[string]*fakeDriveFile
	uploads  map[string]*fakeDriveFile // сессия загрузки → файл
	got      map[string]int64          // сессия → принято байт
	size     map[string]int64
	next     int
	tokens   int
	pending  int // сколько раз ответить «ещё не разрешено» на опрос устройства
	srv      *httptest.Server
	checkTok bool
}

func newFakeGDrive(t *testing.T) *fakeGDrive {
	f := &fakeGDrive{t: t, files: map[string]*fakeDriveFile{}, uploads: map[string]*fakeDriveFile{}, got: map[string]int64{}, size: map[string]int64{}, pending: 1}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	oa, oapi, oup, oc := googleAuth, googleAPI, googleUpload, gdriveChunk
	googleAuth, googleAPI, googleUpload = f.srv.URL+"/auth", f.srv.URL+"/drive/v3", f.srv.URL+"/upload/drive/v3"
	gdriveChunk = 1 << 20
	t.Cleanup(func() { googleAuth, googleAPI, googleUpload, gdriveChunk = oa, oapi, oup, oc })
	gdriveTokensMu.Lock()
	gdriveTokens = map[string]gdriveToken{}
	gdriveTokensMu.Unlock()
	return f
}

func (f *fakeGDrive) out(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (f *fakeGDrive) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/token":
		r.ParseForm()
		if r.Form.Get("client_id") != "cid.apps.test" || r.Form.Get("client_secret") != "csecret" {
			f.out(w, 401, map[string]any{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "refresh_token":
			if r.Form.Get("refresh_token") != "GREFRESH" {
				f.out(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			f.tokens++
			f.out(w, 200, map[string]any{"access_token": fmt.Sprintf("GACCESS-%d", f.tokens), "expires_in": 3599})
		case "urn:ietf:params:oauth:grant-type:device_code":
			if r.Form.Get("device_code") != "DEVCODE" {
				f.out(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			if f.pending > 0 {
				f.pending--
				f.out(w, 428, map[string]any{"error": "authorization_pending"})
				return
			}
			f.tokens++
			f.out(w, 200, map[string]any{"access_token": fmt.Sprintf("GACCESS-%d", f.tokens), "refresh_token": "GREFRESH", "expires_in": 3599})
		}
		return
	case "/auth/device/code":
		r.ParseForm()
		if r.Form.Get("client_id") != "cid.apps.test" || r.Form.Get("scope") != googleScope {
			f.out(w, 400, map[string]any{"error": "invalid_client"})
			return
		}
		f.out(w, 200, map[string]any{"device_code": "DEVCODE", "user_code": "ABCD-EFGH", "verification_url": "https://www.google.com/device", "expires_in": 1800, "interval": 5})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer GACCESS-") || r.Header.Get("Authorization") != fmt.Sprintf("Bearer GACCESS-%d", f.tokens) {
		f.out(w, 401, map[string]any{"error": map[string]any{"message": "Invalid Credentials"}})
		return
	}
	switch {
	case r.URL.Path == "/drive/v3/about":
		f.out(w, 200, map[string]any{"user": map[string]any{"emailAddress": "me@gmail.test", "permissionId": "p1"}})
	case r.URL.Path == "/drive/v3/files" && r.Method == "GET":
		q := r.URL.Query().Get("q")
		name := regexpFind(q, `name='([^']*)'`)
		parent := regexpFind(q, `'([^']+)' in parents`)
		wantFolder := strings.Contains(q, "mimeType='"+gdriveFolderMIM+"'")
		notFolder := strings.Contains(q, "mimeType!=")
		var ids []string
		for id, fl := range f.files {
			if fl.parent == parent && (name == "" || fl.name == name) && (!wantFolder || fl.folder) && (!notFolder || !fl.folder) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		start := 0
		fmt.Sscanf(r.URL.Query().Get("pageToken"), "t%d", &start)
		res := map[string]any{}
		list := []map[string]any{}
		for _, id := range ids[min(start, len(ids)):min(start+1, len(ids))] {
			list = append(list, map[string]any{"id": id, "name": f.files[id].name})
		}
		res["files"] = list
		if start+1 < len(ids) {
			res["nextPageToken"] = fmt.Sprintf("t%d", start+1)
		}
		f.out(w, 200, res)
	case r.URL.Path == "/drive/v3/files" && r.Method == "POST":
		var in struct {
			Name     string   `json:"name"`
			MimeType string   `json:"mimeType"`
			Parents  []string `json:"parents"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.next++
		id := fmt.Sprintf("ID%d", f.next)
		f.files[id] = &fakeDriveFile{id: id, name: in.Name, parent: in.Parents[0], folder: in.MimeType == gdriveFolderMIM}
		f.out(w, 200, map[string]any{"id": id})
	case r.URL.Path == "/upload/drive/v3/files" && r.Method == "POST":
		var in struct {
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.next++
		id := fmt.Sprintf("ID%d", f.next)
		sess := fmt.Sprintf("sess%d", f.next)
		f.uploads[sess] = &fakeDriveFile{id: id, name: in.Name, parent: in.Parents[0]}
		fmt.Sscan(r.Header.Get("X-Upload-Content-Length"), new(int64))
		var total int64
		fmt.Sscan(r.Header.Get("X-Upload-Content-Length"), &total)
		f.size[sess] = total
		w.Header().Set("Location", f.srv.URL+"/upload/session/"+sess)
		w.WriteHeader(200)
	case strings.HasPrefix(r.URL.Path, "/upload/session/") && r.Method == "PUT":
		sess := strings.TrimPrefix(r.URL.Path, "/upload/session/")
		up := f.uploads[sess]
		body, _ := io.ReadAll(r.Body)
		var a, b, total int64
		fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &a, &b, &total)
		if up == nil || a != int64(len(up.data)) || int64(len(body)) != b-a+1 {
			f.out(w, 400, map[string]any{"error": map[string]any{"message": "bad range"}})
			return
		}
		up.data = append(up.data, body...)
		if int64(len(up.data)) < total {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(up.data)-1))
			w.WriteHeader(308)
			return
		}
		f.files[up.id] = up
		f.out(w, 200, map[string]any{"id": up.id})
	case strings.HasPrefix(r.URL.Path, "/drive/v3/files/") && r.Method == "GET":
		fl := f.files[strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")]
		if fl == nil {
			f.out(w, 404, map[string]any{"error": map[string]any{"message": "File not found"}})
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			var a, b int
			fmt.Sscanf(rg, "bytes=%d-%d", &a, &b)
			if b >= len(fl.data) {
				b = len(fl.data) - 1
			}
			w.WriteHeader(206)
			w.Write(fl.data[a : b+1])
			return
		}
		w.Write(fl.data)
	case strings.HasPrefix(r.URL.Path, "/drive/v3/files/") && r.Method == "DELETE":
		id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
		if f.files[id] == nil {
			f.out(w, 404, map[string]any{"error": map[string]any{"message": "File not found"}})
			return
		}
		delete(f.files, id)
		w.WriteHeader(204)
	default:
		f.out(w, 404, map[string]any{"error": map[string]any{"message": "no route"}})
	}
}

func regexpFind(s, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func TestGDriveStoreConformance(t *testing.T) {
	f := newFakeGDrive(t)
	cl, err := newGDriveStore(BackupGDrive{ClientID: "cid.apps.test", ClientSecret: "csecret", RefreshToken: "GREFRESH", Account: "me@gmail.test", Dir: "itrupanel/main"})
	if err != nil {
		t.Fatal(err)
	}
	cloudConformance(t, cl)
	if f.tokens != 1 {
		t.Fatalf("токен доступа запрашивался %d раз", f.tokens)
	}
	// Вложенные папки созданы один раз, а не при каждой загрузке.
	folders := 0
	for _, fl := range f.files {
		if fl.folder {
			folders++
		}
	}
	if folders != 2 {
		t.Fatalf("папок в хранилище: %d", folders)
	}
}

func TestGDriveRevokedAndNormalize(t *testing.T) {
	newFakeGDrive(t)
	bad, _ := newGDriveStore(BackupGDrive{ClientID: "cid.apps.test", ClientSecret: "csecret", RefreshToken: "OLD", Dir: "x"})
	err := bad.Test(context.Background())
	if err == nil || !strings.Contains(err.Error(), "заново") {
		t.Fatalf("отозванный доступ: %v", err)
	}
	for _, c := range []BackupGDrive{{ClientSecret: "s", RefreshToken: "r"}, {ClientID: "i", RefreshToken: "r"}, {ClientID: "i", ClientSecret: "s"}, {ClientID: "i", ClientSecret: "s", RefreshToken: "r", Dir: "../x"}} {
		if _, err := gdriveNormalize(c); err == nil {
			t.Fatalf("принято: %+v", c)
		}
	}
	// Источник не меняется при повторной авторизации того же аккаунта (новый refresh-токен).
	a, _ := newGDriveStore(BackupGDrive{ClientID: "i", ClientSecret: "s", RefreshToken: "r1", Account: "Me@gmail.test", Dir: "a"})
	b, _ := newGDriveStore(BackupGDrive{ClientID: "i2", ClientSecret: "s", RefreshToken: "r2", Account: "me@gmail.test", Dir: "a"})
	if a.Source() != b.Source() || cloudKindOfSource(a.Source()) != cloudGDrive {
		t.Fatal("идентификатор хранилища зависит от токена")
	}
}

func TestGDriveOAuthDeviceFlowAndSettings(t *testing.T) {
	mux := testPanel(t)
	f := newFakeGDrive(t)
	_, r := testAPI(t, mux, "backups.cloud.oauth.start", `{"kind":"gdrive","client_id":"cid.apps.test","client_secret":"csecret"}`)
	d, _ := r["data"].(map[string]any)
	if d == nil || d["user_code"] != "ABCD-EFGH" || !strings.Contains(fmt.Sprint(d["url"]), "google.com/device") {
		t.Fatalf("код устройства: %v", r)
	}
	state, _ := d["state"].(string)
	fin := fmt.Sprintf(`{"kind":"gdrive","state":%q}`, state)
	// Пока пользователь не ввёл код, ответ «ждём».
	if _, r := testAPI(t, mux, "backups.cloud.oauth.finish", fin); r["ok"] != true || r["data"].(map[string]any)["pending"] != true {
		t.Fatalf("ожидание: %v", r)
	}
	_, r = testAPI(t, mux, "backups.cloud.oauth.finish", fin)
	if r["ok"] != true || store.Settings.BackupGDrive.RefreshToken != "GREFRESH" || store.Settings.BackupGDrive.Account != "me@gmail.test" || store.Settings.BackupGDrive.Dir != "itrupanel" {
		t.Fatalf("подключение: %v %+v", r, store.Settings.BackupGDrive)
	}
	if strings.Contains(fmt.Sprint(r), "GREFRESH") {
		t.Fatal("токен попал в ответ браузеру")
	}
	if _, r := testAPI(t, mux, "backups.cloud.oauth.start", `{"kind":"gdrive","client_id":"wrong","client_secret":"x"}`); r["error"] == nil {
		t.Fatalf("неверный клиент принят: %v", r)
	}
	// Секрет клиента скрыт в ответе; при сохранении с маской остаётся прежним; токен из браузера не принимается.
	_, g := testAPI(t, mux, "backups.ftp.get", `{}`)
	gd := g["data"].(map[string]any)["gdrive"].(map[string]any)
	if strings.Contains(fmt.Sprint(gd), "csecret") || strings.Contains(fmt.Sprint(gd), "GREFRESH") {
		t.Fatalf("секреты в ответе: %v", gd)
	}
	store.Settings.BackupGDrive.ClientSecret = "csecret"
	_, r = testAPI(t, mux, "backups.ftp.save", `{"target":"gdrive","gdrive":{"client_id":"cid.apps.test","client_secret":"••••••••••••","refresh_token":"HACK","dir":"itrupanel/main"},"schedule":{"keep":5,"hour":2}}`)
	if r["ok"] != true || store.Settings.BackupTarget != "gdrive" || store.Settings.BackupGDrive.RefreshToken != "GREFRESH" || store.Settings.BackupGDrive.ClientSecret != "csecret" || store.Settings.BackupGDrive.Dir != "itrupanel/main" {
		t.Fatalf("сохранение: %v %+v", r, store.Settings.BackupGDrive)
	}
	_ = f
	// Целый цикл через общий слой: загрузка, скачивание, удаление, поиск.
	store.Sites = []*Site{{Domain: "example.test"}}
	os.MkdirAll(P(backupDir), 0700)
	name := "example.test_20261019-120000-abcdef12.tar.gz"
	b := &Backup{ID: "20261019-120000-abcdef12", File: backupDir + "/" + name, Site: "example.test", Kind: "files", Local: true}
	os.WriteFile(P(b.File), []byte("archive-bytes"), 0600)
	if err := uploadBackup(b); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.Remote, "ID") || cloudKindOfSource(b.RemoteSource) != cloudGDrive || b.Local || fileExists(b.File) {
		t.Fatalf("после загрузки: %+v", b)
	}
	path, tmp, err := fetchBackup(b)
	if err != nil || !tmp {
		t.Fatalf("скачивание: %v", err)
	}
	if data, _ := os.ReadFile(P(path)); string(data) != "archive-bytes" {
		t.Fatalf("скачано: %q", data)
	}
	deleteRemote(b)
	cl, _ := newCloudStore(cloudGDrive)
	if objs, _ := cl.List(context.Background()); len(objs) != 0 {
		t.Fatalf("файл остался: %v", objs)
	}
	m := discoveryMeta()
	archive := discoveryArchive(t, m)
	cl.Upload(context.Background(), filepathJoinTemp(t, archive), "example.test_20261013-120000-aabb.tar.gz")
	store.Backups = nil
	res, err := scanBackupCloud(&Job{ctx: context.Background()}, cloudGDrive)
	if err != nil || res.(map[string]any)["added"] != 1 || len(store.Backups) != 1 || cloudKindOfSource(store.Backups[0].RemoteSource) != cloudGDrive {
		t.Fatalf("поиск: %v %v", res, err)
	}
}

func TestBackupWebDAVSettingsAPI(t *testing.T) {
	mux := testPanel(t)
	srv, _ := newTestDAV(t)
	body := func(pass string, test bool, target string) string {
		return fmt.Sprintf(`{"target":%q,"test":%t,"webdav":{"provider":"custom","url":%q,"user":"user@example.test","pass":%q,"dir":"itrupanel/main"},"schedule":{"keep":5,"hour":2}}`, target, test, srv.URL, pass)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("wrong", true, "webdav")); r["error"] == nil || !strings.Contains(fmt.Sprint(r["error"]), "облак") {
		t.Fatalf("неверный пароль принят: %v", r)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("app-password", true, "webdav")); r["ok"] != true || store.Settings.BackupTarget == "webdav" {
		t.Fatalf("проверка не должна сохранять: %v", r)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("app-password", false, "local_webdav")); r["ok"] != true || store.Settings.BackupTarget != "local_webdav" || store.Settings.BackupDAV.Pass != "app-password" {
		t.Fatalf("сохранение: %v %+v", r, store.Settings.BackupDAV)
	}
	_, g := testAPI(t, mux, "backups.ftp.get", `{}`)
	dav := g["data"].(map[string]any)["webdav"].(map[string]any)
	if strings.Contains(fmt.Sprint(dav), "app-password") || dav["dir"] != "itrupanel/main" {
		t.Fatalf("пароль в ответе: %v", dav)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("••••••••••••", false, "local_webdav")); r["ok"] != true || store.Settings.BackupDAV.Pass != "app-password" {
		t.Fatalf("маска затёрла пароль: %v", r)
	}
	if _, r := testAPI(t, mux, "backups.ftp.save", body("x", false, "nowhere")); r["error"] == nil {
		t.Fatal("неизвестное место хранения принято")
	}
	p := capturePanelBackupSettings(store.Settings)
	if p.BackupDAV.User != "user@example.test" || p.validate() != nil {
		t.Fatalf("настройки панели в копии: %v", p.validate())
	}
	p.BackupDAV.Pass = ""
	if p.validate() == nil {
		t.Fatal("пустой пароль в копии принят")
	}
}

// Серверная ошибка 500 (как у Яндекс.Диска «500 Server Error») — временная: загрузка повторяется,
// а в сообщении видна причина из тела ответа.
func TestWebDAVServerErrorRetriedAndExplained(t *testing.T) {
	old := cloudRetryDelay
	cloudRetryDelay = func(int) time.Duration { return time.Millisecond }
	defer func() { cloudRetryDelay = old }()
	puts := 0
	var seenExpect, seenCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(405)
		case "PUT":
			puts++
			seenExpect, seenCT = r.Header.Get("Expect"), r.Header.Get("Content-Type")
			io.Copy(io.Discard, r.Body)
			if puts <= 2 {
				w.WriteHeader(500)
				io.WriteString(w, "<error><message>Temporary upload failure</message></error>")
				return
			}
			w.WriteHeader(201)
		case "PROPFIND":
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cl, _ := newDAVStore(BackupWebDAV{URL: srv.URL, User: "u", Pass: "p", Dir: "x"})
	f := filepathJoinTemp(t, bytes.Repeat([]byte("z"), 2<<20))
	if _, err := cl.Upload(context.Background(), f, "a.tar.gz"); err != nil || puts != 3 {
		t.Fatalf("после двух 500 загрузка должна пройти: %v, запросов %d", err, puts)
	}
	if seenCT != "" {
		t.Fatalf("Content-Type не должен отправляться: %q", seenCT)
	}
	_ = seenExpect
	// Постоянная ошибка 500: сообщение содержит причину.
	puts = 100
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			w.WriteHeader(405)
			return
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(500)
		io.WriteString(w, "<error><message>Disk is broken</message></error>")
	})
	_, err := cl.Upload(context.Background(), f, "b.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "Disk is broken") {
		t.Fatalf("причина ошибки не показана: %v", err)
	}
}

// Проверка подключения повторяет шаги копии и при сбое показывает, на каком именно шаге ответил сервер.
func TestWebDAVTestReportsFailingStep(t *testing.T) {
	old := cloudRetryDelay
	cloudRetryDelay = func(int) time.Duration { return time.Millisecond }
	defer func() { cloudRetryDelay = old }()
	h := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && r.ContentLength > 1<<20 {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(500)
			io.WriteString(w, "<error>Upload node is down</error>")
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cl, _ := newDAVStore(BackupWebDAV{URL: srv.URL, User: "u", Pass: "p", Dir: "a/b"})
	err := cl.Test(context.Background())
	if err == nil {
		t.Fatal("сбой записи крупного файла не обнаружен")
	}
	for _, want := range []string{"запись 2 МБ", "Ход проверки", "вход: ok", "папка: ok", "запись 64 Б: ok", "Upload node is down"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отчёте нет %q: %v", want, err)
		}
	}
	// После проверки временный файл удалён.
	if objs, _ := cl.List(context.Background()); len(objs) != 0 {
		t.Fatalf("остался файл проверки: %v", objs)
	}
}

// Если папки нет, сначала пробуем писать, а потом создаём папку и повторяем запись; MKCOL на существующую папку не нужен.
func TestWebDAVUploadCreatesMissingFolderOnlyWhenNeeded(t *testing.T) {
	h := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	var mkcols, puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			mkcols++
		case "PUT":
			puts++
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cl, _ := newDAVStore(BackupWebDAV{URL: srv.URL, User: "u", Pass: "p", Dir: "one/two"})
	f := filepathJoinTemp(t, []byte("data"))
	if _, err := cl.Upload(context.Background(), f, "a.tar.gz"); err != nil || mkcols != 2 || puts != 2 {
		t.Fatalf("первая загрузка: %v, MKCOL %d, PUT %d", err, mkcols, puts)
	}
	mkcols, puts = 0, 0
	if _, err := cl.Upload(context.Background(), f, "b.tar.gz"); err != nil || mkcols != 0 || puts != 1 {
		t.Fatalf("вторая загрузка не должна трогать папки: %v, MKCOL %d, PUT %d", err, mkcols, puts)
	}
}
