// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// Раздел «Docker» (только администратор): служба Docker, контейнеры, образы, сети, тома,
// проекты Compose и настройки /etc/docker/daemon.json. Контейнеры серверов БД панели (itru-db-*)
// здесь видны, но остановить или удалить их можно только в разделе «Серверы БД».

const dockerDaemonJSON = "/etc/docker/daemon.json"
const dockerDefaultProjects = "/var/lib/itrupanel-docker/projects"

var (
	reDockerName    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	reDockerProject = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	reDockerImage   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
	reDockerPort    = regexp.MustCompile(`^((\d{1,3}\.){3}\d{1,3}:|\[[0-9a-fA-F:]+\]:)?(\d{1,5}(-\d{1,5})?:)?\d{1,5}(-\d{1,5})?(/(tcp|udp|sctp))?$`)
	reDockerEnv     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*=`)
	reDockerSize    = regexp.MustCompile(`^[1-9][0-9]{0,5}[kmg]$`)
)

func dockerPanelContainer(name string) bool { return strings.HasPrefix(name, "itru-db-") }

func dockerProjectDir() string {
	mu.Lock()
	defer mu.Unlock()
	if store.Settings.DockerProjectDir != "" {
		return store.Settings.DockerProjectDir
	}
	return dockerDefaultProjects
}

// dockerCompose — команда Compose: плагин «docker compose» или отдельный docker-compose.
func dockerCompose() []string {
	if cfg.Dry {
		return []string{"docker", "compose"}
	}
	if _, err := run("docker", "compose", "version"); err == nil {
		return []string{"docker", "compose"}
	}
	if p, err := exec.LookPath("docker-compose"); err == nil {
		return []string{p}
	}
	return nil
}

func dockerComposeVersion() string {
	c := dockerCompose()
	if c == nil {
		return ""
	}
	out, err := run(c[0], append(c[1:], "version", "--short")...)
	if err != nil || out == "" {
		out, _ = run(c[0], append(c[1:], "version")...)
	}
	return firstLine(out)
}

// dockerJSONLines разбирает вывод docker … --format '{{json .}}' (одна строка — один объект).
func dockerJSONLines(out string) []map[string]any {
	rows := []map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			rows = append(rows, m)
		}
	}
	return rows
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// dockerLabel достаёт значение метки из строки «k=v,k2=v2» вывода docker ps.
func dockerLabel(labels, key string) string {
	for _, p := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(p, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func dockerContainers() ([]map[string]any, error) {
	if cfg.Dry {
		return dockerDemoContainers(), nil
	}
	out, err := run("docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, errf("Docker не отвечает: %s", firstLine(out))
	}
	stats := map[string]map[string]any{}
	if strings.Contains(out, `"State":"running"`) {
		if s, err := runTimeout(20*time.Second, "", "docker", "stats", "--no-stream", "--format", "{{json .}}"); err == nil {
			for _, r := range dockerJSONLines(s) {
				stats[str(r, "Name")] = r
			}
		}
	}
	rows := []map[string]any{}
	for _, c := range dockerJSONLines(out) {
		name := strings.TrimPrefix(strings.Split(str(c, "Names"), ",")[0], "/")
		id := str(c, "ID")
		if len(id) > 12 {
			id = id[:12]
		}
		row := map[string]any{"id": id, "name": name, "image": str(c, "Image"), "command": str(c, "Command"), "state": str(c, "State"), "status": str(c, "Status"),
			"ports": str(c, "Ports"), "created": str(c, "CreatedAt"), "networks": str(c, "Networks"), "mounts": str(c, "Mounts"),
			"project": dockerLabel(str(c, "Labels"), "com.docker.compose.project"), "panel": dockerPanelContainer(name)}
		if s := stats[name]; s != nil {
			row["cpu"], row["mem"], row["mem_perc"] = str(s, "CPUPerc"), str(s, "MemUsage"), str(s, "MemPerc")
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i]["name"].(string) < rows[k]["name"].(string) })
	return rows, nil
}

func dockerImages() []map[string]any {
	if cfg.Dry {
		return dockerDemoImages()
	}
	out, _ := run("docker", "images", "--format", "{{json .}}")
	rows := []map[string]any{}
	for _, m := range dockerJSONLines(out) {
		rows = append(rows, map[string]any{"id": strings.TrimPrefix(str(m, "ID"), "sha256:"), "repository": str(m, "Repository"), "tag": str(m, "Tag"),
			"size": str(m, "Size"), "created": str(m, "CreatedAt"), "since": str(m, "CreatedSince")})
	}
	return rows
}

func dockerNetworks() []map[string]any {
	if cfg.Dry {
		return dockerDemoNetworks()
	}
	out, _ := run("docker", "network", "ls", "-q")
	ids := strings.Fields(out)
	rows := []map[string]any{}
	if len(ids) == 0 {
		return rows
	}
	info, _ := run("docker", append([]string{"network", "inspect", "--format", "{{.ID}}\t{{.Name}}\t{{.Driver}}\t{{.Scope}}\t{{range .IPAM.Config}}{{.Subnet}} {{end}}\t{{range .IPAM.Config}}{{.Gateway}} {{end}}\t{{len .Containers}}\t{{.Internal}}"}, ids...)...)
	for _, line := range strings.Split(info, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		n, _ := strconv.Atoi(f[6])
		id := f[0]
		if len(id) > 12 {
			id = id[:12]
		}
		builtin := f[1] == "bridge" || f[1] == "host" || f[1] == "none"
		rows = append(rows, map[string]any{"id": id, "name": f[1], "driver": f[2], "scope": f[3], "subnet": strings.TrimSpace(f[4]), "gateway": strings.TrimSpace(f[5]), "containers": n, "internal": f[7] == "true", "builtin": builtin})
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i]["name"].(string) < rows[k]["name"].(string) })
	return rows
}

func dockerVolumes(containers []map[string]any) []map[string]any {
	if cfg.Dry {
		return dockerDemoVolumes()
	}
	out, _ := run("docker", "volume", "ls", "-q")
	names := strings.Fields(out)
	rows := []map[string]any{}
	if len(names) == 0 {
		return rows
	}
	used := map[string][]string{}
	for _, c := range containers {
		for _, m := range strings.Split(c["mounts"].(string), ",") {
			if m = strings.TrimSpace(m); m != "" {
				used[m] = append(used[m], c["name"].(string))
			}
		}
	}
	info, _ := run("docker", append([]string{"volume", "inspect", "--format", "{{.Name}}\t{{.Driver}}\t{{.Mountpoint}}\t{{.CreatedAt}}"}, names...)...)
	for _, line := range strings.Split(info, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		// docker ps сокращает длинные имена томов в Mounts — сверяем и по началу имени
		var by []string
		for m, cs := range used {
			if m == f[0] || (strings.HasSuffix(m, "…") && strings.HasPrefix(f[0], strings.TrimSuffix(m, "…"))) {
				by = append(by, cs...)
			}
		}
		rows = append(rows, map[string]any{"name": f[0], "driver": f[1], "mountpoint": f[2], "created": f[3], "used_by": by})
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i]["name"].(string) < rows[k]["name"].(string) })
	return rows
}

// ---------- daemon.json ----------

type DockerSettings struct {
	Mirrors     []string `json:"mirrors"`
	Insecure    []string `json:"insecure"`
	LogRotate   bool     `json:"log_rotate"`
	LogMaxSize  string   `json:"log_max_size"`
	LogMaxFile  int      `json:"log_max_file"`
	IPv6        bool     `json:"ipv6"`
	IPv6CIDR    string   `json:"ipv6_cidr"`
	IPTables    bool     `json:"iptables"`
	LiveRestore bool     `json:"live_restore"`
	ProjectDir  string   `json:"project_dir"`
}

func dockerReadDaemon() (map[string]any, error) {
	raw := strings.TrimSpace(readFile(dockerDaemonJSON))
	conf := map[string]any{}
	if raw == "" {
		return conf, nil
	}
	if err := json.Unmarshal([]byte(raw), &conf); err != nil {
		return nil, errf("Файл %s содержит ошибку JSON: %s. Исправьте его в «Конфигурационный файл»", dockerDaemonJSON, err.Error())
	}
	return conf, nil
}

func dockerStrings(v any) []string {
	out := []string{}
	if a, ok := v.([]string); ok {
		return append(out, a...)
	}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func dockerSettingsFrom(conf map[string]any) DockerSettings {
	s := DockerSettings{Mirrors: dockerStrings(conf["registry-mirrors"]), Insecure: dockerStrings(conf["insecure-registries"]), IPTables: true, LogMaxSize: "10m", LogMaxFile: 3, ProjectDir: dockerProjectDir()}
	if opts, ok := conf["log-opts"].(map[string]any); ok && (conf["log-driver"] == nil || conf["log-driver"] == "json-file" || conf["log-driver"] == "local") {
		if v, ok := opts["max-size"].(string); ok && v != "" {
			s.LogRotate, s.LogMaxSize = true, v
		}
		if v, ok := opts["max-file"].(string); ok {
			if n, e := strconv.Atoi(v); e == nil {
				s.LogMaxFile = n
			}
		}
	}
	s.IPv6, _ = conf["ipv6"].(bool)
	s.IPv6CIDR, _ = conf["fixed-cidr-v6"].(string)
	if v, ok := conf["iptables"].(bool); ok {
		s.IPTables = v
	}
	s.LiveRestore, _ = conf["live-restore"].(bool)
	return s
}

func dockerRegistry(r string) (string, error) {
	r = strings.TrimSpace(r)
	if r == "" || len(r) > 255 || strings.ContainsAny(r, " \t\"'\\") {
		return "", errf("Неверный адрес реестра: %s", r)
	}
	return r, nil
}

// applyDockerSettings меняет только ключи раздела, остальное в daemon.json сохраняется.
func applyDockerSettings(conf map[string]any, s DockerSettings) (map[string]any, error) {
	list := func(key string, in []string, mirror bool) error {
		out := []string{}
		for _, r := range in {
			if strings.TrimSpace(r) == "" {
				continue
			}
			v, err := dockerRegistry(r)
			if err != nil {
				return err
			}
			if mirror && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
				return errf("Адрес ускорения должен начинаться с https:// или http://: %s", v)
			}
			out = append(out, v)
		}
		if len(out) == 0 {
			delete(conf, key)
		} else {
			conf[key] = out
		}
		return nil
	}
	if err := list("registry-mirrors", s.Mirrors, true); err != nil {
		return nil, err
	}
	if err := list("insecure-registries", s.Insecure, false); err != nil {
		return nil, err
	}
	opts, _ := conf["log-opts"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	if s.LogRotate {
		size := strings.ToLower(strings.TrimSpace(s.LogMaxSize))
		if !reDockerSize.MatchString(size) {
			return nil, errf("Размер файла журнала: число с единицей k, m или g, например 10m")
		}
		if s.LogMaxFile < 1 || s.LogMaxFile > 100 {
			return nil, errf("Количество файлов журнала: от 1 до 100")
		}
		if d, _ := conf["log-driver"].(string); d == "" {
			conf["log-driver"] = "json-file"
		}
		opts["max-size"], opts["max-file"] = size, strconv.Itoa(s.LogMaxFile)
		conf["log-opts"] = opts
	} else {
		delete(opts, "max-size")
		delete(opts, "max-file")
		if len(opts) == 0 {
			delete(conf, "log-opts")
		}
	}
	if s.IPv6 {
		cidr := strings.TrimSpace(s.IPv6CIDR)
		if cidr == "" {
			cidr = "fd00:d0c:1::/64"
		}
		if p, err := fwPrefix(cidr); err != nil || !p.Addr().Is6() || p.Bits() > 80 {
			return nil, errf("Подсеть IPv6 для Docker: сеть вида fd00:d0c:1::/64 (не меньше /80)")
		}
		conf["ipv6"], conf["fixed-cidr-v6"] = true, cidr
	} else {
		delete(conf, "ipv6")
		delete(conf, "fixed-cidr-v6")
	}
	if s.IPTables {
		delete(conf, "iptables")
	} else {
		conf["iptables"] = false
	}
	if s.LiveRestore {
		conf["live-restore"] = true
	} else {
		delete(conf, "live-restore")
	}
	return conf, nil
}

// dockerWriteDaemon записывает конфигурацию и перезапускает Docker; если служба не поднялась,
// возвращает прежний файл, чтобы контейнеры (в том числе серверы БД) не остались остановленными.
func dockerWriteDaemon(data []byte) error {
	old, had := readFile(dockerDaemonJSON), fileExists(dockerDaemonJSON)
	if err := writeFile(dockerDaemonJSON, data, 0644); err != nil {
		return err
	}
	if !have("docker") || cfg.Dry {
		return nil
	}
	if err := dockerRestart(); err != nil {
		if had {
			writeFile(dockerDaemonJSON, []byte(old), 0644)
		} else {
			os.Remove(P(dockerDaemonJSON))
		}
		dockerRestart()
		return errf("Docker не запустился с новыми настройками, прежний файл восстановлен: %s", firstLine(err.Error()))
	}
	return nil
}

func dockerRestart() error {
	if osInfo.Systemd {
		run("systemctl", "reset-failed", "docker")
	}
	if err := service("restart", "docker"); err != nil {
		out, _ := run("journalctl", "-u", "docker", "-n", "5", "--no-pager", "-o", "cat")
		return fmt.Errorf("%v %s", err, out)
	}
	for i := 0; i < 30; i++ {
		if _, err := run("docker", "info", "--format", "{{.ServerVersion}}"); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errf("служба не отвечает после перезапуска")
}

// ---------- создание контейнера ----------

type DockerContainerIn struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Ports   []string `json:"ports"`
	Env     []string `json:"env"`
	Volumes []string `json:"volumes"`
	Restart string   `json:"restart"`
	Network string   `json:"network"`
	Command string   `json:"command"`
	Memory  int      `json:"memory"` // МБ, 0 — без ограничения
	CPUs    float64  `json:"cpus"`   // 0 — без ограничения
}

// splitCommand делит строку команды на аргументы с учётом кавычек (без подстановок shell).
func splitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			in = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if quote != 0 {
		return nil, errf("Незакрытая кавычка в команде")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

func dockerRunArgs(in DockerContainerIn) ([]string, error) {
	in.Image = strings.TrimSpace(in.Image)
	if !reDockerImage.MatchString(in.Image) {
		return nil, errf("Укажите образ, например nginx:latest")
	}
	args := []string{"run", "-d"}
	if in.Name = strings.TrimSpace(in.Name); in.Name != "" {
		if !reDockerName.MatchString(in.Name) {
			return nil, errf("Имя контейнера: латинские буквы, цифры, «_», «.» и «-», до 63 символов")
		}
		if dockerPanelContainer(in.Name) {
			return nil, errf("Имена itru-db-* зарезервированы для серверов БД панели")
		}
		args = append(args, "--name", in.Name)
	}
	switch in.Restart {
	case "", "unless-stopped":
		args = append(args, "--restart=unless-stopped")
	case "no", "always", "on-failure":
		args = append(args, "--restart="+in.Restart)
	default:
		return nil, errf("Неизвестная политика перезапуска")
	}
	for _, p := range in.Ports {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !reDockerPort.MatchString(p) {
			return nil, errf("Порт «%s»: формат 8080:80, 127.0.0.1:8080:80 или 53:53/udp", p)
		}
		args = append(args, "--publish="+p)
	}
	for _, e := range in.Env {
		if e = strings.TrimRight(e, "\r"); strings.TrimSpace(e) == "" {
			continue
		}
		if !reDockerEnv.MatchString(e) || strings.ContainsAny(e, "\n\x00") {
			return nil, errf("Переменная окружения «%s»: формат ИМЯ=значение", e)
		}
		args = append(args, "--env="+e)
	}
	for _, v := range in.Volumes {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		parts := strings.Split(v, ":")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || !strings.HasPrefix(parts[1], "/") || (len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw") {
			return nil, errf("Том «%s»: формат /путь/на/сервере:/путь/в/контейнере[:ro] или имя_тома:/путь", v)
		}
		if strings.HasPrefix(parts[0], "/") {
			if filepath.Clean(parts[0]) != parts[0] {
				return nil, errf("Путь на сервере «%s» должен быть без «..» и лишних «/»", parts[0])
			}
		} else if !reDockerName.MatchString(parts[0]) {
			return nil, errf("Имя тома «%s»: латинские буквы, цифры, «_», «.» и «-»", parts[0])
		}
		args = append(args, "--volume="+v)
	}
	if in.Network = strings.TrimSpace(in.Network); in.Network != "" && in.Network != "bridge" {
		if !reDockerName.MatchString(in.Network) {
			return nil, errf("Неверное имя сети")
		}
		args = append(args, "--network="+in.Network)
	}
	if in.Memory < 0 || in.Memory > 1<<20 {
		return nil, errf("Лимит памяти: от 0 до 1048576 МБ")
	} else if in.Memory > 0 {
		if in.Memory < 6 {
			return nil, errf("Лимит памяти: не меньше 6 МБ")
		}
		args = append(args, fmt.Sprintf("--memory=%dm", in.Memory))
	}
	if in.CPUs < 0 || in.CPUs > 1024 {
		return nil, errf("Лимит процессора: от 0 до 1024 ядер")
	} else if in.CPUs > 0 {
		args = append(args, "--cpus="+strconv.FormatFloat(in.CPUs, 'f', -1, 64))
	}
	args = append(args, in.Image)
	if strings.TrimSpace(in.Command) != "" {
		cmd, err := splitCommand(in.Command)
		if err != nil {
			return nil, err
		}
		args = append(args, cmd...)
	}
	return args, nil
}

// ---------- Compose ----------

func dockerProjectPath(name string) (string, error) {
	if !reDockerProject.MatchString(name) {
		return "", errf("Имя проекта: строчные латинские буквы, цифры, «_» и «-», до 63 символов")
	}
	return filepath.Join(dockerProjectDir(), name), nil
}

func dockerComposeFile(dir string) string {
	for _, n := range []string{"docker-compose.yml", "compose.yml", "docker-compose.yaml", "compose.yaml"} {
		if fileExists(filepath.Join(dir, n)) {
			return filepath.Join(dir, n)
		}
	}
	return filepath.Join(dir, "docker-compose.yml")
}

func dockerComposeArgs(name string, rest ...string) ([]string, error) {
	c := dockerCompose()
	if c == nil {
		return nil, errf("Docker Compose не установлен. Нажмите «Установить» в разделе Docker")
	}
	dir, err := dockerProjectPath(name)
	if err != nil {
		return nil, err
	}
	return append(append(append([]string{}, c...), "-p", name, "--project-directory", dir, "-f", dockerComposeFile(dir)), rest...), nil
}

func dockerProjects(containers []map[string]any) []map[string]any {
	root := dockerProjectDir()
	rows := []map[string]any{}
	entries, _ := os.ReadDir(P(root))
	for _, e := range entries {
		if !e.IsDir() || !reDockerProject.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		total, running := 0, 0
		for _, c := range containers {
			if c["project"] == e.Name() {
				total++
				if c["state"] == "running" {
					running++
				}
			}
		}
		st, _ := os.Stat(P(dockerComposeFile(dir)))
		var mod time.Time
		if st != nil {
			mod = st.ModTime()
		}
		rows = append(rows, map[string]any{"name": e.Name(), "path": dir, "file": dockerComposeFile(dir), "containers": total, "running": running, "modified": mod})
	}
	return rows
}

var dockerTemplates = map[string]string{
	"nginx":       "services:\n  web:\n    image: nginx:stable-alpine\n    restart: unless-stopped\n    ports:\n      - \"127.0.0.1:8080:80\"\n    volumes:\n      - ./html:/usr/share/nginx/html:ro\n",
	"redis":       "services:\n  redis:\n    image: redis:7-alpine\n    restart: unless-stopped\n    command: [\"redis-server\", \"--appendonly\", \"yes\", \"--maxmemory\", \"128mb\", \"--maxmemory-policy\", \"allkeys-lru\"]\n    ports:\n      - \"127.0.0.1:6379:6379\"\n    volumes:\n      - redis-data:/data\nvolumes:\n  redis-data:\n",
	"wordpress":   "services:\n  db:\n    image: mariadb:11.4\n    restart: unless-stopped\n    environment:\n      MARIADB_DATABASE: wordpress\n      MARIADB_USER: wordpress\n      MARIADB_PASSWORD: ${DB_PASSWORD}\n      MARIADB_RANDOM_ROOT_PASSWORD: \"1\"\n    volumes:\n      - db-data:/var/lib/mysql\n  wordpress:\n    image: wordpress:php8.3-apache\n    restart: unless-stopped\n    depends_on:\n      - db\n    ports:\n      - \"127.0.0.1:8081:80\"\n    environment:\n      WORDPRESS_DB_HOST: db\n      WORDPRESS_DB_USER: wordpress\n      WORDPRESS_DB_PASSWORD: ${DB_PASSWORD}\n      WORDPRESS_DB_NAME: wordpress\n    volumes:\n      - wp-data:/var/www/html\nvolumes:\n  db-data:\n  wp-data:\n",
	"uptime-kuma": "services:\n  uptime-kuma:\n    image: louislam/uptime-kuma:1\n    restart: unless-stopped\n    ports:\n      - \"127.0.0.1:3001:3001\"\n    volumes:\n      - kuma-data:/app/data\nvolumes:\n  kuma-data:\n",
	"portainer":   "services:\n  portainer:\n    image: portainer/portainer-ce:lts\n    restart: unless-stopped\n    ports:\n      - \"127.0.0.1:9443:9443\"\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n      - portainer-data:/data\nvolumes:\n  portainer-data:\n",
}

// dockerLegacyCompose — docker-compose до 1.27 (Debian 11) не понимает файлы без поля version.
func dockerLegacyCompose(ver string) bool {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	var major, minor int
	if n, _ := fmt.Sscanf(ver, "%d.%d", &major, &minor); n < 2 {
		return false
	}
	return major == 1 && minor < 27
}

func dockerTemplatesFor(composeVer string) map[string]string {
	if !dockerLegacyCompose(composeVer) {
		return dockerTemplates
	}
	out := map[string]string{}
	for k, v := range dockerTemplates {
		out[k] = "version: \"3.7\"\n" + v
	}
	return out
}

// ---------- установка и удаление ----------

func dockerInstall(j *Job) error {
	jobStage(j, "Установка Docker")
	if err := ensureDocker(); err != nil {
		return err
	}
	if dockerCompose() == nil {
		jobStage(j, "Установка Docker Compose")
		var tries []string
		if osInfo.Family == "rhel" {
			tries = []string{"docker-compose-plugin"}
		} else {
			tries = []string{"docker-compose-plugin", "docker-compose-v2", "docker-compose"}
		}
		for _, p := range tries {
			if pkgInstall(p) == nil && dockerCompose() != nil {
				break
			}
		}
		if dockerCompose() == nil {
			j.Write([]byte("Docker Compose не найден в репозиториях системы. Контейнеры работают; проекты Compose будут недоступны.\n"))
		}
	}
	return nil
}

func dockerInstalledPackages() []string {
	// docker-cli и docker-buildx — отдельные пакеты в Debian 13 и новее.
	cands := []string{"docker.io", "docker-cli", "docker-buildx", "docker-ce", "docker-ce-cli", "docker-ce-rootless-extras", "docker-buildx-plugin", "docker-compose-plugin", "docker-compose-v2", "docker-compose", "containerd.io", "containerd", "runc"}
	var out []string
	for _, p := range cands {
		var err error
		if osInfo.Family == "rhel" {
			_, err = run("rpm", "-q", p)
		} else {
			var s string
			s, err = run("dpkg-query", "-W", "-f", "${Status}", p)
			if err == nil && !strings.Contains(s, "install ok installed") {
				err = fmt.Errorf("not installed")
			}
		}
		if err == nil {
			out = append(out, p)
		}
	}
	return out
}

func dockerUninstall(j *Job, purgeData bool) error {
	jobStage(j, "Остановка Docker")
	if osInfo.Systemd {
		run("systemctl", "disable", "--now", "docker.socket", "docker", "containerd")
	}
	pkgs := dockerInstalledPackages()
	if len(pkgs) > 0 {
		jobStage(j, "Удаление пакетов")
		var err error
		if osInfo.Family == "rhel" {
			err = liveCommand(j, 20*time.Minute, "dnf", append([]string{"remove", "-y"}, pkgs...)...)
		} else {
			err = liveCommand(j, 20*time.Minute, "apt-get", append([]string{"purge", "-y"}, pkgs...)...)
		}
		if err != nil {
			return err
		}
	}
	if purgeData {
		jobStage(j, "Удаление данных Docker")
		freed := dockerDataSize()
		// Слои контейнеров могут остаться смонтированными: сначала они отключаются.
		for _, m := range dockerDataMounts() {
			run("umount", "-l", m)
		}
		for _, d := range append(append([]string{}, dockerDataDirs...), "/etc/docker", "/etc/containerd") {
			os.RemoveAll(P(d))
		}
		j.Write([]byte("\nОсвобождено: " + fmtGB(freed) + "\n"))
	} else if left := dockerDataSize(); left > 0 {
		j.Write([]byte("\nДанные Docker сохранены (" + fmtGB(left) + "). Удалить их можно в разделе «Docker».\n"))
	}
	return nil
}

// Папки с образами, контейнерами и томами: в новых версиях Docker образы хранит containerd.
var dockerDataDirs = []string{"/var/lib/docker", "/var/lib/containerd"}

// dockerDataSize — сколько места занимают данные Docker (в том числе после удаления пакетов).
func dockerDataSize() int64 {
	if cfg.Dry {
		return 0
	}
	var total int64
	for _, d := range dockerDataDirs {
		if !fileExists(d) {
			continue
		}
		out, err := runTimeout(2*time.Minute, "", "du", "-sxb", d)
		if f := strings.Fields(out); err == nil && len(f) > 0 {
			var n int64
			fmt.Sscan(f[0], &n)
			total += n
		}
	}
	return total
}

func dockerDataMounts() []string {
	var out []string
	data, _ := os.ReadFile("/proc/mounts")
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, d := range append(append([]string{}, dockerDataDirs...), "/run/docker", "/run/containerd") {
			if f[1] == d || strings.HasPrefix(f[1], d+"/") {
				out = append(out, f[1])
			}
		}
	}
	// Вложенные точки монтирования отключаются первыми.
	sort.Slice(out, func(i, k int) bool { return len(out[i]) > len(out[k]) })
	return out
}

func fmtGB(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d МБ", n>>20)
	}
	return fmt.Sprintf("%d КБ", n>>10)
}

// dockerRemoveContainerFully удаляет контейнер, его анонимные тома, а также именованные тома
// и образ, если их не использует другой контейнер. Папки сервера, подключённые в контейнер,
// не трогаются.
func dockerRemoveContainerFully(id string) (any, error) {
	info, err := run("docker", "inspect", "-f", `{{.Image}}|{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}} {{end}}{{end}}`, id)
	if err != nil {
		return nil, errf("Docker: %s", dockerErr(info, err))
	}
	parts := strings.SplitN(strings.TrimSpace(info), "|", 2)
	image, volumes := parts[0], []string{}
	if len(parts) == 2 {
		volumes = strings.Fields(parts[1])
	}
	if out, err := runTimeout(2*time.Minute, "", "docker", "rm", "-f", "-v", id); err != nil {
		return nil, errf("Docker: %s", dockerErr(out, err))
	}
	var removed []string
	for _, v := range volumes {
		if users, _ := run("docker", "ps", "-aq", "--filter", "volume="+v); strings.TrimSpace(users) != "" {
			continue
		}
		if _, err := run("docker", "volume", "rm", v); err == nil {
			removed = append(removed, "том "+v)
		}
	}
	if image != "" {
		if users, _ := run("docker", "ps", "-aq", "--filter", "ancestor="+image); strings.TrimSpace(users) == "" {
			if _, err := run("docker", "image", "rm", image); err == nil {
				removed = append(removed, "образ")
			}
		}
	}
	info = "Контейнер удалён"
	if len(removed) > 0 {
		info += " вместе с: " + strings.Join(removed, ", ")
	}
	return map[string]string{"info": info}, nil
}

// ---------- терминал контейнера ----------

func handleDockerShell(w http.ResponseWriter, r *http.Request) {
	s := getSession(r)
	id := r.URL.Query().Get("id")
	if !reDockerName.MatchString(id) {
		http.Error(w, "bad container", 400)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(64 << 10)
	appendJSONL("ops.jsonl", OpEntry{Time: time.Now(), User: s.User, IP: s.IP, Action: "Docker: терминал контейнера", Object: id, Status: "ok"})
	if cfg.Dry {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\nДемо-режим: терминал доступен только на сервере.\r\n"))
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if !sessionStillActive(r, s) {
					ws.Close()
					return
				}
			}
		}
	}()
	cmd := exec.Command("docker", "exec", "-it", "-e", "TERM=xterm-256color", id, "sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=C.UTF-8")
	f, err := pty.Start(cmd)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Ошибка: "+err.Error()))
		return
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		f.Close()
	}()
	ptyBridge(ws, f)
}

// ---------- API ----------

func dockerReady() error {
	if !have("docker") {
		return errf("Docker не установлен. Нажмите «Установить» в разделе Docker")
	}
	return nil
}

func dockerBindID(q *Req) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := q.Bind(&in); err != nil {
		return "", err
	}
	if !reDockerName.MatchString(in.ID) {
		return "", errf("Неверный идентификатор")
	}
	return in.ID, dockerReady()
}

// dockerContainerName возвращает имя контейнера по ID или имени (для защиты контейнеров панели).
func dockerContainerName(id string) string {
	out, err := run("docker", "inspect", "-f", "{{.Name}}", id)
	if err != nil || strings.TrimSpace(out) == "" {
		return id
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "/")
}

func registerDocker() {
	api("docker.status", func(q *Req) (any, error) {
		res := map[string]any{"installed": have("docker"), "project_dir": dockerProjectDir(), "templates": dockerTemplates}
		defer func() { res["templates"] = dockerTemplatesFor(fmt.Sprint(res["compose"])) }()
		mu.Lock()
		res["db_servers"] = len(store.DBServers)
		mu.Unlock()
		conf, err := dockerReadDaemon()
		if err != nil {
			res["config_error"] = err.Error()
			conf = map[string]any{}
		}
		res["settings"] = dockerSettingsFrom(conf)
		if !have("docker") {
			if left := dockerDataSize(); left > 0 {
				res["leftover"], res["leftover_text"] = left, fmtGB(left)
			}
			return res, nil
		}
		if cfg.Dry {
			res["running"] = true
			dockerDemoStatus(res)
			return res, nil
		}
		res["running"] = serviceActive("docker")
		res["client"] = firstLine(func() string { s, _ := run("docker", "--version"); return s }())
		if v, err := run("docker", "version", "--format", "{{.Server.Version}}"); err == nil {
			res["version"] = v
		} else {
			res["running"] = false
		}
		res["compose"] = dockerComposeVersion()
		if res["running"] == true {
			df, _ := run("docker", "system", "df", "--format", "{{json .}}")
			res["df"] = dockerJSONLines(df)
			if info, err := run("docker", "info", "--format", "{{.Containers}}\t{{.ContainersRunning}}\t{{.Images}}\t{{.Driver}}\t{{.DockerRootDir}}"); err == nil {
				f := strings.Split(info, "\t")
				if len(f) == 5 {
					res["containers"], res["containers_running"], res["images"], res["storage_driver"], res["root_dir"] = f[0], f[1], f[2], f[3], f[4]
				}
			}
		}
		return res, nil
	})
	api("docker.containers", func(q *Req) (any, error) {
		if err := dockerReady(); err != nil {
			return nil, err
		}
		cs, err := dockerContainers()
		if err != nil {
			return nil, err
		}
		return map[string]any{"containers": cs, "networks": dockerNetworks()}, nil
	})
	api("docker.container.logs", func(q *Req) (any, error) {
		var in struct {
			ID   string `json:"id"`
			Tail int    `json:"tail"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !reDockerName.MatchString(in.ID) {
			return nil, errf("Неверный идентификатор")
		}
		if in.Tail <= 0 || in.Tail > 5000 {
			in.Tail = 300
		}
		out, err := runTimeout(30*time.Second, "", "docker", "logs", "--timestamps", "--tail", strconv.Itoa(in.Tail), in.ID)
		if err != nil && out == "" {
			return nil, errf("Не удалось получить журнал: %s", firstLine(err.Error()))
		}
		return map[string]string{"log": out}, nil
	})
	api("docker.container.inspect", func(q *Req) (any, error) {
		id, err := dockerBindID(q)
		if err != nil {
			return nil, err
		}
		out, err := run("docker", "inspect", id)
		if err != nil {
			return nil, errf("Контейнер не найден")
		}
		var v []map[string]any
		if json.Unmarshal([]byte(out), &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			out = string(b)
		}
		return map[string]string{"text": out}, nil
	})
	apiOp("docker.container.action", "Docker: действие с контейнером", func(q *Req) (any, error) {
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Purge  bool   `json:"purge"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !reDockerName.MatchString(in.ID) {
			return nil, errf("Неверный идентификатор")
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		var args []string
		switch in.Action {
		case "start", "stop", "restart", "pause", "unpause", "kill":
			args = []string{in.Action, in.ID}
		case "remove":
			args = []string{"rm", "-f", in.ID}
		default:
			return nil, errf("Неизвестное действие")
		}
		if in.Action != "start" && in.Action != "restart" && in.Action != "unpause" && (dockerPanelContainer(in.ID) || dockerPanelContainer(dockerContainerName(in.ID))) {
			return nil, errf("Это контейнер сервера БД панели. Управляйте им в разделе «Серверы БД»")
		}
		if in.Action == "remove" && in.Purge && !cfg.Dry {
			return dockerRemoveContainerFully(in.ID)
		}
		if out, err := runTimeout(2*time.Minute, "", "docker", args...); err != nil {
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	apiOp("docker.container.create", "Docker: создание контейнера", func(q *Req) (any, error) {
		var in DockerContainerIn
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		args, err := dockerRunArgs(in)
		if err != nil {
			return nil, err
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		image := strings.TrimSpace(in.Image)
		id := startJob("docker:create:"+in.Name+image, "Docker: создание контейнера из "+image, q.S.User, func(j *Job) (any, error) {
			if _, err := run("docker", "image", "inspect", image); err != nil {
				jobStage(j, "Загрузка образа "+image)
				if err := liveCommand(j, 30*time.Minute, "docker", "pull", image); err != nil {
					return nil, err
				}
			}
			jobStage(j, "Запуск контейнера")
			if err := liveCommand(j, 5*time.Minute, "docker", args...); err != nil {
				return nil, err
			}
			return map[string]string{"info": "Контейнер запущен"}, nil
		})
		return map[string]string{"job": id}, nil
	})
	api("docker.images", func(q *Req) (any, error) {
		if err := dockerReady(); err != nil {
			return nil, err
		}
		return dockerImages(), nil
	})
	apiOp("docker.image.pull", "Docker: загрузка образа", func(q *Req) (any, error) {
		var in struct {
			Image string `json:"image"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		in.Image = strings.TrimSpace(in.Image)
		if !reDockerImage.MatchString(in.Image) {
			return nil, errf("Укажите образ, например nginx:latest")
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		id := startJob("docker:pull:"+in.Image, "Docker: загрузка образа "+in.Image, q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Образ загружен"}, liveCommand(j, 30*time.Minute, "docker", "pull", in.Image)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("docker.image.remove", "Docker: удаление образа", func(q *Req) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		// Образ указывается ID или именем repository:tag
		if !reDockerImage.MatchString(in.ID) {
			return nil, errf("Неверный идентификатор")
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		if out, err := run("docker", "rmi", in.ID); err != nil {
			if strings.Contains(out, "being used") || strings.Contains(out, "conflict") {
				return nil, errf("Образ используется контейнером. Сначала удалите контейнер")
			}
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	apiOp("docker.prune", "Docker: очистка", func(q *Req) (any, error) {
		var in struct {
			What string `json:"what"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		var args []string
		switch in.What {
		case "images":
			args = []string{"image", "prune", "-a", "-f"}
		case "volumes":
			args = []string{"volume", "prune", "-f"}
		case "networks":
			args = []string{"network", "prune", "-f"}
		case "system":
			args = []string{"system", "prune", "-f"}
		default:
			return nil, errf("Неизвестный вид очистки")
		}
		id := startJob("docker:prune:"+in.What, "Docker: очистка неиспользуемого", q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Очистка завершена"}, liveCommand(j, 30*time.Minute, "docker", args...)
		})
		return map[string]string{"job": id}, nil
	})
	api("docker.networks", func(q *Req) (any, error) {
		if err := dockerReady(); err != nil {
			return nil, err
		}
		return dockerNetworks(), nil
	})
	apiOp("docker.network.create", "Docker: создание сети", func(q *Req) (any, error) {
		var in struct {
			Name     string `json:"name"`
			Driver   string `json:"driver"`
			Subnet   string `json:"subnet"`
			Gateway  string `json:"gateway"`
			Internal bool   `json:"internal"`
		}
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		if !reDockerName.MatchString(in.Name) {
			return nil, errf("Имя сети: латинские буквы, цифры, «_», «.» и «-»")
		}
		if in.Driver != "bridge" && in.Driver != "macvlan" && in.Driver != "ipvlan" {
			return nil, errf("Неизвестный драйвер сети")
		}
		args := []string{"network", "create", "--driver=" + in.Driver}
		if in.Subnet = strings.TrimSpace(in.Subnet); in.Subnet != "" {
			p, err := fwPrefix(in.Subnet)
			if err != nil || p.Addr().IsUnspecified() {
				return nil, errf("Подсеть: сеть вида 172.30.0.0/24")
			}
			args = append(args, "--subnet="+p.String())
			if p.Addr().Is6() {
				args = append(args, "--ipv6")
			}
		}
		if in.Gateway = strings.TrimSpace(in.Gateway); in.Gateway != "" {
			if in.Subnet == "" || dockerGatewayIP(in.Gateway) == "" {
				return nil, errf("Шлюз указывается вместе с подсетью, например 172.30.0.1")
			}
			args = append(args, "--gateway="+in.Gateway)
		}
		if in.Internal {
			args = append(args, "--internal")
		}
		args = append(args, in.Name)
		if err := dockerReady(); err != nil {
			return nil, err
		}
		if out, err := run("docker", args...); err != nil {
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	apiOp("docker.network.remove", "Docker: удаление сети", func(q *Req) (any, error) {
		id, err := dockerBindID(q)
		if err != nil {
			return nil, err
		}
		if id == "bridge" || id == "host" || id == "none" {
			return nil, errf("Встроенную сеть Docker удалить нельзя")
		}
		if out, err := run("docker", "network", "rm", id); err != nil {
			if strings.Contains(out, "active endpoints") {
				return nil, errf("Сеть используется контейнерами. Сначала отключите или удалите их")
			}
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	api("docker.volumes", func(q *Req) (any, error) {
		if err := dockerReady(); err != nil {
			return nil, err
		}
		cs, _ := dockerContainers()
		return dockerVolumes(cs), nil
	})
	apiOp("docker.volume.create", "Docker: создание тома", func(q *Req) (any, error) {
		id, err := dockerBindID(q)
		if err != nil {
			return nil, err
		}
		if out, err := run("docker", "volume", "create", id); err != nil {
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	apiOp("docker.volume.remove", "Docker: удаление тома", func(q *Req) (any, error) {
		id, err := dockerBindID(q)
		if err != nil {
			return nil, err
		}
		if out, err := run("docker", "volume", "rm", id); err != nil {
			if strings.Contains(out, "in use") {
				return nil, errf("Том используется контейнером. Сначала удалите контейнер")
			}
			return nil, errf("Docker: %s", dockerErr(out, err))
		}
		return nil, nil
	})
	api("docker.compose.list", func(q *Req) (any, error) {
		if err := dockerReady(); err != nil {
			return nil, err
		}
		cs, _ := dockerContainers()
		return map[string]any{"projects": dockerProjects(cs), "compose": dockerCompose() != nil, "dir": dockerProjectDir()}, nil
	})
	api("docker.compose.get", func(q *Req) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		dir, err := dockerProjectPath(in.Name)
		if err != nil {
			return nil, err
		}
		return map[string]string{"yaml": readFile(dockerComposeFile(dir)), "env": readFile(filepath.Join(dir, ".env"))}, nil
	})
	apiOp("docker.compose.save", "Docker: сохранение проекта Compose", func(q *Req) (any, error) {
		var in struct {
			Name string `json:"name"`
			YAML string `json:"yaml"`
			Env  string `json:"env"`
			New  bool   `json:"new"`
			Up   bool   `json:"up"`
		}
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		dir, err := dockerProjectPath(in.Name)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.YAML) == "" || len(in.YAML) > 256<<10 || len(in.Env) > 64<<10 {
			return nil, errf("Заполните docker-compose.yml (до 256 КБ)")
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		if in.New && fileExists(dir) {
			return nil, errf("Проект %s уже есть", in.Name)
		}
		file := dockerComposeFile(dir)
		oldYAML, oldEnv := readFile(file), readFile(filepath.Join(dir, ".env"))
		if err := writeFile(file, []byte(in.YAML), 0600); err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(dir, ".env"), []byte(in.Env), 0600); err != nil {
			return nil, err
		}
		args, err := dockerComposeArgs(in.Name, "config", "-q")
		if err == nil {
			var out string
			if out, err = run(args[0], args[1:]...); err != nil {
				err = errf("Ошибка в docker-compose.yml: %s", dockerErr(out, err))
			}
		}
		if err != nil {
			if in.New {
				os.RemoveAll(P(dir))
			} else {
				writeFile(file, []byte(oldYAML), 0600)
				writeFile(filepath.Join(dir, ".env"), []byte(oldEnv), 0600)
			}
			return nil, err
		}
		if !in.Up {
			return map[string]string{}, nil
		}
		return map[string]string{"job": dockerComposeJob(q, in.Name, "up")}, nil
	})
	apiOp("docker.compose.action", "Docker: действие с проектом Compose", func(q *Req) (any, error) {
		var in struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if _, err := dockerProjectPath(in.Name); err != nil {
			return nil, err
		}
		switch in.Action {
		case "up", "down", "restart", "stop", "start", "pull":
		default:
			return nil, errf("Неизвестное действие")
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		if _, err := dockerComposeArgs(in.Name); err != nil {
			return nil, err
		}
		return map[string]string{"job": dockerComposeJob(q, in.Name, in.Action)}, nil
	})
	api("docker.compose.logs", func(q *Req) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		args, err := dockerComposeArgs(in.Name, "logs", "--no-color", "--tail", "300")
		if err != nil {
			return nil, err
		}
		out, _ := runTimeout(30*time.Second, "", args[0], args[1:]...)
		return map[string]string{"log": out}, nil
	})
	apiOp("docker.compose.delete", "Docker: удаление проекта Compose", func(q *Req) (any, error) {
		var in struct {
			Name    string `json:"name"`
			Volumes bool   `json:"volumes"`
			Images  bool   `json:"images"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		dir, err := dockerProjectPath(in.Name)
		if err != nil {
			return nil, err
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		id := startJob("docker:compose:"+in.Name, "Docker: удаление проекта "+in.Name, q.S.User, func(j *Job) (any, error) {
			rest := []string{"down", "--remove-orphans"}
			if in.Volumes {
				rest = append(rest, "-v")
			}
			if in.Images {
				// Образы, которые использует другой контейнер, Docker не удалит.
				rest = append(rest, "--rmi", "all")
			}
			if args, err := dockerComposeArgs(in.Name, rest...); err == nil && fileExists(dockerComposeFile(dir)) {
				jobStage(j, "Остановка контейнеров проекта")
				if err := liveCommand(j, 10*time.Minute, args[0], args[1:]...); err != nil {
					return nil, err
				}
			}
			jobStage(j, "Удаление папки проекта")
			return map[string]string{"info": "Проект удалён"}, os.RemoveAll(P(dir))
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("docker.install", "Docker: установка", func(q *Req) (any, error) {
		id := startJob("docker:install", "Установка Docker", q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Docker установлен и запущен"}, dockerInstall(j)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("docker.uninstall", "Docker: удаление", func(q *Req) (any, error) {
		var in struct {
			PurgeData bool `json:"purge_data"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		n := len(store.DBServers)
		mu.Unlock()
		if n > 0 {
			return nil, errf("Docker используется серверами БД панели (%d). Сначала удалите их в разделе «Серверы БД»", n)
		}
		id := startJob("docker:uninstall", "Удаление Docker", q.S.User, func(j *Job) (any, error) {
			return map[string]string{"info": "Docker удалён"}, dockerUninstall(j, in.PurgeData)
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("docker.service", "Docker: управление службой", func(q *Req) (any, error) {
		var in struct {
			Action string `json:"action"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if err := dockerReady(); err != nil {
			return nil, err
		}
		switch in.Action {
		case "start":
			if osInfo.Systemd {
				run("systemctl", "reset-failed", "docker")
			}
			if err := service("enable", "docker"); err != nil {
				return nil, errf("Docker не запустился: %s", firstLine(err.Error()))
			}
		case "restart":
			if err := dockerRestart(); err != nil {
				return nil, errf("Docker не запустился: %s", firstLine(err.Error()))
			}
		case "stop":
			if osInfo.Systemd {
				// docker.socket иначе снова запустит службу при первом обращении
				run("systemctl", "stop", "docker.socket")
			}
			if err := service("stop", "docker"); err != nil {
				return nil, errf("Docker: %s", firstLine(err.Error()))
			}
		default:
			return nil, errf("Неизвестное действие")
		}
		return nil, nil
	})
	apiOp("docker.settings.save", "Docker: настройки", func(q *Req) (any, error) {
		var in DockerSettings
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		dir := strings.TrimSpace(in.ProjectDir)
		if dir == "" {
			dir = dockerDefaultProjects
		}
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" || strings.ContainsAny(dir, "\n\r\x00") {
			return nil, errf("Директория проектов: абсолютный путь, например %s", dockerDefaultProjects)
		}
		for _, sys := range []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/docker"} {
			if dir == sys || strings.HasPrefix(dir, sys+"/") {
				return nil, errf("Директория проектов не может быть внутри %s", sys)
			}
		}
		conf, err := dockerReadDaemon()
		if err != nil {
			return nil, err
		}
		before, _ := json.Marshal(conf)
		conf, err = applyDockerSettings(conf, in)
		if err != nil {
			return nil, err
		}
		after, _ := json.MarshalIndent(conf, "", "  ")
		restarted := false
		if compact, _ := json.Marshal(conf); !bytes.Equal(before, compact) {
			if err := dockerWriteDaemon(append(after, '\n')); err != nil {
				return nil, err
			}
			restarted = have("docker")
		}
		mu.Lock()
		if dir == dockerDefaultProjects {
			dir = ""
		}
		store.Settings.DockerProjectDir = dir
		save()
		mu.Unlock()
		return map[string]bool{"restarted": restarted}, nil
	})
	api("docker.config.get", func(q *Req) (any, error) {
		return map[string]string{"text": readFile(dockerDaemonJSON), "path": dockerDaemonJSON}, nil
	})
	apiOp("docker.config.save", "Docker: daemon.json", func(q *Req) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		if len(in.Text) > 64<<10 {
			return nil, errf("Файл больше 64 КБ")
		}
		text := strings.TrimSpace(in.Text)
		if text == "" {
			text = "{}"
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return nil, errf("Ошибка JSON: %s", err.Error())
		}
		return nil, dockerWriteDaemon([]byte(text + "\n"))
	})
}

func dockerComposeJob(q *Req, name, action string) string {
	titles := map[string]string{"up": "запуск", "down": "остановка и удаление контейнеров", "restart": "перезапуск", "stop": "остановка", "start": "запуск", "pull": "обновление образов"}
	return startJob("docker:compose:"+name, "Docker Compose: "+titles[action]+" "+name, q.S.User, func(j *Job) (any, error) {
		rest := []string{action}
		if action == "up" {
			rest = []string{"up", "-d", "--remove-orphans"}
		}
		args, err := dockerComposeArgs(name, rest...)
		if err != nil {
			return nil, err
		}
		return map[string]string{"info": "Готово"}, liveCommand(j, 30*time.Minute, args[0], args[1:]...)
	})
}

// dockerErr — последняя строка вывода команды с ошибкой (Docker пишет причину в конце).
func dockerErr(out string, err error) string {
	s := strings.TrimSpace(out)
	if s == "" && err != nil {
		s = err.Error()
	}
	lines := strings.Split(s, "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func dockerGatewayIP(s string) string {
	a, err := fwPrefix(s)
	if err != nil || !a.IsSingleIP() {
		return ""
	}
	return a.Addr().String()
}
