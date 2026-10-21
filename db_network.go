// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"strings"
)

func restartDatabaseNetwork() error {
	if !osInfo.Systemd {
		if err := service("stop", "mariadb"); err != nil {
			return err
		}
	}
	return service("restart", "mariadb")
}

func registerDatabaseNetwork() {
	apiOp("db.network", "MariaDB: сетевые подключения", func(q *Req) (any, error) {
		if !isAdmin(q.S.User) {
			return nil, forbidden()
		}
		var in struct{ Save, Enabled bool }
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		dbCreateMu.Lock()
		defer dbCreateMu.Unlock()
		if !in.Save {
			out, err := mysql("SHOW VARIABLES WHERE Variable_name IN ('bind_address', 'port');")
			if err != nil {
				return nil, err
			}
			values := map[string]string{"bind_address": "127.0.0.1", "port": "3306"}
			for _, line := range strings.Split(out, "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					values[parts[0]] = parts[1]
				}
			}
			return values, nil
		}
		path := "/etc/mysql/mariadb.conf.d/99-itrupanel-network.cnf"
		if osInfo.Family == "rhel" {
			path = "/etc/my.cnf.d/zz-itrupanel-network.cnf"
		}
		old, readErr := os.ReadFile(P(path))
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		address := "127.0.0.1"
		if in.Enabled {
			address = "0.0.0.0"
		}
		if err := writeFile(path, []byte("# ITRUPANEL\n[mysqld]\nbind-address="+address+"\n"), 0644); err != nil {
			return nil, err
		}
		err := restartDatabaseNetwork()
		if err == nil {
			var actual string
			actual, err = mysql("SELECT @@bind_address;")
			if err == nil && !cfg.Dry && strings.TrimSpace(actual) != address {
				err = fmt.Errorf("MariaDB bind_address: %s", actual)
			}
		}
		if err != nil {
			var restoreErr error
			if readErr == nil {
				restoreErr = writeFile(path, old, 0644)
			} else {
				restoreErr = os.Remove(P(path))
			}
			if restoreErr == nil {
				restoreErr = restartDatabaseNetwork()
			}
			return nil, fmt.Errorf("MariaDB: %w; restore: %v", err, restoreErr)
		}
		return map[string]string{"bind_address": address}, nil
	})
}
