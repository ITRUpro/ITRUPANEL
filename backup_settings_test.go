// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBackupSettingsSelectionAndSecrets(t *testing.T) {
	testPanel(t)
	store.Settings.AdminHash = "never-export-admin-hash"
	store.Settings.PMASecret = "never-export-pma-secret"
	store.Settings.BackupFTP.Pass = "saved-ftp-password"
	s := Site{Domain: "example.test", Mode: "static", Root: "/var/www/alice/data/www/example.test"}
	for _, o := range []BackupOptions{{}, {SiteSettings: true}, {PanelSettings: true}, {SiteSettings: true, PanelSettings: true}} {
		m := newBackupMetadata(s, "settings", o, capturePanelBackupSettings(store.Settings), "# custom rules")
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeBackupMetadata(data)
		if err != nil {
			t.Fatal(err)
		}
		if (decoded.Site != nil) != o.SiteSettings || (decoded.CustomNginx != nil) != o.SiteSettings || (decoded.Panel != nil) != o.PanelSettings {
			t.Fatalf("wrong components: %s", data)
		}
		if strings.Contains(string(data), "never-export") {
			t.Fatal("authentication secrets leaked")
		}
		if strings.Contains(string(data), "saved-ftp-password") != o.PanelSettings {
			t.Fatal("FTP secret included without selection or missing")
		}
	}
}
func TestRestorePanelBackupPreferencesPreservesAccess(t *testing.T) {
	testPanel(t)
	saved := capturePanelBackupSettings(store.Settings)
	saved.Email = "restored@example.test"
	saved.BackupHour = 6
	saved.BackupFTP.Pass = "restored-password"
	store.Settings.AdminHash = "keep-hash"
	store.Settings.PanelDomain = "panel.example.test"
	store.Settings.PanelCert = "keep-cert"
	m := backupMetadata{Version: 2, Domain: "example.test", Panel: &saved}
	o := BackupRestoreOptions{BackupOptions: BackupOptions{PanelSettings: true}}
	if err := restoreBackupSettings("example.test", m, o); err != nil {
		t.Fatal(err)
	}
	if store.Settings.Email != saved.Email || store.Settings.BackupHour != 6 || store.Settings.BackupFTP.Pass != "restored-password" {
		t.Fatal("preferences not restored")
	}
	if store.Settings.AdminHash != "keep-hash" || store.Settings.PanelDomain != "panel.example.test" || store.Settings.PanelCert != "keep-cert" {
		t.Fatal("panel access replaced")
	}
	data, err := os.ReadFile(statePath())
	if err != nil || !strings.Contains(string(data), "restored@example.test") {
		t.Fatal("preferences not persisted", err)
	}
	saved.BackupKeep = 0
	if err := restoreBackupSettings("example.test", m, o); err == nil {
		t.Fatal("invalid preferences accepted")
	}
}
func TestRestoreSiteBackupSettings(t *testing.T) {
	testPanel(t)
	s, err := createOrUpdateSite(siteIn{Domain: "example.test", User: "alice", Mode: "static", Gzip: true})
	if err != nil {
		t.Fatal(err)
	}
	originalRoot := s.Root
	snapshot := *s
	snapshot.Aliases = []string{"www.example.test"}
	snapshot.Gzip = false
	snapshot.User = "not-the-owner"
	snapshot.Root = "/must-not-be-used"
	custom := "add_header X-Backup restored;\n"
	m := backupMetadata{Version: 2, Domain: s.Domain, Site: &snapshot, CustomNginx: &custom}
	o := BackupRestoreOptions{BackupOptions: BackupOptions{SiteSettings: true}}
	if err := restoreBackupSettings(s.Domain, m, o); err != nil {
		t.Fatal(err)
	}
	if s.Gzip || s.User != "alice" || s.Root != originalRoot || len(s.Aliases) != 1 {
		t.Fatalf("wrong site restoration: %+v", s)
	}
	if readFile(customPath(s.Domain)) != custom || !strings.Contains(readFile(vhostPath(s.Domain)), "www.example.test") {
		t.Fatal("runtime configuration not applied")
	}
	snapshot.SSL = "missing-cert"
	if err := restoreBackupSettings(s.Domain, m, o); err == nil {
		t.Fatal("missing certificate accepted")
	}
	if s.SSL != "" {
		t.Fatal("invalid restoration changed site")
	}
}
func TestBackupScheduleSettingsOptions(t *testing.T) {
	mux := testPanel(t)
	code, r := testAPI(t, mux, "backups.ftp.save", `{"target":"local","schedule":{"daily":true,"keep":9,"hour":5,"site_settings":true,"panel_settings":true}}`)
	if code != 200 || !store.Settings.BackupSiteSettings || !store.Settings.BackupPanelSettings || store.Settings.BackupKeep != 9 {
		t.Fatal(code, r)
	}
	testAPI(t, mux, "backups.ftp.save", `{"target":"local","schedule":{"daily":true,"keep":9,"hour":5}}`)
	if !store.Settings.BackupSiteSettings || !store.Settings.BackupPanelSettings {
		t.Fatal("old client cleared options")
	}
	testAPI(t, mux, "backups.ftp.save", `{"target":"ftp","ftp":{"host":"example.test","user":"demo"},"test":true,"schedule":{"keep":7,"hour":3,"site_settings":false,"panel_settings":false}}`)
	if !store.Settings.BackupSiteSettings || !store.Settings.BackupPanelSettings || store.Settings.BackupKeep != 9 {
		t.Fatal("connection test mutated settings")
	}
	code, r = testAPI(t, mux, "backups.create", `{"site":"*","kind":"settings"}`)
	if r["error"] == nil {
		t.Fatal("empty settings-only backup accepted")
	}
	code, r = testAPI(t, mux, "backups.restore", `{"id":"absent","options":{}}`)
	if r["error"] == nil {
		t.Fatal("empty restoration accepted")
	}
}

func TestSettingsArchiveRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar unavailable")
	}
	testPanel(t)
	store.Sites = []*Site{{Domain: "example.test", User: "alice", Root: "/var/www/alice/data/www/example.test", Mode: "static", Gzip: true}}
	store.Settings.Email = "saved@example.test"
	store.Settings.AdminHash = "keep-current-password"
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	b, err := makeBackup("example.test", "settings", false, BackupOptions{SiteSettings: true, PanelSettings: true})
	if err != nil {
		t.Fatal(err)
	}
	if !b.SiteSettings || !b.PanelSettings || b.Size == 0 {
		t.Fatal("archive flags or size missing")
	}
	f, err := os.Open(P(b.File))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(h.Name, "files/") {
			t.Fatal("settings-only archive contains site data")
		}
		if h.Name == "meta.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			m, err := decodeBackupMetadata(data)
			if err != nil || m.Site == nil || m.Panel == nil || m.Panel.Email != "saved@example.test" {
				t.Fatal("incorrect archive metadata", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("metadata missing")
	}
	store.Settings.Email = "changed@example.test"
	store.Sites[0].Gzip = false
	if err := restoreBackup(b.ID, BackupRestoreOptions{BackupOptions: BackupOptions{PanelSettings: true}}); err != nil {
		t.Fatal(err)
	}
	if store.Settings.Email != "saved@example.test" || store.Settings.AdminHash != "keep-current-password" || store.Sites[0].Gzip {
		t.Fatal("restore selection not respected")
	}
}
