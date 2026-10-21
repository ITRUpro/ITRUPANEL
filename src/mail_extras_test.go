// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMailboxQuotaPersistenceAndLegacyEdits(t *testing.T) {
	mux := testPanel(t)
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	testAPI(t, mux, "mail.domain.add", `{"name":"example.test"}`)
	call := func(body string) {
		t.Helper()
		_, r := testAPI(t, mux, "mail.box.save", body)
		if r["ok"] != true {
			t.Fatal(r)
		}
	}
	call(`{"address":"a@example.test","quota_mb":1024}`)
	if store.Mail.Boxes[0].QuotaMB != 1024 {
		t.Fatal("quota not saved")
	}
	call(`{"address":"a@example.test","edit":true,"enabled":false}`)
	call(`{"address":"a@example.test","edit":true,"enabled":true}`)
	if store.Mail.Boxes[0].QuotaMB != 1024 {
		t.Fatal("legacy edit cleared quota")
	}
	mustLoad()
	if store.Mail.Boxes[0].QuotaMB != 1024 {
		t.Fatal("reload lost quota")
	}
	if !strings.Contains(readFile("/etc/dovecot/itrupanel-users"), "userdb_quota_storage_size=1024M") {
		t.Fatal("quota not sent to userdb")
	}
	call(`{"address":"a@example.test","edit":true,"enabled":true,"quota_mb":0}`)
	if !strings.Contains(readFile("/etc/dovecot/itrupanel-users"), "userdb_quota_storage_size=0M") {
		t.Fatal("unlimited quota not applied")
	}
	for _, v := range []string{"-1", "1048577", "1.5"} {
		_, r := testAPI(t, mux, "mail.box.save", `{"address":"a@example.test","edit":true,"enabled":true,"quota_mb":`+v+`}`)
		if r["ok"] == true {
			t.Fatal("invalid quota accepted", v)
		}
	}
}
func TestDovecotQuotaVersions(t *testing.T) {
	testPanel(t)
	for _, modern := range []bool{false, true} {
		m := MailState{Dovecot24: modern, Hostname: "mail.example.test", UID: 5000, GID: 5000, Domains: []MailDomain{{Name: "example.test"}}, Boxes: []MailBox{{Address: "a@example.test", Hash: "{PLAIN}unused", Enabled: true, QuotaMB: 12}}}
		f, err := mailConfigFiles(m)
		if err != nil {
			t.Fatal(err)
		}
		field := "userdb_quota_rule=*:storage=12M"
		if modern {
			field = "userdb_quota_storage_size=12M"
		}
		if !strings.Contains(f["/etc/dovecot/itrupanel-users"], field) {
			t.Fatal("wrong quota field")
		}
		if !strings.Contains(f["/etc/dovecot/dovecot.conf"], "imap_quota") {
			t.Fatal("IMAP quota missing")
		}
	}
	for _, out := range []string{"Quota name\tType\tValue\tLimit\t%\nUser quota\tSTORAGE\t120\t1024\t11\n", "User quota STORAGE 120 - 0"} {
		n, e := parseMailQuota(out)
		if e != nil || n != 122880 {
			t.Fatalf("%d %v", n, e)
		}
	}
	if _, e := parseMailQuota("Error: quota disabled"); e == nil {
		t.Fatal("unknown quota reported as zero")
	}
}
func TestSpamInstallDisableAndSecrets(t *testing.T) {
	mux := testPanel(t)
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	s := SpamSettings{Enabled: true, Mark: 6, Reject: 15}
	if err := setupSpam(s, nil); err != nil {
		t.Fatal(err)
	}
	main := readFile("/etc/postfix/main.cf")
	if !strings.Contains(main, "inet:127.0.0.1:11332, inet:127.0.0.1:8891") || !strings.Contains(main, "milter_default_action = tempfail") {
		t.Fatal(main)
	}
	if !strings.Contains(readFile("/etc/rspamd/local.d/worker-proxy.inc"), "127.0.0.1:11332") {
		t.Fatal("public listener")
	}
	_, r := testAPI(t, mux, "mail.list", `{}`)
	encoded, _ := json.Marshal(r)
	secret := strings.TrimSpace(readFile(spamRedisSecret))
	if len(secret) != 64 || strings.Contains(string(encoded), secret) {
		t.Fatal("secret missing or disclosed")
	}
	for _, p := range []string{spamRedisSecret, "/etc/rspamd/local.d/redis.conf", "/etc/itrupanel/rspamd-redis.conf", "/etc/rspamd/override.d/worker-controller.inc"} {
		st, e := os.Stat(P(p))
		if e != nil || st.Mode().Perm()&0007 != 0 {
			t.Fatal("public secret", p, e)
		}
	}
	s.Enabled = false
	if err := setupSpam(s, nil); err != nil {
		t.Fatal(err)
	}
	main = readFile("/etc/postfix/main.cf")
	if strings.Contains(main, "11332") || !strings.Contains(main, "8891") {
		t.Fatal("disable lost DKIM or retained spam filter")
	}
}
func TestRoundcubeSetupIsolationAndCredentials(t *testing.T) {
	mux := testPanel(t)
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := setupRoundcube("webmail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	secret := readFile(webmailKey)
	conf := readFile("/etc/roundcube/config.inc.php")
	for _, v := range []string{"'verify_peer' => true", "'verify_peer_name' => true", "'peer_name' => 'mail.example.test'", "enable_installer", "sqlite:///", "'smtp_user'] = '%u'"} {
		if !strings.Contains(conf, v) {
			t.Fatal(v)
		}
	}
	if !strings.Contains(readFile(webmailData+"/mail-ca.pem"), "BEGIN CERTIFICATE") {
		t.Fatal("mail trust missing")
	}
	ng := readFile(webmailVhost)
	for _, v := range []string{"listen 443 ssl", "location = /index.php", "(config|temp|logs|SQL|bin|installer|vendor", "location ~ /\\."} {
		if !strings.Contains(ng, v) {
			t.Fatal(v)
		}
	}
	_, r := testAPI(t, mux, "mail.list", `{}`)
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), secret) {
		t.Fatal("Roundcube secret leaked")
	}
	if err := setupRoundcube("webmail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	if readFile(webmailKey) != secret {
		t.Fatal("reconfigure invalidated credentials")
	}
	_, err := createOrUpdateSite(siteIn{Domain: "webmail.example.test", User: "web", Mode: "static"})
	if err == nil {
		t.Fatal("webmail hostname can be overwritten by a site")
	}
	store.Settings.PanelDomain = "panel.example.test"
	if err := validateWebmailHost("panel.example.test", ""); err == nil {
		t.Fatal("webmail shares panel origin")
	}
}

// Ящик хранит имя владельца; домены сайтов предлагаются для ящика, пока их нет в почте.
func TestMailBoxNameAndSiteDomains(t *testing.T) {
	mux := testPanel(t)
	mu.Lock()
	store.Mail.Ready = true
	store.Mail.Domains = append(store.Mail.Domains, MailDomain{Name: "mailbox-name.example"})
	store.Sites = append(store.Sites, &Site{Domain: "site-only.example", User: "test"})
	mu.Unlock()
	// Через API: mail.list держит mu, поэтому так проверяется и отсутствие взаимной блокировки
	_, out := testAPI(t, mux, "mail.list", `{}`)
	data, _ := out["data"].(map[string]any)
	list, _ := data["site_domains"].([]any)
	found := false
	for _, x := range list {
		d, _ := x.(string)
		found = found || d == "site-only.example"
		if d == "mailbox-name.example" {
			t.Fatal("почтовый домен предложен повторно")
		}
	}
	if !found {
		t.Fatal("домен сайта не предложен")
	}
	if _, out = testAPI(t, mux, "mail.box.save", `{"address":"info@mailbox-name.example","name":"<script>","password":"TestingMailbox123"}`); out["error"] == nil {
		t.Fatal("имя с угловыми скобками принято")
	}
	if _, out = testAPI(t, mux, "mail.box.save", `{"address":"info@mailbox-name.example","name":"Иван Петров","password":"TestingMailbox123"}`); out["error"] != nil {
		t.Fatalf("создание ящика: %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, b := range store.Mail.Boxes {
		if b.Address == "info@mailbox-name.example" && b.Name == "Иван Петров" {
			return
		}
	}
	t.Fatal("имя ящика не сохранено")
}
