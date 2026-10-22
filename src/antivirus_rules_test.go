// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func signPack(t *testing.T, priv ed25519.PrivateKey, payload any) []byte {
	t.Helper()
	b, _ := json.Marshal(payload)
	env, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(b), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b))})
	return env
}

func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, hex.EncodeToString(pub)
}

func okPack(version string) map[string]any {
	return map[string]any{"schema": 1, "version": version, "released": "2026-10-22", "rules": []map[string]any{
		{"id": "pack-test-rule", "title": "Тестовая угроза", "severity": "high", "exts": []string{".php"}, "pattern": `EVIL_MARKER_\d+`},
	}}
}

// Опубликованный набор подписан ключом выпусков, правила срабатывают на своих образцах и молчат на обычном коде.
func TestCommittedRulePackIsSignedAndDetectsItsSamples(t *testing.T) {
	data, err := os.ReadFile("../antivirus/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := avParseRulePack(data, releasePublicKey)
	if err != nil {
		t.Fatalf("опубликованный набор не проходит проверку: %v", err)
	}
	var samples struct {
		Positive map[string][]struct{ Ext, Text string } `json:"positive"`
		Negative []struct{ Ext, Text string }            `json:"negative"`
	}
	raw, _ := os.ReadFile("testdata/antivirus/pack-samples.json")
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	old := avPack.Load()
	avPack.Store(p)
	defer avPack.Store(old)
	threat := map[string]string{}
	for _, r := range p.Rules {
		threat[r.id] = r.threat
		if len(samples.Positive[r.id]) == 0 {
			t.Errorf("у правила %s нет образца для проверки", r.id)
		}
	}
	for id, list := range samples.Positive {
		if threat[id] == "" {
			t.Errorf("образец для несуществующего правила %s", id)
			continue
		}
		for _, s := range list {
			found := false
			for _, f := range avScanContent("site/file"+s.Ext, []byte(s.Text)) {
				if f.Threat == threat[id] {
					found = true
				}
			}
			if !found {
				t.Errorf("правило %s не нашло образец %q", id, s.Text)
			}
		}
	}
	for _, s := range samples.Negative {
		for _, f := range avScanContent("site/file"+s.Ext, []byte(s.Text)) {
			for _, th := range threat {
				if f.Threat == th {
					t.Errorf("ложное срабатывание %q на безобидном коде %q", th, s.Text)
				}
			}
		}
	}
}

func TestRulePackRejectsTamperingAndBadRules(t *testing.T) {
	priv, pub := testKey(t)
	_, otherPub := testKey(t)
	good := signPack(t, priv, okPack("2026.10.22"))
	if _, err := avParseRulePack(good, pub); err != nil {
		t.Fatalf("исправный набор отклонён: %v", err)
	}
	if _, err := avParseRulePack(good, otherPub); err == nil {
		t.Fatal("набор с чужой подписью принят")
	}
	// Подмена содержимого при прежней подписи.
	var env map[string]string
	json.Unmarshal(good, &env)
	payload, _ := base64.StdEncoding.DecodeString(env["payload"])
	tampered := strings.Replace(string(payload), "Тестовая угроза", "Другая угроза", 1)
	env["payload"] = base64.StdEncoding.EncodeToString([]byte(tampered))
	bad, _ := json.Marshal(env)
	if _, err := avParseRulePack(bad, pub); err == nil {
		t.Fatal("набор с подменённым содержимым принят")
	}
	mut := func(f func(m map[string]any, r map[string]any)) []byte {
		m := okPack("2026.10.22")
		f(m, m["rules"].([]map[string]any)[0])
		return signPack(t, priv, m)
	}
	cases := map[string][]byte{
		"неверная версия формата": mut(func(m, r map[string]any) { m["schema"] = 2 }),
		"неверная версия набора":  mut(func(m, r map[string]any) { m["version"] = "latest" }),
		"неверная дата":           mut(func(m, r map[string]any) { m["released"] = "вчера" }),
		"пустой список":           mut(func(m, r map[string]any) { m["rules"] = []map[string]any{} }),
		"чужое поле":              mut(func(m, r map[string]any) { m["exec"] = "rm -rf /" }),
		"встроенный id":           mut(func(m, r map[string]any) { r["id"] = "eval-decode" }),
		"плохой id":               mut(func(m, r map[string]any) { r["id"] = "Bad ID" }),
		"опасность":               mut(func(m, r map[string]any) { r["severity"] = "critical" }),
		"расширение":              mut(func(m, r map[string]any) { r["exts"] = []string{"php"} }),
		"не компилируется":        mut(func(m, r map[string]any) { r["pattern"] = `(unclosed` }),
		"совпадает с пустым":      mut(func(m, r map[string]any) { r["pattern"] = `x*` }),
		"слишком жадное":          mut(func(m, r map[string]any) { r["pattern"] = `<\?php` }),
		"жадное на eval":          mut(func(m, r map[string]any) { r["pattern"] = `base64_decode` }),
		"длинное выражение":       mut(func(m, r map[string]any) { r["pattern"] = strings.Repeat("a", 1001) }),
	}
	for name, env := range cases {
		if _, err := avParseRulePack(env, pub); err == nil {
			t.Errorf("%s: набор принят", name)
		}
	}
	many := okPack("2026.10.22")
	rules := []map[string]any{}
	for i := 0; i < avRulesMaxRules+1; i++ {
		rules = append(rules, map[string]any{"id": fmt.Sprintf("pack-r%d", i), "title": "t", "severity": "high", "pattern": fmt.Sprintf("MARKER%dX", i)})
	}
	many["rules"] = rules
	if _, err := avParseRulePack(signPack(t, priv, many), pub); err == nil {
		t.Error("слишком много правил принято")
	}
	if _, err := avParseRulePack([]byte(strings.Repeat("x", avRulesMaxBytes+1)), pub); err == nil {
		t.Error("слишком большой набор принят")
	}
}

// rulesUpdate запускает обновление (оно идёт задачей) и ждёт её конца; результат приводится к виду ответа API.
func rulesUpdate(t *testing.T, mux *http.ServeMux) (int, map[string]any) {
	t.Helper()
	code, r := testAPI(t, mux, "av.rules.update", `{}`)
	data, _ := r["data"].(map[string]any)
	id, _ := data["job"].(string)
	if id == "" {
		return code, r
	}
	for i := 0; i < 500; i++ {
		jobsMu.Lock()
		j := jobs[id]
		done := j.Status != "running"
		res, jerr := j.Result, j.Error
		jobsMu.Unlock()
		if done {
			if jerr != "" {
				return code, map[string]any{"error": jerr}
			}
			return code, map[string]any{"ok": true, "data": res}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("задача обновления правил не завершилась")
	return 0, nil
}

// Работа антивируса идёт по очереди: вторая ждёт, пока закончит первая.
func TestAntivirusWorkRunsOneAtATime(t *testing.T) {
	avScanMu.Lock()
	started := make(chan struct{})
	go func() {
		avQueue(&Job{})
		close(started)
		avScanMu.Unlock()
	}()
	select {
	case <-started:
		t.Fatal("вторая работа началась, не дождавшись первой")
	case <-time.After(100 * time.Millisecond):
	}
	avScanMu.Unlock()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("вторая работа не началась после окончания первой")
	}
}

func TestRulePackUpdateFlow(t *testing.T) {
	mux := testPanel(t)
	priv, pub := testKey(t)
	oldKey, oldURLs := avRulesKey, avRulesURLs
	defer func() { avRulesKey, avRulesURLs = oldKey, oldURLs; avPack.Store(nil) }()
	avRulesKey, avRulesInsecureOK = pub, true
	avPack.Store(nil)
	body := signPack(t, priv, okPack("2026.10.22"))
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	avRulesURLs = []string{srv.URL + "/first", srv.URL + "/second"}
	// Первое обновление: набор скачан, сохранён и применён к проверке.
	_, r := rulesUpdate(t, mux)
	if r["ok"] != true || avPack.Load() == nil || avPack.Load().Version != "2026.10.22" {
		t.Fatalf("первое обновление: %v", r)
	}
	if f := avScanContent("a/x.php", []byte("<?php // EVIL_MARKER_42\n")); len(f) != 1 || f[0].Threat != "Тестовая угроза" {
		t.Fatalf("новое правило не работает: %+v", f)
	}
	if _, err := os.Stat(avRulesPath()); err != nil {
		t.Fatal("набор не сохранён на диск")
	}
	// После перезапуска панели сохранённый набор подхватывается.
	avPack.Store(nil)
	avLoadRulePack()
	if avPack.Load() == nil {
		t.Fatal("сохранённый набор не прочитан при запуске")
	}
	// Тот же набор: «уже актуальны».
	if _, r = rulesUpdate(t, mux); r["ok"] != true || !strings.Contains(fmt.Sprint(r["data"]), "актуальн") {
		t.Fatalf("повторное обновление: %v", r)
	}
	// Откат на более старый набор отклоняется.
	body = signPack(t, priv, okPack("2026.09.01"))
	if _, r = rulesUpdate(t, mux); r["error"] == nil || avPack.Load().Version != "2026.10.22" {
		t.Fatalf("откат принят: %v", r)
	}
	if store.Antivirus.RulesError == "" {
		t.Fatal("ошибка обновления не запомнена")
	}
	// Более новый принимается; недоступный источник даёт ошибку, а работающий набор остаётся.
	body = signPack(t, priv, okPack("2026.11.01"))
	if _, r = rulesUpdate(t, mux); r["ok"] != true || avPack.Load().Version != "2026.11.01" || store.Antivirus.RulesError != "" {
		t.Fatalf("новый набор: %v", r)
	}
	status = 500
	if _, r = rulesUpdate(t, mux); r["error"] == nil || avPack.Load().Version != "2026.11.01" {
		t.Fatalf("сбой источника: %v", r)
	}
	// Набор, подписанный чужим ключом, не применяется.
	status = 200
	otherPriv, _ := testKey(t)
	body = signPack(t, otherPriv, okPack("2026.12.01"))
	if _, r = rulesUpdate(t, mux); r["error"] == nil || avPack.Load().Version != "2026.11.01" {
		t.Fatalf("чужая подпись принята: %v", r)
	}
	// Состояние для раздела «Антивирус» и выключение автообновления.
	_, st := testAPI(t, mux, "av.status", `{}`)
	rules := st["data"].(map[string]any)["rules"].(map[string]any)
	if rules["version"] != "2026.11.01" || rules["auto"] != true || rules["source"] != "GitHub" {
		t.Fatalf("сведения о правилах: %v", rules)
	}
	if _, r = testAPI(t, mux, "av.rules.settings", `{"auto":false}`); r["ok"] != true || !store.Antivirus.NoRulesUpdate {
		t.Fatalf("выключение автообновления: %v", r)
	}
}
