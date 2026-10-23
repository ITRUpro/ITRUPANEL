// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Шаблоны сайтов: готовые наборы правил nginx под тип сайта (WordPress, Laravel, SPA и т. п.).
// Каркас сайта (порты, SSL, логи, защита, PHP-обработчик, закрытие служебных файлов) остаётся у панели и
// не ломается шаблоном: шаблон только добавляет правила внутрь server {}. Свои правила сайта
// (sites.custom.save) работают поверх шаблона. Переменные: %DOMAIN%, %ROOT%, %USER%, %PHP%, %SOCK%.

type SiteTemplate struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Desc    string    `json:"desc"`
	Rules   string    `json:"rules"`
	Created time.Time `json:"created"`
}

type siteTemplateInfo struct {
	SiteTemplate
	Builtin bool   `json:"builtin"`
	Sites   int    `json:"sites"`
	NameEn  string `json:"name_en,omitempty"`
	DescEn  string `json:"desc_en,omitempty"`
}

// Английские названия и описания встроенных шаблонов для интерфейса на английском.
var siteTemplateEn = map[string][2]string{
	"plain":     {"Standard", "No extra rules: the panel base does everything."},
	"hardened":  {"Protect service files", "Denies access to .git, .env, .htpasswd, composer.*, package.json, database dumps (*.sql), backups (*.bak, *.old) and logs (*.log). Suits any website."},
	"wordpress": {"WordPress", "Pretty permalinks, no PHP execution from the uploads folder, wp-config.php and xmlrpc.php closed. Site mode: nginx + PHP-FPM. xmlrpc.php is closed; the WordPress mobile app and Jetpack need it, so remove that line in your own copy of the template."},
	"laravel":   {"Laravel / Symfony", "All requests go to index.php; .env, storage, vendor and service files are closed. Set the public folder as the site root in the site settings."},
	"joomla":    {"Joomla", "Pretty links, configuration.php, logs and cache closed."},
	"bitrix":    {"1C-Bitrix", "Routing through urlrewrite.php, no PHP execution from upload and the core service folders closed. Only the nginx syntax is checked; verify that it works on your site."},
	"spa":       {"Single-page application (SPA)", "Addresses that are not files open index.html (React, Vue, Angular). Site mode: static only."},
}

var siteTemplateBuiltins = []SiteTemplate{
	{ID: "plain", Name: "Стандартный", Desc: "Без дополнительных правил: всё делает основа панели."},
	{ID: "hardened", Name: "Закрыть служебные файлы", Desc: "Запрещает доступ к .git, .env, .htpasswd, composer.*, package.json, дампам баз (*.sql), резервным копиям (*.bak, *.old) и журналам (*.log). Подходит любому сайту.",
		Rules: `    location ~* ^/(?:\.git|\.svn|\.hg)(?:/|$) { deny all; }
    location ~* /(?:\.env|\.htpasswd|composer\.(?:json|lock)|package(?:-lock)?\.json|yarn\.lock)$ { deny all; }
    location ~* \.(?:sql|sqlite|bak|old|orig|swp|log|ini|conf|sh)$ { deny all; }
`},
	{ID: "wordpress", Name: "WordPress", Desc: "Красивые ссылки, запрет запуска PHP из папки загрузок, закрытый wp-config.php и xmlrpc.php. Режим сайта — nginx + PHP-FPM. xmlrpc.php закрыт: приложению WordPress для телефона и Jetpack он нужен, тогда уберите эту строку в своей копии шаблона.",
		Rules: `    location / { try_files $uri $uri/ /index.php?$args; }
    location = /xmlrpc.php { deny all; }
    location ~* /wp-config\.php$ { deny all; }
    location ~* ^/wp-content/(?:uploads|files|cache)/.*\.php$ { deny all; }
`},
	{ID: "laravel", Name: "Laravel / Symfony", Desc: "Все запросы на index.php, закрыты .env, storage, vendor и служебные файлы. В настройках сайта укажите папку public как корень.",
		Rules: `    location / { try_files $uri $uri/ /index.php?$query_string; }
    location ~* ^/(?:storage|vendor|bootstrap/cache)/.*\.php$ { deny all; }
    location ~* /(?:artisan|composer\.(?:json|lock)|phpunit\.xml|webpack\.mix\.js)$ { deny all; }
`},
	{ID: "joomla", Name: "Joomla", Desc: "Красивые ссылки, закрыты configuration.php, журналы и кэш.",
		Rules: `    location / { try_files $uri $uri/ /index.php?$args; }
    location ~* /configuration\.php$ { deny all; }
    location ~* ^/(?:logs|tmp|cache)/.*\.php$ { deny all; }
`},
	{ID: "bitrix", Name: "1С-Битрикс", Desc: "Маршрутизация через urlrewrite.php, запрет запуска PHP из upload и закрытие служебных папок ядра. Проверяет только синтаксис nginx; работу на вашем сайте проверьте сами.",
		Rules: `    location / { try_files $uri $uri/ @bitrix; }
    location @bitrix {
        include fastcgi_params;
        fastcgi_pass unix:%SOCK%;
        fastcgi_param SCRIPT_FILENAME $document_root/bitrix/urlrewrite.php;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_read_timeout 300;
    }
    location ~* ^/upload/.*\.(?:php|phtml|php3|php4|php5|php7)$ { deny all; }
    location ~* ^/bitrix/(?:modules|local_cache|stack_cache|managed_cache|php_interface)/ { deny all; }
    location ~* ^/bitrix/(?:\.settings|\.settings_extra)\.php$ { deny all; }
`},
	{ID: "spa", Name: "Одностраничное приложение (SPA)", Desc: "Все адреса, которых нет в виде файлов, открывают index.html (React, Vue, Angular). Режим сайта — «Только статика».",
		Rules: `    location / { try_files $uri $uri/ /index.html; }
`},
}

var reTemplateID = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

func siteTemplateByID(id string) (SiteTemplate, bool) {
	if siteTemplatePreview != nil && id == siteTemplatePreview.ID {
		return *siteTemplatePreview, true
	}
	if id == "" {
		return siteTemplateBuiltins[0], true
	}
	for _, t := range siteTemplateBuiltins {
		if t.ID == id {
			return t, true
		}
	}
	for _, t := range store.Settings.SiteTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return SiteTemplate{}, false
}

// siteTemplateRules возвращает правила шаблона сайта с подставленными переменными. Вызывать под mu.
func siteTemplateRules(s *Site) (name, rules string) {
	t, ok := siteTemplateByID(s.Template)
	if !ok || strings.TrimSpace(t.Rules) == "" {
		return "", ""
	}
	return t.Name, expandSiteTemplate(t.Rules, s)
}

func expandSiteTemplate(rules string, s *Site) string {
	r := strings.NewReplacer("%DOMAIN%", s.Domain, "%ROOT%", s.Root, "%USER%", s.User, "%PHP%", s.PHP, "%SOCK%", sockPath(s.Domain))
	out := r.Replace(strings.ReplaceAll(rules, "\r\n", "\n"))
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return strings.TrimRight(out, "\n")
}

var reTemplateVar = regexp.MustCompile(`%[A-Z]+%`)

// validateSiteTemplate проверяет текст шаблона до применения: размер, баланс скобок, переменные.
func validateSiteTemplateRules(rules string) error {
	if len(rules) > 64<<10 {
		return errf("Шаблон слишком большой")
	}
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(rules); i++ {
		c := rules[i]
		switch {
		case inStr != 0:
			if c == '\\' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			for i < len(rules) && rules[i] != '\n' {
				i++
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth < 0 {
				return errf("В шаблоне лишняя закрывающая скобка }")
			}
		}
	}
	if inStr != 0 {
		return errf("В шаблоне не закрыта кавычка")
	}
	if depth != 0 {
		return errf("В шаблоне не закрыта скобка {")
	}
	for _, v := range reTemplateVar.FindAllString(rules, -1) {
		switch v {
		case "%DOMAIN%", "%ROOT%", "%USER%", "%PHP%", "%SOCK%":
		default:
			return errf("Неизвестная переменная %s. Доступны: %%DOMAIN%%, %%ROOT%%, %%USER%%, %%PHP%%, %%SOCK%%", v)
		}
	}
	return nil
}

// applySiteTemplateTo перезаписывает конфигурации nginx указанных сайтов и проверяет их вместе.
// При ошибке nginx -t все файлы возвращаются к прежним. Вызывать под mu.
func applySiteTemplateTo(sites []*Site) error {
	type snap struct {
		path string
		data []byte
	}
	var before []snap
	restore := func() error {
		var errs []error
		for _, sn := range before {
			errs = append(errs, writeFile(sn.path, sn.data, 0644))
		}
		errs = append(errs, nginxTestReload())
		return errors.Join(errs...)
	}
	for _, s := range sites {
		u := findUser(s.User)
		if u == nil {
			continue
		}
		custom, _ := os.ReadFile(P(customPath(s.Domain)))
		rendered, err := renderSiteNginx(s, u, string(custom))
		if err != nil {
			if len(before) > 0 {
				restore()
			}
			return errf("Сайт %s: %s", s.Domain, err.Error())
		}
		old, _ := os.ReadFile(P(vhostPath(s.Domain)))
		before = append(before, snap{vhostPath(s.Domain), old})
		if err := writeFile(vhostPath(s.Domain), rendered, 0644); err != nil {
			restore()
			return err
		}
	}
	if len(before) == 0 {
		return nil
	}
	if err := nginxTestReload(); err != nil {
		if rerr := restore(); rerr != nil {
			return fmt.Errorf("%w; восстановление nginx: %v", err, rerr)
		}
		return err
	}
	return nil
}

func sitesUsingTemplate(id string) []*Site {
	var out []*Site
	for _, s := range store.Sites {
		if s.Template == id {
			out = append(out, s)
		}
	}
	return out
}

func registerSiteTemplates() {
	api("site.templates.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		var rows []siteTemplateInfo
		for _, t := range siteTemplateBuiltins {
			n := len(sitesUsingTemplate(t.ID))
			if t.ID == "plain" {
				n += countSitesWithoutTemplate()
			}
			rows = append(rows, siteTemplateInfo{SiteTemplate: t, Builtin: true, Sites: n, NameEn: siteTemplateEn[t.ID][0], DescEn: siteTemplateEn[t.ID][1]})
		}
		custom := append([]SiteTemplate(nil), store.Settings.SiteTemplates...)
		sort.Slice(custom, func(i, j int) bool { return custom[i].Name < custom[j].Name })
		for _, t := range custom {
			rows = append(rows, siteTemplateInfo{SiteTemplate: t, Sites: len(sitesUsingTemplate(t.ID))})
		}
		return map[string]any{"templates": rows, "vars": []string{"%DOMAIN%", "%ROOT%", "%USER%", "%PHP%", "%SOCK%"}}, nil
	})
	apiOp("site.templates.save", "Шаблон сайта: сохранение", func(q *Req) (any, error) {
		var in struct {
			ID, Name, Desc, Rules string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		in.Name, in.Desc = strings.TrimSpace(in.Name), strings.TrimSpace(in.Desc)
		if in.Name == "" || len(in.Name) > 60 {
			return nil, errf("Название шаблона: от 1 до 60 символов")
		}
		if len(in.Desc) > 300 {
			return nil, errf("Описание шаблона слишком длинное")
		}
		if err := validateSiteTemplateRules(in.Rules); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, b := range siteTemplateBuiltins {
			if b.ID == in.ID {
				return nil, errf("Встроенный шаблон изменить нельзя: скопируйте его как свой")
			}
			if strings.EqualFold(b.Name, in.Name) {
				return nil, errf("Шаблон с таким названием уже есть")
			}
		}
		idx := -1
		for i, t := range store.Settings.SiteTemplates {
			if t.ID == in.ID && in.ID != "" {
				idx = i
			} else if strings.EqualFold(t.Name, in.Name) {
				return nil, errf("Шаблон с таким названием уже есть")
			}
		}
		if len(store.Settings.SiteTemplates) >= 50 && idx < 0 {
			return nil, errf("Не больше 50 своих шаблонов")
		}
		prev := append([]SiteTemplate(nil), store.Settings.SiteTemplates...)
		t := SiteTemplate{Name: in.Name, Desc: in.Desc, Rules: strings.ReplaceAll(in.Rules, "\r\n", "\n"), Created: time.Now()}
		if idx >= 0 {
			t.ID, t.Created = in.ID, prev[idx].Created
			store.Settings.SiteTemplates[idx] = t
		} else {
			t.ID = "u-" + strings.ToLower(randToken(4))
			store.Settings.SiteTemplates = append(store.Settings.SiteTemplates, t)
		}
		// Сайты, которые используют шаблон, получают новую конфигурацию; при ошибке nginx всё возвращается.
		if err := applySiteTemplateTo(sitesUsingTemplate(t.ID)); err != nil {
			store.Settings.SiteTemplates = prev
			return nil, err
		}
		if err := saveChecked(); err != nil {
			store.Settings.SiteTemplates = prev
			return nil, err
		}
		return map[string]any{"id": t.ID, "sites": len(sitesUsingTemplate(t.ID))}, nil
	})
	apiOp("site.templates.delete", "Шаблон сайта: удаление", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if n := len(sitesUsingTemplate(in.ID)); n > 0 {
			return nil, errf("Шаблон используют сайты (%d). Сначала выберите для них другой шаблон", n)
		}
		out := store.Settings.SiteTemplates[:0:0]
		found := false
		for _, t := range store.Settings.SiteTemplates {
			if t.ID == in.ID {
				found = true
				continue
			}
			out = append(out, t)
		}
		if !found {
			return nil, errf("Шаблон не найден")
		}
		store.Settings.SiteTemplates = out
		return nil, saveChecked()
	})
	api("site.templates.preview", func(q *Req) (any, error) {
		var in struct{ Domain, ID, Rules string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		s := findSite(in.Domain)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		if in.Rules == "" {
			t, ok := siteTemplateByID(in.ID)
			if !ok {
				return nil, errf("Шаблон не найден")
			}
			in.Rules = t.Rules
		}
		if err := validateSiteTemplateRules(in.Rules); err != nil {
			return nil, err
		}
		u := findUser(s.User)
		if u == nil {
			return nil, errf("Нет пользователя %s", s.User)
		}
		clone := *s
		clone.Template = "preview"
		siteTemplatePreview = &SiteTemplate{ID: "preview", Name: "Просмотр", Rules: in.Rules}
		defer func() { siteTemplatePreview = nil }()
		custom, _ := os.ReadFile(P(customPath(s.Domain)))
		b, err := renderSiteNginx(&clone, u, string(custom))
		if err != nil {
			return nil, err
		}
		return map[string]string{"config": string(b)}, nil
	})
}

// siteTemplatePreview подменяет шаблон при просмотре (вызывается под mu).
var siteTemplatePreview *SiteTemplate

func countSitesWithoutTemplate() int {
	n := 0
	for _, s := range store.Sites {
		if s.Template == "" {
			n++
		}
	}
	return n
}

// siteTemplateChoices — список для выбора в форме сайта: id, название, описание.
func siteTemplateChoices() []map[string]string {
	mu.Lock()
	defer mu.Unlock()
	var out []map[string]string
	for _, t := range siteTemplateBuiltins {
		out = append(out, map[string]string{"id": map[bool]string{true: "", false: t.ID}[t.ID == "plain"], "name": t.Name, "desc": t.Desc, "name_en": siteTemplateEn[t.ID][0], "desc_en": siteTemplateEn[t.ID][1]})
	}
	for _, t := range store.Settings.SiteTemplates {
		out = append(out, map[string]string{"id": t.ID, "name": t.Name, "desc": t.Desc})
	}
	return out
}
