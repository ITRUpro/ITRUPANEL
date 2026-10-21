// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTOTPRFC6238Vectors(t *testing.T) {
	// Секрет «12345678901234567890» из RFC 6238 (SHA-1); у нас шесть цифр вместо восьми.
	secret := totpEncoding.EncodeToString([]byte("12345678901234567890"))
	for _, c := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		got, err := totpAt(secret, c.unix/30)
		if err != nil || got != c.want {
			t.Fatalf("t=%d: %q %v, ожидалось %s", c.unix, got, err, c.want)
		}
	}
	now := time.Unix(1111111109, 0)
	if _, ok := totpVerify(secret, "081804", now); !ok {
		t.Fatal("верный код отвергнут")
	}
	if _, ok := totpVerify(secret, "081 804", now); !ok {
		t.Fatal("код с пробелом отвергнут")
	}
	// Допуск ±1 интервал, но не больше.
	if _, ok := totpVerify(secret, "081804", now.Add(30*time.Second)); !ok {
		t.Fatal("код прошлого интервала отвергнут")
	}
	if _, ok := totpVerify(secret, "081804", now.Add(90*time.Second)); ok {
		t.Fatal("устаревший код принят")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok := totpVerify(secret, bad, now); ok && bad != "000000" {
			t.Fatalf("принят %q", bad)
		}
	}
	if u := totpURI("admin", secret); !strings.HasPrefix(u, "otpauth://totp/ITRUPANEL:admin?") || !strings.Contains(u, "secret="+secret) || !strings.Contains(u, "issuer=ITRUPANEL") {
		t.Fatal(u)
	}
}

func TestAllowIPsParsingAndMatching(t *testing.T) {
	list, err := parseAllowIPs("203.0.113.5\n198.51.100.0/24, 2001:db8::/32\n::ffff:192.0.2.9 203.0.113.5")
	if err != nil || len(list) != 4 {
		t.Fatalf("%v %v", list, err)
	}
	for ip, want := range map[string]bool{"203.0.113.5": true, "203.0.113.6": false, "198.51.100.77": true, "198.51.101.1": false, "2001:db8::1": true, "2001:db9::1": false, "192.0.2.9": true, "127.0.0.1": false, "bad": false} {
		if got := ipAllowed(list, ip); got != want {
			t.Fatalf("%s: %v", ip, got)
		}
	}
	if !ipAllowed(nil, "1.2.3.4") {
		t.Fatal("пустой список должен пускать всех")
	}
	for _, bad := range []string{"1.2.3", "1.2.3.4/33", "foo", "10.0.0.0/8/8"} {
		if _, err := parseAllowIPs(bad); err == nil {
			t.Fatalf("принято %q", bad)
		}
	}
	if l, err := parseAllowIPs("  \n "); err != nil || len(l) != 0 {
		t.Fatalf("пустой ввод: %v %v", l, err)
	}
}

func doLogin(mux *http.ServeMux, body string) (*httptest.ResponseRecorder, map[string]any) {
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	r.RemoteAddr = "203.0.113.50:4000"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var res map[string]any
	json.Unmarshal(w.Body.Bytes(), &res)
	return w, res
}

func TestTwoFactorEnableLoginAndRecovery(t *testing.T) {
	mux := testPanel(t)
	setAdminPassword("TestPassword123")
	_, r := testAPI(t, mux, "security.2fa.begin", `{}`)
	d := r["data"].(map[string]any)
	secret := d["secret"].(string)
	if !strings.Contains(d["uri"].(string), secret) {
		t.Fatal("ссылка для приложения без секрета")
	}
	if _, r := testAPI(t, mux, "security.2fa.enable", `{"code":"000000"}`); r["error"] == nil || store.Settings.TOTPEnabled {
		t.Fatalf("неверный код включил защиту: %v", r)
	}
	now := time.Now()
	code, _ := totpAt(secret, now.Unix()/30)
	_, r = testAPI(t, mux, "security.2fa.enable", fmt.Sprintf(`{"code":%q}`, code))
	rec := r["data"].(map[string]any)["recovery"].([]any)
	if r["ok"] != true || !store.Settings.TOTPEnabled || len(rec) != 10 || len(store.Settings.TOTPRecovery) != 10 {
		t.Fatalf("включение: %v", r)
	}
	for _, x := range store.Settings.TOTPRecovery {
		if strings.Contains(x, strings.ReplaceAll(rec[0].(string), "-", "")) {
			t.Fatal("резервные коды хранятся открытым текстом")
		}
	}
	// Вход: без кода — «нужен код», сессии нет; с неверным — отказ; с верным — вход.
	w, res := doLogin(mux, `{"user":"admin","password":"TestPassword123"}`)
	if res["need_code"] != true || len(w.Result().Cookies()) != 0 {
		t.Fatalf("без кода: %v", res)
	}
	if _, res := doLogin(mux, `{"user":"admin","password":"wrong"}`); res["need_code"] != nil || res["error"] == nil {
		t.Fatalf("неверный пароль не должен выдавать, что включена вторая ступень: %v", res)
	}
	if w, res := doLogin(mux, `{"user":"admin","password":"TestPassword123","code":"123456"}`); res["error"] != "Неверный код подтверждения" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("неверный код: %v", res)
	}
	// Код текущего интервала мог быть использован при включении: берём следующий допустимый.
	next, _ := totpAt(secret, now.Unix()/30+1)
	if w, res := doLogin(mux, fmt.Sprintf(`{"user":"admin","password":"TestPassword123","code":%q}`, next)); res["ok"] != true || len(w.Result().Cookies()) != 1 {
		t.Fatalf("верный код: %v", res)
	}
	// Один и тот же код дважды не принимается.
	if _, res := doLogin(mux, fmt.Sprintf(`{"user":"admin","password":"TestPassword123","code":%q}`, next)); res["error"] == nil {
		t.Fatal("код принят повторно")
	}
	// Резервный код работает один раз.
	first := rec[0].(string)
	if _, res := doLogin(mux, fmt.Sprintf(`{"user":"admin","password":"TestPassword123","code":%q}`, strings.ToUpper(first))); res["ok"] != true {
		t.Fatalf("резервный код: %v", res)
	}
	if _, res := doLogin(mux, fmt.Sprintf(`{"user":"admin","password":"TestPassword123","code":%q}`, first)); res["error"] == nil || len(store.Settings.TOTPRecovery) != 9 {
		t.Fatalf("резервный код принят повторно: %v", res)
	}
	// Отключение требует пароль и код.
	other := rec[1].(string)
	if _, r := testAPI(t, mux, "security.2fa.disable", fmt.Sprintf(`{"password":"bad","code":%q}`, other)); r["error"] == nil || !store.Settings.TOTPEnabled {
		t.Fatalf("отключение с неверным паролем: %v", r)
	}
	if _, r := testAPI(t, mux, "security.2fa.disable", fmt.Sprintf(`{"password":"TestPassword123","code":%q}`, other)); r["ok"] != true || store.Settings.TOTPEnabled || store.Settings.TOTPSecret != "" {
		t.Fatalf("отключение: %v", r)
	}
	if _, res := doLogin(mux, `{"user":"admin","password":"TestPassword123"}`); res["ok"] != true {
		t.Fatalf("вход после отключения: %v", res)
	}
}

func TestTwoFactorCodeGuessingIsLimited(t *testing.T) {
	mux := testPanel(t)
	setAdminPassword("TestPassword123")
	store.Settings.TOTPEnabled, store.Settings.TOTPSecret = true, newTOTPSecret()
	for i := 0; i < loginLimit; i++ {
		if _, res := doLogin(mux, `{"user":"admin","password":"TestPassword123","code":"111111"}`); res["error"] == nil {
			t.Fatal("неверный код принят")
		}
		// Запрос «пароль без кода» между попытками не обнуляет счётчик ошибок.
		doLogin(mux, `{"user":"admin","password":"TestPassword123"}`)
	}
	w, _ := doLogin(mux, `{"user":"admin","password":"TestPassword123","code":"111111"}`)
	if w.Code != 429 {
		t.Fatalf("перебор кодов не остановлен: %d %s", w.Code, w.Body.String())
	}
}

func TestIPAllowListGuardAndLockoutProtection(t *testing.T) {
	mux := testPanel(t)
	called := 0
	h := panelIPGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++ }))
	hit := func(addr string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if hit("198.51.100.9:1") != 200 {
		t.Fatal("без ограничений доступ закрыт")
	}
	// Сохранить список без своего адреса нельзя: тест подключается с 192.0.2.1.
	testClient := func(body string) map[string]any {
		r := httptest.NewRequest(http.MethodPost, "/api/security.ips.save", strings.NewReader(body))
		r.Header.Set("X-Itrupanel", "1")
		r.RemoteAddr = "192.0.2.1:5555"
		r.AddCookie(&http.Cookie{Name: sessCookie, Value: "test"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var res map[string]any
		json.Unmarshal(w.Body.Bytes(), &res)
		return res
	}
	if res := testClient(`{"list":"203.0.113.0/24"}`); res["error"] == nil || !strings.Contains(fmt.Sprint(res["error"]), "192.0.2.1") || len(store.Settings.AllowIPs) != 0 {
		t.Fatalf("защита от потери доступа: %v", res)
	}
	if res := testClient(`{"list":"203.0.113.0/24\n192.0.2.1"}`); res["ok"] != true || len(store.Settings.AllowIPs) != 2 {
		t.Fatalf("сохранение: %v", res)
	}
	if hit("203.0.113.77:1") != 200 || hit("192.0.2.1:1") != 200 {
		t.Fatal("разрешённый адрес не пущен")
	}
	if hit("198.51.100.9:1") != 403 {
		t.Fatal("чужой адрес пущен")
	}
	if hit("127.0.0.1:1") != 200 {
		t.Fatal("локальное подключение заблокировано")
	}
	cliAllowAllIPs()
	if hit("198.51.100.9:1") != 200 || len(store.Settings.AllowIPs) != 0 {
		t.Fatal("сброс из консоли не открыл доступ")
	}
	store.Settings.TOTPEnabled, store.Settings.TOTPSecret = true, newTOTPSecret()
	cliDisable2FA()
	if store.Settings.TOTPEnabled || store.Settings.TOTPSecret != "" {
		t.Fatal("сброс 2FA из консоли")
	}
}
