// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type BackupOptions struct {
	Mode          string `json:"mode"`
	SiteSettings  bool   `json:"site_settings"`
	PanelSettings bool   `json:"panel_settings"`
}
type BackupRestoreOptions struct {
	BackupOptions
	Files     bool `json:"files"`
	Databases bool `json:"databases"`
	// Выборочное восстановление из содержимого копии: пути внутри сайта ("" — весь сайт) и базы.
	Paths []string `json:"paths,omitempty"`
	DBs   []string `json:"dbs,omitempty"`
}
type backupMetadata struct {
	ParentID    string               `json:"parent_id,omitempty"`
	Deleted     []string             `json:"deleted,omitempty"`
	Version     int                  `json:"version"`
	Domain      string               `json:"domain"`
	Site        *Site                `json:"site,omitempty"`
	CustomNginx *string              `json:"custom_nginx,omitempty"`
	Panel       *panelBackupSettings `json:"panel_settings,omitempty"`
	DBs         []string             `json:"dbs"`
	DBServers   map[string]string    `json:"db_servers"`
	Created     time.Time            `json:"created"`
	Kind        string               `json:"kind"`
}

// Portable preferences only. Authentication, sessions, panel address, TLS keys,
// and resource inventories are deliberately not copied into a site backup.
type panelBackupSettings struct {
	Email               string        `json:"email"`
	DefaultPHP          string        `json:"default_php"`
	DNSNameservers      []string      `json:"dns_nameservers"`
	DNSMail             bool          `json:"dns_mail"`
	DiskWarn            int           `json:"disk_warn"`
	BackupDaily         bool          `json:"backup_daily"`
	BackupKeep          int           `json:"backup_keep"`
	BackupHour          int           `json:"backup_hour"`
	BackupTarget        string        `json:"backup_target"`
	BackupFTP           BackupFTP     `json:"backup_ftp"`
	BackupS3            BackupS3      `json:"backup_s3"`
	BackupDAV           BackupWebDAV  `json:"backup_dav"`
	BackupDropbox       BackupDropbox `json:"backup_dropbox"`
	BackupGDrive        BackupGDrive  `json:"backup_gdrive"`
	BackupSiteSettings  bool          `json:"backup_site_settings"`
	BackupPanelSettings bool          `json:"backup_panel_settings"`
	BackupMode          string        `json:"backup_mode"`
	BackupSkipDBs       bool          `json:"backup_skip_dbs,omitempty"`
}

func capturePanelBackupSettings(s Settings) panelBackupSettings {
	return panelBackupSettings{s.Email, s.DefaultPHP, append([]string(nil), s.DNSNameservers...), s.DNSMail, s.DiskWarn, s.BackupDaily, s.BackupKeep, s.BackupHour, s.BackupTarget, s.BackupFTP, s.BackupS3, s.BackupDAV, s.BackupDropbox, s.BackupGDrive, s.BackupSiteSettings, s.BackupPanelSettings, s.BackupMode, s.BackupSkipDBs}
}
func (p panelBackupSettings) apply(s *Settings) {
	s.BackupMode, s.BackupSkipDBs = p.BackupMode, p.BackupSkipDBs
	s.Email, s.DefaultPHP, s.DNSNameservers, s.DNSMail, s.DiskWarn = p.Email, p.DefaultPHP, append([]string(nil), p.DNSNameservers...), p.DNSMail, p.DiskWarn
	s.BackupDaily, s.BackupKeep, s.BackupHour = p.BackupDaily, p.BackupKeep, p.BackupHour
	s.BackupTarget, s.BackupFTP, s.BackupS3 = p.BackupTarget, p.BackupFTP, p.BackupS3
	s.BackupDAV, s.BackupDropbox, s.BackupGDrive = p.BackupDAV, p.BackupDropbox, p.BackupGDrive
	s.BackupSiteSettings, s.BackupPanelSettings = p.BackupSiteSettings, p.BackupPanelSettings
}
func (p panelBackupSettings) validate() error {
	if p.BackupMode != "" && p.BackupMode != "full" && p.BackupMode != "incremental" {
		return errf("Неверный режим архивации в копии")
	}
	if p.BackupKeep < 1 || p.BackupKeep > 60 || p.BackupHour < 0 || p.BackupHour > 23 || p.DiskWarn < 1 || p.DiskWarn > 100 {
		return errf("Некорректные настройки панели в копии")
	}
	if p.BackupTarget != "" && p.BackupTarget != "local" && p.BackupTarget != "ftp" && p.BackupTarget != "both" && !backupTargetIsCloud(p.BackupTarget) {
		return errf("Неверное место хранения в копии")
	}
	if p.BackupTarget == "ftp" || p.BackupTarget == "both" {
		if p.BackupFTP.Host == "" || p.BackupFTP.User == "" || p.BackupFTP.Port < 0 || p.BackupFTP.Port > 65535 || strings.ContainsAny(p.BackupFTP.Host+p.BackupFTP.User+p.BackupFTP.Pass+p.BackupFTP.Dir, " \t\r\n") {
			return errf("Некорректные настройки FTP в копии")
		}
	}
	switch cloudKindOfTarget(p.BackupTarget) {
	case cloudS3:
		if _, err := s3Normalize(p.BackupS3); err != nil {
			return errf("Некорректные настройки облака в копии")
		}
	case cloudWebDAV:
		if _, err := davNormalize(p.BackupDAV); err != nil {
			return errf("Некорректные настройки WebDAV в копии")
		}
	case cloudDropbox:
		if _, err := dropboxNormalize(p.BackupDropbox); err != nil {
			return errf("Некорректные настройки Dropbox в копии")
		}
	case cloudGDrive:
		if _, err := gdriveNormalize(p.BackupGDrive); err != nil {
			return errf("Некорректные настройки Google Drive в копии")
		}
	}
	for _, name := range p.DNSNameservers {
		if _, err := toASCII(name); err != nil {
			return err
		}
	}
	return nil
}

// Caller holds mu; no files have been changed at this stage.
func validateBackupSettings(domain string, m backupMetadata, o BackupRestoreOptions) error {
	if m.Version != 2 || m.Domain != domain {
		return errf("Настройки этой копии не подходят для сайта %s", domain)
	}
	if o.PanelSettings {
		if m.Panel == nil {
			return errf("В копии нет настроек панели")
		}
		if err := m.Panel.validate(); err != nil {
			return err
		}
	}
	if !o.SiteSettings {
		return nil
	}
	if m.Site == nil || m.CustomNginx == nil || m.Site.Domain != domain {
		return errf("В копии нет настроек сайта")
	}
	s := m.Site
	if findSite(domain) == nil {
		return errf("Сайт не найден")
	}
	if s.Mode != "php" && s.Mode != "static" && s.Mode != "proxy" {
		return errf("Неверный режим сайта в копии")
	}
	if s.Backend != "" && s.Backend != "apache" && s.Backend != "openlitespeed" {
		return errf("Неверный веб-сервер в копии")
	}
	if s.SSL != "" && findCert(s.SSL) == nil {
		return errf("Сначала установите сертификат %s, указанный в копии", s.SSL)
	}
	if s.Mode == "php" && s.PHP != "" && !phpIsInstalled(s.PHP) {
		return errf("Сначала установите PHP %s, указанный в копии", s.PHP)
	}
	for _, name := range siteNames(s) {
		if _, err := toASCII(name); err != nil {
			return err
		}
		for _, other := range store.Sites {
			if other.Domain != domain {
				for _, n := range siteNames(other) {
					if name == n {
						return errf("Имя %s уже занято сайтом %s", name, other.Domain)
					}
				}
			}
		}
		if name == store.Mail.Webmail.Hostname || name == store.Settings.PanelDomain {
			return errf("Имя %s занято панелью или почтой", name)
		}
	}
	return nil
}
func restoreBackupSettings(domain string, m backupMetadata, o BackupRestoreOptions) (resultErr error) {
	mu.Lock()
	defer mu.Unlock()
	if err := validateBackupSettings(domain, m, o); err != nil {
		return err
	}
	current := findSite(domain)
	oldSettings := store.Settings
	var oldSite Site
	var rollback func() error
	var oldCustom []byte
	var customMissing bool
	if o.SiteSettings {
		oldSite = *current
		next := *m.Site
		next.Security.Revision = oldSite.Security.Revision + 1
		// Restore configuration onto the selected site, never relocate its files or owner.
		next.Domain, next.User, next.Root, next.Created = oldSite.Domain, oldSite.User, oldSite.Root, oldSite.Created
		var err error
		oldCustom, err = os.ReadFile(P(customPath(domain)))
		customMissing = os.IsNotExist(err)
		if err != nil && !customMissing {
			return err
		}
		rollback, err = snapshotSiteRuntime(&next, oldSite.PHP)
		if err != nil {
			return err
		}
		defer func() {
			if resultErr != nil {
				*current = oldSite
				if customMissing {
					os.Remove(P(customPath(domain)))
				} else {
					writeFile(customPath(domain), oldCustom, 0644)
				}
				resultErr = errors.Join(resultErr, rollback(), applySite(current, next.PHP))
			}
		}()
		if err = writeFile(customPath(domain), []byte(*m.CustomNginx), 0644); err != nil {
			return err
		}
		*current = next
		if err = applySite(current, oldSite.PHP); err != nil {
			return err
		}
	}
	if o.PanelSettings {
		m.Panel.apply(&store.Settings)
	}
	if err := saveChecked(); err != nil {
		store.Settings = oldSettings
		return fmt.Errorf("Сохранение настроек: %w", err)
	}
	return nil
}

// Kept separate for regression tests and tools that inspect an archive manifest.
func decodeBackupMetadata(data []byte) (backupMetadata, error) {
	var m backupMetadata
	err := json.Unmarshal(data, &m)
	return m, err
}

func newBackupMetadata(site Site, kind string, o BackupOptions, panel panelBackupSettings, custom string) backupMetadata {
	m := backupMetadata{Version: 2, Domain: site.Domain, Kind: kind, Created: time.Now()}
	if o.SiteSettings {
		m.Site = &site
		m.CustomNginx = &custom
	}
	if o.PanelSettings {
		m.Panel = &panel
	}
	return m
}
