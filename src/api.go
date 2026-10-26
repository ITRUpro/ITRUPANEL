// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---------- сессии ----------

type session struct {
	Actor   string
	User    string
	IP      string
	Expires time.Time
}

var (
	sessions = map[string]*session{}
	sessMu   sync.Mutex
)

const sessCookie = "itrupanel_sid"

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if a, e := netip.ParseAddr(h); e == nil {
		// За nginx панели адрес посетителя приходит в X-Real-IP; заголовку верим только
		// от своего nginx (локальное подключение с секретом из его конфигурации).
		if a.IsLoopback() && fromPanelProxy(r) {
			if real, e := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); e == nil {
				return real.Unmap().String()
			}
		}
		return a.Unmap().String()
	}
	return h
}

func getSession(r *http.Request) *session {
	c, err := r.Cookie(sessCookie)
	if err != nil {
		return nil
	}
	sessMu.Lock()
	s := sessions[c.Value]
	if s == nil || time.Now().After(s.Expires) {
		delete(sessions, c.Value)
		sessMu.Unlock()
		return nil
	}
	s.Expires = time.Now().Add(8 * time.Hour)
	user := s.User
	sessMu.Unlock()
	if !isAdmin(user) {
		return nil
	}
	return s
}

func requireAuthPage(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := getSession(r)
		if s == nil || !termsAccepted(s.User) {
			http.Redirect(w, r, "/#/login", http.StatusFound)
			return
		}
		if !isAdmin(s.User) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// requirePMAPage — phpMyAdmin доступен только администратору; вход выполняется логином
// и паролем пользователя СУБД.
func requirePMAPage(h http.Handler) http.Handler { return requireAuthPage(h) }

// ---------- журналы ----------

type OpEntry struct {
	Actor  string    `json:"actor,omitempty"`
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	IP     string    `json:"ip"`
	Action string    `json:"action"`
	Object string    `json:"object"`
	Status string    `json:"status"` // ok | error | running
	Error  string    `json:"error,omitempty"`
	Ms     int64     `json:"ms"`
}

type VisitEntry struct {
	Time time.Time `json:"time"`
	User string    `json:"user"`
	IP   string    `json:"ip"`
	UA   string    `json:"ua"`
	OK   bool      `json:"ok"`
	Kind string    `json:"kind"` // login | logout | fail
}

var logMu sync.Mutex

func appendJSONL(name string, v any) {
	logMu.Lock()
	defer logMu.Unlock()
	p := filepath.Join(cfg.Home, "var", name)
	if st, err := os.Stat(p); err == nil && st.Size() > 20<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	f.Write(append(b, '\n'))
}

// ---------- API ----------

type Req struct {
	W    http.ResponseWriter
	R    *http.Request
	S    *session
	body []byte
}

func (q *Req) Bind(v any) error {
	if len(q.body) == 0 {
		return nil
	}
	if err := json.Unmarshal(q.body, v); err != nil {
		return fmt.Errorf("неверный запрос: %v", err)
	}
	return nil
}

type apiFunc func(q *Req) (any, error)

type apiDef struct {
	fn     apiFunc
	action string // если не пусто — запись в журнал операций
}

var apis = map[string]apiDef{}

// api регистрирует метод только для чтения.
func api(name string, fn apiFunc) { apis[name] = apiDef{fn: fn} }

// apiOp регистрирует изменяющий метод: он пишется в журнал операций.
func apiOp(name, action string, fn apiFunc) { apis[name] = apiDef{fn: fn, action: action} }

// userErr — ошибка, понятная пользователю.
type userErr string

func (e userErr) Error() string { return string(e) }

func errf(f string, a ...any) error { return userErr(fmt.Sprintf(f, a...)) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	if localized, ok := w.(*localizedWriter); ok {
		v = localizeResponse(v, localized.route)
		w.Header().Set("Content-Language", "en")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func registerAPI(mux *http.ServeMux) {
	registerAll()
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if !checkBrowserRequest(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if c, err := r.Cookie(sessCookie); err == nil {
			sessMu.Lock()
			if s := sessions[c.Value]; s != nil {
				appendJSONL("visits.jsonl", VisitEntry{time.Now(), s.User, clientIP(r), r.UserAgent(), true, "logout"})
			}
			delete(sessions, c.Value)
			sessMu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: sessCookie, Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/shell", withAuth(withUpdateGuard(handleShell)))
	mux.HandleFunc("/api/docker-shell", withAuth(withUpdateGuard(handleDockerShell)))
	mux.HandleFunc("/api/files/download", withAuth(handleFileDownload))
	mux.HandleFunc("/api/files/upload", withAuth(withUpdateGuard(withIdleBody(handleFileUpload))))
	mux.HandleFunc("/api/db/export", withAuth(handleDBExport))
	mux.HandleFunc("/api/transfer/tree", withAuth(handleTransferTree))
	mux.HandleFunc("/api/transfer/dump", withAuth(handleTransferDump))
	mux.HandleFunc("/api/transfer/mail", withAuth(handleTransferMail))
	mux.HandleFunc("/api/db/import-upload", withAuth(withUpdateGuard(withIdleBody(handleDBImportUpload))))
	mux.HandleFunc("/api/backups/download", withAuth(handleBackupDownload))
	mux.HandleFunc("/api/backups/extracted", withAuth(handleBackupExtracted))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if !checkBrowserRequest(w, r) {
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/")
		def, ok := apis[name]
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "нет метода " + name})
			return
		}
		s := getSession(r)
		if s == nil {
			writeJSON(w, 401, map[string]any{"error": "auth"})
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("X-Itrupanel") != "1" {
			writeJSON(w, 400, map[string]any{"error": "bad request"})
			return
		}
		if name != "me" && name != "terms.get" && name != "terms.accept" && !requireTerms(w, s.User) {
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 24<<20))
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		if readErr != nil {
			var tooLarge *http.MaxBytesError
			code := http.StatusBadRequest
			if errors.As(readErr, &tooLarge) {
				code = http.StatusRequestEntityTooLarge
			}
			writeJSON(w, code, map[string]any{"error": "Не удалось прочитать запрос: повреждён или превышает 24 МБ"})
			return
		}
		if def.action != "" && name != "updates.start" && name != "updates.apply" {
			updateGate.RLock()
			defer updateGate.RUnlock()
			if panelUpdating() {
				writeJSON(w, 503, map[string]any{"error": "Выполняется обновление панели. Дождитесь завершения."})
				return
			}
		}
		q := &Req{W: w, R: r, S: s, body: body}
		start := time.Now()
		var res any
		var err error
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("внутренняя ошибка: %v", p)
					log.Printf("panic in %s: %v", name, p)
				}
			}()
			res, err = def.fn(q)
		}()
		if def.action != "" {
			e := OpEntry{Actor: s.Actor, Time: start, User: s.User, IP: s.IP, Action: def.action, Object: objectOf(body), Status: "ok", Ms: time.Since(start).Milliseconds()}
			if err != nil {
				e.Status, e.Error = "error", err.Error()
			}
			appendJSONL("ops.jsonl", e)
		}
		if err != nil {
			var ue userErr
			msg := err.Error()
			if !errors.As(err, &ue) {
				log.Printf("api %s: %v", name, err)
			}
			writeJSON(w, 200, map[string]any{"error": msg})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "data": res})
	})
}

func withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkBrowserRequest(w, r) {
			return
		}
		s := getSession(r)
		if s == nil {
			http.Error(w, "auth", 401)
			return
		}
		if !requireTerms(w, s.User) {
			return
		}
		if !isAdmin(s.User) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// objectOf достаёт из запроса понятное имя объекта для журнала.
func objectOf(body []byte) string {
	var m map[string]any
	json.Unmarshal(body, &m)
	for _, k := range []string{"domain", "name", "path", "id", "version", "user"} {
		if v, ok := m[k]; ok {
			s := fmt.Sprint(v)
			if len(s) > 120 {
				s = s[:120]
			}
			return s
		}
	}
	return ""
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !checkBrowserRequest(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "POST"})
		return
	}
	ip := clientIP(r)
	var in struct {
		User, Password string
		Code           string `json:"code"`
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	if e := dec.Decode(&in); e != nil || len(in.User) > 256 || len(in.Password) > 1024 || len(in.Code) > 64 {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос входа"})
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос входа"})
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	wait, e := loginGuard.begin(ip, time.Now())
	if e != nil {
		log.Printf("login guard: %v", e)
		writeJSON(w, 503, map[string]any{"error": "Защита входа временно недоступна"})
		return
	}
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSON(w, 429, map[string]any{"error": "Слишком много попыток. Повторите вход позже."})
		return
	}
	mu.Lock()
	hash, admin := store.Settings.AdminHash, store.Settings.AdminUser
	validUser := in.User == admin
	mu.Unlock()
	// Compare for all usernames to avoid a cheap username oracle.
	passwordOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) == nil
	success := validUser && passwordOK
	badCode := false
	if success {
		mu.Lock()
		second := store.Settings.TOTPEnabled
		mu.Unlock()
		if second {
			if strings.TrimSpace(in.Code) == "" {
				// Пароль верный, нужен код: попытка не считается ошибкой, но и не сбрасывает прежние.
				if e := loginGuard.finishNeutral(ip); e != nil {
					writeJSON(w, 503, map[string]any{"error": "Защита входа временно недоступна"})
					return
				}
				writeJSON(w, 200, map[string]any{"need_code": true})
				return
			}
			if !secondFactorOK(in.Code) {
				success, badCode = false, true
			}
		}
	}
	if e := loginGuard.finish(ip, success, time.Now()); e != nil {
		log.Printf("login guard persistence: %v", e)
		writeJSON(w, 503, map[string]any{"error": "Защита входа временно недоступна"})
		return
	}
	if !success {
		appendJSONL("visits.jsonl", VisitEntry{time.Now(), in.User, ip, r.UserAgent(), false, "fail"})
		if badCode {
			writeJSON(w, 200, map[string]any{"error": "Неверный код подтверждения"})
			return
		}
		writeJSON(w, 200, map[string]any{"error": "Неверный логин или пароль"})
		return
	}
	sid := randToken(24)
	sessMu.Lock()
	pruneSessionsLocked(time.Now())
	// Limit successful-login session growth.
	trimUserSessionsLocked(in.User, 31)
	sessions[sid] = &session{User: in.User, IP: ip, Expires: time.Now().Add(8 * time.Hour)}
	sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessCookie, Value: sid, Path: "/", HttpOnly: true, Secure: !cfg.Insecure, SameSite: http.SameSiteStrictMode})
	appendJSONL("visits.jsonl", VisitEntry{time.Now(), in.User, ip, r.UserAgent(), true, "login"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func registerAll() {
	registerTerms()
	registerImportExport()
	registerFeedback()
	registerPanelUpdates()
	registerPanelAutoUpdate()
	registerLoginSecurity()
	api("me", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		unread := 0
		for _, n := range store.Notices {
			if !n.Read {
				unread++
			}
		}
		return map[string]any{"role": "admin", "actor": q.S.Actor, "terms_required": !termsAcceptedLocked(q.S.User), "user": q.S.User, "version": Version, "demo": cfg.Dry, "unread": unread, "os": osInfo.Pretty, "family": osInfo.Family}, nil
	})
	apiOp("settings.password", "Смена логина и пароля панели", func(q *Req) (any, error) {
		var in struct{ Old, New, Login string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		hash, cur := store.Settings.AdminHash, store.Settings.AdminUser
		mu.Unlock()
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Old)) != nil {
			return nil, errf("Текущий пароль неверен")
		}
		in.Login = strings.TrimSpace(in.Login)
		newLogin := in.Login != "" && in.Login != cur
		if in.New == "" && !newLogin {
			return nil, errf("Укажите новый логин или новый пароль")
		}
		if in.New != "" && len(in.New) < 8 {
			return nil, errf("Новый пароль — минимум 8 символов")
		}
		if newLogin {
			if err := validAdminLogin(in.Login); err != nil {
				return nil, err
			}
		}
		if in.New != "" {
			if err := setAdminPassword(in.New); err != nil {
				return nil, err
			}
		}
		if newLogin {
			if err := setAdminLogin(in.Login); err != nil {
				return nil, err
			}
		}
		return map[string]any{"login": in.Login, "info": "Данные для входа изменены"}, nil
	})
	registerPanelSettings()
	registerJobs()
	registerMail()
	registerSites()
	registerDB()
	registerDBServers()
	registerDocker()
	registerDBTransfer()
	registerDNS()
	registerSSL()
	registerFiles()
	registerBackups()
	registerDefaultSite()
	registerAntivirus()
	registerRecycle()
	registerImport()
	registerFTP()
	registerCron()
	registerLogs()
	registerStats()
	registerPHP()
	registerComponents()
	registerSystem()
	registerFirewall()
	registerSecurity()
	registerAuthGuard()
}

func isAdmin(name string) bool { mu.Lock(); defer mu.Unlock(); return name == store.Settings.AdminUser }

// strictBind разбирает JSON запроса и отклоняет неизвестные поля.
func strictBind(q *Req, v any) error {
	d := json.NewDecoder(strings.NewReader(string(q.body)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errf("Некорректный JSON-запрос")
	}
	return nil
}

func forbidden() error { return errf("Нет доступа к ресурсу") }
