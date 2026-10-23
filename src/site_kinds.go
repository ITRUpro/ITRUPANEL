// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

// Тип сайта при создании: один вопрос вместо отдельного выбора режима, PHP, шаблона и корневой папки.
// Тип задаёт значения по умолчанию только для нового сайта; всё можно поменять в форме и позже.

type siteKind struct {
	ID, Name, Desc, NameEn, DescEn string
	Mode, Template, Index          string
	TryIndex                       bool
	RootSub                        string // подпапка корня внутри папки сайта (Laravel — public)
}

var siteKinds = []siteKind{
	{ID: "php", Name: "Обычный сайт на PHP", Desc: "Свой код на PHP или конструктор. Режим nginx + PHP-FPM, красивые ссылки включены.",
		NameEn: "Regular PHP website", DescEn: "Your own PHP code or a site builder. nginx + PHP-FPM mode, pretty links on.",
		Mode: "php", Index: "index.php index.html index.htm", TryIndex: true},
	{ID: "wordpress", Name: "WordPress", Desc: "Шаблон WordPress: красивые ссылки, закрыты wp-config.php и запуск PHP из загрузок.",
		NameEn: "WordPress", DescEn: "The WordPress template: pretty permalinks, wp-config.php and PHP execution from uploads closed.",
		Mode: "php", Template: "wordpress", Index: "index.php index.html index.htm", TryIndex: true},
	{ID: "laravel", Name: "Laravel / Symfony", Desc: "Корень сайта — папка public, все запросы идут на index.php, служебные файлы закрыты.",
		NameEn: "Laravel / Symfony", DescEn: "The site root is the public folder, all requests go to index.php, service files are closed.",
		Mode: "php", Template: "laravel", Index: "index.php index.html", TryIndex: true, RootSub: "public"},
	{ID: "joomla", Name: "Joomla", Desc: "Шаблон Joomla: красивые ссылки, закрыты configuration.php, журналы и кэш.",
		NameEn: "Joomla", DescEn: "The Joomla template: pretty links, configuration.php, logs and cache closed.",
		Mode: "php", Template: "joomla", Index: "index.php index.html index.htm", TryIndex: true},
	{ID: "bitrix", Name: "1С-Битрикс", Desc: "Шаблон Битрикс: маршрутизация через urlrewrite.php, закрыты служебные папки ядра.",
		NameEn: "1C-Bitrix", DescEn: "The Bitrix template: routing through urlrewrite.php, the core service folders closed.",
		Mode: "php", Template: "bitrix", Index: "index.php index.html index.htm"},
	{ID: "spa", Name: "Одностраничное приложение (React, Vue, Angular)", Desc: "Готовая сборка из файлов: все адреса открывают index.html. Без PHP.",
		NameEn: "Single-page application (React, Vue, Angular)", DescEn: "A built set of files: all addresses open index.html. No PHP.",
		Mode: "static", Template: "spa", Index: "index.html index.htm"},
	{ID: "static", Name: "Статический сайт (HTML)", Desc: "Простые HTML-страницы, картинки, стили. Без PHP.",
		NameEn: "Static website (HTML)", DescEn: "Plain HTML pages, images and styles. No PHP.",
		Mode: "static", Index: "index.html index.htm"},
	{ID: "app", Name: "Приложение (Node.js, Python, Go…)", Desc: "Сайт работает как программа на сервере; nginx передаёт ей запросы. Нужен адрес приложения.",
		NameEn: "Application (Node.js, Python, Go…)", DescEn: "The site runs as a program on the server; nginx passes requests to it. The application address is required.",
		Mode: "proxy"},
}

func siteKindByID(id string) (siteKind, bool) {
	for _, k := range siteKinds {
		if k.ID == id {
			return k, true
		}
	}
	return siteKind{}, false
}

// applySiteKind подставляет значения типа сайта в параметры нового сайта. Корневая папка меняется,
// только если её не выбрали вручную.
func applySiteKind(in *siteIn) error {
	if in.Kind == "" {
		return nil
	}
	k, ok := siteKindByID(in.Kind)
	if !ok {
		return errf("Неизвестный тип сайта")
	}
	if k.ID == "php" {
		// «Обычный сайт на PHP» ничего не навязывает: режим, версию PHP и веб-сервер выбирают в форме.
		return nil
	}
	in.Mode, in.Template, in.Backend, in.TryIndex = k.Mode, k.Template, "", k.TryIndex
	if k.Index != "" {
		in.Index = k.Index
	}
	if k.Mode != "php" {
		in.PHP = ""
	}
	return nil
}

func siteKindChoices() []map[string]string {
	var out []map[string]string
	for _, k := range siteKinds {
		out = append(out, map[string]string{"id": k.ID, "name": k.Name, "desc": k.Desc, "name_en": k.NameEn, "desc_en": k.DescEn, "mode": k.Mode, "template": k.Template, "index": k.Index, "root_sub": k.RootSub, "try_index": map[bool]string{true: "1", false: ""}[k.TryIndex]})
	}
	return out
}
