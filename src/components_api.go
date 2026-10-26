// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	apacheSuryList = "/etc/apt/sources.list.d/apache2-sury.list"
	apacheSuryKey  = "/usr/share/keyrings/apache2-sury.gpg"
)

// ---------- подключение источников ----------

func removeFile(p string) {
	os.Remove(P(p))
}

// nginxApplySource подключает источник пакетов nginx (system — убирает наш источник).
func nginxApplySource(channel string) error {
	switch {
	case isBSD():
		return nil
	case osInfo.Family == "rhel":
		if channel == "system" {
			removeFile(nginxRepoFile)
			return nil
		}
		base := nginxOrgRPMBase(channel)
		if base == "" {
			return errf("Для вашей версии системы у nginx.org нет пакетов")
		}
		repo := "[nginx-itrupanel]\nname=nginx.org (" + channel + ")\nbaseurl=" + base + "\ngpgcheck=1\nenabled=1\ngpgkey=https://nginx.org/keys/nginx_signing.key\nmodule_hotfixes=true\n"
		return writeFile(nginxRepoFile, []byte(repo), 0644)
	}
	if channel == "system" {
		removeFile(nginxAptList)
		removeFile(nginxAptPin)
		return nil
	}
	code := osCodename()
	if code == "" || !compExists(nginxOrgDebBase(channel)+"/dists/"+code+"/Release") {
		return errf("Для %s %s у nginx.org нет пакетов", osInfo.ID, code)
	}
	key, err := compFetch("https://nginx.org/keys/nginx_signing.key")
	if err != nil {
		return errf("Не удалось загрузить ключ nginx.org: %s", firstLine(err.Error()))
	}
	if err = writeFile(nginxAptKey, key, 0644); err != nil {
		return err
	}
	list := "deb [signed-by=" + nginxAptKey + "] " + nginxOrgDebBase(channel) + " " + code + " nginx\n"
	if err = writeFile(nginxAptList, []byte(list), 0644); err != nil {
		return err
	}
	return writeFile(nginxAptPin, []byte("Package: *\nPin: origin nginx.org\nPin-Priority: 900\n"), 0644)
}

// apacheApplySource подключает репозиторий Ondřej Surý (только Debian и Ubuntu).
func apacheApplySource(j *Job, channel string) error {
	if osInfo.ID == "ubuntu" {
		if channel == "system" {
			if have("add-apt-repository") {
				return liveCommand(j, 5*time.Minute, "add-apt-repository", "-r", "-y", "ppa:ondrej/apache2")
			}
			return nil
		}
		if !have("add-apt-repository") {
			if err := liveCommand(j, 15*time.Minute, "apt-get", "install", "-y", "software-properties-common"); err != nil {
				return err
			}
		}
		return liveCommand(j, 5*time.Minute, "env", "LC_ALL=C.UTF-8", "add-apt-repository", "-y", "ppa:ondrej/apache2")
	}
	if channel == "system" {
		removeFile(apacheSuryList)
		return nil
	}
	code := osCodename()
	if code == "" || !compExists(suryApacheBase()+"/dists/"+code+"/Release") {
		return errf("Для Debian %s у репозитория Ondřej Surý нет пакетов Apache", code)
	}
	key, err := compFetch("https://packages.sury.org/apache2/apt.gpg")
	if err != nil {
		return errf("Не удалось загрузить ключ репозитория: %s", firstLine(err.Error()))
	}
	if err = writeFile(apacheSuryKey, key, 0644); err != nil {
		return err
	}
	return writeFile(apacheSuryList, []byte("deb [signed-by="+apacheSuryKey+"] "+suryApacheBase()+"/ "+code+" main\n"), 0644)
}

// ---------- смена источника ----------

// compSwitch меняет источник пакетов компонента и ставит версию из него. При ошибке настройки и источник возвращаются.
func compSwitch(j *Job, id, channel string) error {
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	d := compFind(id)
	if d == nil || !d.Switch {
		return errf("Для этого компонента источник выбрать нельзя")
	}
	prevSrc := d.Source()
	prevPkg := compInstalledPkg(d.Main)
	if prevSrc == channel {
		return errf("Этот источник уже выбран; для новой версии нажмите «Обновить»")
	}
	jobStage(j, "Сохранение настроек "+d.Title)
	backup, err := compBackup(d.ID, d.Config)
	if err != nil {
		return err
	}
	apply := func(ch string) error {
		if id == "nginx" {
			return nginxApplySource(ch)
		}
		return apacheApplySource(j, ch)
	}
	rollback := func(cause error) error {
		jobStage(j, "Возвращаю прежний источник и настройки: "+firstLine(cause.Error()))
		compRestore(backup)
		apply(prevSrc)
		compRefreshIndexes(j)
		if osInfo.Family == "rhel" && !isBSD() && d.ID == "nginx" {
			// на RHEL-системах пакеты nginx системы и nginx.org конфликтуют по файлам: заменяем целиком
			if left := d.Pkgs(); len(left) > 0 && !(prevSrc == "system" && compInstalledPkg(d.Main) == prevPkg) {
				rpmReplaceNginx(j, left, backup)
			}
		} else if prevPkg != "" {
			compInstallExact(j, d, prevPkg)
		}
		compPostFix(d)
		service("restart", d.Service)
		compInvalidate()
		return cause
	}
	jobStage(j, "Подключение источника: "+channel)
	if err = apply(channel); err != nil {
		return rollback(err)
	}
	if err = compRefreshIndexes(j); err != nil {
		return rollback(err)
	}
	jobStage(j, "Установка версии из источника")
	if err = compInstallFrom(j, d, channel, prevSrc, backup); err != nil {
		return rollback(err)
	}
	compPostFix(d)
	jobStage(j, "Проверка и перезапуск "+d.Title)
	if err = compVerify(d); err != nil {
		return rollback(err)
	}
	compInvalidate()
	jobStage(j, fmt.Sprintf("%s: источник «%s», версия %s", d.Title, channel, upstreamVersion(compInstalledPkg(d.Main))))
	return nil
}

// compInstallFrom ставит пакеты компонента из выбранного источника (в том числе более старую версию).
func compInstallFrom(j *Job, d *compDef, channel, prevSrc, backup string) error {
	switch {
	case isBSD():
		pkg := "nginx"
		if channel == "mainline" {
			pkg = "nginx-devel"
		}
		return liveCommand(j, 30*time.Minute, "pkg", "install", "-y", pkg)
	case osInfo.Family == "rhel":
		pkgs := d.Pkgs()
		if len(pkgs) == 0 {
			pkgs = []string{d.Main}
		}
		if prevSrc != "system" && channel != "system" {
			// stable ↔ mainline: пакеты одного происхождения, distro-sync ставит лучшую версию источника, в том числе более старую
			return liveCommand(j, 30*time.Minute, "dnf", append([]string{"distro-sync", "-y", "--allowerasing"}, pkgs...)...)
		}
		return rpmReplaceNginx(j, pkgs, backup)
	}
	pkgs := d.Pkgs()
	if len(pkgs) == 0 {
		pkgs = []string{d.Main}
	}
	// apt не понижает версию без явного указания: берём самую новую версию из подключённых источников
	// (madison не показывает установленную) и ставим её для всех пакетов компонента
	target := aptTargetVersion(d.Main)
	var specs []string
	for _, p := range pkgs {
		switch {
		case d.ID == "nginx" && channel != "system" && p != d.Main && !strings.HasPrefix(p, "nginx-module-"):
			// пакет nginx.org конфликтует с nginx-common и модулями из пакетов системы: они убираются при замене
			specs = append(specs, p+"-")
		case d.ID == "nginx" && p == "nginx-common" && channel == "system":
			if v := aptTargetVersion(p); v != "" {
				specs = append(specs, p+"="+v)
			} else {
				specs = append(specs, p)
			}
		case target != "" && aptHasVersion(p, target):
			specs = append(specs, p+"="+target)
		default:
			specs = append(specs, p)
		}
	}
	if d.ID == "nginx" && channel == "system" && !containsStr(pkgs, "nginx-common") {
		if v := aptTargetVersion("nginx-common"); v != "" {
			specs = append(specs, "nginx-common="+v)
		}
	}
	aptLockWait(j, 10*time.Minute)
	args := []string{"install", "-y", "--allow-downgrades", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef"}
	return liveCommand(j, 40*time.Minute, "apt-get", append(args, specs...)...)
}

func containsStr(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// rpmReplaceNginx заменяет nginx системы на nginx.org (или обратно): у этих пакетов одни и те же файлы
// (/usr/sbin/nginx), поэтому сначала удаляются все пакеты nginx, потом ставится новый, а настройки возвращаются из копии.
func rpmReplaceNginx(j *Job, old []string, backup string) error {
	args := append([]string{"remove", "-y", "--setopt=clean_requirements_on_remove=False", "--noautoremove"}, old...)
	if err := liveCommand(j, 20*time.Minute, "dnf", args...); err != nil {
		return err
	}
	if err := liveCommand(j, 30*time.Minute, "dnf", "install", "-y", "--allowerasing", "--best", "nginx"); err != nil {
		return err
	}
	// rpm переименовывает изменённые файлы настроек при удалении (.rpmsave): возвращаем свои
	compRestore(backup)
	service("enable", "nginx")
	return nil
}

// aptTargetVersion — самая новая версия пакета среди подключённых источников.
func aptTargetVersion(pkg string) string {
	out, _ := run("apt-cache", "madison", pkg)
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Split(l, "|"); len(f) >= 3 {
			return strings.TrimSpace(f[1])
		}
	}
	return ""
}

func aptHasVersion(pkg, ver string) bool {
	out, _ := run("apt-cache", "madison", pkg)
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Split(l, "|"); len(f) >= 3 && strings.TrimSpace(f[1]) == ver {
			return true
		}
	}
	return false
}

// compInstallExact возвращает точную прежнюю версию главного пакета (откат).
func compInstallExact(j *Job, d *compDef, ver string) {
	switch {
	case isBSD():
		return
	case osInfo.Family == "rhel":
		if liveCommand(j, 30*time.Minute, "dnf", "install", "-y", "--allowerasing", d.Main+"-"+ver) != nil {
			liveCommand(j, 30*time.Minute, "dnf", "downgrade", "-y", d.Main+"-"+ver)
		}
		return
	}
	aptLockWait(j, 10*time.Minute)
	liveCommand(j, 30*time.Minute, "apt-get", "install", "-y", "--allow-downgrades", "-o", "Dpkg::Options::=--force-confold", d.Main+"="+ver)
}

// ---------- API ----------

func registerComponents() {
	api("components.list", func(q *Req) (any, error) {
		var in struct{ Force bool }
		q.Bind(&in)
		rows := compList(in.Force)
		for i := range rows {
			if jobRunning("comp:" + rows[i].ID) {
				rows[i].Job = "running"
			}
		}
		mu.Lock()
		stamp := compAt
		mu.Unlock()
		_ = stamp
		return map[string]any{"rows": rows, "checking": jobRunning("comp:check"), "demo": cfg.Dry, "bsd": isBSD(), "family": osInfo.Family}, nil
	})
	api("components.channels", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		ch, err := compChannelsFor(in.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"channels": ch}, nil
	})
	apiOp("components.check", "Веб-серверы: проверка обновлений", func(q *Req) (any, error) {
		id := startJob("comp:check", "Проверка обновлений веб-серверов и PHP", q.S.User, func(j *Job) (any, error) {
			compCheckNotify(j)
			return map[string]string{"info": "Проверка завершена"}, nil
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("components.update", "Веб-серверы: обновление", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		d := compFind(in.ID)
		if d == nil {
			return nil, errf("Компонент не найден")
		}
		if runningPanelUpdate(reconcilePanelUpdate()) {
			return nil, errf("Сначала завершите обновление панели")
		}
		title := "Обновление " + d.Title
		id := startJob("comp:"+in.ID, title, q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": d.Title + " обновлён"}, compUpdate(j, in.ID)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("components.switch", "Веб-серверы: источник пакетов", func(q *Req) (any, error) {
		var in struct{ ID, Channel string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		d := compFind(in.ID)
		if d == nil || !d.Switch {
			return nil, errf("Для этого компонента источник выбрать нельзя")
		}
		allowed := map[string]bool{"system": true, "stable": true, "mainline": true, "ondrej": true}
		if in.ID == "nginx" && in.Channel == "ondrej" || in.ID == "apache" && (in.Channel == "stable" || in.Channel == "mainline") || !allowed[in.Channel] {
			return nil, errf("Неизвестный источник")
		}
		if isBSD() && in.Channel == "system" {
			return nil, errf("Неизвестный источник")
		}
		title := "Источник пакетов " + d.Title + ": " + in.Channel
		id := startJob("comp:"+in.ID, title, q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Источник пакетов " + d.Title + " изменён"}, compSwitch(j, in.ID, in.Channel)
		})
		return map[string]string{"job": id}, nil
	})
}

var _ = strings.TrimSpace
