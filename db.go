// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// MariaDB: панель работает от root через unix-сокет (mysql без пароля).

func sqlStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\x00", "", "\n", `\n`, "\r", `\r`)
	return "'" + r.Replace(s) + "'"
}

func mysql(sql string) (string, error) {
	return runInput(sql, "mysql", "-N", "-B")
}

func dbSizes(server string) map[string]int64 {
	out, _ := sqlOn(server, "SELECT table_schema, SUM(data_length+index_length) FROM information_schema.tables GROUP BY table_schema;")
	m := map[string]int64{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 2 {
			var n int64
			fmt.Sscan(f[1], &n)
			m[f[0]] = n
		}
	}
	return m
}

var dbCreateMu sync.Mutex

func createDB(server, name, user, pass, owner, site, charset string) error {
	dbCreateMu.Lock()
	defer dbCreateMu.Unlock()
	if !reDBName.MatchString(name) || len(name) > 64 {
		return errf("Имя базы: латиница, цифры и _")
	}
	if user == "" {
		user = name
	}
	if !reDBName.MatchString(user) || len(user) > 32 {
		return errf("Имя пользователя БД: латиница, цифры и _, до 32 символов")
	}
	if len(pass) < 8 {
		return errf("Пароль БД — минимум 8 символов")
	}
	if charset != "utf8mb4" && charset != "utf8" && charset != "cp1251" && charset != "latin1" {
		charset = "utf8mb4"
	}
	coll := map[string]string{"utf8mb4": "utf8mb4_unicode_ci", "utf8": "utf8_general_ci", "cp1251": "cp1251_general_ci", "latin1": "latin1_swedish_ci"}[charset]
	mu.Lock()
	if findDB(name) != nil {
		mu.Unlock()
		return errf("База %s уже есть", name)
	}
	mu.Unlock()
	if out, _ := sqlOn(server, "SHOW DATABASES LIKE "+sqlStr(name)+";"); strings.TrimSpace(out) == name {
		return errf("База %s уже есть на сервере", name)
	}
	host := sqlStr(dbUserHost(server))
	sql := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET %s COLLATE %s;\n"+
		"CREATE USER IF NOT EXISTS %s@%s IDENTIFIED BY %s;\n"+
		"ALTER USER %s@%s IDENTIFIED BY %s;\n"+
		"GRANT ALL PRIVILEGES ON `%s`.* TO %s@%s;\nFLUSH PRIVILEGES;\n",
		name, charset, coll, sqlStr(user), host, sqlStr(pass), sqlStr(user), host, sqlStr(pass), strings.ReplaceAll(name, "_", `\_`), sqlStr(user), host)
	if _, err := sqlOn(server, sql); err != nil {
		return err
	}
	mu.Lock()
	store.DBs = append(store.DBs, &DB{Name: name, User: user, Owner: owner, Site: site, Server: server, Created: time.Now()})
	save()
	mu.Unlock()
	return nil
}

func dropDB(name string, dropUser bool) error {
	mu.Lock()
	d := findDB(name)
	mu.Unlock()
	if d == nil {
		return errf("База не найдена")
	}
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\n", name)
	if dropUser {
		mu.Lock()
		shared := false
		for _, o := range store.DBs {
			shared = shared || (o != d && o.User == d.User && o.Server == d.Server)
		}
		mu.Unlock()
		if !shared {
			for _, host := range databaseManagedHosts(*d) {
				sql += fmt.Sprintf("DROP USER IF EXISTS %s@%s;\n", sqlStr(d.User), sqlStr(host))
			}
		}
	}
	if _, err := sqlOn(d.Server, sql); err != nil {
		return err
	}
	mu.Lock()
	var keep []*DB
	for _, o := range store.DBs {
		if o != d {
			keep = append(keep, o)
		}
	}
	store.DBs = keep
	save()
	mu.Unlock()
	return nil
}

func registerDB() {
	registerDatabaseNetwork()
	registerDatabaseAccess()
	api("db.list", func(q *Req) (any, error) {
		mu.Lock()
		servers := []string{""}
		for _, s := range store.DBServers {
			if s.Status == "" {
				servers = append(servers, s.Name)
			}
		}
		mu.Unlock()
		sizes := map[string]map[string]int64{}
		for _, s := range servers {
			sizes[s] = dbSizes(s)
		}
		mu.Lock()
		defer mu.Unlock()
		out := []map[string]any{}
		for _, d := range store.DBs {
			srv, port := "MariaDB (основной)", 3306
			if x := findDBServer(d.Server); x != nil {
				srv = map[string]string{"mysql": "MySQL", "mariadb": "MariaDB"}[x.Type] + " " + x.Version
				port = x.Port
			}
			out = append(out, map[string]any{"db": d, "size": sizes[d.Server][d.Name], "server": srv, "port": port})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["db"].(*DB).Name < out[j]["db"].(*DB).Name })
		return map[string]any{"rows": out, "server": serviceActive("mariadb"), "pma": fileExists(cfg.Home + "/www/phpmyadmin/index.php")}, nil
	})
	apiOp("db.create", "База данных: создание", func(q *Req) (any, error) {
		var in struct{ Name, User, Password, Owner, Site, Charset, Server string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Password == "" {
			in.Password = randPass(16)
		}
		if err := createDB(in.Server, in.Name, in.User, in.Password, in.Owner, in.Site, in.Charset); err != nil {
			return nil, err
		}
		return map[string]any{"password": in.Password}, nil
	})
	apiOp("db.delete", "База данных: удаление", func(q *Req) (any, error) {
		var in struct {
			Name     string
			DropUser bool `json:"drop_user"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, dropDB(in.Name, in.DropUser)
	})
	apiOp("db.password", "База данных: смена пароля", func(q *Req) (any, error) {
		var in struct{ Name, Password string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		d := findDB(in.Name)
		mu.Unlock()
		if d == nil {
			return nil, errf("База не найдена")
		}
		if in.Password == "" {
			in.Password = randPass(16)
		}
		if len(in.Password) < 8 {
			return nil, errf("Пароль — минимум 8 символов")
		}
		err := changeDatabasePassword(*d, in.Password)
		return map[string]any{"password": in.Password}, err
	})
	apiOp("pma.install", "phpMyAdmin: установка", func(q *Req) (any, error) {
		return nil, installPMA()
	})
}

// ---------- phpMyAdmin ----------

const pmaPort = "127.0.0.1:8088"

func installPMA() error {
	v := defaultPHP()
	if v == "" {
		return errf("Сначала установите PHP")
	}
	dir := cfg.Home + "/www/phpmyadmin"
	if !fileExists(dir + "/index.php") {
		os.MkdirAll(P(cfg.Home+"/www"), 0755)
		_, err := sh(fmt.Sprintf(`set -e; cd %s/www; rm -rf pma.tmp; mkdir pma.tmp; curl -fsSL https://www.phpmyadmin.net/downloads/phpMyAdmin-latest-all-languages.tar.gz | tar xz -C pma.tmp --strip-components=1; rm -rf phpmyadmin; mv pma.tmp phpmyadmin`, cfg.Home))
		if err != nil {
			return err
		}
	}
	if err := preparePMADirs(); err != nil {
		return err
	}
	if err := writePMAConfig(); err != nil {
		return err
	}
	if _, err := run("chown", "-R", "root:"+osInfo.WebUser, dir); err != nil {
		return err
	}
	pool := fmt.Sprintf(`; ITRUPANEL: phpMyAdmin
[itru_pma]
user = %[1]s
group = %[1]s
listen = %[2]s/itrupanel-pma.sock
listen.owner = %[1]s
listen.group = %[1]s
pm = ondemand
pm.max_children = 4
php_admin_value[session.save_path] = %[3]s/sessions
php_admin_value[upload_tmp_dir] = %[3]s/tmp
php_admin_value[sys_temp_dir] = %[3]s/tmp
php_admin_value[session.gc_probability] = 1
php_admin_value[session.gc_divisor] = 100
php_admin_value[upload_max_filesize] = 256M
php_admin_value[post_max_size] = 256M
`, osInfo.WebUser, sockDir, pmaDataDir)
	for _, x := range phpInstalled() {
		os.Remove(P(phpPoolDir(x) + "/itru_pma.conf"))
	}
	if err := writeFile(phpPoolDir(v)+"/itru_pma.conf", []byte(pool), 0644); err != nil {
		return err
	}
	if err := service("restart", phpService(v)); err != nil {
		return err
	}
	ng := fmt.Sprintf(`# ITRUPANEL: phpMyAdmin (доступ только через панель)
server {
    server_tokens off;
    listen %s;
    root %s/www;
    index index.php;
    access_log off;
    location / { return 404; }
    client_max_body_size 256m;
    location /phpmyadmin/ { try_files $uri $uri/ =404; }
    location ~ ^/phpmyadmin/(libraries|setup|templates|tmp|vendor)/ { deny all; }
    location ~ ^/phpmyadmin/.*\.php$ {
        try_files $uri =404;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param HTTPS on;
        fastcgi_pass unix:%s/itrupanel-pma.sock;
    }
}
`, pmaPort, cfg.Home, sockDir)
	if err := writeFile(vhostDir+"/000-itrupanel-pma.conf", []byte(ng), 0644); err != nil {
		return err
	}
	if err := labelPMAPath(cfg.Home + "/www"); err != nil {
		return err
	}
	return nginxTestReload()
}

// writePMAConfig — config.inc.php со всеми серверами БД.
func writePMAConfig() error {
	mu.Lock()
	secret := store.Settings.PMASecret
	servers := append([]*DBServer{}, store.DBServers...)
	mu.Unlock()
	if len(secret) < 32 {
		secret = (secret + randToken(16))[:32]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<?php\n// ITRUPANEL: файл перезаписывается панелью\n$cfg['blowfish_secret'] = '%s';\n", secret[:32])
	b.WriteString("$i = 0;\n$i++;\n$cfg['Servers'][$i]['verbose'] = 'MariaDB (основной)';\n$cfg['Servers'][$i]['host'] = 'localhost';\n$cfg['Servers'][$i]['auth_type'] = 'cookie';\n$cfg['Servers'][$i]['AllowRoot'] = false;\n")
	for _, s := range servers {
		if s.Status != "" {
			continue
		}
		fmt.Fprintf(&b, "$i++;\n$cfg['Servers'][$i]['verbose'] = '%s %s';\n$cfg['Servers'][$i]['host'] = '127.0.0.1';\n$cfg['Servers'][$i]['port'] = '%d';\n$cfg['Servers'][$i]['auth_type'] = 'cookie';\n$cfg['Servers'][$i]['AllowRoot'] = false;\n",
			map[string]string{"mysql": "MySQL", "mariadb": "MariaDB"}[s.Type], s.Version, s.Port)
	}
	fmt.Fprintf(&b, "$cfg['TempDir'] = '%s/tmp';\n$cfg['SessionSavePath'] = '%s/sessions';\n", pmaDataDir, pmaDataDir)
	b.WriteString(`// The panel supplies this header after checking the temporary link and login.
if (!empty($_SERVER['HTTP_X_ITRUPANEL_PMA_URL'])) {
    $cfg['PmaAbsoluteUri'] = $_SERVER['HTTP_X_ITRUPANEL_PMA_URL'];
}
`)
	b.WriteString("$cfg['DefaultLang'] = 'ru';\n$cfg['VersionCheck'] = false;\n$cfg['ShowPhpInfo'] = false;\n")
	if err := writeFile(cfg.Home+"/www/phpmyadmin/config.inc.php", []byte(b.String()), 0640); err != nil {
		return err
	}
	_, err := run("chown", "root:"+osInfo.WebUser, cfg.Home+"/www/phpmyadmin/config.inc.php")
	return err
}
