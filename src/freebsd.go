// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Поддержка FreeBSD (бета). Устанавливается отдельным архивом itrupanel-freebsd-*.zip.
// На FreeBSD нет systemd, пакеты лежат в /usr/local, а в системе одновременно живёт одна
// версия PHP (пакеты php83, php84 и т. д. конфликтуют друг с другом). Поэтому здесь собраны
// только отличия: службы rc.d, пакеты pkg, пользователи pw и пути PHP.

const (
	bsdFamily   = "freebsd"
	bsdPrefix   = "/usr/local"
	bsdSockDir  = "/var/db/itrupanel-php"
	bsdShell    = "/usr/local/bin/bash"
	bsdPHPIni   = bsdPrefix + "/etc/php.ini"
	bsdPoolDir  = bsdPrefix + "/etc/php-fpm.d"
	bsdFPMBin   = bsdPrefix + "/sbin/php-fpm"
	bsdPHPCLI   = bsdPrefix + "/bin/php"
	bsdMyCnfDir = bsdPrefix + "/etc/mysql"
)

func isBSD() bool { return osInfo.Family == bsdFamily }

// bsdPHPVersions — версии PHP, которые ставятся из пакетов FreeBSD.
var bsdPHPVersions = []string{"8.2", "8.3", "8.4", "8.5"}

// detectFreeBSD заполняет osInfo для FreeBSD.
func detectFreeBSD() {
	osInfo = OSInfo{ID: "freebsd", Family: bsdFamily, WebUser: "www", Systemd: false}
	out, _ := exec.Command("freebsd-version", "-u").Output()
	v := strings.TrimSpace(string(out))
	if v == "" {
		out, _ = exec.Command("uname", "-r").Output()
		v = strings.TrimSpace(string(out))
	}
	osInfo.Version = v
	if i := strings.IndexAny(v, "-"); i > 0 {
		osInfo.Version = v[:i]
	}
	osInfo.Pretty = "FreeBSD " + v
	sockDir = bsdSockDir
	fwConfig = "/etc/itrupanel/firewall.pf"
	ftpGuardPath = bsdPrefix + "/etc/fail2ban/jail.d/itrupanel-ftp.local"
}

// bsdRCName — имя службы в /usr/local/etc/rc.d для имени, которым пользуется остальной код.
func bsdRCName(name string) string {
	switch {
	case name == "nginx":
		return "nginx"
	case name == "mariadb" || name == "mysql" || name == "mysqld":
		return "mysql-server"
	case name == "cron" || name == "crond":
		return "cron"
	case strings.HasPrefix(name, "pure-ftpd"):
		return "pure-ftpd"
	case strings.Contains(name, "fpm"):
		return "php_fpm"
	case name == "named":
		return "nsd"
	case name == "apache2" || name == "httpd":
		return "apache24"
	case name == "itrupanel-spam-redis":
		return "itrupanel_spam_redis"
	case name == "opendkim":
		return "milter-opendkim"
	}
	return name
}

// bsdPrepare создаёт то, без чего служба не запустится после перезагрузки (каталог сокетов PHP-FPM).
func bsdPrepare(rc string) {
	if rc == "php_fpm" || rc == "nginx" {
		os.MkdirAll(P(sockDir), 0755)
	}
}

// bsdRC запускает команду службы rc.d. Вывод идёт во временный файл: демон, запущенный службой,
// наследует каналы и иначе держит их открытыми, а ожидание команды не заканчивается.
func bsdRC(rc, action string) error {
	script := `f=$(mktemp -t itrurc) || exit 1; service "$1" "$2" </dev/null >"$f" 2>&1; r=$?; cat "$f"; rm -f "$f"; exit $r`
	_, err := run("sh", "-c", script, "sh", rc, action)
	if err != nil && strings.Contains(err.Error(), "already running") {
		// служба поднялась между проверкой и запуском (например, её только что включили)
		return nil
	}
	if err == nil && (action == "onestart" || action == "onerestart") && !cfg.Dry {
		// демон пишет pid-файл не мгновенно: следующая проверка состояния не должна считать службу остановленной
		for i := 0; i < 20 && !bsdServiceActive(rc); i++ {
			time.Sleep(150 * time.Millisecond)
		}
	}
	return err
}

func bsdService(action, name string) error {
	if name == "lsws" {
		return errf("OpenLiteSpeed недоступен на FreeBSD")
	}
	rc := bsdRCName(name)
	bsdPrepare(rc)
	switch action {
	case "enable":
		if _, err := run("service", rc, "enable"); err != nil {
			return err
		}
		if bsdServiceActive(rc) {
			return nil
		}
		return bsdRC(rc, "onestart")
	case "start":
		if bsdServiceActive(rc) {
			return nil
		}
		return bsdRC(rc, "onestart")
	case "stop":
		if !bsdServiceActive(rc) {
			return nil
		}
		return bsdRC(rc, "onestop")
	case "restart":
		if !bsdServiceActive(rc) {
			return bsdRC(rc, "onestart")
		}
		return bsdRC(rc, "onerestart")
	case "reload":
		if !bsdServiceActive(rc) {
			return bsdRC(rc, "onestart")
		}
		return bsdRC(rc, "onereload")
	}
	return bsdRC(rc, "one"+action)
}

func bsdServiceActive(rc string) bool {
	if cfg.Dry {
		return true
	}
	return exec.Command("service", rc, "onestatus").Run() == nil
}

// bsdManagedPkg — пакеты, которыми управляет панель: их нельзя удалять побочно.
var bsdManagedPkg = regexp.MustCompile(`^(nginx|apache24|nsd|pure-ftpd|postfix|dovecot|opendkim|rspamd|redis|clamav|mariadb[0-9]+-(server|client)|php[0-9]+|py[0-9]+-fail2ban|roundcube-php[0-9]+)(-|$)`)

// bsdPkgCheck проверяет план установки: у пакетов FreeBSD бывают конфликты (например, bind920 требует
// старую lmdb0, а postfix — новую lmdb), и тогда pkg молча удаляет установленное. Если план удалил бы
// пакет, которым управляет панель, установка не выполняется.
func bsdPkgCheck(pkgs ...string) error {
	if cfg.Dry || len(pkgs) == 0 {
		return nil
	}
	out, _ := runTimeout(2*time.Minute, "", "pkg", append([]string{"install", "-n", "-y"}, pkgs...)...)
	var gone []string
	removing := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    "):
			if removing {
				name, _, _ := strings.Cut(strings.TrimSpace(line), ":")
				if bsdManagedPkg.MatchString(name + "-") {
					gone = append(gone, name)
				}
			}
		default:
			removing = strings.Contains(line, "to be REMOVED")
		}
	}
	if len(gone) > 0 {
		return errf("Установка %s удалила бы пакеты %s (конфликт пакетов FreeBSD); ничего не изменено", strings.Join(pkgs, ", "), strings.Join(gone, ", "))
	}
	return nil
}

func bsdPkgInstall(pkgs ...string) error {
	if err := bsdPkgCheck(pkgs...); err != nil {
		return err
	}
	_, err := runTimeout(30*time.Minute, "", "pkg", append([]string{"install", "-y"}, pkgs...)...)
	return err
}

// ---------- PHP ----------

var (
	bsdPHPMu      sync.Mutex
	bsdPHPVer     string
	bsdPHPVerTime time.Time
	reBsdPHPVer   = regexp.MustCompile(`^(\d+)\.(\d+)`)
)

// bsdInstalledPHP — версия установленного PHP-FPM, например "8.3"; пусто, если PHP нет.
func bsdInstalledPHP() string {
	if cfg.Dry {
		return "8.3"
	}
	if _, err := os.Stat(bsdFPMBin); err != nil {
		return ""
	}
	bsdPHPMu.Lock()
	defer bsdPHPMu.Unlock()
	if bsdPHPVer != "" && time.Since(bsdPHPVerTime) < time.Minute {
		return bsdPHPVer
	}
	out, _ := runTimeout(10*time.Second, "", bsdPHPCLI, "-r", "echo PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION;")
	if m := reBsdPHPVer.FindStringSubmatch(strings.TrimSpace(out)); m != nil {
		bsdPHPVer = m[1] + "." + m[2]
		bsdPHPVerTime = time.Now()
	}
	return bsdPHPVer
}

func bsdPHPForget() {
	bsdPHPMu.Lock()
	bsdPHPVer = ""
	bsdPHPMu.Unlock()
}

// bsdPHPPackages — пакеты PHP для FreeBSD (php83, php83-gd и т. д.).
func bsdPHPPackages(v string) []string {
	p := "php" + vv(v)
	out := []string{p}
	for _, e := range []string{"mysqli", "pdo", "pdo_mysql", "pdo_sqlite", "sqlite3", "gd", "curl", "mbstring", "dom", "simplexml", "xml", "xmlreader", "xmlwriter", "zip", "intl", "bcmath", "opcache", "soap", "session", "filter", "ctype", "fileinfo", "iconv", "tokenizer", "openssl", "phar", "posix", "zlib", "exif", "sodium", "gmp"} {
		out = append(out, p+"-"+e)
	}
	return append(out, p+"-pecl-imagick")
}

// bsdPHPInstall ставит PHP. Если на сервере уже стоит другая версия, сначала нужно её убрать:
// в пакетах FreeBSD версии PHP конфликтуют.
func bsdPHPInstall(v string) error {
	if cur := bsdInstalledPHP(); cur != "" && cur != v {
		return errf("На FreeBSD одновременно работает одна версия PHP, сейчас установлен PHP %s. Чтобы перейти на PHP %s, нажмите «Перейти на эту версию» у неё в разделе «PHP»: все сайты будут переведены на неё.", cur, v)
	}
	all := bsdPHPPackages(v)
	if err := bsdPkgInstall(all...); err != nil {
		// не каждое расширение есть у каждой версии: ставим ядро и добавляем остальное по одному
		if err2 := bsdPkgInstall(all[0]); err2 != nil {
			return err2
		}
		for _, p := range all[1:] {
			bsdPkgInstall(p)
		}
	}
	bsdPHPForget()
	if err := bsdEnsurePHPIni(); err != nil {
		return err
	}
	os.MkdirAll(P(sockDir), 0755)
	return service("enable", phpService(v))
}

// bsdEnsurePHPIni создаёт php.ini из php.ini-production: пакет FreeBSD его не создаёт сам.
func bsdEnsurePHPIni() error {
	if fileExists(bsdPHPIni) {
		return nil
	}
	b, err := os.ReadFile(P(bsdPHPIni + "-production"))
	if err != nil {
		return nil
	}
	return writeFile(bsdPHPIni, b, 0644)
}

// bsdPHPRemove удаляет PHP со всеми его модулями.
func bsdPHPRemove(v string) error {
	_, err := run("pkg", "remove", "-y", "-f", "-g", "php"+vv(v)+"*")
	if err == nil {
		bsdPHPForget()
	}
	return err
}

// ---------- пользователи ----------

func bsdAddUser(name, home string) error {
	if _, err := run("pw", "useradd", "-n", name, "-m", "-d", home, "-s", bsdShell, "-c", "ITRUPANEL site"); err != nil {
		return err
	}
	return nil
}

// bsdAddToGroup добавляет пользователя в группу (чтобы веб-сервер читал файлы сайта).
func bsdAddToGroup(user, group string) {
	run("pw", "groupmod", group, "-m", user)
}

// bsdAsUser настраивает запуск команды от имени пользователя без runuser (его нет во FreeBSD).
func bsdAsUser(cmd *exec.Cmd, name string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return err
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	cmd.Env = append(os.Environ(), "HOME="+u.HomeDir, "USER="+name, "LOGNAME="+name)
	return nil
}

// asUserCommand — команда от имени пользователя: runuser на Linux, смена идентификатора процесса на FreeBSD.
func asUserCommand(ctx context.Context, name string, argv ...string) (*exec.Cmd, error) {
	if !isBSD() {
		return exec.CommandContext(ctx, "runuser", append([]string{"-u", name, "--"}, argv...)...), nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	return cmd, bsdAsUser(cmd, name)
}

// runAsUser выполняет команду от имени пользователя и возвращает объединённый вывод.
func runAsUser(t time.Duration, name string, argv ...string) (string, error) {
	if cfg.Dry {
		return run("runuser", append([]string{"-u", name, "--"}, argv...)...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), t)
	defer cancel()
	cmd, err := asUserCommand(ctx, name, argv...)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err = cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("%s: %w\n%s", argv[0], err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// bsdUnsupported — разделы, которых пока нет на FreeBSD; в них возвращается понятная ошибка.
func bsdUnsupported(what string) error {
	if isBSD() {
		return errf("%s пока недоступно на FreeBSD (бета)", what)
	}
	return nil
}

// ---------- обновление панели ----------

// bsdStartUpdateWorker запускает службу обновления отдельным процессом (daemon(8)), не связанным с панелью:
// при остановке панели во время обновления она продолжает работать.
func bsdStartUpdateWorker(dir, id string) error {
	pid := filepath.Join(dir, "worker.pid")
	_, err := runTimeout(15*time.Second, "", "daemon", "-f", "-p", pid, "-o", filepath.Join(dir, "worker.log"), filepath.Join(dir, "worker"), "update-worker", id)
	return err
}

func bsdUpdateWorkerAlive(id string) bool {
	b, err := os.ReadFile(filepath.Join(cfg.Home, "var", "panel-updates", id, "worker.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// ---------- переключение версии PHP ----------

var reBsdSwitchPkg = func(v string) *regexp.Regexp {
	p := regexp.QuoteMeta("php" + vv(v))
	return regexp.MustCompile("^(" + p + "(-.*)?|roundcube(-.*)?-" + p + ")$")
}

// bsdPHPPackagesInstalled — установленные пакеты PHP версии v вместе с пакетами, которые от неё зависят (Roundcube).
func bsdPHPPackagesInstalled(v string) []string {
	out, _ := run("pkg", "query", "%n")
	re := reBsdSwitchPkg(v)
	var res []string
	for _, n := range strings.Fields(out) {
		if re.MatchString(n) {
			res = append(res, n)
		}
	}
	return res
}

// bsdPHPMapName переводит имя пакета на другую версию: php83-gd → php85-gd, roundcube-php83 → roundcube-php85.
func bsdPHPMapName(name, from, to string) string {
	return strings.Replace(name, "php"+vv(from), "php"+vv(to), 1)
}

// bsdPHPSwitch заменяет установленный PHP версии from на версию to: пакеты старой версии удаляются, ставятся
// такие же пакеты новой (которых нет — пропускаются и перечисляются в журнале). При ошибке старая версия
// возвращается. Вызывать под phpInstallMu. Файл php.ini и пулы PHP-FPM сайтов от версии не зависят и остаются.
func bsdPHPSwitch(j *Job, from, to string) (missing []string, err error) {
	logf := func(format string, a ...any) {
		if j != nil {
			j.Write([]byte(fmt.Sprintf(format, a...) + "\n"))
		}
	}
	oldPkgs := bsdPHPPackagesInstalled(from)
	if len(oldPkgs) == 0 {
		return nil, errf("PHP %s не найден среди установленных пакетов", from)
	}
	var newPkgs []string
	for _, n := range oldPkgs {
		m := bsdPHPMapName(n, from, to)
		if out, _ := run("pkg", "rquery", "%n", m); strings.TrimSpace(out) == "" {
			missing = append(missing, m)
			continue
		}
		newPkgs = append(newPkgs, m)
	}
	core := "php" + vv(to)
	hasCore := false
	for _, n := range newPkgs {
		hasCore = hasCore || n == core
	}
	if !hasCore {
		return nil, errf("В репозитории пакетов FreeBSD нет PHP %s", to)
	}
	if len(missing) > 0 {
		logf("Нет пакетов для PHP %s (будут пропущены): %s", to, strings.Join(missing, ", "))
	}
	// Скачиваем новые пакеты заранее: если сеть недоступна, версия не меняется
	jobStage(j, "Загрузка пакетов PHP "+to)
	if _, e := runTimeout(30*time.Minute, "", "pkg", append([]string{"fetch", "-y"}, newPkgs...)...); e != nil {
		return nil, errf("Не удалось загрузить пакеты PHP %s: %s", to, firstLine(e.Error()))
	}
	jobStage(j, "Остановка PHP-FPM и удаление PHP "+from)
	service("stop", phpService(from))
	if out, e := run("pkg", append([]string{"delete", "-y", "-f"}, oldPkgs...)...); e != nil {
		logf("%s", out)
		service("start", phpService(from))
		return missing, errf("Не удалось удалить PHP %s: %s", from, firstLine(e.Error()))
	}
	bsdPHPForget()
	jobStage(j, "Установка PHP "+to)
	if _, e := runTimeout(30*time.Minute, "", "pkg", append([]string{"install", "-y"}, newPkgs...)...); e != nil {
		// возвращаем прежнюю версию
		logf("Установка PHP %s не удалась, возвращаю PHP %s", to, from)
		runTimeout(30*time.Minute, "", "pkg", append([]string{"install", "-y"}, oldPkgs...)...)
		bsdPHPForget()
		service("start", phpService(from))
		return missing, errf("Не удалось установить PHP %s: %s. Прежняя версия возвращена.", to, firstLine(e.Error()))
	}
	bsdPHPForget()
	if e := bsdEnsurePHPIni(); e != nil {
		return missing, e
	}
	if _, e := run(bsdFPMBin, "-t"); e != nil {
		return missing, errf("Конфигурация PHP-FPM не прошла проверку: %s", firstLine(e.Error()))
	}
	os.MkdirAll(P(sockDir), 0755)
	return missing, service("restart", phpService(to))
}

// bsdPHPSwitchAll переключает PHP и переводит на новую версию сайты, версию по умолчанию и веб-почту.
func bsdPHPSwitchAll(j *Job, from, to string) error {
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	if _, err := bsdPHPSwitch(j, from, to); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, s := range store.Sites {
		if s.PHP == from {
			s.PHP = to
			n++
		}
	}
	if store.Settings.DefaultPHP == from || store.Settings.DefaultPHP == "" {
		store.Settings.DefaultPHP = to
	}
	if store.Mail.Webmail.PHP == from {
		store.Mail.Webmail.PHP = to
	}
	jobStage(j, fmt.Sprintf("PHP %s работает; на него переведено сайтов: %d", to, n))
	return saveChecked()
}
