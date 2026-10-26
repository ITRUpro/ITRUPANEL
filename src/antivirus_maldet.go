// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Maldet (Linux Malware Detect, R-fx Networks, GPL v2) — третий антивирус рядом со встроенным и ClamAV,
// по желанию. Лицензия разрешает его ставить: панель скачивает выпуск с GitHub по https и запускает
// его собственный install.sh. Постоянное наблюдение (inotify), письма и задания cron панель отключает:
// проверку и обновление сигнатур она запускает сама, по очереди с другими антивирусами. Только Linux.

const (
	maldetDir     = "/usr/local/maldetect"
	maldetBin     = "/usr/local/maldetect/maldet"
	maldetMaxSize = 64 << 20
	// чуть меньше 4 часов: проверка идёт раз в минуту, а время попытки запоминается при её начале
	maldetAutoEvery = 3*time.Hour + 55*time.Minute
)

var (
	maldetAPIURL     = "https://api.github.com/repos/rfxn/linux-malware-detect/releases/latest"
	maldetTarballFmt = "https://github.com/rfxn/linux-malware-detect/archive/refs/tags/%s.tar.gz"
	maldetFallback   = "https://www.rfxn.com/downloads/maldetect-current.tar.gz"
	maldetInsecureOK bool // http разрешён только в тестах
	maldetClient     = &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 6 || (req.URL.Scheme != "https" && !maldetInsecureOK) {
			return errf("Перенаправление на небезопасный адрес запрещено")
		}
		return nil
	}}
	reMaldetTag    = regexp.MustCompile(`^v?[0-9][0-9A-Za-z._-]{0,30}$`)
	reMaldetReport = regexp.MustCompile(`--report ([0-9]{6}-[0-9]{4}\.[0-9]+)`)
	reMaldetSigVer = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})`)
)

// Файлы, которые ставит install.sh помимо каталога программы.
func maldetExtraFiles() []string {
	return []string{
		"/etc/cron.daily/maldet", "/etc/cron.d/maldet_pub", "/etc/cron.d/maldet-sigup", "/etc/cron.weekly/maldet-watchdog",
		"/etc/logrotate.d/maldet", "/etc/default/maldet", "/etc/sysconfig/maldet",
		"/usr/local/share/man/man1/maldet.1.gz", "/usr/local/sbin/maldet", "/usr/local/sbin/lmd",
	}
}

func maldetSupported() bool { return !isBSD() }

func maldetInstalled() bool {
	if cfg.Dry || !maldetSupported() {
		return false
	}
	st, err := os.Stat(P(maldetBin))
	return err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0
}

func maldetUsable() (bool, string) {
	switch {
	case !maldetSupported():
		return false, "Maldet работает только на Linux"
	case !maldetInstalled():
		return false, "Maldet не установлен"
	case !have("perl"):
		return false, "Для Maldet нужен perl"
	}
	return true, ""
}

func maldetVersion() string {
	return strings.TrimSpace(firstLine(readFile(filepath.Join(maldetDir, "VERSION"))))
}

// maldetSigDate — дата набора сигнатур: версия набора начинается с ГГГГММДД.
func maldetSigDate() (string, time.Time) {
	v := strings.TrimSpace(readFile(filepath.Join(maldetDir, "sigs", "maldet.sigs.ver")))
	m := reMaldetSigVer.FindStringSubmatch(v)
	if m == nil {
		return v, time.Time{}
	}
	t, err := time.Parse("20060102", m[1]+m[2]+m[3])
	if err != nil {
		return v, time.Time{}
	}
	return v, t
}

func maldetGet(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ITRUPANEL/"+Version)
	res, err := maldetClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errf("сервер ответил %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errf("файл слишком большой")
	}
	return data, nil
}

// maldetLatestTag — метка последнего выпуска на GitHub.
func maldetLatestTag(ctx context.Context) (string, error) {
	data, err := maldetGet(ctx, maldetAPIURL, 1<<20)
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &rel); err != nil || !reMaldetTag.MatchString(rel.Tag) {
		return "", errf("не удалось узнать последнюю версию")
	}
	return rel.Tag, nil
}

// maldetSetConf меняет (или добавляет) параметр key="value" в conf.maldet.
func maldetSetConf(key, value string) error {
	path := filepath.Join(maldetDir, "conf.maldet")
	cur := readFile(path)
	if cur == "" {
		return errf("Нет файла настроек Maldet")
	}
	line := key + `="` + value + `"`
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)
	if re.MatchString(cur) {
		cur = re.ReplaceAllLiteralString(cur, line)
	} else {
		cur = strings.TrimRight(cur, "\n") + "\n" + line + "\n"
	}
	return writeFile(path, []byte(cur), 0640)
}

// maldetHarden отключает всё, что install.sh включил сам: слежение inotify, задания cron
// (проверку и обновление запускает панель), письма и карантин (карантин ведёт панель).
func maldetHarden() {
	for _, f := range []string{"/etc/cron.daily/maldet", "/etc/cron.d/maldet_pub", "/etc/cron.d/maldet-sigup", "/etc/cron.weekly/maldet-watchdog"} {
		os.Remove(P(f))
	}
	if !cfg.Dry && have("systemctl") {
		run("systemctl", "disable", "--now", "maldet.service")
	}
	for k, v := range map[string]string{
		"autoupdate_signatures": "0", "autoupdate_version": "0", "autoupdate_version_hashed": "0",
		"cron_daily_scan": "0", "email_alert": "0", "quarantine_hits": "0", "quarantine_clean": "0",
		"scan_ignore_root": "0", "scan_clamscan": "0", "scan_tmpdir_paths": "",
	} {
		maldetSetConf(k, v)
	}
}

// maldetInstall скачивает последний выпуск и запускает install.sh. Повторный запуск обновляет программу.
func maldetInstall(j *Job) (any, error) {
	if !maldetSupported() {
		return nil, errf("Maldet работает только на Linux")
	}
	ctx := context.Background()
	if j != nil && j.ctx != nil {
		ctx = j.ctx
	}
	if !have("perl") && !cfg.Dry {
		jobStage(j, "Установка perl")
		if err := pkgInstall("perl"); err != nil {
			return nil, errf("Для Maldet нужен perl, установить его не удалось: %s", firstLine(err.Error()))
		}
	}
	if cfg.Dry {
		jobStage(j, "Установка Maldet")
		return map[string]string{"info": "Maldet установлен (демо)"}, nil
	}
	jobStage(j, "Загрузка Maldet")
	var data []byte
	tag, err := maldetLatestTag(ctx)
	if err == nil {
		data, err = maldetGet(ctx, fmt.Sprintf(maldetTarballFmt, tag), maldetMaxSize)
	}
	if err != nil {
		if jobStopped(j) {
			return nil, errf("Задача остановлена")
		}
		if j != nil {
			j.Write([]byte("GitHub недоступен (" + firstLine(err.Error()) + "), пробуем сайт разработчика\n"))
		}
		if data, err = maldetGet(ctx, maldetFallback, maldetMaxSize); err != nil {
			return nil, errf("Maldet: не удалось скачать выпуск: %s", firstLine(err.Error()))
		}
	}
	work, err := os.MkdirTemp("", "maldet-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	archive := filepath.Join(work, "maldet.tar.gz")
	if err := os.WriteFile(archive, data, 0600); err != nil {
		return nil, err
	}
	src := filepath.Join(work, "src")
	if err := os.Mkdir(src, 0700); err != nil {
		return nil, err
	}
	if out, err := runTimeout(2*time.Minute, "", "tar", "-xzf", archive, "-C", src, "--no-same-owner"); err != nil {
		return nil, errf("Maldet: архив не распаковался: %s", firstLine(out+" "+err.Error()))
	}
	dir := ""
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(src, e.Name(), "install.sh")); err == nil {
				dir = filepath.Join(src, e.Name())
				break
			}
		}
	}
	if dir == "" {
		return nil, errf("Maldet: в архиве нет install.sh")
	}
	jobStage(j, "Установка Maldet")
	if err := liveCommand(j, 20*time.Minute, "bash", "-c", `cd "$1" && exec bash ./install.sh`, "_", dir); err != nil {
		return nil, err
	}
	if !maldetInstalled() {
		return nil, errf("Maldet: установка не создала программу")
	}
	maldetHarden()
	mu.Lock()
	store.Antivirus.NoMaldet = false
	store.Antivirus.MaldetInstalled = time.Now()
	err = saveChecked()
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]string{"info": "Maldet " + maldetVersion() + " установлен"}, nil
}

// maldetUpdateSigs обновляет сигнатуры (maldet -u) и запоминает результат для раздела «Антивирус».
func maldetUpdateSigs(j *Job) (any, error) {
	avQueue(j)
	defer avScanMu.Unlock()
	var err error
	if ok, why := maldetUsable(); !ok {
		err = errf("%s", why)
	} else {
		jobStage(j, "Обновление сигнатур Maldet")
		err = liveCommand(j, 15*time.Minute, P(maldetBin), "-u")
	}
	mu.Lock()
	st := &store.Antivirus
	st.MaldetChecked = time.Now()
	if err != nil {
		st.MaldetError = firstLine(err.Error())
		addNotice("warn", "av:maldet", "Антивирус: сигнатуры Maldet не обновились: "+st.MaldetError)
	} else {
		st.MaldetError, st.MaldetUpdated = "", time.Now()
	}
	save()
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]string{"info": "Сигнатуры Maldet обновлены"}, nil
}

// maldetAutoUpdate обновляет сигнатуры каждые 4 часа (у Maldet новые сигнатуры выходят чаще, чем у баз ClamAV).
func maldetAutoUpdate(now time.Time) {
	if !maldetInstalled() || jobRunning("av:maldet") || jobRunning("av:maldet-sigs") {
		return
	}
	mu.Lock()
	due := !store.Antivirus.NoMaldet && now.Sub(store.Antivirus.MaldetChecked) >= maldetAutoEvery
	if due {
		store.Antivirus.MaldetChecked = now
		save()
	}
	mu.Unlock()
	if due {
		startJob("av:maldet-sigs", "Обновление сигнатур Maldet (автоматически)", "admin", maldetUpdateSigs)
	}
}

// maldetHits разбирает файл сеанса session.tsv.<ID>: строка заголовка начинается с «#»,
// дальше «сигнатура, путь, карантин, …».
func maldetHits(data string) (names, paths []string) {
	for _, l := range strings.Split(data, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 2 || f[0] == "" || !strings.HasPrefix(f[1], "/") {
			continue
		}
		names, paths = append(names, f[0]), append(paths, f[1])
	}
	return
}

func maldetThreat(sig string) string {
	if i := strings.Index(sig, "}"); strings.HasPrefix(sig, "{") && i > 0 {
		sig = sig[i+1:]
	}
	return "Maldet: " + sig
}

// maldetScan проверяет папки по одной. Файлы не меняются: карантин и лечение ведёт панель.
func maldetScan(j *Job, roots []string) (map[string][]AVFinding, error) {
	out := map[string][]AVFinding{}
	if ok, _ := maldetUsable(); !ok {
		return out, nil
	}
	var firstErr error
	for i, r := range roots {
		root := strings.TrimRight(P(r), "/")
		jobStage(j, fmt.Sprintf("Maldet: проверка %d из %d", i+1, len(roots)))
		text, rerr := maldetRun(j, 3*time.Hour, "-co", "quarantine_hits=0,quarantine_clean=0,email_alert=0,scan_clamscan=0,scan_ignore_root=0,scan_tmpdir_paths=", "-a", root)
		if jobStopped(j) {
			return out, rerr
		}
		id := ""
		if m := reMaldetReport.FindStringSubmatch(text); m != nil {
			id = m[1]
		} else if strings.Contains(text, "scan returned empty file list") || strings.Contains(text, "malware hits 0") {
			continue
		}
		if id == "" {
			if firstErr == nil {
				firstErr = errf("Maldet: отчёт не создан. %s", firstLine(lastLines(text, 3)+" "+fmt.Sprint(rerr)))
			}
			continue
		}
		names, paths := maldetHits(readFile(filepath.Join(maldetDir, "sess", "session.tsv."+id)))
		for k, abs := range paths {
			abs = filepath.Clean(abs)
			if !strings.HasPrefix(abs, root+"/") {
				continue
			}
			f := AVFinding{Path: strings.TrimPrefix(abs, root+"/"), Threat: maldetThreat(names[k]), Engine: "maldet", Severity: "high"}
			if b, err := os.ReadFile(abs); err == nil {
				f.SHA256, f.Size = avSHA256(b), int64(len(b))
			}
			if info, err := os.Stat(abs); err == nil {
				f.Modified = info.ModTime().Unix()
			}
			out[r] = append(out[r], f)
		}
		// Журналы и отчёты сеансов Maldet больше не нужны: результаты уже у панели.
		maldetRun(j, 5*time.Minute, "-p")
	}
	return out, firstErr
}

func maldetRun(j *Job, limit time.Duration, args ...string) (string, error) {
	ctx := context.Background()
	if j != nil && j.ctx != nil {
		ctx = j.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, P(maldetBin), args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8")
	out, err := cmd.CombinedOutput()
	if jobStopped(j) {
		return string(out), errf("Задача остановлена")
	}
	return string(out), err
}

func maldetRemove() error {
	if !cfg.Dry && have("systemctl") {
		run("systemctl", "disable", "--now", "maldet.service")
	}
	for _, f := range []string{"/usr/lib/systemd/system/maldet.service", "/lib/systemd/system/maldet.service", "/etc/systemd/system/maldet.service", "/etc/systemd/system/multi-user.target.wants/maldet.service"} {
		os.Remove(P(f))
	}
	if !cfg.Dry && have("systemctl") {
		run("systemctl", "daemon-reload")
	}
	for _, f := range maldetExtraFiles() {
		os.Remove(P(f))
	}
	os.RemoveAll(P("/var/log/maldet"))
	if err := os.RemoveAll(P(maldetDir)); err != nil {
		return err
	}
	mu.Lock()
	store.Antivirus.MaldetInstalled, store.Antivirus.MaldetChecked, store.Antivirus.MaldetUpdated = time.Time{}, time.Time{}, time.Time{}
	store.Antivirus.MaldetError, store.Antivirus.NoMaldet = "", false
	err := saveChecked()
	mu.Unlock()
	return err
}

func maldetInfo() map[string]any {
	ok, why := maldetUsable()
	mu.Lock()
	a := store.Antivirus
	mu.Unlock()
	out := map[string]any{"supported": maldetSupported(), "installed": maldetInstalled(), "usable": ok, "reason": why, "enabled": !a.NoMaldet, "perl": have("perl")}
	if maldetInstalled() {
		out["version"] = maldetVersion()
		if v, d := maldetSigDate(); v != "" {
			out["sigs"] = v
			if !d.IsZero() {
				out["sigs_date"] = d
			}
		}
	}
	if !a.MaldetInstalled.IsZero() {
		out["installed_at"] = a.MaldetInstalled
	}
	if !a.MaldetChecked.IsZero() {
		out["checked"], out["next"] = a.MaldetChecked, a.MaldetChecked.Add(maldetAutoEvery)
	}
	if a.MaldetError != "" {
		out["error"] = a.MaldetError
	}
	return out
}

func registerAntivirusMaldet() {
	apiOp("av.maldet.install", "Антивирус: установка Maldet", func(q *Req) (any, error) {
		if !maldetSupported() {
			return nil, errf("Maldet работает только на Linux")
		}
		return map[string]string{"job": startJob("av:maldet", "Установка Maldet", q.S.User, func(j *Job) (any, error) {
			info, err := maldetInstall(j)
			if err != nil {
				return nil, err
			}
			// Сигнатуры подтягивает install.sh; если сеть не дала, пробуем ещё раз и сообщаем итог.
			if _, serr := maldetUpdateSigs(j); serr != nil {
				return map[string]any{"info": "Maldet установлен, но сигнатуры не загружены: " + firstLine(serr.Error())}, nil
			}
			return info, nil
		})}, nil
	})
	apiOp("av.maldet.sigs", "Антивирус: обновление сигнатур Maldet", func(q *Req) (any, error) {
		if !maldetInstalled() {
			return nil, errf("Maldet не установлен")
		}
		return map[string]string{"job": startJob("av:maldet-sigs", "Обновление сигнатур Maldet", q.S.User, maldetUpdateSigs)}, nil
	})
	apiOp("av.maldet.settings", "Антивирус: настройки Maldet", func(q *Req) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		store.Antivirus.NoMaldet = !in.Enabled
		err := saveChecked()
		mu.Unlock()
		return nil, err
	})
	apiOp("av.maldet.remove", "Антивирус: удаление Maldet", func(q *Req) (any, error) {
		if jobRunning("av:scan") || jobRunning("av:maldet") || jobRunning("av:maldet-sigs") {
			return nil, errf("Дождитесь окончания проверки или обновления")
		}
		return nil, maldetRemove()
	})
}
