// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"time"
)

// DNS на FreeBSD: NSD. Пакеты BIND (bind920, bind9-devel) требуют устаревшую библиотеку lmdb0, а Postfix —
// новую lmdb; вместе они не ставятся, и установка почты молча удаляла BIND. NSD с ними совместим и читает те же
// файлы зон (формат RFC 1035), поэтому панель пишет зоны как обычно, а список зон — в формате nsd.conf.

const (
	bsdNSDDir  = bsdPrefix + "/etc/nsd"
	bsdNSDConf = bsdNSDDir + "/nsd.conf"
	bsdNSDBin  = bsdPrefix + "/sbin/nsd"
)

func bsdNSDConfig() string {
	return `# ITRUPANEL: NSD как авторитетный DNS-сервер. Исходный файл сохранён как nsd.conf.itrupanel-orig.
server:
    ip-address: 0.0.0.0
    ip-address: ::0
    port: 53
    hide-version: yes
    hide-identity: yes
    zonesdir: "` + bsdNSDDir + `"
    pidfile: "/var/run/nsd/nsd.pid"
    database: "/var/db/nsd/nsd.db"
    username: nsd

remote-control:
    control-enable: yes
    control-interface: 127.0.0.1
    control-port: 8952
    server-key-file: "` + bsdNSDDir + `/nsd_server.key"
    server-cert-file: "` + bsdNSDDir + `/nsd_server.pem"
    control-key-file: "` + bsdNSDDir + `/nsd_control.key"
    control-cert-file: "` + bsdNSDDir + `/nsd_control.pem"

include: "` + dnsZonesConf() + `"
`
}

// dnsCheckZone проверяет файл зоны: nsd-checkzone на FreeBSD, named-checkzone на Linux.
func dnsCheckZone(name, path string) (string, error) {
	if isBSD() {
		return runTimeout(15*time.Second, "", bsdPrefix+"/sbin/nsd-checkzone", name, path)
	}
	return runTimeout(15*time.Second, "", "named-checkzone", name, path)
}

// nsdReload проверяет конфигурацию и просит NSD перечитать зоны; если служба не запущена, запускает её.
func nsdReload() error {
	if out, err := runTimeout(15*time.Second, "", bsdPrefix+"/sbin/nsd-checkconf", bsdNSDConf); err != nil {
		return errf("nsd-checkconf: %s", lastLines(out, 3))
	}
	if !serviceActive("named") {
		return service("start", "named")
	}
	if _, err := runTimeout(30*time.Second, "", bsdPrefix+"/sbin/nsd-control", "reconfig"); err == nil {
		runTimeout(30*time.Second, "", bsdPrefix+"/sbin/nsd-control", "reload")
		return nil
	}
	return service("restart", "named")
}

func bsdSetupBind() error {
	if !dnsInstalled() {
		if err := bsdPkgInstall("nsd"); err != nil {
			return errf("Не удалось установить NSD: %s", firstLine(err.Error()))
		}
	}
	os.MkdirAll(P(dnsDir()), 0755)
	run("chown", "root:nsd", dnsDir())
	if cur := readFile(bsdNSDConf); cur != "" && !fileExists(bsdNSDConf+".itrupanel-orig") {
		writeFile(bsdNSDConf+".itrupanel-orig", []byte(cur), 0644)
	}
	if err := writeFile(bsdNSDConf, []byte(bsdNSDConfig()), 0640); err != nil {
		return err
	}
	run("chown", "root:nsd", bsdNSDConf)
	// ключи удалённого управления: через них панель перечитывает список зон без перезапуска службы
	if !fileExists(bsdNSDDir + "/nsd_control.pem") {
		run(bsdPrefix+"/sbin/nsd-control-setup", "-d", bsdNSDDir)
	}
	os.MkdirAll(P("/var/db/nsd"), 0755)
	os.MkdirAll(P("/var/run/nsd"), 0755)
	run("chown", "nsd:nsd", "/var/db/nsd", "/var/run/nsd")
	run("chmod", "640", bsdNSDDir+"/nsd_server.key", bsdNSDDir+"/nsd_control.key")
	run("chown", "root:nsd", bsdNSDDir+"/nsd_server.key", bsdNSDDir+"/nsd_server.pem", bsdNSDDir+"/nsd_control.key", bsdNSDDir+"/nsd_control.pem")
	mu.Lock()
	err := writeZonesConf()
	mu.Unlock()
	if err != nil {
		return err
	}
	if err := service("enable", "named"); err != nil {
		return err
	}
	return service("restart", "named")
}

// nsdRRsetTTL: NSD не загружает зону, если у записей одного набора (имя + тип) разные TTL, а BIND берёт меньший.
// Для таких наборов возвращает общий TTL (наименьший); у остальных записей TTL остаётся как задан.
func nsdRRsetTTL(z *DNSZone, recs []*DNSRecord) map[string]int {
	min, mixed := map[string]int{}, map[string]bool{}
	for _, r := range recs {
		name := r.Name
		if name == "" {
			name = "@"
		}
		ttl := r.TTL
		if ttl <= 0 {
			ttl = z.TTL
		}
		key := name + "/" + r.Type
		if cur, ok := min[key]; !ok {
			min[key] = ttl
		} else {
			if ttl != cur {
				mixed[key] = true
			}
			if ttl < cur {
				min[key] = ttl
			}
		}
	}
	out := map[string]int{}
	for k := range mixed {
		out[k] = min[k]
	}
	return out
}
