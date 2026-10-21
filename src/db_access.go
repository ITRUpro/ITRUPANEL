// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

func databaseManagedHosts(d DB) []string {
	if len(d.AccessHosts) > 0 {
		return dedup(d.AccessHosts)
	}
	return []string{dbUserHost(d.Server)}
}

func changeDatabasePassword(d DB, password string) error {
	var sql strings.Builder
	for _, host := range databaseManagedHosts(d) {
		fmt.Fprintf(&sql, "ALTER USER %s@%s IDENTIFIED BY %s;\n", sqlStr(d.User), sqlStr(host), sqlStr(password))
	}
	_, err := sqlOn(d.Server, sql.String())
	return err
}

func databaseAccessHosts(mode string, ips []string) ([]string, error) {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	switch mode {
	case "local":
	case "all":
		hosts = append(hosts, "%")
	case "ips":
		if len(ips) == 0 || len(ips) > 64 {
			return nil, errf("Укажите от 1 до 64 IP-адресов")
		}
		for _, raw := range ips {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
				return nil, errf("Некорректный IP-адрес")
			}
			hosts = append(hosts, ip.String())
		}
	default:
		return nil, errf("Неизвестный режим доступа")
	}
	hosts = dedup(hosts)
	sort.Strings(hosts)
	return hosts, nil
}
func registerDatabaseAccess() {
	handler := func(q *Req) (any, error) {
		var in struct {
			Name, Mode, Password string
			IPs                  []string
			Save                 bool
		}
		if e := strictBind(q, &in); e != nil {
			return nil, e
		}
		dbCreateMu.Lock()
		defer dbCreateMu.Unlock()
		mu.Lock()
		d := findDB(in.Name)
		if d == nil {
			mu.Unlock()
			return nil, errf("База %s не найдена", in.Name)
		}
		db := *d
		for _, other := range store.DBs {
			if other.Name != db.Name && other.User == db.User && other.Server == db.Server {
				mu.Unlock()
				return nil, errf("Пользователь СУБД используется несколькими базами. Сначала назначьте базе отдельный логин.")
			}
		}
		mu.Unlock()
		if !in.Save {
			mode := db.AccessMode
			if mode == "" {
				mode = "local"
				if dbUserHost(db.Server) == "%" {
					mode = "all"
				}
			}
			return map[string]any{"mode": mode, "ips": db.AccessIPs, "local_always": true}, nil
		}
		hosts, e := databaseAccessHosts(in.Mode, in.IPs)
		if e != nil {
			return nil, e
		}
		if len(in.Password) < 12 || len(in.Password) > 72 {
			return nil, errf("Для изменения доступа укажите пароль БД: от 12 до 72 символов")
		}
		if db.Server != "" {
			// Keep this panel's connection to an external/container server working.
			source, err := sqlOn(db.Server, "SELECT SUBSTRING_INDEX(USER(), '@', -1);")
			if err != nil {
				return nil, err
			}
			if cfg.Dry {
				source = "127.0.0.1"
			}
			if net.ParseIP(source) == nil && !reHost.MatchString(source) {
				return nil, errf("Не удалось определить адрес подключения панели к СУБД")
			}
			hosts = dedup(append(hosts, source))
		}
		// Read actual grants as well as metadata, so legacy wildcard access is removed.
		actual, e := sqlOn(db.Server, "SELECT Host FROM mysql.user WHERE User="+sqlStr(db.User)+";")
		if e != nil {
			return nil, e
		}
		previous := strings.Fields(actual)
		if cfg.Dry {
			previous = append(previous, db.AccessHosts...)
			if db.AccessMode == "all" {
				previous = append(previous, "%")
			}
		}
		for _, host := range hosts {
			// Never adopt an unrelated pre-existing remote identity. Only our previously
			// managed identities (and the original local login) can be changed.
			managed := contains(databaseManagedHosts(db), host)
			if contains(previous, host) && !managed {
				return nil, errf("На сервере уже существует отдельный логин БД для этого адреса. Требуется проверка администратора.")
			}
		}
		var sql strings.Builder
		for _, host := range hosts {
			id := sqlStr(db.User) + "@" + sqlStr(host)
			fmt.Fprintf(&sql, "CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s;\nALTER USER %s IDENTIFIED BY %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO %s;\n", id, sqlStr(in.Password), id, sqlStr(in.Password), strings.ReplaceAll(db.Name, "_", `\_`), id)
		}
		for _, host := range previous {
			if !contains(hosts, host) {
				if contains(databaseManagedHosts(db), host) {
					fmt.Fprintf(&sql, "DROP USER IF EXISTS %s@%s;\n", sqlStr(db.User), sqlStr(host))
				} else {
					fmt.Fprintf(&sql, "REVOKE ALL PRIVILEGES ON `%s`.* FROM %s@%s;\n", strings.ReplaceAll(db.Name, "_", `\_`), sqlStr(db.User), sqlStr(host))
				}
			}
		}
		if _, e = sqlOn(db.Server, sql.String()); e != nil {
			return nil, errf("Не удалось применить правила доступа. Проверьте журнал СУБД и повторите сохранение.")
		}
		mu.Lock()
		defer mu.Unlock()
		d.AccessMode = in.Mode
		d.AccessIPs = append([]string{}, in.IPs...)
		d.AccessHosts = hosts
		if e = saveChecked(); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, nil
	}
	apiOp("db.access", "База данных: доступ по IP", handler)
}
