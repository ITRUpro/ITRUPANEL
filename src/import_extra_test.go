// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestImpTransferDNS(t *testing.T) {
	testPanel(t)
	store.DNSZones = nil
	z := impExportZone{Name: "example.test", Email: "hostmaster@example.test", TTL: 3600, Records: []*DNSRecord{
		{ID: "1", Name: "@", Type: "A", Value: "203.0.113.5"},
		{ID: "2", Name: "www", Type: "A", Value: "198.51.100.9"},
		{ID: "3", Name: "@", Type: "MX", Prio: 10, Value: "mail.example.test."},
		{ID: "4", Name: "mail._domainkey", Type: "TXT", Value: "v=DKIM1; k=rsa; p=OLD"},
		{ID: "5", Name: "bad name", Type: "A", Value: "203.0.113.5"},
	}}
	r, name := impTransferDNS(nil, z, "203.0.113.5", map[string]bool{"example.test": true})
	if r.Status != "ok" || name != "example.test" {
		t.Fatalf("перенос зоны: %+v", r)
	}
	nz := findZone("example.test")
	if nz == nil || len(nz.Records) != 3 {
		t.Fatalf("записи: %+v", nz)
	}
	// defaultIP() зависит от сети, поэтому проверяется сам факт замены, а не конкретный адрес.
	if nz.Records[0].Value == "203.0.113.5" {
		t.Fatalf("IP старого сервера не заменён: %q", nz.Records[0].Value)
	}
	if nz.Records[1].Value != "198.51.100.9" {
		t.Fatalf("чужой IP изменён: %q", nz.Records[1].Value)
	}
	if !strings.Contains(r.Detail, "пропущено записей: 2") {
		t.Fatalf("описание: %q", r.Detail)
	}
	// Повтор пропускается.
	if r2, _ := impTransferDNS(nil, z, "203.0.113.5", nil); r2.Status != "skipped" {
		t.Fatalf("повторный перенос: %+v", r2)
	}
	// Если почта не переносится, ключ DKIM остаётся.
	z2 := impExportZone{Name: "keep.test", TTL: 3600, Records: []*DNSRecord{{ID: "1", Name: "mail._domainkey", Type: "TXT", Value: "v=DKIM1; p=KEEP"}}}
	if r3, _ := impTransferDNS(nil, z2, "", nil); r3.Status != "ok" || len(findZone("keep.test").Records) != 1 {
		t.Fatalf("DKIM без переноса почты: %+v", r3)
	}
}

func TestImpNeedsMailDNS(t *testing.T) {
	testPanel(t)
	needs := impNeeds(&ImpInventory{}, impSelection{Mail: []string{"a.test"}, DNS: []string{"a.test"}})
	keys := map[string]bool{}
	for _, n := range needs {
		keys[n.Key] = true
	}
	if !keys["mail"] || !keys["dns"] {
		t.Fatalf("для почты и DNS нужны службы: %+v", needs)
	}
}

func TestImpTransferBackup(t *testing.T) {
	testPanel(t)
	// Только локальное хранение: переносить нечего.
	p := &impPanel{backup: impExportBackup{Target: "local"}}
	if r := impTransferBackup(nil, p); r.Status != "skipped" {
		t.Fatalf("локальное хранение: %+v", r)
	}
	// FTP без адреса и с запрещёнными символами.
	if r := impTransferBackup(nil, &impPanel{backup: impExportBackup{Target: "ftp", FTP: BackupFTP{Host: "ftp.example.test", User: "u", Pass: "a b"}}}); r.Status != "error" {
		t.Fatalf("пробел в пароле принят: %+v", r)
	}
	// Настройки применяются, расписание остаётся выключенным; в тестах curl не выполняется.
	store.Settings.BackupDaily = true
	p = &impPanel{backup: impExportBackup{Target: "both", Daily: true, Keep: 5, Hour: 4, Mode: "incremental", SiteSettings: true,
		FTP: BackupFTP{Host: "ftp.example.test", Port: 21, User: "u", Pass: "secret", Dir: "/backups/old"}}}
	r := impTransferBackup(nil, p)
	st := store.Settings
	if r.Status != "ok" || st.BackupTarget != "both" || st.BackupFTP.Host != "ftp.example.test" || st.BackupFTP.Dir != "/backups/old" || st.BackupFTP.Pass != "secret" {
		t.Fatalf("настройки не применены: %+v %+v", r, st.BackupFTP)
	}
	if st.BackupDaily || st.BackupKeep != 5 || st.BackupHour != 4 || st.BackupMode != "incremental" || !st.BackupSiteSettings {
		t.Fatalf("расписание/параметры: daily=%v keep=%d hour=%d mode=%s", st.BackupDaily, st.BackupKeep, st.BackupHour, st.BackupMode)
	}
	if !strings.Contains(r.Detail, "расписание выключено") || !strings.Contains(r.Detail, "ежедневно в 4:00, хранить копий: 5") {
		t.Fatalf("описание: %q", r.Detail)
	}
	// Перевод описания для английского интерфейса.
	if en := englishText(r.Detail); !strings.Contains(en, "the schedule is off") || !strings.Contains(en, "daily at 4:00, keep 5 backups") {
		t.Fatalf("перевод: %q", en)
	}
}
