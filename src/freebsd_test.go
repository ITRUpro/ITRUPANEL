// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

// asFreeBSD включает семейство freebsd на время теста.
func asFreeBSD(t *testing.T) {
	t.Helper()
	old, oldSock, oldFW, oldGuard := osInfo, sockDir, fwConfig, ftpGuardPath
	osInfo = OSInfo{ID: "freebsd", Family: bsdFamily, WebUser: "www"}
	sockDir, fwConfig = bsdSockDir, "/etc/itrupanel/firewall.pf"
	t.Cleanup(func() { osInfo, sockDir, fwConfig, ftpGuardPath = old, oldSock, oldFW, oldGuard })
}

func TestBSDServiceNames(t *testing.T) {
	cases := map[string]string{
		"nginx": "nginx", "mariadb": "mysql-server", "cron": "cron", "crond": "cron", "pure-ftpd": "pure-ftpd",
		"php_fpm": "php_fpm", "php8.3-fpm": "php_fpm", "apache2": "apache24", "opendkim": "milter-opendkim",
		"itrupanel-spam-redis": "itrupanel_spam_redis", "postfix": "postfix", "named": "nsd",
	}
	for in, want := range cases {
		if got := bsdRCName(in); got != want {
			t.Errorf("bsdRCName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBSDPHPLayout(t *testing.T) {
	asFreeBSD(t)
	if phpService("8.4") != "php_fpm" || phpPoolDir("8.4") != "/usr/local/etc/php-fpm.d" || phpIni("8.4") != "/usr/local/etc/php.ini" {
		t.Fatalf("phpService/phpPoolDir/phpIni = %q %q %q", phpService("8.4"), phpPoolDir("8.4"), phpIni("8.4"))
	}
	if phpFPMBin("8.4") != "/usr/local/sbin/php-fpm" || phpCLI("8.4") != "/usr/local/bin/php" {
		t.Fatalf("binaries %q %q", phpFPMBin("8.4"), phpCLI("8.4"))
	}
	pk := strings.Join(phpPackages("8.4"), " ")
	for _, want := range []string{"php84 ", "php84-mysqli", "php84-gd", "php84-pecl-imagick"} {
		if !strings.Contains(pk+" ", want) {
			t.Errorf("пакеты PHP не содержат %q: %s", want, pk)
		}
	}
	if got := phpAllVersions(); len(got) != 4 || got[0] != "8.2" || got[3] != "8.5" {
		t.Errorf("версии PHP на FreeBSD: %v", got)
	}
}

func TestBSDExtensionPackages(t *testing.T) {
	asFreeBSD(t)
	cases := map[string]string{
		"mysql": "php83-mysqli php83-pdo_mysql", "redis": "php83-pecl-redis", "apcu": "php83-pecl-APCu",
		"imagick": "php83-pecl-imagick", "gd": "php83-gd", "exif": "php83-exif", "zstd": "php83-zstd",
	}
	for name, want := range cases {
		e := phpExtFind(name)
		if e == nil {
			t.Fatalf("нет расширения %s", name)
		}
		if got := strings.Join(phpExtPackagesBSD("8.3", e), " "); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if ok, why := ioncubeSupported("8.3"); ok || why == "" {
		t.Error("ionCube на FreeBSD должен быть недоступен с пояснением")
	}
}

func TestBSDFirewallScript(t *testing.T) {
	asFreeBSD(t)
	state := FirewallState{Ready: true, Enabled: true, Rules: []FirewallRule{
		{Name: "web", Action: "allow", Protocol: "tcp", Port: "80", Source: "*", Enabled: true},
		{Name: "admin only", Action: "allow_selected", Protocol: "tcp", Port: "8000-8100", Source: "*", Addresses: []string{"203.0.113.0/24", "2001:db8::/32"}, Enabled: true},
		{Name: "off", Action: "deny", Protocol: "udp", Port: "53", Source: "*", Enabled: false},
		{Name: "ping", Action: "deny", Protocol: "icmp", Source: "198.51.100.7", Enabled: true},
	}}
	got, err := fwScript(state, []int{22, 3333})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pass in quick on lo0 all",
		"port { 22, 3333 } keep state",
		"pass in quick proto tcp from any to any port 80 keep state",
		"pass in quick proto tcp from 203.0.113.0/24 to any port 8000:8100 keep state",
		"pass in quick proto tcp from 2001:db8::/32 to any port 8000:8100 keep state",
		"block drop in quick proto tcp from any to any port 8000:8100",
		"block drop in quick proto icmp from 198.51.100.7/32 to any",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в правилах pf нет %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "udp from any to any port 53") {
		t.Errorf("выключенное правило попало в pf:\n%s", got)
	}
	// управляющие порты идут раньше пользовательских правил: блокировка не отрежет панель и SSH
	if strings.Index(got, "port { 22, 3333 }") > strings.Index(got, "port 80") {
		t.Error("порты управления должны быть раньше правил")
	}
	off, _ := fwScript(FirewallState{}, nil)
	if strings.Contains(off, "pass") || strings.Contains(off, "block") {
		t.Errorf("пустой набор не должен содержать правил: %q", off)
	}
}

func TestPureConfApply(t *testing.T) {
	conf := "# ChrootEveryone yes\nNoAnonymous no\nLimitRecursion 10000 8\n"
	got := pureConfApply(conf, map[string]string{"ChrootEveryone": "yes", "NoAnonymous": "yes", "TLS": "1"})
	for _, want := range []string{"ChrootEveryone    yes", "NoAnonymous    yes", "LimitRecursion 10000 8", "TLS    1"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "NoAnonymous") != 1 || strings.Count(got, "ChrootEveryone") != 1 {
		t.Errorf("значения продублированы:\n%s", got)
	}
	if again := pureConfApply(got, map[string]string{"ChrootEveryone": "yes", "NoAnonymous": "yes", "TLS": "1"}); again != got {
		t.Errorf("повторное применение меняет файл:\n%s\n---\n%s", got, again)
	}
}

func TestBSDApacheConfig(t *testing.T) {
	asFreeBSD(t)
	c := bsdApacheConfig()
	for _, want := range []string{bsdApacheMark, "Listen 127.0.0.1:8080", "LoadModule mpm_event_module libexec/apache24/mod_mpm_event.so", "LoadModule proxy_fcgi_module", "LoadModule remoteip_module", "User www", "/usr/local/etc/apache24/itrupanel/*.conf"} {
		if !strings.Contains(c, want) {
			t.Errorf("в конфигурации Apache нет %q", want)
		}
	}
	if strings.Contains(c, "mpm_prefork") {
		t.Error("Apache за nginx должен работать на mpm_event")
	}
	if apacheSiteDir() != "/usr/local/etc/apache24/itrupanel" {
		t.Errorf("apacheSiteDir = %q", apacheSiteDir())
	}
}

func TestBSDPathsAndFlags(t *testing.T) {
	asFreeBSD(t)
	if dnsDir() != "/usr/local/etc/nsd/itrupanel" || dnsZonesConf() != "/usr/local/etc/nsd/itrupanel-zones.conf" {
		t.Errorf("пути DNS: %q %q", dnsDir(), dnsZonesConf())
	}
	if f, unit := duFlags(); f != "-sxAk" || unit != 1024 {
		t.Errorf("duFlags = %q %d", f, unit)
	}
	if releaseBinaryName("arm64") != "itrupanel-freebsd-arm64" || releaseScriptName() != "update-freebsd.sh" {
		t.Error("имена файлов выпуска для FreeBSD")
	}
	if releaseArchiveURL("0.2.21") != "https://itrupanel.com/panel/itrupanel-freebsd-0.2.21.zip" || releaseEndpoint() != releaseEndpointFreeBSD {
		t.Errorf("адреса выпуска: %q %q", releaseArchiveURL("0.2.21"), releaseEndpoint())
	}
	if spamUser() != "rspamd" {
		t.Errorf("spamUser = %q", spamUser())
	}
	if !strings.Contains(bsdFTPGuardConfig(21), "action = pf[name=itrupanel-ftp") || !strings.Contains(bsdFTPGuardConfig(21), "logpath = /var/log/xferlog") {
		t.Error("jail FTP для FreeBSD")
	}
	unit := spamRedisUnit("/usr/local/bin/redis-server", "redis")
	if !strings.Contains(unit, "#!/bin/sh") || !strings.Contains(unit, "itrupanel_spam_redis_enable") || !strings.Contains(unit, "/etc/itrupanel/rspamd-redis.conf") {
		t.Errorf("rc-скрипт Redis:\n%s", unit)
	}
}

func TestLinuxLayoutUnchanged(t *testing.T) {
	if isBSD() {
		t.Skip("тест Linux")
	}
	if releaseEndpoint() != releaseEndpointLinux || releaseArchiveURL("0.2.21") != "https://itrupanel.com/panel/itrupanel-full-0.2.21.zip" || releaseBinaryName("amd64") != "itrupanel-linux-amd64" {
		t.Error("выпуск Linux изменился")
	}
	if spamUser() != "_rspamd" || opendkimPidFile() != "/run/opendkim/opendkim.pid" {
		t.Error("пути почты Linux изменились")
	}
}

func TestBSDZonesConfNSD(t *testing.T) {
	asFreeBSD(t)
	oldCfg, oldZones := cfg, store.DNSZones
	t.Cleanup(func() { cfg, store.DNSZones = oldCfg, oldZones })
	cfg = Config{Root: t.TempDir(), Home: t.TempDir(), Dry: true}
	store.DNSZones = []*DNSZone{{Name: "b.example"}, {Name: "a.example"}}
	if err := writeZonesConf(); err != nil {
		t.Fatal(err)
	}
	got := readFile(dnsZonesConf())
	// комментарии в nsd.conf начинаются с #; «//» (как в BIND) nsd-checkconf не принимает
	if strings.Contains(got, "//") || !strings.HasPrefix(got, "# ITRUPANEL") {
		t.Errorf("список зон NSD:\n%s", got)
	}
	want := "zone:\n    name: \"a.example\"\n    zonefile: \"/usr/local/etc/nsd/itrupanel/a.example.db\"\nzone:\n    name: \"b.example\""
	if !strings.Contains(got, want) {
		t.Errorf("нет зон в формате nsd.conf:\n%s", got)
	}
	if c := bsdNSDConfig(); !strings.Contains(c, "include: \"/usr/local/etc/nsd/itrupanel-zones.conf\"") || !strings.Contains(c, "control-enable: yes") {
		t.Errorf("nsd.conf:\n%s", c)
	}
}

func TestBSDManagedPackages(t *testing.T) {
	for _, n := range []string{"postfix", "bind-tools-not", "nsd", "php83", "php83-gd", "mariadb118-server", "py312-fail2ban", "roundcube-php83", "redis", "pure-ftpd"} {
		got := bsdManagedPkg.MatchString(n + "-")
		want := n != "bind-tools-not"
		if got != want {
			t.Errorf("bsdManagedPkg(%q) = %v, want %v", n, got, want)
		}
	}
	if bsdManagedPkg.MatchString("lmdb0-") || bsdManagedPkg.MatchString("libuv-") {
		t.Error("библиотеки не относятся к управляемым пакетам")
	}
}

func TestNSDZoneTTLs(t *testing.T) {
	asFreeBSD(t)
	z := &DNSZone{Name: "t.example", TTL: 3600, Serial: 1, Records: []*DNSRecord{
		{Name: "www", Type: "A", TTL: 300, Value: "203.0.113.8"},
		{Name: "www", Type: "A", TTL: 0, Value: "203.0.113.7"},
		{Name: "mail", Type: "A", TTL: 600, Value: "203.0.113.9"},
	}}
	out := renderZone(z)
	if strings.Contains(out, "www\t\tIN\tA") || strings.Count(out, "www\t300\tIN\tA") != 2 {
		t.Errorf("записи www должны иметь общий TTL 300:\n%s", out)
	}
	if !strings.Contains(out, "mail\t600\tIN\tA") {
		t.Errorf("запись mail изменилась:\n%s", out)
	}
}

func TestBSDPHPSwitchNames(t *testing.T) {
	re := reBsdSwitchPkg("8.3")
	for _, n := range []string{"php83", "php83-gd", "php83-pecl-redis", "roundcube-php83", "roundcube-classic-php83"} {
		if !re.MatchString(n) {
			t.Errorf("%s должен относиться к PHP 8.3", n)
		}
	}
	for _, n := range []string{"php84", "php830", "php84-gd", "roundcube-php84", "nginx", "phpmyadmin5-php83x"} {
		if re.MatchString(n) {
			t.Errorf("%s не относится к PHP 8.3", n)
		}
	}
	if got := bsdPHPMapName("php83-pecl-redis", "8.3", "8.5"); got != "php85-pecl-redis" {
		t.Errorf("php83-pecl-redis → %s", got)
	}
	if got := bsdPHPMapName("roundcube-php83", "8.3", "8.5"); got != "roundcube-php85" {
		t.Errorf("roundcube-php83 → %s", got)
	}
}
