// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Всё состояние панели — один JSON-файл etc/state.json. Объёмы маленькие, так проще и надёжнее.

type SiteUser struct {
	Name    string    `json:"name"`
	Home    string    `json:"home"`
	Created time.Time `json:"created"`
}

type Site struct {
	Security      SiteSecurity `json:"security"`
	Domain        string       `json:"domain"`
	Aliases       []string     `json:"aliases"`
	User          string       `json:"user"`
	Root          string       `json:"root"`
	PHP           string       `json:"php"` // "" — без PHP, иначе версия "8.3"
	Index         string       `json:"index"`
	SSL           string       `json:"ssl"` // имя сертификата
	RedirectHTTPS bool         `json:"redirect_https"`
	WWWRedirect   string       `json:"www_redirect"` // "" | "to_www" | "from_www"
	Gzip          bool         `json:"gzip"`
	StaticExpires string       `json:"static_expires"` // "", "7d", "30d"
	MaxBody       int          `json:"max_body"`       // МБ
	Mode          string       `json:"mode"`           // "php" | "static" | "proxy"
	Backend       string       `json:"backend"`        // "" — nginx, "apache", "openlitespeed" (backends behind nginx, PHP-FPM)
	ProxyTo       string       `json:"proxy_to"`
	TryIndex      bool         `json:"try_index"` // try_files на index.php (ЧПУ)
	AutoSSL       bool         `json:"auto_ssl"`  // выпускать Let's Encrypt автоматически
	Suspended     bool         `json:"suspended"`
	Created       time.Time    `json:"created"`
}

type DB struct {
	AccessMode  string    `json:"access_mode,omitempty"`
	AccessIPs   []string  `json:"access_ips,omitempty"`
	AccessHosts []string  `json:"access_hosts,omitempty"`
	Name        string    `json:"name"`
	User        string    `json:"user"`
	Owner       string    `json:"owner"` // системный пользователь
	Site        string    `json:"site"`
	Server      string    `json:"server"` // "" — основной MariaDB, иначе имя сервера БД
	Created     time.Time `json:"created"`
}

type FTPUser struct {
	Name    string    `json:"name"`
	Owner   string    `json:"owner"`
	Home    string    `json:"home"`
	Created time.Time `json:"created"`
}

type CronJob struct {
	ID       string    `json:"id"`
	User     string    `json:"user"`
	Schedule string    `json:"schedule"`
	Command  string    `json:"command"`
	Comment  string    `json:"comment"`
	Enabled  bool      `json:"enabled"`
	Mail     string    `json:"mail"`
	Created  time.Time `json:"created"`
}

type Cert struct {
	Name    string    `json:"name"`
	Type    string    `json:"type"` // le | self | custom
	Domains []string  `json:"domains"`
	Expires time.Time `json:"expires"`
	Issuer  string    `json:"issuer"`
	Crt     string    `json:"crt"`
	Key     string    `json:"key"`
	Email   string    `json:"email"`
	Created time.Time `json:"created"`
	LastErr string    `json:"last_err"`
}

type Backup struct {
	Imported      bool      `json:"imported,omitempty"`
	RemoteSource  string    `json:"remote_source,omitempty"`
	ManifestHash  string    `json:"manifest_hash,omitempty"`
	RestoreIssue  string    `json:"restore_issue,omitempty"`
	Mode          string    `json:"mode"`
	ParentID      string    `json:"parent_id,omitempty"`
	SiteSettings  bool      `json:"site_settings"`
	PanelSettings bool      `json:"panel_settings"`
	ID            string    `json:"id"`
	File          string    `json:"file"`
	Site          string    `json:"site"`
	Kind          string    `json:"kind"`         // files | settings | database | panel; старые: full | db
	DB            string    `json:"db,omitempty"` // для kind database
	Size          int64     `json:"size"`
	Created       time.Time `json:"created"`
	Auto          bool      `json:"auto"`
	Local         bool      `json:"local"`  // файл есть на сервере
	Remote        string    `json:"remote"` // путь на FTP, если загружена
}

type BackupFTP struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
	Dir  string `json:"dir"`
	TLS  bool   `json:"tls"`
}

type Notice struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"` // info | warn | error
	Text  string    `json:"text"`
	Read  bool      `json:"read"`
	Key   string    `json:"key"` // для дедупликации
}

type Settings struct {
	BackupMode          string    `json:"backup_mode"`
	BackupSiteSettings  bool      `json:"backup_site_settings"`
	BackupPanelSettings bool      `json:"backup_panel_settings"`
	BackupSkipDBs       bool      `json:"backup_skip_dbs,omitempty"` // по умолчанию каждый день копируются все базы
	AdminUser           string    `json:"admin_user"`
	AdminHash           string    `json:"admin_hash"`
	Email               string    `json:"email"`
	BackupDaily         bool      `json:"backup_daily"`
	BackupKeep          int       `json:"backup_keep"`
	BackupHour          int       `json:"backup_hour"`
	DefaultPHP          string    `json:"default_php"`
	RecycleOff          bool      `json:"recycle_off,omitempty"`  // корзина менеджера файлов выключена
	DefaultSite         string    `json:"default_site,omitempty"` // "" — страница «Сайт не найден», "-" — закрыть соединение, иначе домен
	BackupTarget        string    `json:"backup_target"`          // local | ftp | both
	DNSNameservers      []string  `json:"dns_nameservers"`
	DNSMail             bool      `json:"dns_mail"`
	BackupFTP           BackupFTP `json:"backup_ftp"`
	BackupS3            BackupS3  `json:"backup_s3"` // облачное хранилище (S3-совместимое)
	DiskWarn            int       `json:"disk_warn"`
	PMASecret           string    `json:"pma_secret"`
	PanelDomain         string    `json:"panel_domain"`
	PanelCert           string    `json:"panel_cert"`
	DockerProjectDir    string    `json:"docker_project_dir,omitempty"`
	PanelProxy          bool      `json:"panel_proxy,omitempty"`         // панель открывается через nginx на 443 без порта
	PanelDirectClosed   bool      `json:"panel_direct_closed,omitempty"` // прямой доступ к порту панели только с самого сервера
	PanelProxySecret    string    `json:"panel_proxy_secret,omitempty"`  // заголовок, по которому панель узнаёт свой nginx
}

type State struct {
	TermsAcceptance map[string]TermsAcceptance `json:"terms_acceptance,omitempty"`
	Firewall        FirewallState              `json:"firewall"`
	Antivirus       AntivirusState             `json:"antivirus"`
	Mail            MailState                  `json:"mail"`
	Users           []*SiteUser                `json:"users"`
	Sites           []*Site                    `json:"sites"`
	DBs             []*DB                      `json:"dbs"`
	DBServers       []*DBServer                `json:"db_servers"`
	DNSZones        []*DNSZone                 `json:"dns_zones"`
	FTP             []*FTPUser                 `json:"ftp"`
	Cron            []*CronJob                 `json:"cron"`
	Certs           []*Cert                    `json:"certs"`
	Backups         []*Backup                  `json:"backups"`
	Notices         []*Notice                  `json:"notices"`
	Settings        Settings                   `json:"settings"`
}

var (
	store State
	mu    sync.Mutex // защищает store и все изменения конфигурации сервера
)

func statePath() string { return filepath.Join(cfg.Home, "etc", "state.json") }

func mustLoad() {
	// Apply defaults before decoding so an explicitly saved 00:00 is preserved.
	store = State{Settings: Settings{BackupHour: 3}}
	os.MkdirAll(filepath.Join(cfg.Home, "etc"), 0700)
	os.MkdirAll(filepath.Join(cfg.Home, "var"), 0700)
	b, err := os.ReadFile(statePath())
	if err == nil {
		if err := json.Unmarshal(b, &store); err != nil {
			log.Fatalf("state.json повреждён: %v", err)
		}
	}
	s := &store.Settings
	if s.AdminUser == "" {
		s.AdminUser = "admin"
	}
	if s.BackupKeep == 0 {
		s.BackupKeep = 7
	}
	if s.DiskWarn == 0 {
		s.DiskWarn = 90
	}
	if s.PMASecret == "" {
		s.PMASecret = randToken(16)
	}
	refreshPanelProxyLocked()
}

// save вызывается под mu.
func save() {
	if err := saveChecked(); err != nil {
		log.Printf("save: %v", err)
	}
}
func saveChecked() error {
	b, err := json.MarshalIndent(&store, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(statePath(), b, 0600)
}

// writeFileAtomic пишет файл через временный: данные сбрасываются на диск до переименования, поэтому при потере
// питания или принудительной остановке остаётся прежняя или новая версия, но не пустой файл.
func writeFileAtomic(p string, b []byte, mode os.FileMode) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, p); err != nil {
		return err
	}
	if d, e := os.Open(filepath.Dir(p)); e == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

var reAdminLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,31}$`)

// validAdminLogin проверяет логин администратора: латиница, цифры, «_», «.», «-», 3–32 символа.
func validAdminLogin(name string) error {
	if !reAdminLogin.MatchString(name) {
		return errf("Логин: 3–32 символа, латинские буквы, цифры, «_», «.» и «-», начинается с буквы или цифры")
	}
	return nil
}

// randomLogin создаёт случайный логин из 8 символов (первая — буква), как в других панелях.
func randomLogin() string {
	const letters, alnum = "abcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	r := make([]byte, 8)
	if _, err := rand.Read(r); err != nil {
		return "admin" + fmt.Sprint(time.Now().UnixNano()%1000)
	}
	for i := range b {
		set := alnum
		if i == 0 {
			set = letters
		}
		b[i] = set[int(r[i])%len(set)]
	}
	return string(b)
}

// setAdminLogin меняет логин администратора: принятые условия и действующие сессии переходят на новое имя.
func setAdminLogin(name string) error {
	name = strings.TrimSpace(name)
	if err := validAdminLogin(name); err != nil {
		return err
	}
	mu.Lock()
	old := store.Settings.AdminUser
	if old == name {
		mu.Unlock()
		return nil
	}
	store.Settings.AdminUser = name
	if a, ok := store.TermsAcceptance[old]; ok {
		store.TermsAcceptance[name] = a
		delete(store.TermsAcceptance, old)
	}
	save()
	mu.Unlock()
	sessMu.Lock()
	for _, s := range sessions {
		if s.User == old {
			s.User = name
		}
	}
	sessMu.Unlock()
	return nil
}

func setAdminPassword(p string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	mu.Lock()
	store.Settings.AdminHash = string(h)
	save()
	mu.Unlock()
	return nil
}

func findSite(domain string) *Site {
	for _, s := range store.Sites {
		if s.Domain == domain {
			return s
		}
	}
	return nil
}

func findUser(name string) *SiteUser {
	for _, u := range store.Users {
		if u.Name == name {
			return u
		}
	}
	return nil
}

func findCert(name string) *Cert {
	for _, c := range store.Certs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func findDB(name string) *DB {
	for _, d := range store.DBs {
		if d.Name == name {
			return d
		}
	}
	return nil
}
