// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Перенос данных со старого сервера. Старый сервер только читается; на новом сначала ставится
// всё недостающее (версии PHP), затем создаются пользователи и сайты, копируются файлы и базы.

type impSelection struct {
	Sites  []string `json:"sites"` // домены
	DBs    []string `json:"dbs"`
	Cron   bool     `json:"cron"`
	Mail   []string `json:"mail"`   // почтовые домены
	DNS    []string `json:"dns"`    // DNS-зоны
	FTP    []string `json:"ftp"`    // FTP-пользователи
	Backup bool     `json:"backup"` // настройки резервного копирования (FTP, расписание)
}

// ImpNeed — что нужно на новом сервере для выбранного переноса.
type ImpNeed struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	Installed bool   `json:"installed"`
}

type ImpSiteResult struct {
	Domain string `json:"domain"`
	User   string `json:"user"`
	Root   string `json:"root"`
	Status string `json:"status"` // ok | skipped | error
	Detail string `json:"detail,omitempty"`
	Bytes  int64  `json:"bytes"`
}
type ImpDBResult struct {
	Name           string `json:"name"`
	User           string `json:"user"`
	Status         string `json:"status"`
	Detail         string `json:"detail,omitempty"`
	PasswordKept   bool   `json:"password_kept"`
	NewPasswordKey string `json:"-"`
}
type ImpResult struct {
	Sites    []ImpSiteResult `json:"sites"`
	DBs      []ImpDBResult   `json:"dbs"`
	Cron     int             `json:"cron"`
	Mail     []ImpItemResult `json:"mail"`
	DNS      []ImpItemResult `json:"dns"`
	FTP      []ImpItemResult `json:"ftp"`
	Backup   *ImpItemResult  `json:"backup,omitempty"`
	Warnings []string        `json:"warnings"`
	Info     string          `json:"info"`
}

var reImpWebUser = regexp.MustCompile(`^(root|www-data|www|nginx|apache|httpd|nobody|daemon)?$`)

// impUserName подбирает имя пользователя панели для владельца со старого сервера.
func impUserName(owner, domain string) string {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if reImpWebUser.MatchString(owner) {
		return userFromDomain(domain)
	}
	owner = regexp.MustCompile(`[^a-z0-9_]`).ReplaceAllString(owner, "_")
	if owner == "" || owner[0] < 'a' || owner[0] > 'z' {
		owner = "u" + owner
	}
	if len(owner) > 32 {
		owner = owner[:32]
	}
	return owner
}

// impNeeds сравнивает выбранный перенос с новым сервером.
func impNeeds(inv *ImpInventory, sel impSelection) []ImpNeed {
	want := map[string]bool{}
	chosen := map[string]bool{}
	for _, d := range sel.Sites {
		chosen[d] = true
	}
	phpSites := 0
	for _, s := range inv.Sites {
		if !chosen[s.Domain] || s.Mode != "php" {
			continue
		}
		phpSites++
		if s.PHP != "" {
			want[s.PHP] = true
		}
	}
	out := []ImpNeed{}
	if phpSites > 0 && len(want) == 0 {
		out = append(out, ImpNeed{Key: "php:default", Title: "PHP", Detail: "версия на старом сервере не определена, будет использована версия по умолчанию", Installed: defaultPHP() != ""})
	}
	var vers []string
	for v := range want {
		vers = append(vers, v)
	}
	sort.Strings(vers)
	for _, v := range vers {
		out = append(out, ImpNeed{Key: "php:" + v, Title: "PHP " + v, Detail: "нужен сайтам со старого сервера", Installed: phpIsInstalled(v)})
	}
	if len(sel.DBs) > 0 {
		out = append(out, ImpNeed{Key: "mysql", Title: "MariaDB (MySQL)", Detail: "для переноса баз данных", Installed: have("mysql") && serviceActive(mysqlServiceName())})
	}
	if len(sel.Mail) > 0 {
		mu.Lock()
		ready := store.Mail.Ready
		mu.Unlock()
		out = append(out, ImpNeed{Key: "mail", Title: "Почтовый сервер (Postfix, Dovecot, OpenDKIM)", Detail: "для переноса почты", Installed: ready})
	}
	if len(sel.DNS) > 0 {
		out = append(out, ImpNeed{Key: "dns", Title: "DNS-сервер BIND", Detail: "для переноса DNS-зон", Installed: dnsInstalled()})
	}
	return out
}

func mysqlServiceName() string {
	for _, n := range []string{"mariadb", "mysql", "mysqld"} {
		if serviceActive(n) {
			return n
		}
	}
	return "mariadb"
}

type countWriter struct {
	w io.Writer
	n atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// impTarCmd — tar со старого сервера: код 1 («файл изменился при чтении») не считается ошибкой.
func impTarCmd(root string) string {
	return `tar -C ` + shQuote(root) + ` -cf - . ; r=$?; [ "$r" -le 1 ]`
}

// impCopyTree копирует папку со старого сервера в папку сайта от имени его владельца: так
// содержимое архива не может записать файлы за пределами сайта.
func impCopyTree(ctx context.Context, r impRunner, remoteRoot, localRoot, user string, tick func(int64)) (int64, error) {
	cmd := exec.CommandContext(ctx, "runuser", "-u", user, "--", "tar", "-xpf", "-", "-C", localRoot)
	in, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	cw := &countWriter{w: in}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if tick != nil {
					tick(cw.n.Load())
				}
			}
		}
	}()
	var serr error
	if ps, ok := r.(impPanelSource); ok {
		serr = ps.Tree(ctx, remoteRoot, cw)
	} else {
		serr = r.Stream(ctx, impTarCmd(remoteRoot), cw)
	}
	close(done)
	in.Close()
	werr := cmd.Wait()
	if serr != nil {
		return cw.n.Load(), serr
	}
	if werr != nil {
		return cw.n.Load(), errf("Не удалось распаковать файлы: %s", firstLine(stderr.String()+" "+werr.Error()))
	}
	return cw.n.Load(), nil
}

var reImpDefiner = `s/\/\*!?[0-9]* ?DEFINER=[^*]*\*\///g; s/DEFINER=` + "`" + `[^` + "`" + `]*` + "`" + `@` + "`" + `[^` + "`" + `]*` + "`" + `//g`

// impSQLScript — правила sed для дампа: убирается DEFINER (старых пользователей на новом сервере
// нет), а кодировки, которых новый сервер не знает, заменяются близкими. Так, MariaDB 11 создаёт
// таблицы с utf8mb4_uca1400_ai_ci, а MySQL 8 — с utf8mb4_0900_ai_ci; MariaDB 10.x их не понимает.
func impSQLScript(has func(query string) bool) string {
	script := reImpDefiner
	if !has("SHOW COLLATION LIKE 'utf8mb4_uca1400_ai_ci'") {
		script += `; s/utf8mb4_uca1400_[a-z0-9_]+/utf8mb4_unicode_ci/g; s/utf8mb3_uca1400_[a-z0-9_]+/utf8_unicode_ci/g; s/utf8_uca1400_[a-z0-9_]+/utf8_unicode_ci/g`
	}
	if !has("SHOW COLLATION LIKE 'utf8mb4_0900_ai_ci'") {
		script += `; s/utf8mb4_0900_[a-z0-9_]+/utf8mb4_unicode_ci/g`
	}
	if !has("SHOW CHARACTER SET LIKE 'utf8mb3'") {
		script += `; s/utf8mb3_/utf8_/g; s/utf8mb3/utf8/g`
	}
	return script
}

func impLocalSQLScript() string {
	return impSQLScript(func(q string) bool {
		out, _ := sqlOn("", q+";")
		return strings.TrimSpace(out) != ""
	})
}

// impMySQLError выбирает из вывода mysql строку с ошибкой (до неё идёт показ запроса).
func impMySQLError(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "ERROR") {
			return strings.TrimSpace(lines[i])
		}
	}
	return firstLine(out)
}

// impDumpDB передаёт дамп базы со старого сервера в новую: на пути gzip, без DEFINER (старых
// пользователей на новом сервере нет).
func impDumpDB(ctx context.Context, r impRunner, name, charset string) error {
	dump := `bash -o pipefail -c ` + shQuote(`mysqldump --single-transaction --quick --routines --triggers --events --default-character-set=`+charset+` `+shQuote(name)+` | gzip -c`)
	cmd := exec.CommandContext(ctx, "bash", "-c", "set -o pipefail; gunzip -c | sed -E "+shQuote(impLocalSQLScript())+" | mysql "+shQuote(name))
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	var serr error
	if ps, ok := r.(impPanelSource); ok {
		serr = ps.Dump(ctx, name, charset, in)
	} else {
		serr = r.Stream(ctx, dump, in)
	}
	in.Close()
	werr := cmd.Wait()
	if serr != nil {
		return serr
	}
	if werr != nil {
		return errf("Не удалось загрузить базу: %s", impMySQLError(stderr.String()+" "+werr.Error()))
	}
	return nil
}

var reImpNativeHash = regexp.MustCompile(`^\*[0-9A-F]{40}$`)

// impDBPasswordHash возвращает хеш пароля пользователя базы, если его можно перенести как есть.
func impDBPasswordHash(ctx context.Context, r impRunner, user string) string {
	if ps, ok := r.(impPanelSource); ok {
		return ps.DBHash(user)
	}
	for _, q := range []string{
		`SELECT COALESCE(NULLIF(Password,''), authentication_string), plugin FROM mysql.user WHERE user=` + sqlStr(user) + ` LIMIT 1`,
		`SELECT authentication_string, plugin FROM mysql.user WHERE user=` + sqlStr(user) + ` LIMIT 1`,
	} {
		out, err := r.Run(ctx, `mysql -N -B -e `+shQuote(q))
		if err != nil {
			continue
		}
		return impPickHash(out)
	}
	return ""
}

// impNormCharset приводит имя кодировки базы к тому, что понимает mysqldump.
func impNormCharset(out string) string {
	switch cs := strings.TrimSpace(out); {
	case cs == "utf8mb3":
		return "utf8"
	case cs == "utf8mb4" || cs == "utf8" || cs == "cp1251" || cs == "latin1":
		return cs
	}
	return "utf8mb4"
}

// impPickHash разбирает «хеш<TAB>плагин»: переносится только mysql_native_password.
func impPickHash(out string) string {
	f := strings.Fields(out)
	if len(f) >= 1 && reImpNativeHash.MatchString(strings.ToUpper(f[0])) && (len(f) == 1 || f[1] == "mysql_native_password") {
		return strings.ToUpper(f[0])
	}
	return ""
}

func impDBCharset(ctx context.Context, r impRunner, name string) string {
	if ps, ok := r.(impPanelSource); ok {
		return ps.DBCharset(name)
	}
	out, err := r.Run(ctx, `mysql -N -B -e `+shQuote(`SELECT default_character_set_name FROM information_schema.schemata WHERE schema_name=`+sqlStr(name)))
	if err != nil {
		return "utf8mb4"
	}
	return impNormCharset(out)
}

// impTransferDB создаёт базу и пользователя на новом сервере и загружает данные. Пароль
// пользователя сохраняется, если его хеш можно перенести: тогда настройки сайтов не меняются.
func impTransferDB(ctx context.Context, j *Job, r impRunner, db ImpDB, owner string, secrets map[string]string) ImpDBResult {
	res := ImpDBResult{Name: db.Name, Status: "error"}
	if !reDBName.MatchString(db.Name) || len(db.Name) > 64 {
		res.Detail = "недопустимое имя базы"
		return res
	}
	mu.Lock()
	exists := findDB(db.Name) != nil
	mu.Unlock()
	if exists {
		res.Status, res.Detail = "skipped", "база с таким именем уже есть на новом сервере"
		return res
	}
	user := db.Name
	if len(db.Users) > 0 && reDBName.MatchString(db.Users[0]) && len(db.Users[0]) <= 32 {
		user = db.Users[0]
	} else if len(user) > 32 {
		user = user[:32]
	}
	res.User = user
	charset := impDBCharset(ctx, r, db.Name)
	pass := randToken(12)
	hash := impDBPasswordHash(ctx, r, user)
	if err := createDB("", db.Name, user, pass, owner, "", charset); err != nil {
		res.Detail = err.Error()
		return res
	}
	if hash != "" {
		if _, err := sqlOn("", fmt.Sprintf("ALTER USER %s@%s IDENTIFIED VIA mysql_native_password USING %s;\nFLUSH PRIVILEGES;", sqlStr(user), sqlStr(dbUserHost("")), sqlStr(hash))); err == nil {
			res.PasswordKept = true
		}
	}
	if !res.PasswordKept {
		secrets[db.Name] = pass
	}
	jobStage(j, "Загрузка данных базы "+db.Name)
	if err := impDumpDB(ctx, r, db.Name, charset); err != nil {
		// Неполная база не остаётся: иначе повторный запуск пропустил бы её как «уже есть».
		dropDB(db.Name, true)
		res.Detail = err.Error()
		return res
	}
	res.Status = "ok"
	if !res.PasswordKept {
		res.Detail = "пароль пользователя базы изменён: он показан в отчёте, укажите его в настройках сайта"
	}
	return res
}

// impTransferSite создаёт пользователя и сайт на новом сервере и копирует файлы.
func impTransferSite(ctx context.Context, j *Job, r impRunner, s ImpSite, phpFallback string) ImpSiteResult {
	res := ImpSiteResult{Domain: s.Domain, Status: "error"}
	user := impUserName(s.Owner, s.Domain)
	res.User = user
	mu.Lock()
	exists := findSite(s.Domain) != nil
	mu.Unlock()
	if exists {
		res.Status, res.Detail = "skipped", "сайт уже есть на новом сервере"
		return res
	}
	if s.Mode != "proxy" && s.Root == "" {
		res.Detail = "на старом сервере не найдена папка сайта"
		return res
	}
	mu.Lock()
	_, uerr := ensureUser(user)
	if uerr == nil {
		save()
	}
	mu.Unlock()
	if uerr != nil {
		res.Detail = uerr.Error()
		return res
	}
	in := siteIn{Domain: s.Domain, Aliases: strings.Join(s.Aliases, ","), User: user, Mode: s.Mode, Index: "index.php index.html", MaxBody: 64, Gzip: true, ProxyTo: s.ProxyTo}
	if s.Mode == "php" {
		in.TryIndex = true
		in.PHP = s.PHP
		if in.PHP == "" || !phpIsInstalled(in.PHP) {
			in.PHP = phpFallback
		}
	}
	site, err := createSiteJob(in, j)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	res.Root = site.Root
	if s.Mode == "proxy" {
		res.Status = "ok"
		return res
	}
	// Заглушка нового сайта убирается, чтобы она не осталась рядом с файлами со старого сервера.
	if b, e := os.ReadFile(P(site.Root + "/index.html")); e == nil && strings.Contains(string(b), "Сайт создан. Загрузите файлы") {
		os.Remove(P(site.Root + "/index.html"))
	}
	jobStage(j, "Копирование файлов "+s.Domain)
	n, err := impCopyTree(ctx, r, s.Root, site.Root, user, func(n int64) {
		jobStage(j, fmt.Sprintf("Копирование файлов %s: %s", s.Domain, humanBytes(n)))
	})
	res.Bytes = n
	relabelWeb(site.Root)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	res.Status = "ok"
	return res
}

// impRun переносит выбранное. Недостающее на новом сервере ставится в начале.
func impRun(ctx context.Context, j *Job, sess *impSession, sel impSelection) (*ImpResult, error) {
	inv := sess.Inv
	if cfg.Dry {
		// Демо-режим: перенос имитируется по выбранному, ничего не создаётся.
		res := &ImpResult{}
		for _, d := range sel.Sites {
			for _, st := range inv.Sites {
				if st.Domain == d {
					jobStage(j, "Перенос сайта "+d)
					res.Sites = append(res.Sites, ImpSiteResult{Domain: d, User: impUserName(st.Owner, d), Root: "/var/www/" + impUserName(st.Owner, d) + "/data/www/" + d, Status: "ok", Bytes: st.Size})
				}
			}
		}
		for _, d := range sel.DBs {
			jobStage(j, "Перенос базы "+d)
			res.DBs = append(res.DBs, ImpDBResult{Name: d, User: d, Status: "ok", PasswordKept: true})
		}
		if sel.Cron {
			res.Cron = len(inv.Cron)
		}
		for _, d := range sel.Mail {
			jobStage(j, "Перенос почты домена "+d)
			n := 1
			for _, m := range inv.MailDomains {
				if m.Name == d {
					n = m.Boxes
				}
			}
			res.Mail = append(res.Mail, ImpItemResult{Name: d, Status: "ok", Count: n, Detail: fmt.Sprintf("ящиков: %d, пароли сохранены", n)})
		}
		for _, d := range sel.DNS {
			jobStage(j, "Перенос DNS-зоны "+d)
			res.DNS = append(res.DNS, ImpItemResult{Name: d, Status: "ok", Count: 12, Detail: "записей: 12, IP старого сервера заменён на IP этого в 3"})
		}
		for _, d := range sel.FTP {
			jobStage(j, "Перенос FTP-пользователя "+d)
			res.FTP = append(res.FTP, ImpItemResult{Name: d, Status: "ok", Detail: "пароль сохранён"})
		}
		if sel.Backup {
			res.Backup = &ImpItemResult{Name: "backup", Status: "ok", Detail: "FTP ftp.example.com сохранён; расписание выключено (на старом: ежедневно в 3:00, хранить копий: 7)"}
		}
		res.Info = fmt.Sprintf("Перенесено сайтов: %d из %d, баз: %d из %d", len(res.Sites), len(res.Sites), len(res.DBs), len(res.DBs))
		if n := len(res.Mail) + len(res.DNS) + len(res.FTP); n > 0 {
			res.Info += fmt.Sprintf(", почтовых доменов: %d, DNS-зон: %d, FTP-пользователей: %d", len(res.Mail), len(res.DNS), len(res.FTP))
		}
		return res, nil
	}
	if !inv.CanTransfer {
		return nil, errf("Старая панель не отдаёт файлы и базы: обновите её до версии 0.1.94")
	}
	if sess.SSH.Password == "" && strings.TrimSpace(sess.SSH.Key) == "" {
		jobStage(j, "Подключение к старой панели")
		p, err := impOpenPanel(ctx, sess)
		if err != nil {
			return nil, err
		}
		return impRunWith(ctx, j, p, sess, sel)
	}
	cf := sess.SSH
	cf.Fingerprint = inv.Fingerprint
	jobStage(j, "Подключение к старому серверу")
	r, err := impDialSSH(ctx, cf)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return impRunWith(ctx, j, r, sess, sel)
}

func impRunWith(ctx context.Context, j *Job, r impRunner, sess *impSession, sel impSelection) (*ImpResult, error) {
	inv := sess.Inv
	res := &ImpResult{}
	chosen := map[string]bool{}
	for _, d := range sel.Sites {
		chosen[d] = true
	}
	var sites []ImpSite
	for _, s := range inv.Sites {
		if chosen[s.Domain] {
			sites = append(sites, s)
		}
	}
	dbChosen := map[string]bool{}
	for _, d := range sel.DBs {
		dbChosen[d] = true
	}
	var dbs []ImpDB
	for _, d := range inv.DBs {
		if dbChosen[d.Name] {
			dbs = append(dbs, d)
		}
	}
	extras := len(sel.Mail) + len(sel.DNS) + len(sel.FTP)
	if sel.Backup {
		extras++
	}
	if len(sites) == 0 && len(dbs) == 0 && !sel.Cron && extras == 0 {
		return nil, errf("Ничего не выбрано для переноса")
	}
	// 1. Недостающее на новом сервере.
	phpFallback := defaultPHP()
	for _, n := range impNeeds(inv, sel) {
		if n.Installed || !strings.HasPrefix(n.Key, "php:") {
			continue
		}
		v := strings.TrimPrefix(n.Key, "php:")
		if v == "default" {
			v = "8.3"
		}
		jobStage(j, "Установка "+n.Title+" на новом сервере")
		if err := phpInstall(v); err != nil {
			res.Warnings = append(res.Warnings, n.Title+" не установился: "+firstLine(err.Error())+". Сайты будут работать на версии по умолчанию")
			continue
		}
		if phpFallback == "" {
			phpFallback = v
		}
	}
	if phpFallback == "" {
		phpFallback = defaultPHP()
	}
	// 2. Сайты и файлы.
	owners := map[string]string{} // владелец на старом сервере → пользователь панели
	for _, s := range sites {
		if jobStopped(j) {
			return res, errf("Задача остановлена")
		}
		jobStage(j, "Перенос сайта "+s.Domain)
		sr := impTransferSite(ctx, j, r, s, phpFallback)
		res.Sites = append(res.Sites, sr)
		if sr.Status == "ok" {
			owners[s.Owner] = sr.User
		}
		if sr.Status == "error" {
			res.Warnings = append(res.Warnings, s.Domain+": "+sr.Detail)
		}
	}
	// 3. Базы данных.
	for _, d := range dbs {
		if jobStopped(j) {
			return res, errf("Задача остановлена")
		}
		jobStage(j, "Перенос базы "+d.Name)
		dr := impTransferDB(ctx, j, r, d, "", sess.Secrets)
		res.DBs = append(res.DBs, dr)
		if dr.Status == "error" {
			res.Warnings = append(res.Warnings, "база "+d.Name+": "+dr.Detail)
		}
	}
	// 4. Задания cron тех владельцев, чьи сайты перенесены.
	if sel.Cron {
		for _, c := range inv.Cron {
			user, ok := owners[c.User]
			if !ok {
				continue
			}
			job := &CronJob{ID: randToken(6), User: user, Schedule: c.Schedule, Command: c.Command, Comment: "Импорт со старого сервера", Enabled: true, Created: time.Now()}
			mu.Lock()
			store.Cron = append(store.Cron, job)
			mu.Unlock()
			res.Cron++
		}
		for _, user := range owners {
			if err := writeCrontab(user); err != nil {
				res.Warnings = append(res.Warnings, "cron "+user+": "+firstLine(err.Error()))
			}
		}
		mu.Lock()
		save()
		mu.Unlock()
	}
	// 5. Почта, DNS и FTP — только через API старой ITRUPANEL.
	if extras > 0 {
		if p, isPanel := r.(*impPanel); isPanel {
			impTransferExtras(ctx, j, p, sess, sel, owners, res)
		} else {
			res.Warnings = append(res.Warnings, "Почта, DNS и FTP переносятся только с другой ITRUPANEL")
		}
	}
	ok := 0
	for _, s := range res.Sites {
		if s.Status == "ok" {
			ok++
		}
	}
	res.Info = fmt.Sprintf("Перенесено сайтов: %d из %d, баз: %d из %d", ok, len(res.Sites), impCountOK(res.DBs), len(res.DBs))
	if n := len(res.Mail) + len(res.DNS) + len(res.FTP); n > 0 {
		res.Info += fmt.Sprintf(", почтовых доменов: %d, DNS-зон: %d, FTP-пользователей: %d", impItemsOK(res.Mail), impItemsOK(res.DNS), impItemsOK(res.FTP))
	}
	return res, nil
}

func impItemsOK(r []ImpItemResult) int {
	n := 0
	for _, x := range r {
		if x.Status == "ok" {
			n++
		}
	}
	return n
}

func impCountOK(r []ImpDBResult) int {
	n := 0
	for _, x := range r {
		if x.Status == "ok" {
			n++
		}
	}
	return n
}
