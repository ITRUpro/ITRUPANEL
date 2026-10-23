// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type MailDomain struct {
	Hostname       string `json:"hostname"`
	Certificate    string `json:"certificate"`
	DefaultQuotaMB int64  `json:"default_quota_mb"`
	QuotaMB        int64  `json:"quota_mb"`
	MaxBoxes       int    `json:"max_boxes"`
	CatchAll       string `json:"catch_all"`
	// Веб-почта и проверка на спам включаются для каждого домена отдельно.
	Webmail bool `json:"webmail,omitempty"`
	NoSpam  bool `json:"no_spam,omitempty"`

	DNS       MailDNSOptions `json:"dns"`
	Name      string         `json:"name"`
	PublicKey string         `json:"public_key"`
}
type MailBox struct {
	Address string    `json:"address"`
	Name    string    `json:"name,omitempty"` // имя владельца ящика, необязательно
	Hash    string    `json:"hash"`
	Secret  string    `json:"secret,omitempty"` // пароль, зашифрованный ключом mail-secret.key (для копирования)
	Enabled bool      `json:"enabled"`
	Created time.Time `json:"created"`
	QuotaMB int64     `json:"quota_mb"`
}
type MailAlias struct {
	Address string `json:"address"`
	Target  string `json:"target"`
}
type MailState struct {
	MXToolboxKey string          `json:"mxtoolbox_key,omitempty"`
	Ready        bool            `json:"ready"`
	Spam         SpamSettings    `json:"spam"`
	Webmail      WebmailSettings `json:"webmail"`
	Hostname     string          `json:"hostname"`
	Certificate  string          `json:"certificate"`
	Dovecot24    bool            `json:"dovecot24"`
	UID          int             `json:"uid"`
	GID          int             `json:"gid"`
	Domains      []MailDomain    `json:"domains"`
	Boxes        []MailBox       `json:"boxes"`
	Aliases      []MailAlias     `json:"aliases"`
	Forwards     []MailForward   `json:"forwards"`
}

const mailRoot = "/var/vmail/itrupanel"
const mailOwned = "/etc/itrupanel/mail-owned"
const mailTLS = "/etc/itrupanel/mail"

var mailLocal = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._+\-]{0,62}[a-z0-9])?$`)

func mailAddress(raw string) (string, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "@")
	if len(parts) != 2 || !mailLocal.MatchString(parts[0]) || strings.Contains(parts[0], "..") {
		return "", errf("Адрес вида name@example.ru; имя — латиница, цифры, точка, +, - или _")
	}
	d, err := toASCII(parts[1])
	if err != nil {
		return "", err
	}
	return parts[0] + "@" + d, nil
}
func mailHome(address string) string {
	p := strings.SplitN(address, "@", 2)
	return mailRoot + "/" + p[1] + "/" + p[0]
}
func mailDomainExists(m MailState, domain string) bool {
	for _, d := range m.Domains {
		if d.Name == domain {
			return true
		}
	}
	return false
}
func mailAddressUsed(m MailState, address string) bool {
	for _, f := range m.Forwards {
		if f.Address == address {
			return true
		}
	}
	for _, b := range m.Boxes {
		if b.Address == address {
			return true
		}
	}
	for _, a := range m.Aliases {
		if a.Address == address {
			return true
		}
	}
	return false
}
func checkMailState(m MailState) error {
	for _, d := range m.Domains {
		name, err := toASCII(d.Name)
		if err != nil || name != d.Name {
			return errf("Неверный почтовый домен")
		}
	}
	for _, a := range m.Aliases {
		from, e1 := mailAddress(a.Address)
		to, e2 := mailAddress(a.Target)
		if e1 != nil || e2 != nil || from != a.Address || to != a.Target {
			return errf("Неверный адрес псевдонима")
		}
	}
	seen := map[string]bool{}
	for _, b := range m.Boxes {
		if b.QuotaMB < 0 || b.QuotaMB > 1048576 {
			return errf("Квота: от 0 до 1048576 МиБ; 0 — без ограничения")
		}
		a, err := mailAddress(b.Address)
		if err != nil || a != b.Address {
			return errf("Неверный адрес ящика")
		}
		if seen[a] {
			return errf("Адрес уже занят")
		}
		seen[a] = true
		if !mailDomainExists(m, strings.SplitN(a, "@", 2)[1]) {
			return errf("Сначала добавьте почтовый домен")
		}
	}
	for _, a := range m.Aliases {
		if seen[a.Address] {
			return errf("Адрес уже занят")
		}
		seen[a.Address] = true
		if !mailDomainExists(m, strings.SplitN(a.Address, "@", 2)[1]) {
			return errf("Сначала добавьте почтовый домен")
		}
		// Local aliases deliberately target an existing mailbox, not another alias:
		// this rules out loops and forwarding to disabled/nonexistent recipients.
		found := false
		for _, b := range m.Boxes {
			if b.Address == a.Target {
				found = true
				break
			}
		}
		if !found {
			return errf("Псевдоним должен указывать на существующий ящик панели")
		}
	}
	if err := checkMailDomains(m); err != nil {
		return err
	}
	return checkMailForwards(m)
}
func mailConfigFiles(m MailState) (map[string]string, error) {
	if err := checkMailState(m); err != nil {
		return nil, err
	}
	crt, key := mailTLS+"/server.crt", mailTLS+"/server.key"
	if m.Certificate != "" {
		c := findCert(m.Certificate)
		if c == nil {
			return nil, errf("Сертификат почты не найден")
		}
		if _, err := validatedPair(c.Crt, c.Key, m.Hostname); err != nil {
			return nil, err
		}
		crt, key = c.Crt, c.Key
	}
	var domains, boxes, aliases, senders, passwd, keyTable, signTable strings.Builder
	enabled := map[string]bool{}
	for _, d := range m.Domains {
		fmt.Fprintf(&domains, "%s OK\n", d.Name)
		fmt.Fprintf(&keyTable, "itru.%s %s:itru:/etc/opendkim/itrupanel/keys/%s.key\n", d.Name, d.Name, d.Name)
		fmt.Fprintf(&signTable, "*@%s itru.%s\n", d.Name, d.Name)
	}
	for _, b := range m.Boxes {
		if !b.Enabled {
			continue
		}
		enabled[b.Address] = true
		fmt.Fprintf(&boxes, "%s OK\n", b.Address)
		fmt.Fprintf(&senders, "%s %s\n", b.Address, b.Address)
		quota := fmt.Sprintf("userdb_quota_rule=*:storage=%dM", b.QuotaMB)
		if m.Dovecot24 {
			quota = fmt.Sprintf("userdb_quota_storage_size=%dM", b.QuotaMB)
		}
		fmt.Fprintf(&passwd, "%s:%s:%d:%d::%s::%s\n", b.Address, b.Hash, m.UID, m.GID, mailHome(b.Address), quota)
	}
	for _, a := range m.Aliases {
		if enabled[a.Target] {
			fmt.Fprintf(&aliases, "%s %s\n", a.Address, a.Target)
			fmt.Fprintf(&senders, "%s %s\n", a.Address, a.Target)
		}
	}
	aliases.WriteString(mailForwardMap(m))
	for _, d := range m.Domains {
		if d.CatchAll != "" {
			fmt.Fprintf(&aliases, "@%s %s\n", d.Name, d.CatchAll)
		}
	}
	// Preserve delivery to real mailboxes when a wildcard alias is enabled.
	var blocked strings.Builder
	for _, b := range m.Boxes {
		if !b.Enabled {
			fmt.Fprintf(&blocked, "%s REJECT Mailbox disabled\n", b.Address)
		}
		for _, d := range m.Domains {
			if d.CatchAll != "" && strings.HasSuffix(b.Address, "@"+d.Name) {
				if !strings.HasPrefix(aliases.String(), b.Address+" ") && !strings.Contains(aliases.String(), "\n"+b.Address+" ") {
					fmt.Fprintf(&aliases, "%s %s\n", b.Address, b.Address)
				}
			}
		}
	}

	main := fmt.Sprintf(`# ITRUPANEL: managed mail
myhostname = %s
mydestination = localhost
inet_interfaces = all
inet_protocols = ipv4
mynetworks = 127.0.0.0/8
smtpd_banner = $myhostname ESMTP
biff = no
append_dot_mydomain = no
disable_vrfy_command = yes
smtpd_helo_required = yes
smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination
smtpd_recipient_restrictions = reject_non_fqdn_recipient, check_recipient_access texthash:/etc/postfix/itrupanel/blocked, reject_unlisted_recipient
smtpd_reject_unlisted_recipient = yes
virtual_mailbox_domains = texthash:/etc/postfix/itrupanel/domains
virtual_mailbox_maps = texthash:/etc/postfix/itrupanel/boxes
virtual_alias_maps = texthash:/etc/postfix/itrupanel/aliases
virtual_transport = lmtp:unix:private/itrupanel-lmtp
smtpd_sender_login_maps = texthash:/etc/postfix/itrupanel/senders
smtpd_sasl_type = dovecot
smtpd_sasl_path = private/itrupanel-auth
smtpd_sasl_auth_enable = no
smtpd_tls_auth_only = yes
smtpd_tls_security_level = may
smtpd_tls_cert_file = %s
smtpd_tls_key_file = %s
smtpd_tls_mandatory_protocols = >=TLSv1.2
smtp_tls_security_level = may
message_size_limit = 52428800
mailbox_size_limit = 0
smtpd_milters = inet:127.0.0.1:8891
non_smtpd_milters = inet:127.0.0.1:8891
milter_protocol = 6
milter_default_action = tempfail
`, m.Hostname, crt, key)
	if m.Spam.Enabled {
		main = strings.ReplaceAll(main, "= inet:127.0.0.1:8891", "= inet:127.0.0.1:11332, inet:127.0.0.1:8891")
	}
	sni, doveSNI, err := mailDomainTLS(m)
	if err != nil {
		return nil, err
	}
	main += "tls_server_sni_maps = texthash:/etc/postfix/itrupanel/sni\n"
	dove := dovecotConfig(m, crt, key) + doveSNI
	master := readFile("/etc/postfix/master.cf")
	if cfg.Dry && master == "" {
		master = "smtp inet n - n - - smtpd\n"
	}
	if i := strings.Index(master, "# BEGIN ITRUPANEL SUBMISSION"); i >= 0 {
		end := strings.Index(master[i:], "# END ITRUPANEL SUBMISSION")
		if end < 0 {
			return nil, errf("Повреждён блок submission")
		}
		master = master[:i] + master[i+end+len("# END ITRUPANEL SUBMISSION"):]
	}
	// Пустые строки перед блоком не копятся: иначе файл менялся при каждом применении почты.
	master = strings.TrimRight(master, "\n") + "\n"
	master += `\n# BEGIN ITRUPANEL SUBMISSION
submission inet n - n - - smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_recipient_restrictions=check_recipient_access,texthash:/etc/postfix/itrupanel/blocked,permit_sasl_authenticated,reject
  -o smtpd_relay_restrictions=permit_sasl_authenticated,reject
  -o smtpd_sender_restrictions=reject_authenticated_sender_login_mismatch
  -o milter_macro_daemon_name=ORIGINATING
# END ITRUPANEL SUBMISSION
`
	master = strings.Replace(master, `\n# BEGIN`, "\n# BEGIN", 1)
	files := map[string]string{
		"/etc/postfix/itrupanel/sni": sni, "/etc/postfix/itrupanel/blocked": blocked.String(),
		"/etc/postfix/main.cf": main, "/etc/postfix/master.cf": master, "/etc/dovecot/dovecot.conf": dove,
		"/etc/postfix/itrupanel/domains": domains.String(), "/etc/postfix/itrupanel/boxes": boxes.String(), "/etc/postfix/itrupanel/aliases": aliases.String(), "/etc/postfix/itrupanel/senders": senders.String(),
		"/etc/dovecot/itrupanel-users":      passwd.String(),
		"/etc/opendkim/itrupanel/key.table": keyTable.String(), "/etc/opendkim/itrupanel/sign.table": signTable.String(),
		"/etc/opendkim.conf": `# ITRUPANEL: managed mail
Syslog yes
UMask 007
UserID opendkim
Mode sv
Canonicalization relaxed/simple
Socket inet:8891@127.0.0.1
PidFile /run/opendkim/opendkim.pid
KeyTable file:/etc/opendkim/itrupanel/key.table
SigningTable refile:/etc/opendkim/itrupanel/sign.table
InternalHosts 127.0.0.1
MacroList daemon_name=ORIGINATING
RequireSafeKeys true
`}
	if m.Spam.Installed {
		extra, e := spamFiles(m.Spam)
		if e != nil {
			return nil, e
		}
		for p, s := range extra {
			files[p] = s
		}
		files[rspamdSettingsPath] = rspamdDomainSettings(m)
	}
	if m.Webmail.Ready {
		extra, e := roundcubeMailFiles(m)
		if e != nil {
			return nil, e
		}
		for p, s := range extra {
			files[p] = s
		}
		vhosts, e := webmailVhostFiles(m)
		if e != nil {
			return nil, e
		}
		for p, s := range vhosts {
			files[p] = s
		}
	}
	return files, nil
}
func dovecotConfig(m MailState, crt, key string) string {
	common := fmt.Sprintf(`
protocols = imap lmtp
listen = *
ssl = required
auth_mechanisms = plain login
first_valid_uid = %d
mail_uid = %d
mail_gid = %d
namespace inbox {
 inbox = yes
 separator = /
 mailbox Sent {
  special_use = \\Sent
  auto = subscribe
 }
 mailbox Drafts {
  special_use = \\Drafts
  auto = subscribe
 }
 mailbox Trash {
  special_use = \\Trash
  auto = subscribe
 }
}
service imap-login {
 inet_listener imap {
  port = 0
 }
 inet_listener imaps {
  port = 993
  ssl = yes
 }
}
service auth {
 unix_listener /var/spool/postfix/private/itrupanel-auth {
  mode = 0660
  user = postfix
  group = postfix
 }
}
service lmtp {
 unix_listener /var/spool/postfix/private/itrupanel-lmtp {
  mode = 0600
  user = postfix
  group = postfix
 }
}
protocol lmtp {
 postmaster_address = postmaster@%s
}
`, m.UID, m.UID, m.GID, m.Hostname)
	common = strings.ReplaceAll(common, `\\`, `\`)
	if m.Dovecot24 {
		return fmt.Sprintf(`dovecot_config_version = 2.4.0
dovecot_storage_version = 2.4.0
# ITRUPANEL: managed mail
auth_allow_cleartext = no
ssl_server_cert_file = %s
ssl_server_key_file = %s
ssl_min_protocol = TLSv1.2
mail_driver = maildir
mail_path = ~/Maildir
passdb passwd-file {
 passwd_file_path = /etc/dovecot/itrupanel-users
}
userdb passwd-file {
 passwd_file_path = /etc/dovecot/itrupanel-users
}
`, crt, key) + common + dovecotQuotaConfig(m.Dovecot24)
	}
	return fmt.Sprintf(`# ITRUPANEL: managed mail
disable_plaintext_auth = yes
ssl_cert = <%s
ssl_key = <%s
ssl_min_protocol = TLSv1.2
mail_location = maildir:~/Maildir
passdb {
 driver = passwd-file
 args = /etc/dovecot/itrupanel-users
}
userdb {
 driver = passwd-file
 args = /etc/dovecot/itrupanel-users
}
`, crt, key) + common + dovecotQuotaConfig(m.Dovecot24)
}

// Snapshot all generated files before writing; restore and reload on failure.
func applyMail(m MailState) (undo func() error, err error) {
	files, err := mailConfigFiles(m)
	if err != nil {
		return nil, err
	}
	type snap struct {
		data   []byte
		exists bool
		mode   os.FileMode
	}
	previous := map[string]snap{}
	for p := range files {
		b, e := os.ReadFile(P(p))
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		mode := os.FileMode(0644)
		if st, e := os.Stat(P(p)); e == nil {
			mode = st.Mode().Perm()
		}
		previous[p] = snap{b, e == nil, mode}
	}
	undo = func() error {
		var first error
		for p, s := range previous {
			var e error
			if s.exists {
				e = writeFile(p, s.data, s.mode)
				if g := mailFileGroup(p); e == nil && g != "" {
					_, e = run("chown", "root:"+g, p)
				}
			} else {
				e = os.Remove(P(p))
				if os.IsNotExist(e) {
					e = nil
				}
			}
			if first == nil {
				first = e
			}
		}
		if store.Mail.Ready {
			for _, svc := range mailServices(store.Mail) {
				action := "reload"
				if svc == "rspamd" || svc == "itrupanel-spam-redis" {
					action = "restart"
				}
				if e := service(action, svc); first == nil {
					first = e
				}
			}
		}
		if m.Webmail.Ready || store.Mail.Webmail.Ready {
			if e := nginxTestReload(); first == nil {
				first = e
			}
		}
		return first
	}
	// Проверяются и перезагружаются только службы, чьи файлы изменились: добавление ящика не
	// перезапускает Rspamd (загрузка правил занимает десятки секунд) и не держит панель.
	changed, changedFiles := map[string]bool{}, map[string]string{}
	for p, s := range files {
		if old := previous[p]; !old.exists || string(old.data) != s {
			changed[mailFileComponent(p)] = true
			changedFiles[p] = s
		}
	}
	if len(changed) == 0 {
		return undo, nil
	}
	touched := func(component string) bool { return changed[component] || changed["all"] }
	defer func() {
		if err != nil {
			if e := undo(); e != nil {
				err = fmt.Errorf("%w; откат: %v", err, e)
			}
		}
	}()
	for p, s := range changedFiles {
		perm := mailFileMode(p)
		if err = writeFile(p, []byte(s), perm); err != nil {
			return
		}
	}
	for p := range changedFiles {
		if g := mailFileGroup(p); g != "" {
			if _, err = run("chown", "root:"+g, p); err != nil {
				return
			}
		}
	}
	if m.Spam.Installed && touched("spam") {
		if _, err = runTimeout(30*time.Second, "", "rspamadm", "configtest"); err != nil {
			return
		}
		if _, err = run("systemctl", "daemon-reload"); err != nil {
			return
		}
		if _, err = run("systemctl", "unmask", "rspamd"); err != nil {
			return
		}
	}
	if m.Webmail.Ready && touched("web") {
		if _, err = runTimeout(30*time.Second, "", phpCLI(m.Webmail.PHP), "-l", roundcubeConfigPath()); err != nil {
			return
		}
	}
	for _, c := range []struct {
		component string
		cmd       []string
	}{{"dovecot", []string{"doveconf", "-n"}}, {"postfix", []string{"postfix", "check"}}, {"opendkim", []string{"opendkim", "-n", "-x", "/etc/opendkim.conf"}}} {
		if !touched(c.component) {
			continue
		}
		if _, err = runTimeout(30*time.Second, "", c.cmd[0], c.cmd[1:]...); err != nil {
			return
		}
	}
	if store.Mail.Ready {
		for _, svc := range mailServices(m) {
			component, action := svc, "reload"
			if svc == "rspamd" || svc == "itrupanel-spam-redis" {
				component, action = "spam", "restart"
			}
			if !touched(component) {
				continue
			}
			if err = service(action, svc); err != nil {
				return
			}
		}
	}
	if m.Webmail.Ready && touched("web") {
		err = nginxTestReload()
	}
	return
}

// mailFileComponent — какой службе принадлежит файл почтовой конфигурации. Неизвестные файлы
// (например, общие сертификаты) относятся ко всем службам.
func mailFileComponent(p string) string {
	switch {
	case strings.HasPrefix(p, "/etc/rspamd/"), p == "/etc/itrupanel/rspamd-redis.conf", p == "/etc/systemd/system/itrupanel-spam-redis.service":
		return "spam"
	case p == roundcubeConfigPath(), strings.HasPrefix(p, webmailData+"/"), strings.HasPrefix(p, vhostDir+"/"):
		return "web"
	case strings.HasPrefix(p, "/etc/postfix/"):
		return "postfix"
	case strings.HasPrefix(p, "/etc/dovecot/"):
		return "dovecot"
	case strings.HasPrefix(p, "/etc/opendkim/"), p == "/etc/opendkim.conf":
		return "opendkim"
	}
	return "all"
}
func cloneMail(m MailState) MailState {
	b, _ := json.Marshal(m)
	var c MailState
	json.Unmarshal(b, &c)
	return c
}
func changeMail(fn func(*MailState) error) error {
	mu.Lock()
	defer mu.Unlock()
	if !store.Mail.Ready {
		return errf("Сначала настройте почтовый сервер")
	}
	next := cloneMail(store.Mail)
	if err := fn(&next); err != nil {
		return err
	}
	undo, err := applyMail(next)
	if err != nil {
		return err
	}
	prev := store.Mail
	store.Mail = next
	if err = saveChecked(); err != nil {
		store.Mail = prev
		undo()
		return err
	}
	return nil
}
func setupMail(host, cert string, j *Job) error {
	mailSetupMu.Lock()
	defer mailSetupMu.Unlock()
	if osInfo.Family != "debian" && osInfo.Family != "rhel" {
		return errf("Автоустановка почты доступна на Debian, Ubuntu, AlmaLinux, Rocky, CentOS Stream и RHEL")
	}
	jobStage(j, "Проверка существующих почтовых служб")
	if !fileExists(mailOwned) && !cfg.Dry {
		for _, bin := range []string{"postconf", "dovecot", "exim4", "opendkim"} {
			// На RHEL-системах Postfix часто стоит в образе для локальной почты root —
			// такой нетронутый Postfix можно перенастроить, а рабочий почтовый сервер — нет
			if bin == "postconf" && osInfo.Family == "rhel" && stockPostfix() {
				continue
			}
			if have(bin) {
				return errf("Уже установлена почтовая служба %s. Автоматический перенос существующей почты не выполняется", bin)
			}
		}
	}
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if err := writeFile(mailOwned, []byte("ITRUPANEL\n"), 0600); err != nil {
		return err
	}
	if !cfg.Dry && !osInfo.Systemd {
		return errf("Установка почты требует systemd")
	}
	mu.Lock()
	ready := store.Mail.Ready
	mu.Unlock()
	setupComplete := ready
	defer func() {
		if !setupComplete {
			for _, svc := range []string{"postfix", "dovecot", "opendkim"} {
				run("systemctl", "stop", svc)
				run("systemctl", "mask", svc)
			}
		}
	}()
	if !ready {
		for _, svc := range []string{"postfix", "dovecot", "opendkim"} {
			if _, err := run("systemctl", "mask", svc); err != nil {
				return err
			}
		}
	}
	jobStage(j, "Установка Postfix, Dovecot и OpenDKIM")
	pkgs := []string{"postfix", "dovecot-core", "dovecot-imapd", "dovecot-lmtpd", "opendkim", "opendkim-tools"}
	if osInfo.Family == "rhel" {
		// OpenDKIM на RHEL-системах — в EPEL; Dovecot — одним пакетом с IMAP и LMTP
		if err := ensureEPEL(j); err != nil {
			return err
		}
		pkgs = []string{"postfix", "dovecot", "opendkim", "opendkim-tools"}
	}
	if err := installPackagesJob(j, pkgs...); err != nil {
		return err
	}
	version := "2.4.0"
	if !cfg.Dry {
		out, err := run("dovecot", "--version")
		if err != nil {
			return err
		}
		version = out
	}
	if !strings.HasPrefix(version, "2.3.") && !strings.HasPrefix(version, "2.4.") {
		return errf("Не поддерживается Dovecot %s", version)
	}
	jobStage(j, "Создание хранилища почты и TLS")
	if _, err := run("id", "itrumail"); err != nil || cfg.Dry {
		if _, err := run("useradd", "--system", "--user-group", "--home-dir", mailRoot, "--shell", "/usr/sbin/nologin", "itrumail"); err != nil {
			return err
		}
	}
	uid, gid := 5000, 5000
	if !cfg.Dry {
		u, err := run("id", "-u", "itrumail")
		if err != nil {
			return err
		}
		g, err := run("id", "-g", "itrumail")
		if err != nil {
			return err
		}
		uid, err = strconv.Atoi(u)
		if err != nil {
			return err
		}
		gid, err = strconv.Atoi(g)
		if err != nil {
			return err
		}
	}
	if uid <= 0 || gid <= 0 {
		return errf("Почта не может работать от root")
	}
	if err := os.MkdirAll(P(mailRoot), 0750); err != nil {
		return err
	}
	if _, err := run("chown", "itrumail:itrumail", mailRoot); err != nil {
		return err
	}
	// Ensure vmail ancestors can be traversed even on a freshly created hierarchy.
	if err := os.Chmod(P("/var/vmail"), 0755); err != nil {
		return err
	}
	if err := labelMailStore(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	next := cloneMail(store.Mail)
	next.Hostname, next.Certificate, next.Dovecot24, next.UID, next.GID = host, cert, strings.HasPrefix(version, "2.4."), uid, gid
	tlsCommitted := false
	if cert == "" {
		oldCRT, crtErr := os.ReadFile(P(mailTLS + "/server.crt"))
		oldKey, keyErr := os.ReadFile(P(mailTLS + "/server.key"))
		if crtErr != nil && !os.IsNotExist(crtErr) {
			return crtErr
		}
		if keyErr != nil && !os.IsNotExist(keyErr) {
			return keyErr
		}
		defer func() {
			if tlsCommitted {
				return
			}
			if os.IsNotExist(crtErr) {
				os.Remove(P(mailTLS + "/server.crt"))
			} else {
				writeFile(mailTLS+"/server.crt", oldCRT, 0644)
			}
			if os.IsNotExist(keyErr) {
				os.Remove(P(mailTLS + "/server.key"))
			} else {
				writeFile(mailTLS+"/server.key", oldKey, 0600)
			}
			if ready {
				service("reload", "postfix")
				service("reload", "dovecot")
			}
		}()
		cr, k, err := selfSigned([]string{host}, 365)
		if err != nil {
			return err
		}
		if err = writeFile(mailTLS+"/server.crt", cr, 0644); err != nil {
			return err
		}
		if err = writeFile(mailTLS+"/server.key", k, 0600); err != nil {
			return err
		}
	}
	jobStage(j, "Проверка SMTP/IMAP-конфигураций")
	undo, err := applyMail(next)
	if err != nil {
		return err
	}
	for _, svc := range []string{"opendkim", "dovecot", "postfix"} {
		if _, err = run("systemctl", "unmask", svc); err != nil {
			undo()
			return err
		}
		if err = service("enable", svc); err != nil {
			undo()
			return err
		}
	}
	next.Ready = true
	old := store.Mail
	store.Mail = next
	if err = saveChecked(); err != nil {
		store.Mail = old
		undo()
		return err
	}
	tlsCommitted = true
	setupComplete = true
	if err := openMailFirewalld(j); err != nil {
		return err
	}
	jobStage(j, "Открытие почтовых портов в активном UFW")
	if have("ufw") {
		out, _ := run("ufw", "status")
		if strings.Contains(out, "Status: active") {
			for _, p := range []string{"25/tcp", "587/tcp", "993/tcp"} {
				if _, err := run("ufw", "allow", p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func createDKIM(domain string) (string, error) {
	path := "/etc/opendkim/itrupanel/keys/" + domain + ".key"
	var key *rsa.PrivateKey
	if b, err := os.ReadFile(P(path)); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return "", errf("Повреждён DKIM-ключ")
		}
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
	}
	if key == nil {
		var err error
		key, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		if err = writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0640); err != nil {
			return "", err
		}
	}
	if _, err := run("chown", "root:opendkim", path); err != nil {
		return "", err
	}
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return base64.StdEncoding.EncodeToString(pub), nil
}
func registerMail() {
	registerMailDomains()
	registerMailDNS()
	registerMailExtras()
	registerMailFeatures()
	registerMailSecrets()
	registerMailForwards()

	api("mail.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		m := store.Mail
		boxes := []map[string]any{}
		for _, b := range m.Boxes {
			boxes = append(boxes, map[string]any{"address": b.Address, "name": b.Name, "enabled": b.Enabled, "created": b.Created, "quota_mb": b.QuotaMB, "has_password": b.Secret != ""})
		}
		return map[string]any{"quota_active": strings.Contains(readFile("/etc/dovecot/dovecot.conf"), "# ITRUPANEL quotas"), "spam": m.Spam, "rbl_catalog": spamRBLCatalog, "spam_active": m.Spam.Enabled && serviceActive("rspamd"), "webmail": m.Webmail, "ready": m.Ready, "hostname": m.Hostname, "certificate": m.Certificate, "domains": m.Domains, "site_domains": mailSiteDomains(m), "boxes": boxes, "aliases": m.Aliases, "forwards": m.Forwards, "certs": mailCertificateList(), "smtp": m.Ready && serviceActive("postfix"), "imap": m.Ready && serviceActive("dovecot"), "dkim": m.Ready && serviceActive("opendkim")}, nil
	})
	apiOp("mail.setup", "Настройка почты", func(q *Req) (any, error) {
		var in struct {
			Hostname, Certificate, Email string
			LE                           bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		host, err := toASCII(in.Hostname)
		if err != nil {
			return nil, err
		}
		if in.LE {
			in.Certificate = ""
		} else if in.Certificate != "" {
			mu.Lock()
			c := findCert(in.Certificate)
			if c == nil {
				mu.Unlock()
				return nil, errf("Сертификат не найден")
			}
			_, err = validatedPair(c.Crt, c.Key, host)
			mu.Unlock()
			if err != nil {
				return nil, err
			}
		}
		id := startJob("mail:setup", "Настройка почтового сервера", q.S.User, func(j *Job) (any, error) {
			if in.LE {
				c, err := issueMailHostLE(j, host, in.Email)
				if err != nil {
					return nil, err
				}
				in.Certificate = c
			}
			err := setupMail(host, in.Certificate, j)
			return map[string]string{"info": "Почтовый сервер настроен"}, err
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("mail.domain.add", "Почта: добавление домена", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		d, err := toASCII(in.Name)
		if err != nil {
			return nil, err
		}
		return nil, changeMail(func(m *MailState) error {
			if mailDomainExists(*m, d) {
				return errf("Домен уже добавлен")
			}
			pub, err := createDKIM(d)
			if err != nil {
				return err
			}
			nd := MailDomain{Name: d, PublicKey: pub, DefaultQuotaMB: 5120}
			// Как в aaPanel: у каждого домена своё имя mail.<домен>; сертификат на него выпускается
			// кнопкой «SSL», когда DNS имени будет указывать на этот сервер.
			if host := "mail." + d; host != m.Hostname {
				nd.Hostname = host
			}
			m.Domains = append(m.Domains, nd)
			return nil
		})
	})
	apiOp("mail.domain.delete", "Почта: удаление домена", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, changeMail(func(m *MailState) error {
			for _, b := range m.Boxes {
				if strings.HasSuffix(b.Address, "@"+in.Name) {
					return errf("Сначала удалите ящики домена")
				}
			}
			for _, a := range m.Aliases {
				if strings.HasSuffix(a.Address, "@"+in.Name) {
					return errf("Сначала удалите псевдонимы домена")
				}
			}
			for _, f := range m.Forwards {
				if strings.HasSuffix(f.Address, "@"+in.Name) {
					return errf("Сначала удалите пересылки домена")
				}
			}
			keep := []MailDomain{}
			for _, d := range m.Domains {
				if d.Name != in.Name {
					keep = append(keep, d)
				}
			}
			m.Domains = keep
			return nil
		})
	})
	apiOp("mail.box.save", "Почта: сохранение ящика", func(q *Req) (any, error) {
		var in struct {
			Address, Password string
			Edit, Enabled     bool
			QuotaMB           *int64  `json:"quota_mb"`
			Name              *string `json:"name"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Name != nil {
			*in.Name = strings.TrimSpace(*in.Name)
			if len([]rune(*in.Name)) > 100 || strings.ContainsAny(*in.Name, "\x00\r\n\t<>") {
				return nil, errf("Имя ящика: до 100 символов, без переводов строк и угловых скобок")
			}
		}
		if in.QuotaMB != nil && (*in.QuotaMB < 0 || *in.QuotaMB > 1048576) {
			return nil, errf("Квота: от 0 до 1048576 МиБ")
		}
		address, err := mailAddress(in.Address)
		if err != nil {
			return nil, err
		}
		pass := in.Password
		if !in.Edit && pass == "" {
			pass = randPass(20)
		}
		hash, secret := "", ""
		if pass != "" {
			if len(pass) < 12 || len(pass) > 72 {
				return nil, errf("Пароль: от 12 до 72 байт")
			}
			b, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			hash = "{BLF-CRYPT}" + string(b)
			if secret, err = mailSealPassword(pass); err != nil {
				return nil, err
			}
		}
		err = changeMail(func(m *MailState) error {
			for i := range m.Boxes {
				if m.Boxes[i].Address == address {
					if !in.Edit {
						return errf("Ящик уже существует")
					}
					m.Boxes[i].Enabled = in.Enabled
					if in.Name != nil {
						m.Boxes[i].Name = *in.Name
					}
					if in.QuotaMB != nil {
						m.Boxes[i].QuotaMB = *in.QuotaMB
					}
					if hash != "" {
						m.Boxes[i].Hash, m.Boxes[i].Secret = hash, secret
					}
					return nil
				}
			}
			if in.Edit {
				return errf("Ящик не найден")
			}
			if mailAddressUsed(*m, address) {
				return errf("Адрес занят")
			}
			if !mailDomainExists(*m, strings.SplitN(address, "@", 2)[1]) {
				return errf("Сначала добавьте почтовый домен")
			}
			home := mailHome(address)
			for _, sub := range []string{"", "/Maildir/cur", "/Maildir/new", "/Maildir/tmp"} {
				if err := os.MkdirAll(P(home+sub), 0700); err != nil {
					return err
				}
			}
			if _, err := run("chown", "itrumail:itrumail", mailRoot+"/"+strings.SplitN(address, "@", 2)[1], home, home+"/Maildir", home+"/Maildir/cur", home+"/Maildir/new", home+"/Maildir/tmp"); err != nil {
				return err
			}
			quota := int64(0)
			for _, d := range m.Domains {
				if strings.HasSuffix(address, "@"+d.Name) {
					quota = d.DefaultQuotaMB
				}
			}
			if in.QuotaMB != nil {
				quota = *in.QuotaMB
			}
			box := MailBox{Address: address, Hash: hash, Secret: secret, Enabled: true, Created: time.Now(), QuotaMB: quota}
			if in.Name != nil {
				box.Name = *in.Name
			}
			m.Boxes = append(m.Boxes, box)
			return nil
		})
		if err == nil && in.Edit {
			run("doveadm", "kick", address)
		}
		if err != nil {
			return nil, err
		}
		return map[string]string{"address": address, "password": pass}, nil
	})
	apiOp("mail.box.delete", "Почта: удаление ящика", func(q *Req) (any, error) {
		var in struct{ Address string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		address, err := mailAddress(in.Address)
		if err != nil {
			return nil, err
		}
		err = changeMail(func(m *MailState) error {
			for _, a := range m.Aliases {
				if a.Target == address {
					return errf("Сначала удалите псевдонимы этого ящика")
				}
			}
			for _, f := range m.Forwards {
				if f.Address == address {
					return errf("Сначала удалите пересылку этого ящика")
				}
			}
			keep := []MailBox{}
			for _, b := range m.Boxes {
				if b.Address != address {
					keep = append(keep, b)
				}
			}
			m.Boxes = keep
			return nil
		})
		if err == nil {
			run("doveadm", "kick", address)
		}
		return nil, err
	})
	apiOp("mail.alias.save", "Почта: псевдоним", func(q *Req) (any, error) {
		var in struct {
			Address, Target string
			Edit            bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		address, err := mailAddress(in.Address)
		if err != nil {
			return nil, err
		}
		target, err := mailAddress(in.Target)
		if err != nil {
			return nil, err
		}
		return nil, changeMail(func(m *MailState) error {
			for i := range m.Aliases {
				if m.Aliases[i].Address == address {
					if !in.Edit {
						return errf("Адрес занят")
					}
					m.Aliases[i].Target = target
					return nil
				}
			}
			if in.Edit {
				return errf("Псевдоним не найден")
			}
			if mailAddressUsed(*m, address) {
				return errf("Адрес занят")
			}
			m.Aliases = append(m.Aliases, MailAlias{address, target})
			return nil
		})
	})
	apiOp("mail.alias.delete", "Почта: удаление псевдонима", func(q *Req) (any, error) {
		var in struct{ Address string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, changeMail(func(m *MailState) error {
			keep := []MailAlias{}
			for _, a := range m.Aliases {
				if a.Address != in.Address {
					keep = append(keep, a)
				}
			}
			m.Aliases = keep
			return nil
		})
	})
}

// mailSiteDomains — домены сайтов панели, ещё не добавленные в почту: их можно выбрать при создании
// ящика, домен добавится автоматически. Вызывать под mu.
func mailSiteDomains(m MailState) []string {
	out := []string{}
	for _, s := range store.Sites {
		if !mailDomainExists(m, s.Domain) && !strings.HasPrefix(s.Domain, "*.") {
			out = append(out, s.Domain)
		}
	}
	sort.Strings(out)
	return out
}

// stockPostfix — установленный, но не настроенный Postfix (локальная почта из образа RHEL):
// нет виртуальных доменов, внешнего релея и приёма почты из сети.
func stockPostfix() bool {
	out, err := run("postconf", "-n")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "virtual_mailbox_domains", "virtual_alias_domains", "virtual_transport", "relay_domains", "smtpd_sasl_auth_enable":
			if v != "" && v != "no" {
				return false
			}
		case "relayhost":
			if v != "" {
				return false
			}
		case "inet_interfaces":
			if v != "localhost" && v != "loopback-only" && v != "127.0.0.1" {
				return false
			}
		}
	}
	return true
}

// ensureEPEL подключает EPEL на RHEL-системах (OpenDKIM и часть почтовых пакетов есть только там).
func ensureEPEL(j *Job) error {
	if cfg.Dry {
		return nil
	}
	if _, err := run("rpm", "-q", "epel-release"); err != nil {
		jobStage(j, "Подключение EPEL")
		if err := liveCommand(j, 10*time.Minute, "dnf", "install", "-y", "epel-release"); err != nil {
			return err
		}
	}
	// В части образов хостеров epel-release установлен, но репозиторий выключен (enabled=0)
	if _, err := run("dnf", "config-manager", "--set-enabled", "epel"); err != nil {
		_, err = run("sed", "-i", `/^\[epel\]/,/^\[/ s/^enabled=0/enabled=1/`, "/etc/yum.repos.d/epel.repo")
		return err
	}
	return nil
}

// labelMailStore выставляет метку SELinux хранилищу почты: без неё Dovecot не может
// создавать Maildir в /var/vmail (метка var_t по умолчанию).
func labelMailStore() error {
	if !selinuxEnabled() {
		return nil
	}
	rule := "/var/vmail(/.*)?"
	if _, err := run("semanage", "fcontext", "-a", "-t", "mail_spool_t", rule); err != nil {
		if _, err = run("semanage", "fcontext", "-m", "-t", "mail_spool_t", rule); err != nil {
			return errf("SELinux: не удалось задать метку для /var/vmail: %s", firstLine(err.Error()))
		}
	}
	_, err := run("restorecon", "-R", "/var/vmail")
	return err
}

// openMailFirewalld открывает SMTP (25), submission (587) и IMAPS (993) в активном firewalld.
func openMailFirewalld(j *Job) error {
	if cfg.Dry || !have("firewall-cmd") {
		return nil
	}
	if out, _ := run("firewall-cmd", "--state"); strings.TrimSpace(out) != "running" {
		return nil
	}
	jobStage(j, "Открытие почтовых портов в firewalld")
	for _, svc := range []string{"smtp", "smtp-submission", "imaps"} {
		if _, err := run("firewall-cmd", "--permanent", "--add-service="+svc); err != nil {
			return err
		}
	}
	_, err := run("firewall-cmd", "--reload")
	return err
}
