// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Каталог расширений PHP: ставятся из репозиториев Sury/PPA (Debian, Ubuntu) и Remi (AlmaLinux, Rocky, RHEL),
// включаются и выключаются без удаления. ionCube Loader скачивается с сайта разработчика по просьбе администратора.

type phpExtDef struct {
	Name    string   // идентификатор
	Title   string   // название для списка
	Group   string   // раздел списка
	Mods    []string // как расширение называется в «php -m» и в файлах настройки
	Deb     string   // суффикс пакета phpX.Y-<суффикс> (по умолчанию Name)
	Rhel    string   // суффикс пакета phpXY-php-<суффикс> (по умолчанию Name)
	Pecl    bool     // у Remi пакет называется phpXY-php-pecl-<Name>
	Builtin bool     // входит в основной пакет PHP: отдельно не ставится и не удаляется, только включается и выключается
	Special string   // особая установка: "ioncube"
}

func (e phpExtDef) mods() []string {
	if len(e.Mods) > 0 {
		return e.Mods
	}
	return []string{e.Name}
}

const (
	grpDB    = "Базы данных"
	grpCache = "Кэш и очереди"
	grpGfx   = "Графика и медиа"
	grpText  = "Строки, XML и данные"
	grpNet   = "Сеть и протоколы"
	grpSec   = "Шифрование и безопасность"
	grpDebug = "Отладка и профилирование"
	grpArch  = "Архивы"
	grpCode  = "Защита кода"
	grpMisc  = "Прочее"
)

var phpExtCatalog = []phpExtDef{
	{Name: "ioncube", Title: "ionCube Loader (запуск закодированных скриптов)", Group: grpCode, Mods: []string{"ioncube"}, Special: "ioncube"},

	{Name: "mysql", Title: "MySQL / MariaDB (mysqli, PDO)", Group: grpDB, Mods: []string{"mysqli", "pdo_mysql", "mysqlnd"}, Rhel: "mysqlnd"},
	{Name: "pgsql", Title: "PostgreSQL (pgsql, PDO)", Group: grpDB, Mods: []string{"pgsql", "pdo_pgsql"}},
	{Name: "sqlite3", Title: "SQLite (sqlite3, PDO)", Group: grpDB, Mods: []string{"sqlite3", "pdo_sqlite"}, Rhel: "pdo"},
	{Name: "mongodb", Title: "MongoDB", Group: grpDB, Pecl: true},
	{Name: "odbc", Title: "ODBC (odbc, PDO)", Group: grpDB, Mods: []string{"odbc", "pdo_odbc"}},
	{Name: "dba", Title: "DBA (Berkeley DB и аналоги)", Group: grpDB},
	{Name: "ldap", Title: "LDAP / Active Directory", Group: grpDB},
	{Name: "interbase", Title: "Firebird / InterBase", Group: grpDB, Mods: []string{"interbase", "pdo_firebird"}, Rhel: "pdo-firebird"},

	{Name: "opcache", Title: "OPcache (ускорение PHP)", Group: grpCache, Mods: []string{"opcache", "zend opcache"}},
	{Name: "apcu", Title: "APCu (кэш в памяти)", Group: grpCache, Pecl: true},
	{Name: "redis", Title: "Redis", Group: grpCache, Pecl: true},
	{Name: "memcached", Title: "Memcached", Group: grpCache, Pecl: true},
	{Name: "memcache", Title: "Memcache (старый клиент)", Group: grpCache, Pecl: true},
	{Name: "igbinary", Title: "igbinary (быстрая сериализация)", Group: grpCache, Pecl: true},
	{Name: "msgpack", Title: "MessagePack", Group: grpCache, Pecl: true},
	{Name: "lz4", Title: "LZ4 (сжатие)", Group: grpCache, Pecl: true},
	{Name: "zstd", Title: "Zstandard (сжатие)", Group: grpCache, Pecl: true},
	{Name: "amqp", Title: "AMQP / RabbitMQ", Group: grpCache, Pecl: true},
	{Name: "stomp", Title: "STOMP (брокеры сообщений)", Group: grpCache, Pecl: true},
	{Name: "zmq", Title: "ZeroMQ", Group: grpCache, Pecl: true},

	{Name: "gd", Title: "GD (изображения)", Group: grpGfx},
	{Name: "imagick", Title: "ImageMagick (imagick)", Group: grpGfx, Pecl: true},
	{Name: "gmagick", Title: "GraphicsMagick (gmagick)", Group: grpGfx, Pecl: true},
	{Name: "vips", Title: "libvips (быстрая обработка изображений)", Group: grpGfx, Pecl: true},

	{Name: "mbstring", Title: "mbstring (многобайтовые строки)", Group: grpText},
	{Name: "intl", Title: "intl (локализация, ICU)", Group: grpText},
	{Name: "xml", Title: "XML (dom, simplexml, xmlreader, xmlwriter)", Group: grpText, Mods: []string{"xml", "dom", "simplexml", "xmlreader", "xmlwriter"}},
	{Name: "xsl", Title: "XSL (преобразования XSLT)", Group: grpText},
	{Name: "xmlrpc", Title: "XML-RPC", Group: grpText},
	{Name: "yaml", Title: "YAML", Group: grpText, Pecl: true},
	{Name: "bcmath", Title: "BCMath (точные вычисления)", Group: grpText},
	{Name: "gmp", Title: "GMP (большие числа)", Group: grpText},
	{Name: "uuid", Title: "UUID", Group: grpText, Pecl: true},
	{Name: "ds", Title: "DS (структуры данных)", Group: grpText, Pecl: true},
	{Name: "mailparse", Title: "Mailparse (разбор писем)", Group: grpText, Pecl: true},
	{Name: "enchant", Title: "Enchant (проверка орфографии)", Group: grpText},
	{Name: "pspell", Title: "Pspell (проверка орфографии)", Group: grpText},
	{Name: "tidy", Title: "Tidy (чистка HTML)", Group: grpText},

	{Name: "curl", Title: "cURL (HTTP-запросы)", Group: grpNet, Rhel: "common"},
	{Name: "soap", Title: "SOAP", Group: grpNet},
	{Name: "ssh2", Title: "SSH2", Group: grpNet, Pecl: true},
	{Name: "snmp", Title: "SNMP", Group: grpNet},
	{Name: "imap", Title: "IMAP (почта)", Group: grpNet},
	{Name: "http", Title: "pecl_http (расширенный HTTP)", Group: grpNet, Pecl: true},
	{Name: "oauth", Title: "OAuth", Group: grpNet, Pecl: true},
	{Name: "raphf", Title: "raphf (ресурсы для pecl_http)", Group: grpNet, Pecl: true},
	{Name: "grpc", Title: "gRPC", Group: grpNet, Pecl: true},
	{Name: "protobuf", Title: "Protocol Buffers", Group: grpNet, Pecl: true},
	{Name: "smbclient", Title: "SMB (сетевые папки Windows)", Group: grpNet, Pecl: true},
	{Name: "swoole", Title: "Swoole (асинхронный сервер)", Group: grpNet, Pecl: true},
	{Name: "event", Title: "Event (libevent)", Group: grpNet, Pecl: true},
	{Name: "ev", Title: "Ev (libev)", Group: grpNet, Pecl: true},

	{Name: "gnupg", Title: "GnuPG", Group: grpSec, Pecl: true},
	{Name: "mcrypt", Title: "mcrypt (устаревшее шифрование)", Group: grpSec, Pecl: true},

	{Name: "xdebug", Title: "Xdebug (отладка)", Group: grpDebug, Pecl: true},
	{Name: "xhprof", Title: "XHProf (профилирование)", Group: grpDebug, Pecl: true},
	{Name: "pcov", Title: "PCOV (покрытие кода тестами)", Group: grpDebug, Pecl: true},
	{Name: "excimer", Title: "Excimer (профилирование)", Group: grpDebug, Pecl: true},
	{Name: "spx", Title: "SPX (профилировщик)", Group: grpDebug, Pecl: true},
	{Name: "uopz", Title: "uopz (подмена кода для тестов)", Group: grpDebug, Pecl: true},

	{Name: "zip", Title: "Zip", Group: grpArch},
	{Name: "bz2", Title: "Bzip2", Group: grpArch, Rhel: "common"},
	{Name: "rar", Title: "RAR", Group: grpArch, Pecl: true},

	{Name: "ffi", Title: "FFI (вызов библиотек C)", Group: grpMisc},
	{Name: "rrd", Title: "RRDtool", Group: grpMisc, Pecl: true},
	{Name: "solr", Title: "Apache Solr", Group: grpMisc, Pecl: true},
}

func phpExtFind(name string) *phpExtDef {
	for i := range phpExtCatalog {
		if phpExtCatalog[i].Name == name {
			return &phpExtCatalog[i]
		}
	}
	return nil
}

// phpExtPackage — имя пакета расширения в репозитории для версии PHP v.
func phpExtPackage(v, ext string) string {
	e := phpExtFind(ext)
	if e == nil {
		e = &phpExtDef{Name: ext}
	}
	if osInfo.Family == "rhel" {
		suffix := e.Rhel
		if suffix == "" {
			suffix = e.Name
			if e.Pecl {
				suffix = "pecl-" + e.Name
			}
		}
		return "php" + vv(v) + "-php-" + suffix
	}
	suffix := e.Deb
	if suffix == "" {
		suffix = e.Name
		if e.Builtin {
			suffix = "common"
		}
	}
	return "php" + v + "-" + suffix
}

func phpConfDir(v string) string {
	if osInfo.Family == "rhel" {
		return "/etc/opt/remi/php" + vv(v) + "/php.d"
	}
	return "/etc/php/" + v + "/mods-available"
}

// phpExtIniFiles — файлы настройки расширения (включённые и выключенные).
func phpExtIniFiles(v string, mods []string) []string {
	var out []string
	for _, m := range mods {
		if osInfo.Family == "rhel" {
			g1, _ := filepath.Glob(P(phpConfDir(v) + "/*-" + m + ".ini"))
			g2, _ := filepath.Glob(P(phpConfDir(v) + "/*-" + m + ".ini.disabled"))
			out = append(append(out, g1...), g2...)
		} else if fileExists(phpConfDir(v) + "/" + m + ".ini") {
			out = append(out, P(phpConfDir(v)+"/"+m+".ini"))
		}
	}
	return out
}

type phpExtState struct {
	Installed bool
	Enabled   bool
}

// phpExtStates определяет, что установлено и что включено. loaded — имена из «php -m» в нижнем регистре.
func phpExtStates(v string, loaded map[string]bool, ionLoaded bool) map[string]phpExtState {
	out := map[string]phpExtState{}
	for _, e := range phpExtCatalog {
		var st phpExtState
		if e.Special == "ioncube" {
			st.Enabled = ionLoaded
			st.Installed = ionLoaded || len(phpExtIniFiles(v, []string{"ioncube"})) > 0
		} else {
			for _, m := range e.mods() {
				if loaded[strings.ToLower(m)] {
					st.Enabled = true
				}
			}
			st.Installed = st.Enabled || len(phpExtIniFiles(v, e.mods())) > 0
		}
		out[e.Name] = st
	}
	return out
}

// phpExtOverview — список для окна «Модули»: каждому расширению соответствует состояние.
func phpExtOverview(v string) []map[string]any {
	loaded := map[string]bool{}
	ion := false
	if cfg.Dry {
		for _, m := range []string{"mysqli", "pdo_mysql", "mysqlnd", "gd", "curl", "mbstring", "xml", "dom", "zip", "intl", "zend opcache", "sodium", "ftp", "sockets", "iconv", "exif", "gettext", "calendar", "readline", "bz2", "pcntl", "posix", "shmop"} {
			loaded[m] = true
		}
	} else {
		out, _ := run(phpCLI(v), "-m")
		for _, m := range strings.Fields(out) {
			if !strings.HasPrefix(m, "[") {
				loaded[strings.ToLower(m)] = true
			}
		}
		vout, _ := run(phpCLI(v), "-v")
		ion = strings.Contains(strings.ToLower(vout), "ioncube")
	}
	states := phpExtStates(v, loaded, ion)
	var rows []map[string]any
	for _, e := range phpExtCatalog {
		st := states[e.Name]
		row := map[string]any{"name": e.Name, "title": e.Title, "title_en": phpExtTitleEn[e.Name], "group": e.Group, "group_en": phpExtGroupEn[e.Group], "installed": st.Installed, "enabled": st.Enabled, "builtin": e.Builtin}
		if e.Special == "ioncube" {
			ok, why := ioncubeSupported(v)
			row["available"] = ok
			if !ok {
				row["note"] = why
				row["note_en"] = englishText(why)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// phpExtSetEnabled включает или выключает расширение, не удаляя его.
func phpExtSetEnabled(v, ext string, on bool) error {
	e := phpExtFind(ext)
	if e == nil {
		return errf("Неизвестный модуль")
	}
	if cfg.Dry {
		return nil
	}
	files := phpExtIniFiles(v, e.mods())
	if len(files) == 0 {
		return errf("Модуль %s не установлен", ext)
	}
	if osInfo.Family == "rhel" {
		for _, f := range files {
			switch {
			case on && strings.HasSuffix(f, ".ini.disabled"):
				if err := os.Rename(f, strings.TrimSuffix(f, ".disabled")); err != nil {
					return err
				}
			case !on && strings.HasSuffix(f, ".ini"):
				if err := os.Rename(f, f+".disabled"); err != nil {
					return err
				}
			}
		}
	} else {
		cmd := "phpdismod"
		if on {
			cmd = "phpenmod"
		}
		for _, m := range e.mods() {
			if fileExists(phpConfDir(v) + "/" + m + ".ini") {
				if out, err := run(cmd, "-v", v, "-s", "ALL", m); err != nil {
					return errf("%s: %s", cmd, firstLine(out+" "+err.Error()))
				}
			}
		}
	}
	return phpExtRestart(v)
}

// ---------- ionCube Loader ----------

var ioncubeURLBase = "https://downloads.ioncube.com/loader_downloads/"

// Версии PHP, для которых у ionCube есть загрузчик.
var ioncubeVersions = map[string]bool{"5.6": true, "7.0": true, "7.1": true, "7.2": true, "7.3": true, "7.4": true, "8.0": true, "8.1": true, "8.2": true, "8.3": true, "8.4": true, "8.5": true}

func ioncubeSupported(v string) (bool, string) {
	if !ioncubeVersions[v] {
		return false, "Для этой версии PHP загрузчика ionCube нет"
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return false, "Для этой архитектуры загрузчика ionCube нет"
	}
	return true, ""
}

func ioncubeArchive() string {
	if runtime.GOARCH == "arm64" {
		return "ioncube_loaders_lin_aarch64.tar.gz"
	}
	return "ioncube_loaders_lin_x86-64.tar.gz"
}

func ioncubeIniPath(v string) string {
	if osInfo.Family == "rhel" {
		return phpConfDir(v) + "/00-ioncube.ini"
	}
	return phpConfDir(v) + "/ioncube.ini"
}

// ioncubeIni — содержимое файла настройки. Загрузчик должен подключаться раньше остальных расширений Zend.
func ioncubeIni(soPath string) string {
	return "; priority=01\n; ionCube Loader, установлен ITRUPANEL\nzend_extension=" + soPath + "\n"
}

var reExtDir = regexp.MustCompile(`^/[A-Za-z0-9_./+-]+$`)

func ioncubeInstall(ctx context.Context, v string) error {
	if ok, why := ioncubeSupported(v); !ok {
		return errf("%s", why)
	}
	dirOut := "/usr/lib/php/20230831"
	if !cfg.Dry {
		out, err := run(phpCLI(v), "-r", "echo ini_get('extension_dir');")
		dirOut = strings.TrimSpace(out)
		if err != nil || !reExtDir.MatchString(dirOut) {
			return errf("Не удалось определить папку расширений PHP %s", v)
		}
	}
	if cfg.Dry {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", ioncubeURLBase+ioncubeArchive(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return errf("ionCube: не удалось скачать загрузчик с downloads.ioncube.com: %s", firstLine(err.Error()))
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errf("ionCube: сервер ответил %s", res.Status)
	}
	tmp, err := os.MkdirTemp("", "ioncube-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	arch := filepath.Join(tmp, "loaders.tar.gz")
	f, err := os.OpenFile(arch, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, io.LimitReader(res.Body, 64<<20)); err != nil {
		f.Close()
		return errf("ionCube: загрузка прервана: %s", firstLine(err.Error()))
	}
	f.Close()
	so := "ioncube_loader_lin_" + v + ".so"
	if out, err := run("tar", "-xzf", arch, "-C", tmp, "ioncube/"+so); err != nil {
		return errf("ionCube: в архиве нет загрузчика для PHP %s (%s)", v, firstLine(out))
	}
	dst := filepath.Join(P(dirOut), so)
	data, err := os.ReadFile(filepath.Join(tmp, "ioncube", so))
	if err != nil {
		return err
	}
	if err = writeFileAtomic(dst, data, 0644); err != nil {
		return err
	}
	if err = writeFile(ioncubeIniPath(v), []byte(ioncubeIni(dirOut+"/"+so)), 0644); err != nil {
		return err
	}
	if osInfo.Family != "rhel" {
		if out, err := run("phpenmod", "-v", v, "-s", "ALL", "ioncube"); err != nil {
			return errf("phpenmod: %s", firstLine(out+" "+err.Error()))
		}
	}
	if err := phpExtRestart(v); err != nil {
		return err
	}
	if out, _ := run(phpCLI(v), "-v"); !strings.Contains(strings.ToLower(out), "ioncube") {
		return errf("ionCube установлен, но PHP %s его не загрузил. Проверьте файл %s", v, ioncubeIniPath(v))
	}
	return nil
}

func ioncubeRemove(v string) error {
	if cfg.Dry {
		return nil
	}
	if osInfo.Family != "rhel" {
		run("phpdismod", "-v", v, "-s", "ALL", "ioncube")
	}
	for _, f := range phpExtIniFiles(v, []string{"ioncube"}) {
		os.Remove(f)
	}
	if osInfo.Family == "rhel" {
		os.Remove(P(ioncubeIniPath(v) + ".disabled"))
	}
	if out, err := run(phpCLI(v), "-r", "echo ini_get('extension_dir');"); err == nil {
		dir := strings.TrimSpace(out)
		if reExtDir.MatchString(dir) {
			os.Remove(filepath.Join(P(dir), "ioncube_loader_lin_"+v+".so"))
		}
	}
	return phpExtRestart(v)
}

func phpExtInstall(v, ext string) error {
	e := phpExtFind(ext)
	if e == nil {
		return errf("Неизвестный модуль")
	}
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if e.Special == "ioncube" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		return ioncubeInstall(ctx, v)
	}
	if e.Builtin {
		// Входит в основной пакет PHP: нужно только включить.
		return phpExtSetEnabledLocked(v, ext, true)
	}
	if err := pkgInstall(phpExtPackage(v, ext)); err != nil {
		return errf("Модуль %s для PHP %s не установлен: %s", ext, v, phpExtFriendly(err))
	}
	return phpExtRestart(v)
}

// phpExtFriendly объясняет частую причину неудачи: у некоторых версий PHP нет такого пакета.
func phpExtFriendly(err error) string {
	s := firstLine(err.Error())
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "unable to locate package") || strings.Contains(low, "no match for argument") || strings.Contains(low, "nothing provides") {
		return "пакета нет в репозитории для этой версии PHP или системы"
	}
	return s
}

func phpExtSetEnabledLocked(v, ext string, on bool) error { return phpExtSetEnabled(v, ext, on) }

func phpExtRemove(v, ext string) error {
	e := phpExtFind(ext)
	if e == nil {
		return errf("Неизвестный модуль")
	}
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if e.Special == "ioncube" {
		return ioncubeRemove(v)
	}
	if e.Builtin {
		return errf("Модуль %s входит в основной пакет PHP: его можно только выключить", ext)
	}
	pkg := phpExtPackage(v, ext)
	var err error
	if osInfo.Family == "rhel" {
		_, err = run("dnf", "remove", "-y", pkg)
	} else {
		_, err = run("apt-get", "purge", "-y", pkg)
	}
	if err != nil {
		return err
	}
	return phpExtRestart(v)
}

// phpExtRestart перезапускает PHP-FPM; systemd отказывает после нескольких быстрых перезапусков подряд
// (например, установка пакета сама перезапускает службу), поэтому счётчик сбоев сбрасывается заранее.
func phpExtRestart(v string) error {
	if osInfo.Systemd && !cfg.Dry {
		run("systemctl", "reset-failed", phpService(v))
	}
	return service("restart", phpService(v))
}

func phpExtNames() []string {
	var out []string
	for _, e := range phpExtCatalog {
		out = append(out, e.Name)
	}
	return out
}
