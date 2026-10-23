// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPExtCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range phpExtCatalog {
		if seen[e.Name] {
			t.Errorf("повтор %s", e.Name)
		}
		seen[e.Name] = true
		if e.Title == "" || e.Group == "" {
			t.Errorf("%s: нет названия или раздела", e.Name)
		}
		if phpExtTitleEn[e.Name] == "" {
			t.Errorf("%s: нет английского названия", e.Name)
		}
		if phpExtGroupEn[e.Group] == "" {
			t.Errorf("раздел %q без английского названия", e.Group)
		}
		if e.Special == "" && !regexpExtName.MatchString(e.Name) {
			t.Errorf("%s: недопустимое имя", e.Name)
		}
	}
	if len(phpExtCatalog) < 70 {
		t.Errorf("расширений мало: %d", len(phpExtCatalog))
	}
	for _, must := range []string{"ioncube", "redis", "imagick", "xdebug", "intl", "gd", "mbstring", "opcache", "swoole", "mongodb"} {
		if phpExtFind(must) == nil {
			t.Errorf("нет %s", must)
		}
	}
}

var regexpExtName = reExtName()

func reExtName() interface{ MatchString(string) bool } { return nameChecker{} }

type nameChecker struct{}

func (nameChecker) MatchString(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return s != ""
}

func TestPHPExtPackageNames(t *testing.T) {
	old := osInfo
	defer func() { osInfo = old }()
	osInfo.Family = "debian"
	for ext, want := range map[string]string{"redis": "php8.3-redis", "gd": "php8.3-gd", "mysql": "php8.3-mysql", "sodium": "php8.3-common", "pcntl": "php8.3-common"} {
		if got := phpExtPackage("8.3", ext); got != want {
			t.Errorf("debian %s: %s, ждали %s", ext, got, want)
		}
	}
	osInfo.Family = "rhel"
	for ext, want := range map[string]string{"redis": "php83-php-pecl-redis", "gd": "php83-php-gd", "mysql": "php83-php-mysqlnd", "pcntl": "php83-php-process", "curl": "php83-php-common", "imagick": "php83-php-pecl-imagick"} {
		if got := phpExtPackage("8.3", ext); got != want {
			t.Errorf("rhel %s: %s, ждали %s", ext, got, want)
		}
	}
}

func TestIoncubeSupportAndIni(t *testing.T) {
	for v, want := range map[string]bool{"5.6": true, "7.4": true, "8.3": true, "8.5": true, "9.0": false, "5.5": false} {
		if got, _ := ioncubeSupported(v); got != want {
			t.Errorf("ionCube для PHP %s: %v", v, got)
		}
	}
	ini := ioncubeIni("/usr/lib/php/20230831/ioncube_loader_lin_8.3.so")
	if !strings.Contains(ini, "zend_extension=/usr/lib/php/20230831/ioncube_loader_lin_8.3.so") || !strings.Contains(ini, "priority=01") {
		t.Fatalf("ini: %q", ini)
	}
	old := osInfo
	defer func() { osInfo = old }()
	osInfo.Family = "debian"
	if ioncubeIniPath("8.3") != "/etc/php/8.3/mods-available/ioncube.ini" {
		t.Fatal(ioncubeIniPath("8.3"))
	}
	osInfo.Family = "rhel"
	if ioncubeIniPath("8.3") != "/etc/opt/remi/php83/php.d/00-ioncube.ini" {
		t.Fatal(ioncubeIniPath("8.3"))
	}
}

func TestPHPExtStatesFromConfigFiles(t *testing.T) {
	testPanel(t)
	cfg.Dry = false
	defer func() { cfg.Dry = true }()
	old := osInfo
	defer func() { osInfo = old }()
	osInfo.Family = "debian"
	dir := P("/etc/php/8.3/mods-available")
	os.MkdirAll(dir, 0755)
	for _, n := range []string{"redis", "gd", "ioncube"} {
		os.WriteFile(filepath.Join(dir, n+".ini"), []byte("extension="+n+".so\n"), 0644)
	}
	loaded := map[string]bool{"gd": true, "mbstring": true, "mysqli": true}
	st := phpExtStates("8.3", loaded, false)
	if s := st["gd"]; !s.Installed || !s.Enabled {
		t.Errorf("gd: %+v", s)
	}
	if s := st["redis"]; !s.Installed || s.Enabled {
		t.Errorf("redis должен быть установлен, но выключен: %+v", s)
	}
	if s := st["imagick"]; s.Installed || s.Enabled {
		t.Errorf("imagick: %+v", s)
	}
	if s := st["mysql"]; !s.Installed || !s.Enabled {
		t.Errorf("mysql (mysqli загружен): %+v", s)
	}
	if s := st["ioncube"]; !s.Installed || s.Enabled {
		t.Errorf("ionCube: файл есть, но не загружен: %+v", s)
	}
	if s := phpExtStates("8.3", loaded, true)["ioncube"]; !s.Enabled {
		t.Error("ionCube загружен, но не определён")
	}
}

func TestPHPExtRhelDisableEnableRenamesIni(t *testing.T) {
	testPanel(t)
	cfg.Dry = false
	defer func() { cfg.Dry = true }()
	old := osInfo
	defer func() { osInfo = old }()
	osInfo.Family = "rhel"
	osInfo.Systemd = false
	dir := P("/etc/opt/remi/php83/php.d")
	os.MkdirAll(dir, 0755)
	f := filepath.Join(dir, "40-redis.ini")
	os.WriteFile(f, []byte("extension=redis.so\n"), 0644)
	// service() в тесте не должен трогать систему: проверяем только переименование
	_ = phpExtSetEnabled("8.3", "redis", false)
	if _, err := os.Stat(f + ".disabled"); err != nil {
		t.Fatalf("файл не выключен: %v", err)
	}
	_ = phpExtSetEnabled("8.3", "redis", true)
	if _, err := os.Stat(f); err != nil {
		t.Fatalf("файл не включён обратно: %v", err)
	}
}

func TestPHPExtAPIDryActions(t *testing.T) {
	mux := testPanel(t)
	_, r := testAPI(t, mux, "php.ini", `{"version":"8.3"}`)
	d := r["data"].(map[string]any)
	cat := d["catalog"].([]any)
	if len(cat) < 70 {
		t.Fatalf("каталог: %d", len(cat))
	}
	var ion map[string]any
	for _, x := range cat {
		m := x.(map[string]any)
		if m["name"] == "ioncube" {
			ion = m
		}
	}
	if ion == nil || ion["available"] != true {
		t.Fatalf("ionCube: %v", ion)
	}
	for _, a := range []string{"install", "disable", "enable", "remove"} {
		if _, r := testAPI(t, mux, "php.ext", `{"version":"8.3","ext":"ioncube","action":"`+a+`"}`); r["error"] != nil {
			t.Errorf("%s: %v", a, r["error"])
		}
	}
	if _, r := testAPI(t, mux, "php.ext", `{"version":"8.3","ext":"nope","action":"install"}`); r["error"] == nil {
		t.Error("неизвестный модуль принят")
	}
	if _, r := testAPI(t, mux, "php.ext", `{"version":"8.4","ext":"redis","action":"install"}`); r["error"] == nil {
		t.Error("PHP 8.4 не установлен, а действие принято")
	}
}
