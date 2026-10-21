// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- FTP (pure-ftpd, виртуальные пользователи PureDB) ----------

const (
	ftpPasswd = "/etc/pure-ftpd/pureftpd.passwd"
	ftpPDB    = "/etc/pure-ftpd/pureftpd.pdb"
)

var reFTPName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{1,63}$`)

func pureftpdCmd() string {
	if osInfo.Family == "rhel" {
		return "pure-ftpd /etc/pure-ftpd/pure-ftpd.conf &"
	}
	return "/usr/sbin/pure-ftpd-wrapper >/dev/null 2>&1 &"
}

func ftpService() string {
	return "pure-ftpd"
}

var ftpMutationMu sync.Mutex

func registerFTP() {
	api("ftp.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		rows := append([]*FTPUser{}, store.FTP...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		return map[string]any{"rows": rows, "active": serviceActive(ftpService())}, nil
	})
	apiOp("ftp.create", "FTP: создание пользователя", func(q *Req) (any, error) {
		ftpMutationMu.Lock()
		defer ftpMutationMu.Unlock()
		var in struct{ Name, Owner, Home, Password string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !reFTPName.MatchString(in.Name) {
			return nil, errf("Имя FTP-пользователя: латиница, цифры, _ . @ -")
		}
		mu.Lock()
		u := findUser(in.Owner)
		exists := false
		for _, f := range store.FTP {
			exists = exists || f.Name == in.Name
		}
		mu.Unlock()
		if u == nil {
			return nil, errf("Выберите владельца")
		}
		if exists {
			return nil, errf("Пользователь %s уже есть", in.Name)
		}
		home, ok := safeUnder(u.Home, in.Home)
		if !ok {
			return nil, errf("Домашняя папка должна быть внутри %s", u.Home)
		}
		if in.Password == "" {
			in.Password = randPass(14)
		}
		if len(in.Password) < 8 {
			return nil, errf("Пароль — минимум 8 символов")
		}
		uid, _ := run("id", "-u", u.Name)
		gid, _ := run("id", "-g", u.Name)
		if cfg.Dry {
			uid, gid = "1001", "1001"
		}
		pw := in.Password + "\n" + in.Password + "\n"
		if _, err := runInput(pw, "pure-pw", "useradd", in.Name, "-f", ftpPasswd, "-F", ftpPDB, "-u", uid, "-g", gid, "-d", home, "-m"); err != nil {
			return nil, err
		}
		mu.Lock()
		store.FTP = append(store.FTP, &FTPUser{Name: in.Name, Owner: u.Name, Home: home, Created: time.Now()})
		save()
		mu.Unlock()
		return map[string]any{"password": in.Password}, nil
	})
	apiOp("ftp.password", "FTP: смена пароля", func(q *Req) (any, error) {
		var in struct{ Name, Password string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Password == "" {
			in.Password = randPass(14)
		}
		if len(in.Password) < 8 {
			return nil, errf("Пароль — минимум 8 символов")
		}
		pw := in.Password + "\n" + in.Password + "\n"
		_, err := runInput(pw, "pure-pw", "passwd", in.Name, "-f", ftpPasswd, "-F", ftpPDB, "-m")
		return map[string]any{"password": in.Password}, err
	})
	apiOp("ftp.delete", "FTP: удаление пользователя", func(q *Req) (any, error) {
		var in struct{ Name string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !reFTPName.MatchString(in.Name) {
			return nil, errf("Неверное имя")
		}
		run("pure-pw", "userdel", in.Name, "-f", ftpPasswd, "-F", ftpPDB, "-m")
		mu.Lock()
		defer mu.Unlock()
		var keep []*FTPUser
		for _, f := range store.FTP {
			if f.Name != in.Name {
				keep = append(keep, f)
			}
		}
		store.FTP = keep
		save()
		return nil, nil
	})
}

// ---------- CRON ----------

var (
	reCronField = regexp.MustCompile(`^[0-9*/,\-A-Za-z]+$`)
	reCronMacro = regexp.MustCompile(`^@(reboot|yearly|annually|monthly|weekly|daily|hourly)$`)
)

func validSchedule(s string) bool {
	s = strings.TrimSpace(s)
	if reCronMacro.MatchString(s) {
		return true
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return false
	}
	for _, x := range f {
		if !reCronField.MatchString(x) {
			return false
		}
	}
	return true
}

// writeCrontab полностью переписывает crontab пользователя заданиями панели (под mu).
func writeCrontab(user string) error {
	var b strings.Builder
	b.WriteString("# ITRUPANEL: crontab управляется панелью, правки вручную будут перезаписаны\n")
	b.WriteString("SHELL=/bin/bash\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	n := 0
	for _, j := range store.Cron {
		if j.User != user {
			continue
		}
		n++
		if j.Comment != "" {
			b.WriteString("# " + strings.ReplaceAll(j.Comment, "\n", " ") + "\n")
		}
		if !j.Enabled {
			b.WriteString("#off# ")
		}
		b.WriteString(j.Schedule + " " + j.Command + "\n")
	}
	if n == 0 {
		run("crontab", "-u", user, "-r")
		return nil
	}
	_, err := runInput(b.String(), "crontab", "-u", user, "-")
	return err
}

func registerCron() {
	api("cron.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		users := []string{"root"}
		for _, u := range store.Users {
			users = append(users, u.Name)
		}
		return map[string]any{"rows": store.Cron, "users": users}, nil
	})
	apiOp("cron.save", "CRON: сохранение задания", func(q *Req) (any, error) {
		var in CronJob
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		in.Schedule = strings.Join(strings.Fields(in.Schedule), " ")
		in.Command = strings.TrimSpace(in.Command)
		if !validSchedule(in.Schedule) {
			return nil, errf("Расписание: 5 полей (минута час день месяц день_недели) или @daily и т. п.")
		}
		if in.Command == "" || strings.ContainsAny(in.Command, "\n\r") {
			return nil, errf("Команда — одна строка")
		}
		mu.Lock()
		defer mu.Unlock()
		if in.User != "root" && findUser(in.User) == nil {
			return nil, errf("Неизвестный пользователь")
		}
		var job *CronJob
		oldUser := ""
		for _, j := range store.Cron {
			if j.ID == in.ID && in.ID != "" {
				job = j
			}
		}
		if job == nil {
			job = &CronJob{ID: randToken(6), Created: time.Now()}
			store.Cron = append(store.Cron, job)
		} else {
			oldUser = job.User
		}
		job.User, job.Schedule, job.Command, job.Comment, job.Enabled = in.User, in.Schedule, in.Command, in.Comment, in.Enabled
		if err := writeCrontab(job.User); err != nil {
			return nil, err
		}
		if oldUser != "" && oldUser != job.User {
			writeCrontab(oldUser)
		}
		save()
		return job, nil
	})
	apiOp("cron.delete", "CRON: удаление задания", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		var keep []*CronJob
		user := ""
		for _, j := range store.Cron {
			if j.ID == in.ID {
				user = j.User
				continue
			}
			keep = append(keep, j)
		}
		if user == "" {
			return nil, errf("Задание не найдено")
		}
		store.Cron = keep
		save()
		return nil, writeCrontab(user)
	})
	apiOp("cron.run", "CRON: запуск вручную", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		var job CronJob
		for _, j := range store.Cron {
			if j.ID == in.ID {
				job = *j
			}
		}
		mu.Unlock()
		if job.ID == "" {
			return nil, errf("Задание не найдено")
		}
		out, err := runTimeout(2*time.Minute, "", "runuser", "-u", job.User, "--", "bash", "-c", job.Command)
		if len(out) > 20000 {
			out = out[len(out)-20000:]
		}
		if err != nil {
			return map[string]any{"output": out, "error": fmt.Sprint(err)}, nil
		}
		return map[string]any{"output": out}, nil
	})
}
