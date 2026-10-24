// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"strings"
)

// Почта «как в aaPanel»: каждый домен в общем списке со своими настройками. Первый домен
// устанавливает почтовые службы, а веб-почта Roundcube и антиспам включаются у каждого домена.

const rspamdSettingsPath = "/etc/rspamd/local.d/settings.conf"
const webmailCertDir = "/etc/itrupanel/webmail"

// rspamdDomainSettings: письма на домены с выключенным антиспамом Rspamd пропускает без проверки.
func rspamdDomainSettings(m MailState) string {
	var rcpt []string
	for _, d := range m.Domains {
		if d.NoSpam {
			rcpt = append(rcpt, fmt.Sprintf("%q", "@"+d.Name))
		}
	}
	conf := "# ITRUPANEL: домены без проверки на спам\n"
	if len(rcpt) > 0 {
		conf += "itrupanel_no_spam {\n priority = high;\n rcpt = [" + strings.Join(rcpt, ", ") + "];\n want_spam = yes;\n}\n"
	}
	return conf
}

func webmailRoot() string {
	share, _ := roundcubePaths()
	root := "/var/lib/roundcube"
	if osInfo.Family == "rhel" || isBSD() {
		root = share
	}
	if fileExists(root + "/public_html/index.php") {
		root += "/public_html"
	}
	return root
}

// webmailHostOn — открыта ли веб-почта на этом имени: основной адрес Roundcube или домен с включённой веб-почтой.
func webmailHostOn(m MailState, host string) bool {
	if !m.Webmail.Ready {
		return false
	}
	if host == m.Webmail.Hostname {
		return true
	}
	for _, d := range m.Domains {
		if d.Webmail && mailDomainHost(m, d) == host {
			return true
		}
	}
	return false
}

// webmailCertName — установленный сертификат, подходящий к имени: сертификат домена или общий сертификат почты.
func webmailCertName(m MailState, host, cert string) string {
	for _, name := range []string{cert, m.Certificate} {
		if name == "" {
			continue
		}
		if c := findCert(name); c != nil {
			if _, e := validatedPair(c.Crt, c.Key, host); e == nil {
				return name
			}
		}
	}
	return ""
}

func webmailSelfSigned(host string) (string, string) {
	return webmailCertDir + "/" + host + ".crt", webmailCertDir + "/" + host + ".key"
}

func ensureWebmailSelfSigned(host string) error {
	crt, key := webmailSelfSigned(host)
	if _, e := validatedPair(crt, key, host); e == nil {
		return nil
	}
	cr, k, err := selfSigned([]string{host}, 365)
	if err != nil {
		return err
	}
	if err = writeFile(crt, cr, 0644); err != nil {
		return err
	}
	return writeFile(key, k, 0600)
}

// webmailVhostFiles: один Roundcube на основном адресе и на mail-именах доменов, где веб-почта включена.
func webmailVhostFiles(m MailState) (map[string]string, error) {
	main := m.Webmail.Hostname
	crt, key := "/etc/itrupanel/roundcube-web.crt", "/etc/itrupanel/roundcube-web.key"
	if m.Webmail.Certificate != "" {
		c := findCert(m.Webmail.Certificate)
		if c == nil {
			return nil, errf("Сертификат веб-почты не найден")
		}
		crt, key = c.Crt, c.Key
	}
	root := webmailRoot()
	conf := roundcubeNginx(main, crt, key, root)
	files := map[string]string{vhostDir + "/000-itrupanel-mail-" + main + ".conf": "# ACME is served by Roundcube\n"}
	seen := map[string]bool{main: true}
	for _, d := range m.Domains {
		host := mailDomainHost(m, d)
		if !d.Webmail || seen[host] {
			continue
		}
		seen[host] = true
		c, k := webmailSelfSigned(host)
		if name := webmailCertName(m, host, d.Certificate); name != "" {
			cert := findCert(name)
			c, k = cert.Crt, cert.Key
		} else if !cfg.Dry && !fileExists(c) {
			continue
		}
		conf += "\n" + strings.TrimPrefix(roundcubeNginx(host, c, k, root), "# ITRUPANEL: Roundcube\n")
		files[vhostDir+"/000-itrupanel-mail-"+host+".conf"] = "# ACME is served by Roundcube\n"
	}
	files[webmailVhost] = conf
	return files, nil
}

// mailHostLE — выпуск сертификата для нового домена (подменяется в тестах).
var mailHostLE = issueMailHostLE

func mailDomainSnapshot(name string) (MailDomain, MailState, bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, d := range store.Mail.Domains {
		if d.Name == name {
			return d, cloneMail(store.Mail), true
		}
	}
	return MailDomain{}, cloneMail(store.Mail), false
}

func changeMailDomain(name string, fn func(m *MailState, d *MailDomain) error) error {
	return changeMail(func(m *MailState) error {
		for i := range m.Domains {
			if m.Domains[i].Name == name {
				return fn(m, &m.Domains[i])
			}
		}
		return errf("Почтовый домен не найден")
	})
}

// installSpamFor ставит Rspamd по просьбе одного домена; остальные домены остаются без проверки, как были.
func installSpamFor(j *Job, name string) error {
	if err := changeMail(func(m *MailState) error {
		for i := range m.Domains {
			m.Domains[i].NoSpam = m.Domains[i].Name != name
		}
		return nil
	}); err != nil {
		return err
	}
	jobStage(j, "Установка антиспама Rspamd")
	return setupSpam(SpamSettings{Enabled: true, Mark: 6, Reject: 15}, j)
}

// setMailDomainFeatures включает и выключает веб-почту и антиспам домена; при первом включении
// ставит Roundcube или Rspamd.
func setMailDomainFeatures(j *Job, name string, webmail, spam *bool) error {
	d, m, ok := mailDomainSnapshot(name)
	if !ok {
		return errf("Почтовый домен не найден")
	}
	if !m.Ready {
		return errf("Сначала настройте почтовый сервер")
	}
	host := mailDomainHost(m, d)
	if spam != nil {
		switch {
		case *spam && !m.Spam.Installed:
			if err := installSpamFor(j, name); err != nil {
				return err
			}
		default:
			jobStage(j, map[bool]string{true: "Включение антиспама для " + name, false: "Выключение антиспама для " + name}[*spam])
			if err := changeMailDomain(name, func(_ *MailState, d *MailDomain) error { d.NoSpam = !*spam; return nil }); err != nil {
				return err
			}
		}
	}
	if webmail == nil {
		return nil
	}
	if !*webmail {
		if host == m.Webmail.Hostname {
			return errf("%s — основной адрес Roundcube; его меняют в настройках веб-почты", host)
		}
		jobStage(j, "Выключение веб-почты на "+host)
		if err := changeMailDomain(name, func(_ *MailState, d *MailDomain) error { d.Webmail = false; return nil }); err != nil {
			return err
		}
		// Вернуть HTTP-проверку Let’s Encrypt, которую раньше обслуживал Roundcube.
		siteSaveMu.Lock()
		defer siteSaveMu.Unlock()
		return mailChallenge(host)
	}
	if !m.Webmail.Ready {
		jobStage(j, "Установка веб-почты Roundcube на "+host)
		mu.Lock()
		cert := webmailCertName(store.Mail, host, d.Certificate)
		mu.Unlock()
		return setupRoundcube(host, cert, j)
	}
	if webmailHostOn(m, host) && (d.Webmail || host == m.Webmail.Hostname) {
		return nil
	}
	mu.Lock()
	err := validateWebmailHost(host, "")
	cert := webmailCertName(store.Mail, host, d.Certificate)
	mu.Unlock()
	if err != nil {
		return err
	}
	if cert == "" {
		if err = ensureWebmailSelfSigned(host); err != nil {
			return err
		}
	}
	jobStage(j, "Включение веб-почты на https://"+host+"/")
	return changeMailDomain(name, func(_ *MailState, d *MailDomain) error { d.Webmail = true; return nil })
}

// createMailDomain — добавление домена «под ключ»: при первом домене ставит почтовый сервер,
// выпускает сертификат на mail-имя и по желанию включает веб-почту и антиспам. Если Let’s Encrypt
// не выпущен (DNS ещё ведёт на другой сервер), домен всё равно добавляется с самоподписанным
// сертификатом, а причина возвращается предупреждением.
func createMailDomain(j *Job, name, host string, le bool, email string, webmail, spam bool) (warning string, err error) {
	mu.Lock()
	ready := store.Mail.Ready
	exists := mailDomainExists(store.Mail, name)
	mu.Unlock()
	if exists {
		return "", errf("Домен уже добавлен")
	}
	cert := ""
	if le {
		c, e := mailHostLE(j, host, email)
		if e != nil {
			warning = "Let’s Encrypt для " + host + " не выпущен: " + firstLine(e.Error()) + ". Домен работает с самоподписанным сертификатом; выпустите сертификат кнопкой «SSL» у домена, когда A-запись " + host + " будет указывать на этот сервер."
			jobStage(j, "Let’s Encrypt не выпущен — продолжаю с самоподписанным сертификатом")
		} else {
			cert = c
		}
	}
	if !ready {
		jobStage(j, "Установка почтового сервера: Postfix, Dovecot и OpenDKIM")
		if err = setupMail(host, cert, j); err != nil {
			return warning, err
		}
	}
	jobStage(j, "Добавление домена "+name)
	if err := changeMail(func(m *MailState) error {
		if mailDomainExists(*m, name) {
			return errf("Домен уже добавлен")
		}
		pub, e := createDKIM(name)
		if e != nil {
			return e
		}
		d := MailDomain{Name: name, PublicKey: pub, DefaultQuotaMB: 5120, NoSpam: !spam}
		if host != m.Hostname {
			d.Hostname = host
		}
		if cert != "" {
			c := findCert(cert)
			if c == nil {
				return errf("Сертификат не найден")
			}
			if _, e := validatedPair(c.Crt, c.Key, host); e != nil {
				return e
			}
			if host == m.Hostname {
				m.Certificate = cert
			} else {
				d.Certificate = cert
			}
		}
		m.Domains = append(m.Domains, d)
		return nil
	}); err != nil {
		return warning, err
	}
	if spam {
		mu.Lock()
		installed := store.Mail.Spam.Installed
		mu.Unlock()
		if !installed {
			if err = installSpamFor(j, name); err != nil {
				return warning, err
			}
		}
	}
	if webmail {
		on := true
		return warning, setMailDomainFeatures(j, name, &on, nil)
	}
	return warning, nil
}

func registerMailFeatures() {
	apiOp("mail.domain.create", "Почта: новый домен", func(q *Req) (any, error) {
		var in struct {
			Name, Hostname, SSL, Email string
			Webmail, Spam              bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		name, err := toASCII(strings.TrimSpace(in.Name))
		if err != nil {
			return nil, err
		}
		host := strings.TrimSpace(in.Hostname)
		if host == "" {
			host = "mail." + name
		}
		if host, err = toASCII(host); err != nil {
			return nil, err
		}
		if in.SSL != "le" && in.SSL != "self" {
			return nil, errf("Выберите сертификат")
		}
		mu.Lock()
		exists := mailDomainExists(store.Mail, name)
		mu.Unlock()
		if exists {
			return nil, errf("Домен уже добавлен")
		}
		id := startJob("mail:setup", "Почтовый домен "+name, q.S.User, func(j *Job) (any, error) {
			warning, err := createMailDomain(j, name, host, in.SSL == "le", in.Email, in.Webmail, in.Spam)
			info := "Почтовый домен " + name + " готов"
			if warning != "" {
				info += ". " + warning
				mu.Lock()
				addNotice("warn", "mail-le:"+name, warning)
				save()
				mu.Unlock()
			}
			return map[string]string{"info": info}, err
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("mail.domain.features", "Почта: веб-почта и антиспам домена", func(q *Req) (any, error) {
		var in struct {
			Name          string
			Webmail, Spam *bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		id := startJob("mail:domain:"+in.Name, "Почтовый домен "+in.Name, q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Настройки домена " + in.Name + " применены"}, setMailDomainFeatures(j, in.Name, in.Webmail, in.Spam)
		})
		return map[string]string{"job": id}, nil
	})
}
