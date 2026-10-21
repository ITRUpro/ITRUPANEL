// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

// Примерные данные раздела «Docker» для демо-режима (команды там не выполняются).

func dockerDemoContainers() []map[string]any {
	c := func(id, name, image, state, status, ports, project, cpu, mem string) map[string]any {
		return map[string]any{"id": id, "name": name, "image": image, "command": "", "state": state, "status": status, "ports": ports, "created": "2026-10-12 09:30:00 +0300 MSK",
			"networks": "bridge", "mounts": "", "project": project, "panel": dockerPanelContainer(name), "cpu": cpu, "mem": mem}
	}
	rows := []map[string]any{
		c("4f1c2a9b7d10", "itru-db-mysql84", "mysql:8.4", "running", "Up 2 days", "127.0.0.1:3307->3306/tcp", "", "0.41%", "131.2MiB / 1.94GiB"),
		c("9a7e5c3d2b11", "wp-db-1", "mariadb:11.4", "running", "Up 2 days", "3306/tcp", "wp", "0.12%", "96.4MiB / 1.94GiB"),
		c("b2d4f6a8c012", "wp-wordpress-1", "wordpress:php8.3-apache", "running", "Up 2 days", "127.0.0.1:8081->80/tcp", "wp", "0.03%", "74.8MiB / 1.94GiB"),
		c("c3e5a7b9d113", "uptime-kuma", "louislam/uptime-kuma:1", "running", "Up 5 hours", "127.0.0.1:3001->3001/tcp", "", "0.65%", "88.1MiB / 1.94GiB"),
		c("d4f6b8c0e214", "redis-cache", "redis:7-alpine", "exited", "Exited (0) 3 hours ago", "", "", "", ""),
	}
	rows[1]["mounts"], rows[2]["mounts"], rows[3]["mounts"] = "wp_db-data", "wp_wp-data", "kuma-data"
	return rows
}

func dockerDemoImages() []map[string]any {
	i := func(id, repo, tag, size, since string) map[string]any {
		return map[string]any{"id": id, "repository": repo, "tag": tag, "size": size, "created": "", "since": since}
	}
	return []map[string]any{
		i("5c2f8a1e9b30", "louislam/uptime-kuma", "1", "440MB", "3 weeks ago"),
		i("6d3a9b2f0c41", "mariadb", "11.4", "407MB", "2 weeks ago"),
		i("7e4b0c3a1d52", "mysql", "8.4", "603MB", "4 weeks ago"),
		i("8f5c1d4b2e63", "redis", "7-alpine", "41.4MB", "5 weeks ago"),
		i("9a6d2e5c3f74", "wordpress", "php8.3-apache", "701MB", "2 weeks ago"),
	}
}

func dockerDemoNetworks() []map[string]any {
	n := func(id, name, driver, subnet, gw string, count int) map[string]any {
		return map[string]any{"id": id, "name": name, "driver": driver, "scope": "local", "subnet": subnet, "gateway": gw, "containers": count, "internal": false, "builtin": name == "bridge" || name == "host" || name == "none"}
	}
	return []map[string]any{
		n("1b2c3d4e5f60", "bridge", "bridge", "172.17.0.0/16", "172.17.0.1", 2),
		n("2c3d4e5f6a71", "host", "host", "", "", 0),
		n("3d4e5f6a7b82", "none", "null", "", "", 0),
		n("4e5f6a7b8c93", "wp_default", "bridge", "172.18.0.0/16", "172.18.0.1", 2),
	}
}

func dockerDemoVolumes() []map[string]any {
	v := func(name string, by ...string) map[string]any {
		return map[string]any{"name": name, "driver": "local", "mountpoint": "/var/lib/docker/volumes/" + name + "/_data", "created": "2026-10-12T09:30:00+03:00", "used_by": by}
	}
	return []map[string]any{v("kuma-data", "uptime-kuma"), v("redis-data"), v("wp_db-data", "wp-db-1"), v("wp_wp-data", "wp-wordpress-1")}
}

func dockerDemoStatus(res map[string]any) {
	res["db_servers"] = 1
	res["version"], res["client"], res["compose"] = "27.3.1", "Docker version 27.3.1", "2.29.7"
	res["containers"], res["containers_running"], res["images"], res["storage_driver"], res["root_dir"] = "5", "4", "5", "overlay2", "/var/lib/docker"
	res["df"] = []map[string]any{
		{"Type": "Images", "TotalCount": "5", "Active": "5", "Size": "2.19GB", "Reclaimable": "0B (0%)"},
		{"Type": "Containers", "TotalCount": "5", "Active": "4", "Size": "12.6MB", "Reclaimable": "1.1kB (0%)"},
		{"Type": "Local Volumes", "TotalCount": "4", "Active": "3", "Size": "318.4MB", "Reclaimable": "2.3MB (0%)"},
		{"Type": "Build Cache", "TotalCount": "0", "Active": "0", "Size": "0B", "Reclaimable": "0B"},
	}
}
