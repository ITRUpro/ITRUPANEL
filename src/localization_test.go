// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLanguagePreference(t *testing.T) {
	for _, c := range []struct{ header, cookie, want string }{{"ru-RU, en;q=0.9", "", "ru"}, {"en-GB, ru;q=0.5", "", "en"}, {"de-DE", "", "en"}, {"ru-RU", "itrupanel_lang=en", "en"}, {"en-US", "itrupanel_lang=ru", "ru"}, {"ru", "itrupanel_lang=evil", "ru"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", c.header)
		r.Header.Set("Cookie", c.cookie)
		if got := requestLanguage(r); got != c.want {
			t.Fatalf("%+v: %s", c, got)
		}
	}
}
func TestEnglishErrorsPreserveParameters(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"Неверный логин или пароль", "Invalid username or password"}, {"Файл Почта уже существует; конфигурация не изменена", "File Почта already exists; configuration was not changed"}, {"Установка OpenLiteSpeed: Ящик не найден", "Installing OpenLiteSpeed: Mailbox not found"}, {"Диск заполнен на 95%", "Disk is 95% full"}, {"MX не указывает на mail.пример.рф", "MX does not point to mail.пример.рф"}, {"external command: original text", "external command: original text"}, {"Сайт пример.рф", "Site пример.рф"}, {"Сертификат пример.рф истекает 01.01.2027", "Certificate пример.рф expires on 01.01.2027"}} {
		if got := englishText(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}
func TestLocalizationDoesNotChangeUserData(t *testing.T) {
	input := map[string]any{"ok": true, "data": map[string]any{"name": "Резервные копии", "value": "Неверный путь", "text": "Ящик не найден", "content": "Почта", "path": "/Папка/Файл", "status": "running", "size": int64(9007199254740993)}}
	raw, _ := json.Marshal(input)
	got, _ := json.Marshal(localizeResponse(input, "files.list"))
	if string(got) != string(raw) {
		t.Fatalf("User data changed: %s", got)
	}
	orig, _ := json.Marshal(input)
	if string(orig) != string(raw) {
		t.Fatal("Mutated original")
	}
}
func TestLanguageAPIPresentation(t *testing.T) {
	v := map[string]any{"data": map[string]any{"stage": "Создание каталогов и настройка сайта", "title": "Настройка почты", "log": "Оригинальный журнал\nПочта", "error": "Сайт не найден", "status": "error"}}
	got := localizeResponse(v, "jobs.get").(map[string]any)["data"].(map[string]any)
	if got["stage"] != "Creating directories and configuring site" || got["error"] != "Site not found" || got["log"] != "Оригинальный журнал\nПочта" || got["status"] != "error" {
		t.Fatal(got)
	}
	n := localizeResponse(map[string]any{"release": map[string]any{"notes": []string{"Русский"}, "notes_en": []string{"English release note"}}}, "updates.check").(map[string]any)["release"].(map[string]any)
	if n["notes"].([]any)[0] != "English release note" {
		t.Fatal(n)
	}
}
func TestLanguageJSONMiddleware(t *testing.T) {
	h := languageMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, map[string]any{"error": "Неверный логин или пароль"})
	}))
	for _, lang := range []string{"ru", "en"} {
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.AddCookie(&http.Cookie{Name: "itrupanel_lang", Value: lang})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if (lang == "en") != strings.Contains(w.Body.String(), "Invalid username") {
			t.Fatal(w.Body.String())
		}
	}
}
func TestCatalogFormatPlaceholders(t *testing.T) {
	for ru, en := range englishCatalog {
		a, b := formatToken.FindAllString(ru, -1), formatToken.FindAllString(en, -1)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("Format mismatch: %q -> %q", ru, en)
		}
	}
}
