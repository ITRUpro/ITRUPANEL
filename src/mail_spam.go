// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const spamOwned = "/etc/itrupanel/rspamd-owned"
const spamRedisSecret = "/etc/itrupanel/spam-redis-password"

func spamFiles(s SpamSettings) (map[string]string, error) {
	if s.Mark < 1 || s.Reject <= s.Mark || s.Reject > 100 {
		return nil, errf("Неверные пороги антиспама")
	}
	secret := strings.TrimSpace(readFile(spamRedisSecret))
	if secret == "" {
		return nil, errf("Не настроен пароль Redis антиспама")
	}
	if len(secret) != 64 || strings.ContainsAny(secret, "\n\r\"' ") {
		return nil, errf("Повреждён пароль Redis антиспама")
	}
	redisBin, redisUser := spamRedisServer()
	files := map[string]string{
		"/etc/rspamd/local.d/worker-proxy.inc": `bind_socket = "127.0.0.1:11332";
milter = yes;
count = 1;
upstream "local" { self_scan = yes; }
`,
		"/etc/rspamd/local.d/worker-normal.inc":        "enabled = false;\n",
		"/etc/rspamd/override.d/worker-controller.inc": fmt.Sprintf("bind_socket = \"127.0.0.1:11334\";\nsecure_ip = [];\npassword = \"%x\";\nenable_password = \"%x\";\n", sha256.Sum256([]byte("read:"+secret)), sha256.Sum256([]byte("write:"+secret))),
		"/etc/rspamd/local.d/actions.conf":             fmt.Sprintf("reject = %d;\nadd_header = null;\nrewrite_subject = %d;\ngreylist = %s;\nsubject = \"[SPAM] %%s\";\n", s.Reject, s.Mark, spamGreylistAction(s)),
		"/etc/rspamd/local.d/milter_headers.conf": `use = ["x-spam-status", "x-spam-level", "authentication-results"];
extended_spam_headers = true;
skip_local = false;
skip_authenticated = false;
`,
		"/etc/rspamd/local.d/redis.conf":        fmt.Sprintf("servers = \"127.0.0.1:11379\";\npassword = \"%s\";\n", secret),
		"/etc/rspamd/local.d/dkim_signing.conf": "enabled = false;\n", // OpenDKIM already signs outgoing mail.
		"/etc/rspamd/local.d/arc.conf":          "enabled = false;\n",
		// Экономный режим для серверов с памятью меньше 4 ГБ: без Hyperscan (отдельный процесс
		// компиляции и копии баз правил в каждом процессе) и без модуля neural. Иначе файлы пустые.
		"/etc/rspamd/local.d/options.inc": spamLowMemory("disable_hyperscan = true;\n"),
		"/etc/rspamd/local.d/neural.conf": spamLowMemory("enabled = false;\n"),
		"/etc/itrupanel/rspamd-redis.conf": fmt.Sprintf(`bind 127.0.0.1
port 11379
protected-mode yes
requirepass %s
daemonize no
dir /var/lib/itrupanel-spam-redis
save 900 1
save 300 10
appendonly yes
maxmemory 128mb
maxmemory-policy noeviction
`, secret),
		spamRedisUnitPath(): spamRedisUnit(redisBin, redisUser),
		"/etc/systemd/system/itrupanel-spam-redis.service": `[Unit]
Description=ITRUPANEL Redis for Rspamd
After=network.target
[Service]
Type=simple
User=` + redisUser + `
Group=` + redisUser + `
StateDirectory=itrupanel-spam-redis
StateDirectoryMode=0700
ExecStart=` + redisBin + ` /etc/itrupanel/rspamd-redis.conf
Restart=on-failure
` + spamRedisNoNewPrivileges() + `PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
[Install]
WantedBy=multi-user.target
`,
	}
	if isBSD() {
		delete(files, "/etc/systemd/system/itrupanel-spam-redis.service")
	}
	for p, content := range spamExtraFiles(s) {
		files[p] = content
	}
	return files, nil
}
func setupSpam(s SpamSettings, j *Job) (err error) {
	mailSetupMu.Lock()
	defer mailSetupMu.Unlock()
	if !isBSD() && ((osInfo.Family != "debian" && osInfo.Family != "rhel") || (!cfg.Dry && !osInfo.Systemd)) {
		return errf("Антиспам требует Debian, Ubuntu, RHEL-систему с systemd или FreeBSD")
	}
	mu.Lock()
	ready, installed := store.Mail.Ready, store.Mail.Spam.Installed
	mu.Unlock()
	if !ready {
		return errf("Сначала настройте почтовый сервер")
	}
	if !installed && !fileExists(spamOwned) && !cfg.Dry && (have("rspamd") || fileExists("/etc/rspamd/rspamd.conf")) {
		return errf("Найдена отдельная установка Rspamd; её настройки не изменены")
	}
	if err = writeFile(spamOwned, []byte("ITRUPANEL\n"), 0600); err != nil {
		return
	}
	if !installed && !isBSD() {
		if _, err = run("systemctl", "mask", "rspamd"); err != nil {
			return
		}
		defer func() {
			if err != nil {
				run("systemctl", "stop", "rspamd", "itrupanel-spam-redis")
				run("systemctl", "mask", "rspamd")
			}
		}()
	}
	jobStage(j, "Установка Rspamd и отдельного Redis для антиспама")
	phpInstallMu.Lock()
	if err = ensureRspamdRepo(j); err == nil {
		err = installPackagesJob(j, "rspamd", spamRedisPackage())
	}
	phpInstallMu.Unlock()
	if err != nil {
		return
	}
	// SELinux (RHEL): Postfix подключается к фильтру Rspamd на 11332, Valkey/Redis слушает 11379
	// и хранит данные в своей папке
	if err = selinuxPort(11332, "milter_port_t"); err != nil {
		return
	}
	if err = selinuxPort(11379, "redis_port_t"); err != nil {
		return
	}
	if err = selinuxLabel("/var/lib/itrupanel-spam-redis", "redis_var_lib_t"); err != nil {
		return
	}
	if isBSD() {
		if err = bsdSpamPrepare(); err != nil {
			return
		}
	}
	if !fileExists(spamRedisSecret) {
		if err = writeFile(spamRedisSecret, []byte(randToken(32)), 0600); err != nil {
			return
		}
	}
	mu.Lock()
	defer mu.Unlock()
	next := cloneMail(store.Mail)
	s.Installed = true
	next.Spam = s
	// applyMail validates all files, starts the filter before connecting Postfix,
	// and restores the preceding configuration if any step fails.
	undo, e := applyMail(next)
	if e != nil {
		return e
	}
	if isBSD() {
		for _, svc := range []string{"itrupanel-spam-redis", "rspamd"} {
			if err = service("enable", svc); err != nil {
				undo()
				return
			}
		}
	} else if _, err = run("systemctl", "enable", "itrupanel-spam-redis", "rspamd"); err != nil {
		undo()
		return
	}
	old := store.Mail
	store.Mail = next
	if err = saveChecked(); err != nil {
		store.Mail = old
		undo()
		return
	}
	jobStage(j, "Антиспам настроен; служебные порты доступны только локально")
	return nil
}

func spamLowMemory(conf string) string {
	if lowMemoryHost() {
		return conf
	}
	return ""
}

// spamRedisNoNewPrivileges: на RHEL-системах с SELinux NoNewPrivileges запрещает переход Redis
// в домен redis_t (nnp_transition, CentOS Stream 9 и др.) — служба не запускается.
func spamRedisNoNewPrivileges() string {
	if osInfo.Family == "rhel" {
		return ""
	}
	return "NoNewPrivileges=true\n"
}
