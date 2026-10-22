// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// Обновляемые правила антивируса. Набор правил лежит в репозитории проекта на GitHub (запасная копия — на itrupanel.com)
// и подписан ключом выпусков панели. Панель скачивает его раз в сутки или по кнопке, проверяет подпись и содержимое и
// добавляет к встроенным правилам. Правила — только регулярные выражения (RE2: время проверки не растёт лавинообразно),
// никакой код с сервера не выполняется. Устаревший набор не принимается (защита от отката), неподписанный — тоже.

const (
	avRulesSchema   = 1
	avRulesMaxBytes = 512 << 10
	avRulesMaxRules = 500
	avRulesFile     = "antivirus-rules.json"
)

// Адреса набора. Переменные, чтобы тесты могли подставить свой сервер; пользователь адрес не задаёт.
var avRulesURLs = []string{
	"https://raw.githubusercontent.com/ITRUpro/ITRUPANEL/main/antivirus/rules.json",
	"https://itrupanel.com/panel/antivirus-rules.json",
}

// avRulesKey — открытый ключ подписи (тот же, что у выпусков).
var avRulesKey = releasePublicKey

type avPackRule struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Exts     []string `json:"exts"`
	Pattern  string   `json:"pattern"`
	Code     bool     `json:"code,omitempty"` // совпадение в закомментированной строке не считается угрозой
}

type avRulePack struct {
	Schema   int          `json:"schema"`
	Version  string       `json:"version"`
	Released string       `json:"released"`
	Rules    []avPackRule `json:"rules"`
}

type avCompiledPack struct {
	Version  string
	Released string
	Rules    []avCompiledRule
	Envelope []byte
}

type avCompiledRule struct {
	avRule
	code bool
}

var avPack atomic.Pointer[avCompiledPack]

var (
	reAVPackID      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,47}$`)
	reAVPackVersion = regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}(?:\.\d{1,3})?$`)
	reAVPackExt     = regexp.MustCompile(`^\.[a-z0-9.]{1,12}$`)
)

// Типичный безобидный код: ни одно правило набора не должно на нём срабатывать. Если подписанный набор вдруг
// содержит слишком жадное правило, панель его отвергнет, а не завалит владельцев ложными тревогами.
var avPackProbes = []string{
	"<?php\nfunction hello() { return 'Hello, world'; }\necho hello();\n",
	"<?php\n$encoded = base64_encode($data);\n$decoded = base64_decode($encoded, true);\nif ($decoded === false) { wp_die('bad'); }\n",
	"<?php\nadd_action('init', function () { register_post_type('book', ['public' => true]); });\n$file = $_FILES['upload']['tmp_name'];\nmove_uploaded_file($file, wp_upload_dir()['path'] . '/x.jpg');\n",
	"<?php\n// eval is dangerous: never eval(base64_decode($x))\n$list = array_map('trim', explode(',', $_POST['ids']));\nusort($items, function ($a, $b) { return $a['n'] <=> $b['n']; });\n",
	"<script>window.dataLayer = window.dataLayer || []; function gtag(){dataLayer.push(arguments);} var d = atob('aGVsbG8=');</script>\n",
	"RewriteEngine On\nRewriteCond %{HTTPS} off\nRewriteRule ^(.*)$ https://%{HTTP_HOST}/$1 [L,R=301]\n",
	"<?php\ninclude __DIR__ . '/config.php';\nrequire_once 'vendor/autoload.php';\n$r = call_user_func('strtoupper', 'x');\n",
}

// avVersionLess сравнивает версии набора вида ГГГГ.ММ.ДД[.N] по частям.
func avVersionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 4; i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// avParseRulePack проверяет подпись и содержимое набора и компилирует правила.
func avParseRulePack(envelope []byte, keyHex string) (*avCompiledPack, error) {
	if len(envelope) > avRulesMaxBytes {
		return nil, errf("Набор правил слишком велик")
	}
	var env struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, errf("Набор правил: непонятный формат")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, errf("Набор правил: непонятный формат")
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	key, kerr := hex.DecodeString(keyHex)
	if err != nil || kerr != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, sig) {
		return nil, errf("Подпись набора правил не прошла проверку")
	}
	var p avRulePack
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, errf("Набор правил: %s", firstLine(err.Error()))
	}
	if p.Schema != avRulesSchema || !reAVPackVersion.MatchString(p.Version) {
		return nil, errf("Набор правил: неподдерживаемая версия формата")
	}
	if _, err := time.Parse("2006-01-02", p.Released); err != nil {
		return nil, errf("Набор правил: неверная дата выпуска")
	}
	if len(p.Rules) == 0 || len(p.Rules) > avRulesMaxRules {
		return nil, errf("Набор правил: недопустимое число правил")
	}
	builtin := map[string]bool{}
	for _, r := range avRules {
		builtin[r.id] = true
	}
	seen := map[string]bool{}
	out := &avCompiledPack{Version: p.Version, Released: p.Released, Envelope: append([]byte{}, envelope...)}
	for _, r := range p.Rules {
		if !reAVPackID.MatchString(r.ID) || builtin[r.ID] || seen[r.ID] {
			return nil, errf("Набор правил: недопустимый или повторяющийся идентификатор %q", r.ID)
		}
		seen[r.ID] = true
		if t := strings.TrimSpace(r.Title); t == "" || len(t) > 200 {
			return nil, errf("Набор правил: недопустимое название у %s", r.ID)
		}
		if r.Severity != "high" && r.Severity != "medium" {
			return nil, errf("Набор правил: недопустимая опасность у %s", r.ID)
		}
		if len(r.Exts) > 20 {
			return nil, errf("Набор правил: слишком много расширений у %s", r.ID)
		}
		for _, e := range r.Exts {
			if !reAVPackExt.MatchString(e) {
				return nil, errf("Набор правил: недопустимое расширение у %s", r.ID)
			}
		}
		if len(r.Pattern) == 0 || len(r.Pattern) > 1000 {
			return nil, errf("Набор правил: недопустимое выражение у %s", r.ID)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, errf("Набор правил: выражение %s не компилируется", r.ID)
		}
		if re.MatchString("") || re.MatchString("\n") {
			return nil, errf("Набор правил: выражение %s совпадает с пустым текстом", r.ID)
		}
		for _, probe := range avPackProbes {
			if re.MatchString(probe) {
				return nil, errf("Набор правил: правило %s срабатывает на обычном коде и отвергнуто", r.ID)
			}
		}
		out.Rules = append(out.Rules, avCompiledRule{avRule: avRule{id: r.ID, threat: strings.TrimSpace(r.Title), severity: r.Severity, exts: r.Exts, re: re}, code: r.Code})
	}
	return out, nil
}

func avRulesPath() string { return filepath.Join(cfg.Home, "var", avRulesFile) }

// avLoadRulePack читает сохранённый набор при запуске панели; повреждённый или чужой файл игнорируется.
func avLoadRulePack() {
	data, err := os.ReadFile(avRulesPath())
	if err != nil {
		avPack.Store(nil)
		return
	}
	p, err := avParseRulePack(data, avRulesKey)
	if err != nil {
		avPack.Store(nil)
		return
	}
	avPack.Store(p)
}

// avFetchRulePack скачивает набор с первого ответившего адреса.
// Набор лежит на GitHub (raw.githubusercontent.com) и на сайте панели; переходы разрешены только по https.
var avRulesClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 3 || (req.URL.Scheme != "https" && !avRulesInsecureOK) {
		return errf("Перенаправление набора правил запрещено")
	}
	return nil
}}

// avRulesInsecureOK разрешает http только в тестах.
var avRulesInsecureOK bool

func avFetchRulePack(ctx context.Context) ([]byte, string, error) {
	var last error
	for _, u := range avRulesURLs {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			last = err
			continue
		}
		req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
		res, err := avRulesClient.Do(req)
		if err != nil {
			last = err
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(res.Body, avRulesMaxBytes+1))
		res.Body.Close()
		if res.StatusCode != 200 || rerr != nil {
			last = errf("%s: %s", u, res.Status)
			continue
		}
		return data, u, nil
	}
	if last == nil {
		last = errf("нет адресов набора правил")
	}
	return nil, "", last
}

// avUpdateRules скачивает и применяет новый набор. Возвращает пояснение для администратора.
func avUpdateRules(ctx context.Context) (string, error) {
	data, from, err := avFetchRulePack(ctx)
	if err == nil {
		var p *avCompiledPack
		p, err = avParseRulePack(data, avRulesKey)
		if err == nil {
			cur := avPack.Load()
			switch {
			case cur != nil && cur.Version == p.Version:
				avRulesDone(nil, p.Version, "")
				return fmt.Sprintf("Правила уже актуальны: версия %s, правил: %d", p.Version, len(p.Rules)), nil
			case cur != nil && avVersionLess(p.Version, cur.Version):
				err = errf("Набор правил %s старее установленного %s: отклонён", p.Version, cur.Version)
			default:
				if werr := os.MkdirAll(filepath.Dir(avRulesPath()), 0700); werr != nil {
					err = werr
				} else if werr := writeFileAtomic(avRulesPath(), data, 0600); werr != nil {
					err = werr
				} else {
					avPack.Store(p)
					avRulesDone(nil, p.Version, from)
					return fmt.Sprintf("Правила обновлены: версия %s, правил: %d", p.Version, len(p.Rules)), nil
				}
			}
		}
	}
	avRulesDone(err, "", "")
	return "", err
}

func avRulesDone(err error, version, from string) {
	mu.Lock()
	defer mu.Unlock()
	st := &store.Antivirus
	st.RulesChecked = time.Now()
	if err != nil {
		st.RulesError = firstLine(err.Error())
		addNotice("warn", "av:rules", "Антивирус: правила не обновились: "+st.RulesError)
	} else {
		st.RulesError = ""
		if from != "" {
			st.RulesUpdated = time.Now()
		}
	}
	save()
}

// avAutoUpdateRules проверяет набор раз в сутки (если не выключено).
func avAutoUpdateRules(now time.Time) {
	if cfg.Dry {
		return
	}
	mu.Lock()
	due := !store.Antivirus.NoRulesUpdate && now.Sub(store.Antivirus.RulesChecked) >= 23*time.Hour+30*time.Minute
	if due {
		store.Antivirus.RulesChecked = now
		save()
	}
	mu.Unlock()
	if !due || jobRunning("av:rules") {
		return
	}
	startJob("av:rules", "Обновление правил антивируса (автоматически)", "admin", func(j *Job) (any, error) {
		avQueue(j)
		defer avScanMu.Unlock()
		info, err := avUpdateRules(j.ctx)
		return map[string]any{"info": info}, err
	})
}

// avRulesInfo — сведения о правилах для раздела «Антивирус».
func avRulesInfo() map[string]any {
	out := map[string]any{"builtin": len(avRules), "pack": 0}
	if p := avPack.Load(); p != nil {
		out["version"], out["released"], out["pack"] = p.Version, p.Released, len(p.Rules)
	}
	mu.Lock()
	st := store.Antivirus
	mu.Unlock()
	out["auto"] = !st.NoRulesUpdate
	if !st.RulesChecked.IsZero() {
		out["checked"] = st.RulesChecked
	}
	if !st.RulesUpdated.IsZero() {
		out["updated"] = st.RulesUpdated
	}
	if st.RulesError != "" {
		out["error"] = st.RulesError
	}
	out["source"] = "GitHub"
	return out
}

func registerAntivirusRules() {
	avLoadRulePack()
	apiOp("av.rules.update", "Антивирус: обновление правил", func(q *Req) (any, error) {
		if jobRunning("av:rules") {
			return nil, errf("Обновление правил уже идёт")
		}
		return map[string]string{"job": startJob("av:rules", "Обновление правил антивируса", q.S.User, func(j *Job) (any, error) {
			avQueue(j)
			defer avScanMu.Unlock()
			info, err := avUpdateRules(j.ctx)
			return map[string]any{"info": info}, err
		})}, nil
	})
	apiOp("av.rules.settings", "Антивирус: автоматическое обновление правил", func(q *Req) (any, error) {
		var in struct {
			Auto bool `json:"auto"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		store.Antivirus.NoRulesUpdate = !in.Auto
		save()
		mu.Unlock()
		return avRulesInfo(), nil
	})
}
