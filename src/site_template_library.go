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

// Библиотека шаблонов сайтов: подписанный ключом выпусков набор шаблонов в репозитории (templates/library.json).
// Панель раз в сутки проверяет его и показывает новые шаблоны (Drupal, OpenCart, MediaWiki и др.) без выпуска новой версии.
// Принимаются только подписанные и более новые наборы; правила проверяются теми же средствами, что и свои шаблоны.

const (
	tplLibSchema       = 1
	tplLibMaxBytes     = 256 << 10
	tplLibMaxTemplates = 100
	tplLibFile         = "template-library.json"
)

var (
	tplLibURLs = []string{"https://raw.githubusercontent.com/ITRUpro/ITRUPANEL/main/templates/library.json", "https://itrupanel.com/panel/template-library.json"}
	tplLibKey  = releasePublicKey
	// tplLibInsecureOK разрешает http только в тестах.
	tplLibInsecureOK bool
	tplLibClient     = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || (req.URL.Scheme != "https" && !tplLibInsecureOK) {
			return errf("Перенаправление библиотеки шаблонов запрещено")
		}
		return nil
	}}
)

type tplLibTemplate struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	NameEn string `json:"name_en"`
	Desc   string `json:"desc"`
	DescEn string `json:"desc_en"`
	Rules  string `json:"rules"`
}

type tplLibPack struct {
	Schema    int              `json:"schema"`
	Version   string           `json:"version"`
	Released  string           `json:"released"`
	Templates []tplLibTemplate `json:"templates"`
}

type tplLibCompiled struct {
	Version, Released string
	Templates         []tplLibTemplate // идентификаторы уже с префиксом lib-
	Envelope          []byte
}

var tplLib atomic.Pointer[tplLibCompiled]

var (
	reTplLibID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,29}$`)
	// Даже подписанный набор не должен подключать чужие файлы, менять корень, пересылать запросы или запускать скрипты:
	// из директив, которые обращаются к другим сервисам, разрешён только PHP-FPM сайта (fastcgi_pass unix:%SOCK%).
	reTplLibForbidden = regexp.MustCompile(`(?i)(?:^|[\s;{}])(include|load_module|perl|perl_set|[a-z_]*_by_lua[a-z_]*|js_content|js_set|alias|root|proxy_pass|uwsgi_pass|scgi_pass|grpc_pass|ssl_certificate[a-z_]*|auth_basic_user_file|access_log|error_log)\s`)
	reTplLibFastCGI   = regexp.MustCompile(`fastcgi_pass\s+([^;]+);`)
)

func tplLibPath() string { return filepath.Join(cfg.Home, "var", tplLibFile) }

func tplLibParse(envelope []byte, keyHex string) (*tplLibCompiled, error) {
	if len(envelope) > tplLibMaxBytes {
		return nil, errf("Библиотека шаблонов слишком велика")
	}
	var env struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, errf("Библиотека шаблонов: непонятный формат")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, errf("Библиотека шаблонов: непонятный формат")
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	key, kerr := hex.DecodeString(keyHex)
	if err != nil || kerr != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, sig) {
		return nil, errf("Подпись библиотеки шаблонов не прошла проверку")
	}
	var p tplLibPack
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, errf("Библиотека шаблонов: %s", firstLine(err.Error()))
	}
	if p.Schema != tplLibSchema || !reAVPackVersion.MatchString(p.Version) {
		return nil, errf("Библиотека шаблонов: неподдерживаемая версия формата")
	}
	if _, err := time.Parse("2006-01-02", p.Released); err != nil {
		return nil, errf("Библиотека шаблонов: неверная дата выпуска")
	}
	if len(p.Templates) == 0 || len(p.Templates) > tplLibMaxTemplates {
		return nil, errf("Библиотека шаблонов: недопустимое число шаблонов")
	}
	out := &tplLibCompiled{Version: p.Version, Released: p.Released, Envelope: append([]byte{}, envelope...)}
	seen := map[string]bool{}
	for _, t := range p.Templates {
		if !reTplLibID.MatchString(t.ID) || seen[t.ID] {
			return nil, errf("Библиотека шаблонов: недопустимый или повторяющийся идентификатор %q", t.ID)
		}
		seen[t.ID] = true
		if strings.TrimSpace(t.Name) == "" || len(t.Name) > 60 || len(t.NameEn) > 60 || len(t.Desc) > 500 || len(t.DescEn) > 500 {
			return nil, errf("Библиотека шаблонов: недопустимое название или описание у %s", t.ID)
		}
		if strings.TrimSpace(t.Rules) == "" {
			return nil, errf("Библиотека шаблонов: у %s нет правил", t.ID)
		}
		if err := validateSiteTemplateRules(t.Rules); err != nil {
			return nil, errf("Библиотека шаблонов: %s: %s", t.ID, err.Error())
		}
		// Разрешён только стандартный набор параметров FastCGI для обработчика PHP.
		if m := reTplLibForbidden.FindStringSubmatch(strings.ReplaceAll(t.Rules, "include fastcgi_params;", "")); m != nil {
			return nil, errf("Библиотека шаблонов: в %s запрещена директива %s", t.ID, m[1])
		}
		for _, m := range reTplLibFastCGI.FindAllStringSubmatch(t.Rules, -1) {
			if strings.TrimSpace(m[1]) != "unix:%SOCK%" {
				return nil, errf("Библиотека шаблонов: в %s fastcgi_pass только на unix:%%SOCK%%", t.ID)
			}
		}
		t.ID = "lib-" + t.ID
		out.Templates = append(out.Templates, t)
	}
	return out, nil
}

func tplLibLoad() {
	data, err := os.ReadFile(tplLibPath())
	if err != nil {
		tplLib.Store(nil)
		return
	}
	p, err := tplLibParse(data, tplLibKey)
	if err != nil {
		tplLib.Store(nil)
		return
	}
	tplLib.Store(p)
}

func tplLibFind(id string) (tplLibTemplate, bool) {
	if p := tplLib.Load(); p != nil {
		for _, t := range p.Templates {
			if t.ID == id {
				return t, true
			}
		}
	}
	return tplLibTemplate{}, false
}

func tplLibFetch(ctx context.Context) ([]byte, string, error) {
	var last error
	for _, u := range tplLibURLs {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			last = err
			continue
		}
		req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
		res, err := tplLibClient.Do(req)
		if err != nil {
			last = err
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(res.Body, tplLibMaxBytes+1))
		res.Body.Close()
		if res.StatusCode != 200 || rerr != nil {
			last = errf("%s: %s", u, res.Status)
			continue
		}
		return data, u, nil
	}
	if last == nil {
		last = errf("нет адресов библиотеки шаблонов")
	}
	return nil, "", last
}

func tplLibDone(err error, updated bool) {
	mu.Lock()
	st := &store.Settings
	st.TemplateLibChecked = time.Now()
	if err != nil {
		st.TemplateLibError = firstLine(err.Error())
	} else {
		st.TemplateLibError = ""
		if updated {
			st.TemplateLibUpdated = time.Now()
		}
	}
	save()
	mu.Unlock()
}

// tplLibUpdate скачивает и применяет новую библиотеку. Возвращает пояснение для администратора.
func tplLibUpdate(ctx context.Context) (string, error) {
	data, _, err := tplLibFetch(ctx)
	if err == nil {
		var p *tplLibCompiled
		p, err = tplLibParse(data, tplLibKey)
		if err == nil {
			cur := tplLib.Load()
			switch {
			case cur != nil && cur.Version == p.Version:
				tplLibDone(nil, false)
				return fmt.Sprintf("Библиотека уже актуальна: версия %s", p.Version), nil
			case cur != nil && avVersionLess(p.Version, cur.Version):
				err = errf("Библиотека %s старее установленной %s: отклонена", p.Version, cur.Version)
			default:
				if werr := os.MkdirAll(filepath.Dir(tplLibPath()), 0700); werr != nil {
					err = werr
				} else if werr := writeFileAtomic(tplLibPath(), data, 0600); werr != nil {
					err = werr
				} else {
					tplLib.Store(p)
					tplLibDone(nil, true)
					return fmt.Sprintf("Библиотека обновлена: версия %s, шаблонов: %d", p.Version, len(p.Templates)), nil
				}
			}
		}
	}
	tplLibDone(err, false)
	return "", err
}

// tplLibAutoUpdate проверяет библиотеку раз в сутки (если не выключено).
func tplLibAutoUpdate(now time.Time) {
	if cfg.Dry {
		return
	}
	mu.Lock()
	due := !store.Settings.NoTemplateLibrary && now.Sub(store.Settings.TemplateLibChecked) >= 23*time.Hour+30*time.Minute
	if due {
		store.Settings.TemplateLibChecked = now
		save()
	}
	mu.Unlock()
	if !due {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		tplLibUpdate(ctx)
	}()
}

func tplLibInfo() map[string]any {
	out := map[string]any{"source": "GitHub", "count": 0}
	if p := tplLib.Load(); p != nil {
		out["version"], out["released"], out["count"] = p.Version, p.Released, len(p.Templates)
	}
	mu.Lock()
	st := store.Settings
	mu.Unlock()
	out["auto"] = !st.NoTemplateLibrary
	if !st.TemplateLibChecked.IsZero() {
		out["checked"] = st.TemplateLibChecked
	}
	if !st.TemplateLibUpdated.IsZero() {
		out["updated"] = st.TemplateLibUpdated
	}
	if st.TemplateLibError != "" {
		out["error"] = st.TemplateLibError
	}
	return out
}

func registerSiteTemplateLibrary() {
	tplLibLoad()
	apiOp("site.templates.library.update", "Шаблоны сайтов: обновление библиотеки", func(q *Req) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		info, err := tplLibUpdate(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"info": info, "library": tplLibInfo()}, nil
	})
	apiOp("site.templates.library.settings", "Шаблоны сайтов: автоматическое обновление библиотеки", func(q *Req) (any, error) {
		var in struct {
			Auto bool `json:"auto"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		store.Settings.NoTemplateLibrary = !in.Auto
		save()
		mu.Unlock()
		return nil, nil
	})
}
