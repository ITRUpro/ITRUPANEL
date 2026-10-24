// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const webmailData = "/var/lib/itrupanel/roundcube"
const webmailOwned = "/etc/itrupanel/roundcube-owned"
const webmailKey = "/etc/itrupanel/roundcube-des-key"
const webmailVhost = vhostDir + "/000-itrupanel-webmail.conf"

func phpString(s string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(s) + "'"
}

func roundcubeMailFiles(m MailState) (map[string]string, error) {
	secret := strings.TrimSpace(readFile(webmailKey))
	if len(secret) != 24 {
		return nil, errf("Повреждён ключ сессий Roundcube")
	}
	crt := mailTLS + "/server.crt"
	if m.Certificate != "" {
		c := findCert(m.Certificate)
		if c == nil {
			return nil, errf("Сертификат почты не найден")
		}
		crt = c.Crt
	}
	cert, err := os.ReadFile(P(crt))
	if err != nil {
		return nil, err
	}
	ca := readFile(systemCABundle()) + "\n" + string(cert)
	config := fmt.Sprintf(`<?php
// ITRUPANEL: managed Roundcube
$config = [];
$config['db_dsnw'] = 'sqlite:///%s/roundcube.sqlite?mode=0640';
$config['des_key'] = %s;
$config['imap_host'] = 'ssl://127.0.0.1:993';
$config['default_host'] = 'ssl://127.0.0.1';
$config['default_port'] = 993;
$config['smtp_host'] = 'tls://127.0.0.1:587';
$config['smtp_server'] = 'tls://127.0.0.1';
$config['smtp_port'] = 587;
$config['smtp_user'] = '%%u';
$config['smtp_pass'] = '%%p';
$config['imap_conn_options'] = ['ssl' => ['verify_peer' => true, 'verify_peer_name' => true, 'peer_name' => %s, 'cafile' => '%s/mail-ca.pem']];
$config['smtp_conn_options'] = $config['imap_conn_options'];
$config['enable_installer'] = false;
$config['product_name'] = 'ITRUPANEL — Roundcube';
$config['support_url'] = 'https://itrupanel.com/forum/';
$config['skin'] = 'elastic';
$config['language'] = 'ru_RU';
$config['temp_dir'] = '%s/tmp';
$config['log_dir'] = '%s/logs';
$config['session_name'] = 'itruroundcube';
$config['session_lifetime'] = 30;
$config['force_https'] = true;
$config['login_autocomplete'] = 0;
$config['ip_check'] = true;
$config['plugins'] = [];
$config['quota_zero_as_unlimited'] = true;
$config['identities_level'] = 3;
$config['smtp_log'] = false;
`, webmailData, phpString(secret), phpString(m.Hostname), webmailData, webmailData, webmailData)
	return map[string]string{roundcubeConfigPath(): config, webmailData + "/mail-ca.pem": ca}, nil
}

func validateWebmailHost(host, cert string) error {
	if host == store.Settings.PanelDomain {
		return errf("Для веб-почты нужен отдельный от панели домен")
	}
	for _, site := range store.Sites {
		for _, name := range siteNames(site) {
			if name == host {
				return errf("Имя %s уже занято сайтом", host)
			}
		}
	}
	if cert != "" {
		c := findCert(cert)
		if c == nil {
			return errf("Сертификат не найден")
		}
		if _, err := validatedPair(c.Crt, c.Key, host); err != nil {
			return err
		}
	}
	return nil
}
func roundcubeNginx(host, crt, key, root string) string {
	return fmt.Sprintf(`# ITRUPANEL: Roundcube
server {
 listen 80;
 server_name %[1]s;
 location ^~ /.well-known/acme-challenge/ { root /var/www/itrupanel-acme; try_files $uri =404; }
 location / { return 301 https://%[1]s$request_uri; }
}
server {
 listen 443 ssl;
 server_name %[1]s;
 ssl_certificate %[2]s;
 ssl_certificate_key %[3]s;
 ssl_protocols TLSv1.2 TLSv1.3;
 root %[4]s;
 index index.php;
 server_tokens off;
 client_max_body_size 50m;
 add_header X-Content-Type-Options nosniff always;
 add_header Referrer-Policy same-origin always;
 location = /index.php {
  include fastcgi_params;
  fastcgi_param SCRIPT_FILENAME $document_root/index.php;
  fastcgi_param HTTPS on;
  fastcgi_param HTTP_PROXY "";
  fastcgi_pass unix:%[5]s/itrupanel-roundcube.sock;
 }
 location ~* ^/(config|temp|logs|SQL|bin|installer|vendor|program/(include|lib|localization))(/|$) { deny all; }
 location ~ /\. { deny all; }
 location ~* \.(php|inc|ini|sql|db|sqlite|log|sh)(/|$) { deny all; }
 location / { try_files $uri $uri/ /index.php?$query_string; }
}
`, host, crt, key, root, sockDir)
}
func roundcubePool() string {
	return fmt.Sprintf(`; ITRUPANEL: Roundcube
[itru_roundcube]
user = itruroundcube
group = itruroundcube
listen = %s/itrupanel-roundcube.sock
listen.owner = %s
listen.group = %s
listen.mode = 0660
pm = ondemand
pm.max_children = 4
pm.process_idle_timeout = 20s
security.limit_extensions = .php
php_admin_value[session.save_path] = %s/sessions
php_admin_value[upload_tmp_dir] = %s/tmp
php_admin_value[sys_temp_dir] = %s/tmp
php_admin_value[upload_max_filesize] = 40M
php_admin_value[post_max_size] = 50M
php_admin_value[memory_limit] = 256M
php_admin_flag[session.cookie_httponly] = on
php_admin_flag[session.cookie_secure] = on
php_admin_value[session.cookie_samesite] = Lax
php_admin_flag[display_errors] = off
`, sockDir, osInfo.WebUser, osInfo.WebUser, webmailData, webmailData, webmailData)
}

func setupRoundcube(host, cert string, j *Job) (err error) {
	mailSetupMu.Lock()
	defer mailSetupMu.Unlock()
	if !isBSD() && ((osInfo.Family != "debian" && osInfo.Family != "rhel") || (!cfg.Dry && !osInfo.Systemd)) {
		return errf("Установка Roundcube требует Debian, Ubuntu, RHEL-систему с systemd или FreeBSD")
	}
	share, confDir := roundcubePaths()
	mu.Lock()
	ready := store.Mail.Ready
	err = validateWebmailHost(host, cert)
	mu.Unlock()
	if err != nil {
		return
	}
	if !ready {
		return errf("Сначала настройте почтовый сервер")
	}
	if !fileExists(webmailOwned) && !cfg.Dry {
		existing := fileExists("/var/lib/roundcube/index.php") || fileExists("/etc/roundcube/config.inc.php")
		if isBSD() {
			existing = fileExists(bsdPrefix + "/www/roundcube/index.php")
		} else if osInfo.Family == "rhel" {
			_, e := run("rpm", "-q", "roundcubemail")
			existing = e == nil
		}
		if existing {
			return errf("Обнаружена отдельная установка Roundcube. Её данные и настройки не изменены")
		}
	}
	if err = writeFile(webmailOwned, []byte("ITRUPANEL\n"), 0600); err != nil {
		return
	}
	// Use a stable PHP version independent of the versions selected for websites.
	v := "8.3"
	if isBSD() {
		// на FreeBSD работает одна версия PHP: Roundcube берёт её
		if cur := bsdInstalledPHP(); cur != "" {
			v = cur
		}
	}
	jobStage(j, "Подготовка PHP "+v+" для Roundcube")
	if err = phpInstall(v); err != nil {
		return
	}
	jobStage(j, "Установка Roundcube из репозитория ОС")
	if isBSD() {
		phpInstallMu.Lock()
		p := "php" + vv(v)
		err = installPackagesJob(j, "roundcube-"+p, p+"-pdo_sqlite", p+"-sqlite3", p+"-mbstring", p+"-intl", p+"-xml", p+"-curl", p+"-zip", p+"-gd")
		phpInstallMu.Unlock()
	} else if osInfo.Family == "rhel" {
		// Roundcube на RHEL-системах — пакет roundcubemail из EPEL; модули PHP 8.3 уже поставил phpInstall
		if err = ensureEPEL(j); err != nil {
			return
		}
		phpInstallMu.Lock()
		err = installPackagesJob(j, "roundcubemail")
		phpInstallMu.Unlock()
	} else {
		phpInstallMu.Lock()
		_, err = runTimeout(30*time.Second, "roundcube-core roundcube/dbconfig-install boolean false\n", "debconf-set-selections")
		if err == nil {
			err = installPackagesJob(j, "roundcube-core", "roundcube-sqlite3", "php"+v+"-sqlite3", "php"+v+"-mbstring", "php"+v+"-intl", "php"+v+"-xml", "php"+v+"-curl", "php"+v+"-zip", "php"+v+"-gd")
		}
		phpInstallMu.Unlock()
	}
	if err != nil {
		return
	}
	cleanupPackagedApache()
	if _, e := run("id", "itruroundcube"); e != nil || cfg.Dry {
		if isBSD() {
			err = bsdAddSystemUser("itruroundcube", webmailData)
		} else {
			_, err = run("useradd", "--system", "--user-group", "--home-dir", webmailData, "--shell", "/usr/sbin/nologin", "itruroundcube")
		}
		if err != nil {
			return
		}
	}
	if err = os.MkdirAll(P("/var/lib/itrupanel"), 0755); err != nil {
		return
	}
	if err = os.Chmod(P("/var/lib/itrupanel"), 0755); err != nil {
		return
	}
	for _, suffix := range []string{"", "/tmp", "/sessions", "/logs"} {
		if err = os.MkdirAll(P(webmailData+suffix), 0700); err != nil {
			return
		}
		if err = os.Chmod(P(webmailData+suffix), 0700); err != nil {
			return
		}
		if _, err = run("chown", "itruroundcube:itruroundcube", webmailData+suffix); err != nil {
			return
		}
	}
	// SELinux (RHEL): PHP-FPM пишет базу, сессии и журналы Roundcube в эту папку
	if err = selinuxLabel(webmailData, "httpd_sys_rw_content_t"); err != nil {
		return
	}
	if err = os.MkdirAll(P(confDir), 0750); err != nil {
		return
	}
	if _, err = run("chown", "-R", "root:itruroundcube", confDir); err != nil {
		return
	}
	if _, err = run("chmod", "-R", "g+rX", confDir); err != nil {
		return
	}
	if !fileExists(webmailKey) {
		if err = writeFile(webmailKey, []byte(randToken(12)), 0600); err != nil {
			return
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if err = validateWebmailHost(host, cert); err != nil {
		return
	}
	next := cloneMail(store.Mail)
	next.Webmail = WebmailSettings{Ready: true, Hostname: host, Certificate: cert, PHP: v}
	crt, key := "/etc/itrupanel/roundcube-web.crt", "/etc/itrupanel/roundcube-web.key"
	extra := map[string][]byte{}
	if cert != "" {
		c := findCert(cert)
		crt, key = c.Crt, c.Key
	} else {
		if _, e := validatedPair(crt, key, host); e != nil {
			var cr, k []byte
			cr, k, err = selfSigned([]string{host}, 365)
			if err != nil {
				return
			}
			extra[crt], extra[key] = cr, k
		}
	}
	root := webmailRoot()
	if !cfg.Dry && !fileExists(root+"/index.php") {
		return errf("Пакет Roundcube не содержит index.php")
	}
	vhosts, e := webmailVhostFiles(next)
	if e != nil {
		return e
	}
	for p, b := range vhosts {
		extra[p] = []byte(b)
	}
	extra[phpPoolDir(v)+"/itru_roundcube.conf"] = []byte(roundcubePool())
	timers := []string{"roundcube-gc.timer", "roundcube-cleandb.timer"}
	if isBSD() {
		timers = nil
		extra[bsdPrefix+"/etc/newsyslog.conf.d/itrupanel-roundcube.conf"] = []byte(webmailData + "/logs/*.log itruroundcube:itruroundcube 640 4 * @W0 GJC\n")
	} else if osInfo.Family == "rhel" {
		// В пакете EPEL нет systemd-таймеров, а его cron-задание запускается от apache без доступа к настройкам
		for p, b := range roundcubeTimerFiles(v) {
			extra[p] = b
		}
		timers = []string{"itrupanel-roundcube-gc.timer", "itrupanel-roundcube-cleandb.timer"}
		if fileExists("/etc/cron.d/roundcubemail") {
			extra["/etc/cron.d/roundcubemail"] = []byte("# ITRUPANEL: очистку Roundcube выполняют таймеры itrupanel-roundcube-*\n")
		}
	} else {
		for _, task := range []string{"gc", "cleandb"} {
			extra["/etc/systemd/system/roundcube-"+task+".service.d/itrupanel.conf"] = []byte("[Service]\nUser=itruroundcube\nGroup=itruroundcube\nExecStart=\nExecStart=" + phpCLI(v) + " " + share + "/bin/" + task + ".sh\nReadWritePaths=\nReadWritePaths=" + webmailData + "\n")
		}
	}
	if !isBSD() {
		extra["/etc/logrotate.d/itrupanel-roundcube"] = []byte(webmailData + "/logs/*.log {\n weekly\n rotate 4\n compress\n missingok\n notifempty\n su itruroundcube itruroundcube\n create 0640 itruroundcube itruroundcube\n}\n")
	}

	undoWeb, e := writeMailExtraFiles(extra)
	if e != nil {
		return e
	}
	committed := false
	defer func() {
		if !committed {
			undoWeb()
			if !isBSD() {
				run("systemctl", "daemon-reload")
			}
			service("reload", phpService(v))
			nginxTestReload()
		}
	}()
	jobStage(j, "Настройка подключения к IMAP/SMTP и базы Roundcube")
	undoMail, e := applyMail(next)
	if e != nil {
		return e
	}
	defer func() {
		if !committed {
			undoMail()
		}
	}()
	// The packaged script initializes a fresh SQLite database, and upgrades an
	// existing database without dropping contacts or preferences. Run as its owner.
	if !cfg.Dry && fileExists(webmailData+"/roundcube.sqlite") {
		backup := webmailData + "/roundcube-before-update-" + time.Now().UTC().Format("20060102T150405") + ".sqlite"
		code := `$s=new SQLite3($argv[1],SQLITE3_OPEN_READONLY);$d=new SQLite3($argv[2]);if(!$s->backup($d)){exit(1);}chmod($argv[2],0600);`
		if _, err = runAsUser(time.Minute, "itruroundcube", phpCLI(v), "-r", code, webmailData+"/roundcube.sqlite", backup); err != nil {
			return
		}
		jobStage(j, "Резервная копия контактов и настроек: "+backup)
	}
	// Debian/Ubuntu packages omit upstream initdb.sh. Initialize only an empty
	// database with the packaged SQL, then use their supported migration script.
	initCode := `$db=new PDO('sqlite:'.$argv[1]);$db->setAttribute(PDO::ATTR_ERRMODE,PDO::ERRMODE_EXCEPTION);$n=(int)$db->query("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")->fetchColumn();if($n===0){$sql=file_get_contents($argv[2]);if($sql===false){exit(1);}$db->beginTransaction();$db->exec($sql);$db->commit();}chmod($argv[1],0600);`
	if _, err = runAsUser(time.Minute, "itruroundcube", phpCLI(v), "-r", initCode, webmailData+"/roundcube.sqlite", share+"/SQL/sqlite.initial.sql"); err != nil {
		return
	}
	// В Roundcube 1.4 (Debian 11) скрипта нет: база создана из sqlite.initial.sql этой же версии
	if fileExists(share+"/bin/updatedb.sh") || cfg.Dry {
		if isBSD() {
			var out string
			out, err = runAsUser(3*time.Minute, "itruroundcube", phpCLI(v), share+"/bin/updatedb.sh", "--dir="+share+"/SQL", "--package=roundcube")
			if j != nil {
				j.Write([]byte(out + "\n"))
			}
			if err != nil {
				return
			}
		} else if err = liveCommand(j, 3*time.Minute, "runuser", "-u", "itruroundcube", "--", phpCLI(v), share+"/bin/updatedb.sh", "--dir="+share+"/SQL", "--package=roundcube"); err != nil {
			return
		}
	} else {
		jobStage(j, "Обновление схемы базы Roundcube не требуется для этой версии пакета")
	}
	if _, err = run(phpFPMBin(v), "-t"); err != nil {
		return
	}
	if err = service("reload", phpService(v)); err != nil {
		return
	}
	if err = nginxTestReload(); err != nil {
		return
	}
	if isBSD() {
		// очистка Roundcube по расписанию: cron пользователя itruroundcube
		tab := "# ITRUPANEL: очистка Roundcube\n0 3 * * * " + phpCLI(v) + " " + share + "/bin/gc.sh\n30 3 * * * " + phpCLI(v) + " " + share + "/bin/cleandb.sh\n"
		if _, err = runInput(tab, "crontab", "-u", "itruroundcube", "-"); err != nil {
			return
		}
	} else {
		if _, err = run("systemctl", "daemon-reload"); err != nil {
			return
		}
		if _, err = run("systemctl", append([]string{"enable", "--now"}, timers...)...); err != nil {
			return
		}
	}
	old := store.Mail
	store.Mail = next
	if err = saveChecked(); err != nil {
		store.Mail = old
		return
	}
	committed = true
	jobStage(j, "Roundcube готов; логин — полный адрес ящика, пароль — пароль ящика")
	return nil
}

// Web listeners, FPM pools and private TLS keys are rolled back together.
func writeMailExtraFiles(files map[string][]byte) (func() error, error) {
	type snapshot struct {
		data   []byte
		mode   os.FileMode
		exists bool
	}
	old := map[string]snapshot{}
	for p := range files {
		b, e := os.ReadFile(P(p))
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		mode := os.FileMode(0644)
		if st, e := os.Stat(P(p)); e == nil {
			mode = st.Mode().Perm()
		}
		old[p] = snapshot{b, mode, e == nil}
	}
	undo := func() error {
		var first error
		for p, s := range old {
			var e error
			if s.exists {
				e = writeFile(p, s.data, s.mode)
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
		return first
	}
	for p, b := range files {
		mode := os.FileMode(0644)
		if strings.HasSuffix(p, ".key") {
			mode = 0600
		}
		if e := writeFile(p, b, mode); e != nil {
			undo()
			return nil, e
		}
	}
	return undo, nil
}
