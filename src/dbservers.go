// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Серверы баз данных. Основной — MariaDB из дистрибутива (имя "", доступ через unix-сокет).
// Дополнительные — MySQL/MariaDB нужных версий в Docker-контейнерах на 127.0.0.1:3307, 3308…

type DBServer struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"` // mysql | mariadb
	Version  string    `json:"version"`
	Port     int       `json:"port"`
	RootPass string    `json:"root_pass"`
	Created  time.Time `json:"created"`
	Status   string    `json:"status,omitempty"` // installing | error
	Error    string    `json:"error,omitempty"`
}

var dbVersions = map[string][]string{
	"mysql":   {"5.7", "8.0", "8.4", "9.4"},
	"mariadb": {"10.6", "10.11", "11.4", "11.8"},
}

const dbDataRoot = "/var/lib/itrupanel-db"

func findDBServer(name string) *DBServer {
	for _, s := range store.DBServers {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func dbContainer(name string) string { return "itru-db-" + name }

func dbClient(t string) (client, dump string) {
	if t == "mariadb" {
		return "mariadb", "mariadb-dump"
	}
	return "mysql", "mysqldump"
}

// sqlOn выполняет SQL на выбранном сервере ("" — основной).
func sqlOn(server, sql string) (string, error) {
	if server == "" {
		return mysql(sql)
	}
	mu.Lock()
	s := findDBServer(server)
	var srv DBServer
	if s != nil {
		srv = *s
	}
	mu.Unlock()
	if s == nil {
		return "", errf("Нет сервера БД %s", server)
	}
	cl, _ := dbClient(srv.Type)
	return runInput(sql, "docker", "exec", "-i", "-e", "MYSQL_PWD="+srv.RootPass, dbContainer(srv.Name), cl, "-uroot", "-N", "-B")
}

// dumpCmd/importCmd — shell-команды для дампа и загрузки базы на нужном сервере.
func dumpCmd(server, db, out string) string {
	base := "--single-transaction --quick --routines --triggers --default-character-set=utf8mb4 " + db
	if server == "" {
		return "mysqldump " + base + " > " + out
	}
	mu.Lock()
	s := *findDBServer(server)
	mu.Unlock()
	_, dump := dbClient(s.Type)
	return fmt.Sprintf("docker exec -e MYSQL_PWD=%s %s %s -uroot %s > %s", s.RootPass, dbContainer(s.Name), dump, base, out)
}

func importCmd(server, db, in string) string {
	if server == "" {
		return "mysql " + db + " < " + in
	}
	mu.Lock()
	s := *findDBServer(server)
	mu.Unlock()
	cl, _ := dbClient(s.Type)
	return fmt.Sprintf("docker exec -i -e MYSQL_PWD=%s %s %s -uroot %s < %s", s.RootPass, dbContainer(s.Name), cl, db, in)
}

// dbUserHost — с какого адреса приходят подключения: к основному серверу по сокету,
// к контейнерам — через docker-мост.
func dbUserHost(server string) string {
	if server == "" {
		return "localhost"
	}
	return "%"
}

func ensureDocker() error {
	if have("docker") {
		return service("enable", "docker")
	}
	var err error
	if osInfo.Family == "rhel" {
		_, err = sh("dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo && dnf install -y docker-ce docker-ce-cli containerd.io")
	} else {
		err = pkgInstall("docker.io")
		// В Debian 13 консольный клиент вынесен в пакет docker-cli (у docker.io он лишь рекомендован)
		if err == nil && !have("docker") {
			err = pkgInstall("docker-cli")
		}
	}
	if err != nil {
		return errf("Не удалось установить Docker: %s", firstLine(err.Error()))
	}
	if !have("docker") {
		return errf("Пакеты Docker установлены, но команда docker не найдена")
	}
	return service("enable", "docker")
}

func createDBServer(typ, version string) (*DBServer, error) {
	ok := false
	for _, v := range dbVersions[typ] {
		ok = ok || v == version
	}
	if !ok {
		return nil, errf("Неизвестная версия %s %s", typ, version)
	}
	name := typ + strings.ReplaceAll(version, ".", "")
	mu.Lock()
	if findDBServer(name) != nil {
		mu.Unlock()
		return nil, errf("Сервер %s %s уже добавлен", typ, version)
	}
	port := 3307
	for {
		used := false
		for _, s := range store.DBServers {
			used = used || s.Port == port
		}
		if !used {
			break
		}
		port++
	}
	srv := &DBServer{Name: name, Type: typ, Version: version, Port: port, RootPass: randPass(24), Created: time.Now(), Status: "installing"}
	store.DBServers = append(store.DBServers, srv)
	save()
	mu.Unlock()

	go func() {
		err := startDBContainer(srv)
		mu.Lock()
		if err != nil {
			srv.Status, srv.Error = "error", err.Error()
			addNotice("error", "", "Сервер БД "+typ+" "+version+" не запущен: "+firstLine(err.Error()))
		} else {
			srv.Status, srv.Error = "", ""
			addNotice("info", "", "Сервер БД "+typ+" "+version+" запущен на порту "+fmt.Sprint(port))
		}
		save()
		mu.Unlock()
		if err == nil && fileExists(cfg.Home+"/www/phpmyadmin/index.php") {
			writePMAConfig()
		}
	}()
	return srv, nil
}

func startDBContainer(s *DBServer) error {
	if err := ensureDocker(); err != nil {
		return err
	}
	data := dbDataRoot + "/" + s.Name
	os.MkdirAll(P(data), 0700)
	env := "MYSQL_ROOT_PASSWORD"
	if s.Type == "mariadb" {
		env = "MARIADB_ROOT_PASSWORD"
	}
	start := func(lowMemory bool) error {
		run("docker", "rm", "-f", dbContainer(s.Name))
		args := []string{"run", "-d", "--name", dbContainer(s.Name), "--restart", "unless-stopped",
			"-p", fmt.Sprintf("127.0.0.1:%d:3306", s.Port), "-e", env + "=" + s.RootPass,
			"-v", data + ":/var/lib/mysql", s.Type + ":" + s.Version}
		args = append(args, dbServerArgs(lowMemory)...)
		if _, err := runTimeout(20*time.Minute, "", "docker", args...); err != nil {
			return err
		}
		cl, _ := dbClient(s.Type)
		for i := 0; i < 90; i++ {
			if cfg.Dry {
				return nil
			}
			if _, err := runInput("SELECT 1;", "docker", "exec", "-i", "-e", "MYSQL_PWD="+s.RootPass, dbContainer(s.Name), cl, "-uroot"); err == nil {
				return nil
			}
			time.Sleep(2 * time.Second)
		}
		out, _ := run("docker", "logs", "--tail", "20", dbContainer(s.Name))
		return errf("сервер не ответил за 3 минуты: %s", out)
	}
	if lowMemoryHost() {
		// Если экономные настройки не подошли версии сервера, запускаем со стандартными
		if err := start(true); err == nil {
			return nil
		}
	}
	return start(false)
}

// lowMemoryHost — сервер с памятью меньше 4 ГБ: MySQL/MariaDB в контейнерах и Rspamd работают в экономном режиме.
func lowMemoryHost() bool {
	if cfg.Dry {
		return false
	}
	total, _ := memInfo()
	return total > 0 && total < 4<<30
}

// dbServerArgs — параметры mysqld/mariadbd. Стандартные настройки MySQL 8/9 занимают 400+ МБ
// без нагрузки (в основном performance_schema), экономные — около 120–150 МБ.
func dbServerArgs(lowMemory bool) []string {
	args := []string{"--character-set-server=utf8mb4", "--collation-server=utf8mb4_unicode_ci"}
	if lowMemory {
		args = append(args, dbLowMemoryFlag, "--innodb-buffer-pool-size=64M", "--max-connections=50",
			"--key-buffer-size=8M", "--table-open-cache=400", "--innodb-log-buffer-size=8M")
	}
	return args
}

const dbLowMemoryFlag = "--performance-schema=OFF"

// dbContainerLowMemory — запущен ли контейнер уже в экономном режиме.
func dbContainerLowMemory(name string) bool {
	out, err := run("docker", "inspect", "-f", "{{json .Args}}", dbContainer(name))
	return err == nil && strings.Contains(out, dbLowMemoryFlag)
}

func registerDBServers() {
	api("dbservers.list", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		rows := []map[string]any{}
		ver := mainDBVersion()
		n := 0
		for _, d := range store.DBs {
			if d.Server == "" {
				n++
			}
		}
		rows = append(rows, map[string]any{"name": "", "title": "MariaDB (основной)", "type": "mariadb", "version": ver, "port": 3306, "host": "localhost", "dbs": n, "active": serviceActive("mariadb"), "main": true})
		list := append([]*DBServer{}, store.DBServers...)
		sort.Slice(list, func(i, j int) bool { return list[i].Port < list[j].Port })
		for _, s := range list {
			n := 0
			for _, d := range store.DBs {
				if d.Server == s.Name {
					n++
				}
			}
			title := map[string]string{"mysql": "MySQL", "mariadb": "MariaDB"}[s.Type] + " " + s.Version
			active := s.Status == ""
			if active && !cfg.Dry {
				out, _ := run("docker", "inspect", "-f", "{{.State.Running}}", dbContainer(s.Name))
				active = strings.TrimSpace(out) == "true"
			}
			rows = append(rows, map[string]any{"name": s.Name, "title": title, "type": s.Type, "version": s.Version, "port": s.Port, "host": "127.0.0.1", "dbs": n, "active": active, "status": s.Status, "error": s.Error})
		}
		return map[string]any{"rows": rows, "versions": dbVersions, "docker": have("docker")}, nil
	})
	apiOp("dbservers.create", "Сервер БД: добавление", func(q *Req) (any, error) {
		var in struct{ Type, Version string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return createDBServer(in.Type, in.Version)
	})
	apiOp("dbservers.action", "Сервер БД: управление", func(q *Req) (any, error) {
		var in struct {
			Name, Action string
			Data         bool
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		s := findDBServer(in.Name)
		var srv DBServer
		if s != nil {
			srv = *s
		}
		busy := false
		for _, d := range store.DBs {
			busy = busy || d.Server == in.Name
		}
		mu.Unlock()
		if s == nil {
			return nil, errf("Сервер не найден")
		}
		c := dbContainer(srv.Name)
		switch in.Action {
		case "start", "stop", "restart":
			_, err := run("docker", in.Action, c)
			return nil, err
		case "reinstall":
			go func() {
				err := startDBContainer(&srv)
				mu.Lock()
				if x := findDBServer(srv.Name); x != nil {
					x.Status, x.Error = "", ""
					if err != nil {
						x.Status, x.Error = "error", err.Error()
					}
				}
				save()
				mu.Unlock()
			}()
			return nil, nil
		case "delete":
			if busy {
				return nil, errf("На сервере есть базы — сначала удалите их")
			}
			run("docker", "rm", "-f", c)
			if in.Data {
				os.RemoveAll(P(dbDataRoot + "/" + srv.Name))
			}
			mu.Lock()
			var keep []*DBServer
			for _, x := range store.DBServers {
				if x.Name != srv.Name {
					keep = append(keep, x)
				}
			}
			store.DBServers = keep
			save()
			mu.Unlock()
			if fileExists(cfg.Home + "/www/phpmyadmin/index.php") {
				writePMAConfig()
			}
			return nil, nil
		}
		return nil, errf("Неизвестное действие")
	})
}

// mainDBVersion — версия основного MariaDB, например «11.8.6-MariaDB-5ubuntu0.1 from Ubuntu».
func mainDBVersion() string {
	if cfg.Dry {
		return "10.11.6-MariaDB"
	}
	ver, _ := run("mysql", "-N", "-B", "-e", "SELECT VERSION();")
	return strings.TrimSpace(ver)
}
