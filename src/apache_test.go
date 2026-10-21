// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mod_php, чей .so удалён вместе с PHP, ломал apache2ctl -t; рабочий модуль не трогаем.
func TestApacheDisableBrokenModPHP(t *testing.T) {
	testPanel(t)
	// a2dismod недоступен — проверяем и запасной путь с удалением ссылок
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "a2dismod"), []byte("#!/bin/sh\nexit 1\n"), 0755)
	t.Setenv("PATH", bin)
	oldDry := cfg.Dry
	cfg.Dry = false
	defer func() { cfg.Dry = oldDry }()
	writeFile("/etc/apache2/mods-enabled/php7.4.load", []byte("LoadModule php7_module /usr/lib/apache2/modules/libphp7.4.so\n"), 0644)
	writeFile("/etc/apache2/mods-enabled/php7.4.conf", []byte("# php\n"), 0644)
	writeFile("/etc/apache2/mods-enabled/php8.3.load", []byte("LoadModule php_module /usr/lib/apache2/modules/libphp8.3.so\n"), 0644)
	writeFile("/usr/lib/apache2/modules/libphp8.3.so", []byte("so"), 0644)
	apacheDisableModPHP(false)
	if fileExists("/etc/apache2/mods-enabled/php7.4.load") || fileExists("/etc/apache2/mods-enabled/php7.4.conf") {
		t.Fatal("broken mod_php left enabled")
	}
	if !fileExists("/etc/apache2/mods-enabled/php8.3.load") {
		t.Fatal("working mod_php disabled")
	}
	apacheDisableModPHP(true)
	if fileExists("/etc/apache2/mods-enabled/php8.3.load") {
		t.Fatal("mod_php left enabled for PHP-FPM setup")
	}
}

// Apache, поставленный пакетом Roundcube/php, не мешает панели: mod_php и /roundcube отключаются,
// а Apache, не настроенный панелью, останавливается.
func TestCleanupPackagedApache(t *testing.T) {
	testPanel(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("CALLS", log)
	for _, name := range []string{"a2dismod", "a2disconf", "a2enmod", "systemctl", "apache2ctl"} {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> \"$CALLS\"\n"), 0755)
	}
	t.Setenv("PATH", bin)
	oldDry, oldSystemd, oldFamily := cfg.Dry, osInfo.Systemd, osInfo.Family
	cfg.Dry, osInfo.Systemd, osInfo.Family = false, true, "debian"
	defer func() { cfg.Dry, osInfo.Systemd, osInfo.Family = oldDry, oldSystemd, oldFamily }()
	writeFile("/usr/sbin/apache2", []byte("bin"), 0755)
	writeFile("/etc/apache2/mods-enabled/php8.4.load", []byte("LoadModule php_module /usr/lib/apache2/modules/libphp8.4.so\n"), 0644)
	writeFile("/usr/lib/apache2/modules/libphp8.4.so", []byte("so"), 0644)

	cleanupPackagedApache()
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"a2disconf -q roundcube", "a2dismod -f php8.4", "systemctl stop apache2", "systemctl disable apache2"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("нет %q:\n%s", want, calls)
		}
	}
	if strings.Contains(string(calls), "mpm_event") {
		t.Fatalf("Apache не панели не должен перенастраиваться:\n%s", calls)
	}

	// Apache панели: вернуть mpm_event и перезапустить
	os.Remove(log)
	writeFile("/etc/apache2/conf-enabled/itrupanel.conf", []byte("# ITRUPANEL\n"), 0644)
	cleanupPackagedApache()
	calls, _ = os.ReadFile(log)
	for _, want := range []string{"a2dismod -f -q mpm_prefork mpm_worker", "a2enmod -q mpm_event", "apache2ctl -t", "systemctl restart apache2"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("нет %q:\n%s", want, calls)
		}
	}
}
