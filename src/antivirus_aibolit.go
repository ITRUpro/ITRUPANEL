// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AI-Bolit (Revisium) — третий движок антивируса, по желанию. Панель его НЕ поставляет: у сканера
// и его закрытой базы нет лицензии, разрешающей распространение. Администратор сам указывает адрес
// файла ai-bolit.php (например, со страницы разработчика) или путь к уже установленному; панель
// только запускает его и читает отчёт. Нужен PHP в командной строке.

const (
	avAIBolitDir     = "/var/lib/itrupanel/aibolit"
	avAIBolitMaxSize = 24 << 20
)

// avAIBolitScript возвращает путь к ai-bolit.php, если сканер доступен.
func avAIBolitScript() string {
	if cfg.Dry {
		return ""
	}
	mu.Lock()
	custom := store.Antivirus.AIBolitPath
	mu.Unlock()
	for _, p := range []string{custom, avAIBolitDir + "/ai-bolit.php"} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(P(p)); err == nil && st.Mode().IsRegular() {
			return P(p)
		}
	}
	return ""
}

func avAIBolitUsable() (bool, string) {
	switch {
	case avAIBolitScript() == "":
		return false, "AI-Bolit не подключён"
	case !have("php"):
		return false, "Для AI-Bolit нужен PHP в командной строке"
	}
	return true, ""
}

// avAIBolitCheckScript проверяет, что файл — сканер AI-Bolit и синтаксически верный PHP.
func avAIBolitCheckScript(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	head := string(data)
	if len(head) > 200000 {
		head = head[:200000]
	}
	if !strings.Contains(strings.ToLower(head), "ai-bolit") {
		return errf("Это не файл сканера AI-Bolit (ai-bolit.php)")
	}
	if have("php") {
		if out, err := runTimeout(30*time.Second, "", "php", "-l", path); err != nil {
			return errf("Файл не проходит проверку PHP: %s", firstLine(out))
		}
	}
	return nil
}

var avAIBolitClient = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 4 || (req.URL.Scheme != "https" && !avAIBolitInsecureOK) {
		return errf("Перенаправление на небезопасный адрес запрещено")
	}
	return nil
}}

// avAIBolitInsecureOK разрешает http только в тестах.
var avAIBolitInsecureOK bool

// avAIBolitInstall скачивает ai-bolit.php по адресу, который указал администратор.
func avAIBolitInstall(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(avAIBolitInsecureOK && u.Scheme == "http")) {
		return "", errf("Укажите адрес файла ai-bolit.php вида https://…")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
	res, err := avAIBolitClient.Do(req)
	if err != nil {
		return "", errf("AI-Bolit: не удалось скачать файл: %s", firstLine(err.Error()))
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errf("AI-Bolit: сервер ответил %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, avAIBolitMaxSize+1))
	if err != nil {
		return "", errf("AI-Bolit: загрузка прервана: %s", firstLine(err.Error()))
	}
	if len(data) > avAIBolitMaxSize {
		return "", errf("AI-Bolit: файл слишком большой")
	}
	if err := os.MkdirAll(P(avAIBolitDir), 0700); err != nil {
		return "", err
	}
	tmp := filepath.Join(P(avAIBolitDir), "ai-bolit.php.new")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return "", err
	}
	if err := avAIBolitCheckScript(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(P(avAIBolitDir), "ai-bolit.php")); err != nil {
		return "", err
	}
	mu.Lock()
	store.Antivirus.AIBolitSource, store.Antivirus.AIBolitInstalled = u.Host, time.Now()
	err = saveChecked()
	mu.Unlock()
	return "AI-Bolit установлен", err
}

// avAIBolitReport разбирает JSON-отчёт AI-Bolit: списки записей с полем «fn» (файл) под
// ключами вроде php_malware, adware, phishing, vulners и т. п.
type avAIBolitHit struct {
	Path, Name, Group string
}

func avAIBolitParse(data []byte) ([]avAIBolitHit, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, errf("AI-Bolit: непонятный отчёт: %s", firstLine(err.Error()))
	}
	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var hits []avAIBolitHit
	for _, k := range keys {
		var list []map[string]any
		if json.Unmarshal(top[k], &list) != nil {
			continue
		}
		for _, e := range list {
			fn, _ := e["fn"].(string)
			if fn == "" {
				continue
			}
			name := ""
			for _, f := range []string{"sn", "sig_name", "name", "ty_name", "description"} {
				if s, ok := e[f].(string); ok && strings.TrimSpace(s) != "" {
					name = strings.TrimSpace(s)
					break
				}
			}
			hits = append(hits, avAIBolitHit{Path: fn, Name: name, Group: k})
		}
	}
	return hits, nil
}

func avAIBolitSeverity(group string) string {
	g := strings.ToLower(group)
	for _, k := range []string{"malware", "shell", "backdoor", "phishing", "virus"} {
		if strings.Contains(g, k) {
			return "high"
		}
	}
	return "medium"
}

// avAIBolitScan проверяет папки по одной. Запуск: низкий приоритет, без опасных функций PHP.
func avAIBolitScan(j *Job, roots []string) (map[string][]AVFinding, error) {
	out := map[string][]AVFinding{}
	script := avAIBolitScript()
	if script == "" {
		return out, nil
	}
	report, err := os.MkdirTemp("", "aibolit-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(report)
	var firstErr error
	for i, r := range roots {
		root := strings.TrimRight(P(r), "/")
		jobStage(j, fmt.Sprintf("AI-Bolit: проверка %d из %d", i+1, len(roots)))
		rep := filepath.Join(report, fmt.Sprintf("r%d.json", i))
		args := []string{"-n", "19", "php",
			"-d", "memory_limit=512M", "-d", "allow_url_fopen=0", "-d", "max_execution_time=0",
			"-d", "disable_functions=exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec",
			script, "--path=" + root, "--mode=2", "--json_report=" + rep, "--memory=512M", "--delay=0", "--no-html"}
		text, rerr := jobOutput(j, 3*time.Hour, "nice", args...)
		if jobStopped(j) {
			return out, rerr
		}
		data, derr := os.ReadFile(rep)
		if derr != nil {
			if firstErr == nil {
				firstErr = errf("AI-Bolit: отчёт не создан. %s", firstLine(text+" "+fmt.Sprint(rerr)))
			}
			continue
		}
		hits, perr := avAIBolitParse(data)
		if perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		for _, h := range hits {
			abs := h.Path
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(root, abs)
			}
			abs = filepath.Clean(abs)
			if !strings.HasPrefix(abs, root+"/") {
				continue
			}
			name := h.Name
			if name == "" {
				name = h.Group
			}
			f := AVFinding{Path: strings.TrimPrefix(abs, root+"/"), Threat: "AI-Bolit: " + name, Engine: "aibolit", Severity: avAIBolitSeverity(h.Group)}
			if b, err := os.ReadFile(abs); err == nil {
				f.SHA256, f.Size = avSHA256(b), int64(len(b))
			}
			if info, err := os.Stat(abs); err == nil {
				f.Modified = info.ModTime().Unix()
			}
			out[r] = append(out[r], f)
		}
	}
	return out, firstErr
}

func avAIBolitInfo() map[string]any {
	ok, why := avAIBolitUsable()
	mu.Lock()
	a := store.Antivirus
	mu.Unlock()
	out := map[string]any{"installed": avAIBolitScript() != "", "usable": ok, "reason": why, "enabled": !a.NoAIBolit, "path": a.AIBolitPath, "source": a.AIBolitSource, "php": have("php")}
	if !a.AIBolitInstalled.IsZero() {
		out["installed_at"] = a.AIBolitInstalled
	}
	return out
}

func registerAntivirusAIBolit() {
	apiOp("av.aibolit.install", "Антивирус: подключение AI-Bolit", func(q *Req) (any, error) {
		var in struct {
			URL string `json:"url"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !have("php") && !cfg.Dry {
			return nil, errf("Для AI-Bolit нужен PHP в командной строке. Установите PHP в разделе «PHP»")
		}
		return map[string]string{"job": startJob("av:aibolit", "Подключение AI-Bolit", q.S.User, func(j *Job) (any, error) {
			info, err := avAIBolitInstall(j.ctx, in.URL)
			return map[string]string{"info": info}, err
		})}, nil
	})
	apiOp("av.aibolit.settings", "Антивирус: настройки AI-Bolit", func(q *Req) (any, error) {
		var in struct {
			Enabled bool   `json:"enabled"`
			Path    string `json:"path"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		in.Path = strings.TrimSpace(in.Path)
		if in.Path != "" {
			if !filepath.IsAbs(in.Path) || !strings.HasSuffix(in.Path, ".php") || strings.Contains(in.Path, "..") {
				return nil, errf("Путь к AI-Bolit: полный путь к файлу ai-bolit.php")
			}
			if err := avAIBolitCheckScript(P(in.Path)); err != nil {
				return nil, err
			}
		}
		mu.Lock()
		store.Antivirus.NoAIBolit, store.Antivirus.AIBolitPath = !in.Enabled, in.Path
		err := saveChecked()
		mu.Unlock()
		return nil, err
	})
	apiOp("av.aibolit.remove", "Антивирус: удаление AI-Bolit", func(q *Req) (any, error) {
		os.RemoveAll(P(avAIBolitDir))
		mu.Lock()
		store.Antivirus.AIBolitSource, store.Antivirus.AIBolitInstalled = "", time.Time{}
		err := saveChecked()
		mu.Unlock()
		return nil, err
	})
}
