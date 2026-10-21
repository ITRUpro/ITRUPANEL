// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRunArgs(t *testing.T) {
	args, err := dockerRunArgs(DockerContainerIn{Name: "web", Image: "nginx:latest", Ports: []string{"127.0.0.1:8080:80", "", "53:53/udp"}, Env: []string{"TZ=Europe/Moscow", "A=b c"}, Volumes: []string{"/srv/web:/usr/share/nginx/html:ro", "data:/data"}, Restart: "always", Network: "app-net", Command: `sh -c "echo 'hi there'"`, Memory: 256, CPUs: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, "|")
	want := "run|-d|--name|web|--restart=always|--publish=127.0.0.1:8080:80|--publish=53:53/udp|--env=TZ=Europe/Moscow|--env=A=b c|--volume=/srv/web:/usr/share/nginx/html:ro|--volume=data:/data|--network=app-net|--memory=256m|--cpus=0.5|nginx:latest|sh|-c|echo 'hi there'"
	if got != want {
		t.Fatalf("аргументы:\n%s\nожидалось:\n%s", got, want)
	}
	for _, bad := range []DockerContainerIn{
		{Image: "-v/:/host"},
		{Image: "nginx", Name: "itru-db-mysql80"},
		{Image: "nginx", Name: "--privileged"},
		{Image: "nginx", Ports: []string{"80:80 --privileged"}},
		{Image: "nginx", Env: []string{"=x"}},
		{Image: "nginx", Volumes: []string{"/srv/../etc:/x"}},
		{Image: "nginx", Volumes: []string{"relative/path:/x"}},
		{Image: "nginx", Restart: "sometimes"},
		{Image: "nginx", Network: "--host"},
		{Image: "nginx", Memory: 3},
		{Image: "nginx", Command: `echo "open`},
	} {
		if _, err := dockerRunArgs(bad); err == nil {
			t.Fatalf("принят неверный ввод: %+v", bad)
		}
	}
}

func TestDockerSettingsKeepOtherKeys(t *testing.T) {
	testPanel(t)
	conf := map[string]any{"data-root": "/mnt/docker", "log-opts": map[string]any{"labels": "x"}, "iptables": false}
	conf, err := applyDockerSettings(conf, DockerSettings{Mirrors: []string{"https://mirror.gcr.io"}, LogRotate: true, LogMaxSize: "20M", LogMaxFile: 5, IPv6: true, IPTables: true, LiveRestore: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(conf)
	for _, want := range []string{`"data-root":"/mnt/docker"`, `"labels":"x"`, `"max-size":"20m"`, `"max-file":"5"`, `"registry-mirrors":["https://mirror.gcr.io"]`, `"fixed-cidr-v6":"fd00:d0c:1::/64"`, `"live-restore":true`, `"log-driver":"json-file"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("нет %s в %s", want, b)
		}
	}
	if strings.Contains(string(b), "iptables") {
		t.Fatalf("iptables по умолчанию не должен записываться: %s", b)
	}
	s := dockerSettingsFrom(conf)
	if !s.LogRotate || s.LogMaxSize != "20m" || s.LogMaxFile != 5 || !s.IPv6 || !s.IPTables || !s.LiveRestore || len(s.Mirrors) != 1 {
		t.Fatalf("настройки прочитаны неверно: %+v", s)
	}
	conf, _ = applyDockerSettings(conf, DockerSettings{IPTables: true})
	b, _ = json.Marshal(conf)
	if strings.Contains(string(b), "max-size") || strings.Contains(string(b), "ipv6") || !strings.Contains(string(b), `"labels":"x"`) {
		t.Fatalf("выключение настроек: %s", b)
	}
	for _, bad := range []DockerSettings{{Mirrors: []string{"mirror.example"}}, {LogRotate: true, LogMaxSize: "big", LogMaxFile: 3}, {IPv6: true, IPv6CIDR: "10.0.0.0/8"}} {
		if _, err := applyDockerSettings(map[string]any{}, bad); err == nil {
			t.Fatalf("принят неверный ввод: %+v", bad)
		}
	}
}

func TestDockerParsers(t *testing.T) {
	rows := dockerJSONLines("warning\n{\"ID\":\"abc\",\"Names\":\"web\"}\n{bad\n")
	if len(rows) != 1 || str(rows[0], "Names") != "web" {
		t.Fatalf("разбор JSON-строк: %v", rows)
	}
	if v := dockerLabel("a=1,com.docker.compose.project=shop,b=2", "com.docker.compose.project"); v != "shop" {
		t.Fatalf("метка проекта: %q", v)
	}
}

func TestDockerAPI(t *testing.T) {
	mux := testPanel(t)
	if _, r := testAPI(t, mux, "docker.container.action", `{"id":"itru-db-mysql80","action":"stop"}`); !strings.Contains(r["error"].(string), "Серверы БД") {
		t.Fatalf("контейнер сервера БД остановлен через раздел Docker: %v", r)
	}
	if _, r := testAPI(t, mux, "docker.compose.save", `{"name":"Bad Name","yaml":"services: {}"}`); r["error"] == nil {
		t.Fatal("принято неверное имя проекта")
	}
	if _, r := testAPI(t, mux, "docker.compose.save", `{"name":"shop","yaml":"services:\n  web:\n    image: nginx\n","env":"A=1\n","new":true}`); r["error"] != nil {
		t.Fatal(r["error"])
	}
	file := filepath.Join(cfg.Root, dockerDefaultProjects, "shop", "docker-compose.yml")
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), "image: nginx") {
		t.Fatalf("файл проекта не записан: %v", err)
	}
	if _, r := testAPI(t, mux, "docker.compose.save", `{"name":"shop","yaml":"services: {}","new":true}`); r["error"] == nil {
		t.Fatal("проект создан повторно")
	}
	if _, r := testAPI(t, mux, "docker.settings.save", `{"iptables":true,"project_dir":"/etc/docker-projects"}`); r["error"] == nil {
		t.Fatal("директория проектов внутри /etc принята")
	}
	if _, r := testAPI(t, mux, "docker.settings.save", `{"iptables":true,"live_restore":true,"project_dir":"/srv/compose"}`); r["error"] != nil {
		t.Fatal(r["error"])
	}
	if dockerProjectDir() != "/srv/compose" || !strings.Contains(readFile(dockerDaemonJSON), `"live-restore": true`) {
		t.Fatalf("настройки не сохранены: %s %s", dockerProjectDir(), readFile(dockerDaemonJSON))
	}
	if _, r := testAPI(t, mux, "docker.config.save", `{"text":"{bad"}`); r["error"] == nil {
		t.Fatal("принят неверный JSON")
	}
	store.DBServers = []*DBServer{{Name: "mysql80"}}
	if _, r := testAPI(t, mux, "docker.uninstall", `{}`); r["error"] == nil {
		t.Fatal("Docker удаляется при работающих серверах БД")
	}
}

func TestDockerLegacyComposeTemplates(t *testing.T) {
	for v, want := range map[string]bool{"1.25.0": true, "1.26.2": true, "1.29.2": false, "2.40.3+ds1-0ubuntu1": false, "v2.27.0": false, "": false, "5.6.0": false} {
		if dockerLegacyCompose(v) != want {
			t.Fatalf("dockerLegacyCompose(%q) != %v", v, want)
		}
	}
	if !strings.HasPrefix(dockerTemplatesFor("1.25.0")["redis"], "version: \"3.7\"\n") || strings.Contains(dockerTemplatesFor("2.40.3")["redis"], "version:") {
		t.Fatal("шаблоны для старого и нового Compose")
	}
}
