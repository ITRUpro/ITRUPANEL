// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

// Debian/Ubuntu должны получать ровно прежние пути и пакеты; RHEL — свои.
func TestMailDistroPaths(t *testing.T) {
	saved, dry := osInfo, cfg.Dry
	defer func() { osInfo, cfg.Dry = saved, dry }()
	cfg.Dry = false

	osInfo = OSInfo{ID: "debian", Version: "12", Family: "debian"}
	if share, conf := roundcubePaths(); share != "/usr/share/roundcube" || conf != "/etc/roundcube" {
		t.Fatalf("Debian Roundcube: %s %s", share, conf)
	}
	if bin, user := spamRedisServer(); bin != "/usr/bin/redis-server" || user != "redis" || spamRedisPackage() != "redis-server" {
		t.Fatalf("Debian Redis: %s %s %s", bin, user, spamRedisPackage())
	}
	if mailFileGroup("/etc/roundcube/config.inc.php") != "itruroundcube" || mailFileMode("/etc/roundcube/config.inc.php") != 0640 || mailFileGroup("/etc/itrupanel/rspamd-redis.conf") != "redis" {
		t.Fatal("Debian: права файлов почты изменились")
	}
	if selinuxOn() {
		t.Fatal("SELinux на Debian")
	}

	osInfo = OSInfo{ID: "almalinux", Version: "10.2", Family: "rhel"}
	if share, conf := roundcubePaths(); share != "/usr/share/roundcubemail" || conf != "/etc/roundcubemail" {
		t.Fatalf("RHEL Roundcube: %s %s", share, conf)
	}
	if spamRedisPackage() != "valkey" {
		t.Fatalf("RHEL 10 Redis: %s", spamRedisPackage())
	}
	if mailFileGroup("/etc/roundcubemail/config.inc.php") != "itruroundcube" || mailFileMode("/etc/roundcubemail/config.inc.php") != 0640 {
		t.Fatal("RHEL: права настроек Roundcube")
	}
	osInfo.Version = "9.6"
	if spamRedisPackage() != "redis" {
		t.Fatalf("RHEL 9 Redis: %s", spamRedisPackage())
	}
	files := roundcubeTimerFiles("8.3")
	if !strings.Contains(string(files["/etc/systemd/system/itrupanel-roundcube-cleandb.service"]), "/usr/share/roundcubemail/bin/cleandb.sh") {
		t.Fatalf("таймеры Roundcube: %v", files)
	}
}
