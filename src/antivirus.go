// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Антивирус для сайтов. Два движка:
//   - встроенная проверка: правила для веб-шеллов и вредоносных вставок в PHP/JS/HTML/.htaccess,
//     PHP-код в картинках и PHP-файлы в папках загрузок; работает без установки пакетов;
//   - ClamAV (по желанию): известные вирусы по базам сигнатур.
// Найденное можно перенести в карантин, вернуть, пропустить или «вылечить» (antivirus_cure.go).

const (
	avQuarantineDir = "/var/lib/itrupanel/quarantine"
	avMaxFindings   = 500
	avMaxTextFile   = 8 << 20
	avClamMinMemory = 1800 << 20 // ClamAV держит базы в памяти: около 1–1,5 ГБ на время проверки
)

type AntivirusState struct {
	Schedule        string             `json:"schedule"` // daily | weekly | off ("" — weekly)
	Hour            int                `json:"hour"`
	Weekday         int                `json:"weekday"` // 0 — воскресенье
	NoClam          bool               `json:"no_clam,omitempty"`
	NoMaldet        bool               `json:"no_maldet,omitempty"`
	MaldetInstalled time.Time          `json:"maldet_installed,omitempty"`
	MaldetChecked   time.Time          `json:"maldet_checked,omitempty"` // последняя автоматическая попытка обновить сигнатуры
	MaldetUpdated   time.Time          `json:"maldet_updated,omitempty"`
	MaldetError     string             `json:"maldet_error,omitempty"`
	Mirror          string             `json:"mirror,omitempty"`
	LastRun         time.Time          `json:"last_run"`
	DBChecked       time.Time          `json:"db_checked,omitempty"` // последняя автоматическая попытка обновить базы
	DBUpdated       time.Time          `json:"db_updated,omitempty"`
	DBError         string             `json:"db_error,omitempty"`
	Scans           map[string]*AVScan `json:"scans,omitempty"`
	Quarantine      []*AVQuarantine    `json:"quarantine,omitempty"`
	Ignored         map[string]string  `json:"ignored,omitempty"`       // «сайт:путь» → sha256 пропущенного файла
	RulesChecked    time.Time          `json:"rules_checked,omitempty"` // последняя проверка набора правил
	RulesUpdated    time.Time          `json:"rules_updated,omitempty"`
	RulesError      string             `json:"rules_error,omitempty"`
	NoRulesUpdate   bool               `json:"no_rules_update,omitempty"` // автоматическое обновление правил выключено
}

type AVScan struct {
	Site     string      `json:"site"`
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished"`
	Files    int         `json:"files"`
	Engines  []string    `json:"engines"`
	Findings []AVFinding `json:"findings"`
	More     int         `json:"more,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type AVFinding struct {
	Path     string `json:"path"` // относительно корня сайта
	Threat   string `json:"threat"`
	Engine   string `json:"engine"`   // builtin | clamav | maldet
	Severity string `json:"severity"` // high | medium
	Line     int    `json:"line,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	SHA256   string `json:"sha256"`
}

type AVQuarantine struct {
	ID     string    `json:"id"`
	Site   string    `json:"site"`
	Path   string    `json:"path"`
	Threat string    `json:"threat"`
	Time   time.Time `json:"time"`
	Size   int64     `json:"size"`
	Mode   uint32    `json:"mode"`
	UID    int       `json:"uid"`
	GID    int       `json:"gid"`
	Reason string    `json:"reason,omitempty"` // quarantine | cure
}

type avRule struct {
	id, threat, severity string
	exts                 []string // пусто — все текстовые файлы
	re                   *regexp.Regexp
}

var avPHPExts = []string{".php", ".phtml", ".php3", ".php4", ".php5", ".php7", ".php8", ".pht", ".phar", ".inc"}
var avImageExts = []string{".jpg", ".jpeg", ".png", ".gif", ".ico", ".bmp", ".webp"}
var avTextExts = append(append([]string{".js", ".html", ".htm", ".shtml", ".svg", ".htaccess", ".user.ini", ".py", ".pl", ".cgi", ".sh"}, avPHPExts...), avImageExts...)

// Правила подобраны так, чтобы не срабатывать на обычный код WordPress, Joomla, Bitrix и их
// плагинов: одиночный base64_decode, eval или длинная строка сами по себе не считаются угрозой.
// avCodeRules — правила для кода, который выполняется: закомментированная строка (// или # в PHP,
// # в .htaccess) ничего не делает и не считается угрозой. Сигнатуры веб-шеллов и JS-вставки в
// HTML этим не пропускаются: текст «FilesMan» в комментарии всё равно признак, а в HTML «//» — не
// комментарий.
var avCodeRules = map[string]bool{"eval-decode": true, "exec-request": true, "call-request": true, "preg-e": true, "globals-call": true, "hex-eval": true, "chr-chain": true, "htaccess-handler": true, "htaccess-prepend": true}

var avRules = []avRule{
	{"eval-decode", "Выполнение закодированного кода (eval + base64/gzinflate)", "high", avPHPExts,
		regexp.MustCompile(`(?i)\b(?:eval|assert)\s*\(\s*(?:@\s*)?(?:base64_decode|gzinflate|gzuncompress|gzdecode|str_rot13|strrev|rawurldecode|hex2bin|convert_uudecode)\s*\(`)},
	{"exec-request", "Выполнение команд из запроса (веб-шелл)", "high", avPHPExts,
		regexp.MustCompile(`(?i)\b(?:eval|assert|system|passthru|shell_exec|exec|popen|proc_open|pcntl_exec|create_function)\s*\(\s*(?:@\s*)?(?:stripslashes\s*\(\s*|base64_decode\s*\(\s*|urldecode\s*\(\s*|trim\s*\(\s*)?\$_(?:GET|POST|REQUEST|COOKIE|SERVER|FILES)\b`)},
	{"call-request", "Вызов функции, переданной в запросе", "high", avPHPExts,
		regexp.MustCompile(`\$_(?:GET|POST|REQUEST|COOKIE)\s*\[[^\]]{1,40}\]\s*\(`)},
	{"known-shell", "Известный веб-шелл", "high", avPHPExts,
		regexp.MustCompile(`(?i)(?:FilesMan|WSOsetcookie|wso_version|c99shell|c99_buff_prepare|r57shell|b374k|IndoXploit|AnonymousFox|ALFA_DATA|alfacgiapi|Mini\s*Shell\s*By|Sh3ll\s*Upl04d|marijuana\s*shell|\$auth_pass\s*=\s*["'][0-9a-f]{32}["'])`)},
	{"preg-e", "preg_replace с модификатором /e (выполнение кода)", "high", avPHPExts,
		regexp.MustCompile(`(?i)preg_replace\s*\(\s*["'](?:/[^"'/]*/|#[^"'#]*#|~[^"'~]*~|@[^"'@]*@|\|[^"'|]*\|)[imsxuADSUXJ]*e[imsxuADSUXJ]*["']\s*,`)},
	{"globals-call", "Обфусцированный вызов через $GLOBALS", "high", avPHPExts,
		regexp.MustCompile(`\$GLOBALS\s*\[\s*["'][^"']{1,40}["']\s*\]\s*\[\s*\d+\s*\]\s*\(`)},
	{"hex-eval", "Закодированное имя опасной функции (\\x65\\x76\\x61\\x6c…)", "high", avPHPExts,
		regexp.MustCompile(`(?i)\\x65\\x76\\x61\\x6c|\\x62\\x61\\x73\\x65\\x36\\x34\\x5f|\\x67\\x7a\\x69\\x6e\\x66\\x6c\\x61\\x74\\x65|\\x61\\x73\\x73\\x65\\x72\\x74`)},
	{"chr-chain", "Строка собирается из длинной цепочки chr()", "medium", avPHPExts,
		regexp.MustCompile(`(?i)(?:chr\s*\(\s*\d+\s*\)\s*\.\s*){12,}`)},
	{"htaccess-handler", "В .htaccess включено выполнение PHP для картинок или текста", "medium", []string{".htaccess"},
		regexp.MustCompile(`(?i)(?:AddHandler|AddType|SetHandler)\s+\S*(?:php|x-httpd)\S*(?:\s+\.?(?:jpe?g|png|gif|ico|txt|svg))`)},
	{"htaccess-prepend", "В .htaccess или .user.ini подключается посторонний PHP-файл", "medium", []string{".htaccess", ".user.ini"},
		regexp.MustCompile(`(?i)auto_(?:prepend|append)_file`)},
	{"js-charcode", "Скрытый скрипт из String.fromCharCode", "medium", []string{".js", ".html", ".htm", ".php", ".phtml"},
		regexp.MustCompile(`String\.fromCharCode\(\s*(?:\d{2,3}\s*,\s*){40,}`)},
	{"js-unescape", "Скрытая вставка document.write(unescape(…))", "medium", []string{".js", ".html", ".htm", ".php", ".phtml"},
		regexp.MustCompile(`(?i)document\.write\s*\(\s*unescape\s*\(\s*["']%3C`)},
}

var reAVPHPOpen = regexp.MustCompile(`<\?(?:php|=)`)

// В сжатых данных картинки три байта «<?=» встречаются случайно (примерно раз на 16 МБ).
// Рабочая вставка — это открывающий тег и текст до «?>», __halt_compiler, «/*» или конца
// файла: иначе PHP споткнётся о двоичные данные и ничего не выполнит.
var reAVImageOpen = regexp.MustCompile(`(?i)<\?(?:php\b|=)`)
var reAVDrupalFiles = regexp.MustCompile(`/sites/[^/]+/files/`)
var reAVClamLine = regexp.MustCompile(`^(.+): (.+) FOUND$`)
var avScanMu sync.Mutex

// avQueue ставит тяжёлую работу антивируса в очередь: проверка, лечение, обновление баз и правил
// идут по одной, следующая начинается, когда предыдущая закончилась. Возвращает с занятым avScanMu.
func avQueue(j *Job) {
	if avScanMu.TryLock() {
		return
	}
	if j != nil {
		j.Write([]byte("Ждём, пока закончится другая работа антивируса, затем начнём…\n"))
	}
	avScanMu.Lock()
}

func avExt(name string) string {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, ".user.ini") {
		return ".user.ini"
	}
	if low == ".htaccess" {
		return ".htaccess"
	}
	return path.Ext(low)
}

func avHasExt(list []string, ext string) bool {
	for _, x := range list {
		if x == ext {
			return true
		}
	}
	return false
}

func avUploadsPath(rel string) bool {
	low := "/" + strings.ToLower(rel)
	// Просто «files/» не считается: там часто лежат файловые менеджеры и галереи (Files Gallery и т. п.).
	return strings.Contains(low, "/wp-content/uploads/") || strings.Contains(low, "/upload/") || strings.Contains(low, "/uploads/") || strings.Contains(low, "/images/") || reAVDrupalFiles.MatchString(low)
}

// avScanContent проверяет содержимое файла встроенными правилами. rel нужен для правил,
// зависящих от расположения (PHP в папке загрузок).
func avScanContent(rel string, data []byte) []AVFinding {
	ext := avExt(path.Base(rel))
	var out []AVFinding
	if avHasExt(avImageExts, ext) {
		if at := avImagePHP(data); at >= 0 {
			out = append(out, avFindingAt(rel, data, at, "PHP-код внутри картинки", "high"))
		}
		return out
	}
	out = append(out, avRuleFindings(rel, ext, data)...)
	if avHasExt(avPHPExts, ext) {
		if at := avLongBlob(data, 6000); at >= 0 {
			f := avFindingAt(rel, data, at, "Длинная закодированная строка в PHP-файле", "medium")
			avDescribeBlob(&f, data, at)
			out = append(out, f)
		}
	}
	if avHasExt(avPHPExts, ext) && avUploadsPath(rel) && len(out) == 0 && reAVPHPOpen.Match(data) {
		out = append(out, AVFinding{Path: rel, Threat: "PHP-файл в папке загрузок", Severity: "medium"})
	}
	return out
}

func avFindingAt(rel string, data []byte, offset int, threat, severity string) AVFinding {
	line := bytes.Count(data[:offset], []byte("\n")) + 1
	start := bytes.LastIndexByte(data[:offset], '\n') + 1
	end := offset + 120
	if end > len(data) {
		end = len(data)
	}
	if i := bytes.IndexByte(data[offset:end], '\n'); i >= 0 {
		end = offset + i
	}
	if offset-start > 60 {
		start = offset - 60
	}
	snippet := strings.ToValidUTF8(string(data[start:end]), "?")
	return AVFinding{Path: rel, Threat: threat, Engine: "builtin", Severity: severity, Line: line, Snippet: strings.TrimSpace(snippet)}
}

func avSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// avScanTree проходит папку сайта без перехода по символьным ссылкам.
func avScanTree(root string, progress func(int)) (int, []AVFinding, error) {
	return avScanTreeStop(root, progress, nil)
}

func avScanTreeStop(root string, progress func(int), stop func() bool) (int, []AVFinding, error) {
	files := 0
	var out []AVFinding
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if stop != nil && files%200 == 0 && stop() {
			return errf("Задача остановлена")
		}
		if err != nil {
			return nil // недоступная папка не прерывает проверку остальных
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		ext := avExt(d.Name())
		if !avHasExt(avTextExts, ext) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > avMaxTextFile {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files++
		if progress != nil && files%500 == 0 {
			progress(files)
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		found := avScanContent(rel, data)
		if len(found) > 0 {
			sum := avSHA256(data)
			for i := range found {
				found[i].Engine, found[i].Size, found[i].Modified, found[i].SHA256 = "builtin", info.Size(), info.ModTime().Unix(), sum
			}
			out = append(out, found...)
		}
		return nil
	})
	return files, out, err
}

// ---------- ClamAV ----------

func avClamInstalled() bool { return !cfg.Dry && have("clamscan") }

func avMemTotal() int64 {
	if isBSD() {
		t, _ := bsdMemInfo()
		return int64(t)
	}
	data, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}

func avClamDBDir() string {
	if isBSD() {
		return "/var/db/clamav"
	}
	if osInfo.Family == "rhel" {
		return "/var/lib/clamav"
	}
	return "/var/lib/clamav"
}

// avClamDBDate — дата самой свежей базы сигнатур (нулевая, если баз нет).
func avClamDBDate() time.Time {
	var newest time.Time
	entries, _ := os.ReadDir(P(avClamDBDir()))
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".cvd") || strings.HasSuffix(n, ".cld") {
			if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
	}
	return newest
}

func avClamUsable() (bool, string) {
	switch {
	case !avClamInstalled():
		return false, "ClamAV не установлен"
	case avClamDBDate().IsZero():
		return false, "Базы ClamAV ещё не загружены"
	case avMemTotal() > 0 && avMemTotal() < avClamMinMemory:
		return false, "Мало памяти для ClamAV (нужно от 2 ГБ)"
	}
	return true, ""
}

// avClamScan проверяет несколько папок одним запуском (базы загружаются один раз).
func avClamScan(j *Job, roots []string) (map[string][]AVFinding, error) {
	out := map[string][]AVFinding{}
	if len(roots) == 0 {
		return out, nil
	}
	args := []string{"-n", "19", "clamscan", "-r", "-i", "--no-summary", "--stdout", "--max-filesize=25M", "--max-scansize=100M"}
	for _, r := range roots {
		args = append(args, P(r))
	}
	jobStage(j, "ClamAV: загрузка баз и проверка файлов")
	text, err := jobOutput(j, 6*time.Hour, "nice", args...)
	if jobStopped(j) {
		return out, err
	}
	// Код 1 — найдены угрозы, это не ошибка.
	if err != nil && !strings.Contains(err.Error(), "exit status 1") {
		return out, errf("ClamAV: %s", firstLine(text+" "+err.Error()))
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		m := reAVClamLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		for _, r := range roots {
			prefix := strings.TrimRight(P(r), "/") + "/"
			if strings.HasPrefix(m[1], prefix) {
				rel := strings.TrimPrefix(m[1], prefix)
				f := AVFinding{Path: rel, Threat: "ClamAV: " + m[2], Engine: "clamav", Severity: "high"}
				if data, err := os.ReadFile(m[1]); err == nil {
					f.SHA256, f.Size = avSHA256(data), int64(len(data))
				}
				if info, err := os.Stat(m[1]); err == nil {
					f.Modified = info.ModTime().Unix()
				}
				out[r] = append(out[r], f)
			}
		}
	}
	return out, nil
}

// ---------- проверка сайтов ----------

// avSchedule: пока расписание не настроено — раз в неделю, в воскресенье в 04:00.
func avSchedule(a AntivirusState) (string, int) {
	if a.Schedule == "" {
		return "weekly", 4
	}
	return a.Schedule, a.Hour
}

func avIgnoreKey(site, rel string) string { return site + ":" + rel }

// runAVScan проверяет выбранные сайты и сохраняет результат. domain "*" — все сайты.
func runAVScan(j *Job, domain string, auto bool) (map[string]any, error) {
	return runAVScanSites(j, []string{domain}, auto)
}

// runAVScanSites проверяет выбранные сайты; "*" в списке — все сайты.
func runAVScanSites(j *Job, domains []string, auto bool) (map[string]any, error) {
	avQueue(j)
	defer avScanMu.Unlock()
	want := map[string]bool{}
	for _, d := range domains {
		want[d] = true
	}
	mu.Lock()
	var sites []Site
	for _, s := range store.Sites {
		if want["*"] || want[s.Domain] {
			sites = append(sites, *s)
		}
	}
	useClam := !store.Antivirus.NoClam
	useMaldet := !store.Antivirus.NoMaldet
	mu.Unlock()
	if len(sites) == 0 {
		return nil, errf("Сайт не найден")
	}
	clamOK, clamWhy := avClamUsable()
	clam := map[string][]AVFinding{}
	var clamErr error
	if useClam && clamOK && !cfg.Dry {
		var roots []string
		for _, s := range sites {
			roots = append(roots, s.Root)
		}
		clam, clamErr = avClamScan(j, roots)
	} else if useClam && clamWhy != "" {
		jobStage(j, clamWhy+": только встроенная проверка")
	}
	maldetOK, _ := maldetUsable()
	maldetOK = maldetOK && useMaldet && !cfg.Dry
	mald := map[string][]AVFinding{}
	var maldErr error
	if maldetOK {
		var roots []string
		for _, s := range sites {
			roots = append(roots, s.Root)
		}
		mald, maldErr = maldetScan(j, roots)
		if jobStopped(j) {
			return nil, errf("Задача остановлена")
		}
	}
	total := 0
	results := map[string]*AVScan{}
	for i, s := range sites {
		jobStage(j, fmt.Sprintf("Проверка сайта %s (%d из %d)", s.Domain, i+1, len(sites)))
		scan := &AVScan{Site: s.Domain, Started: time.Now(), Engines: []string{"builtin"}}
		if jobStopped(j) {
			return nil, errf("Задача остановлена")
		}
		files, found, err := avScanTreeStop(P(s.Root), func(n int) { jobStage(j, fmt.Sprintf("Проверка сайта %s: %d файлов", s.Domain, n)) }, func() bool { return jobStopped(j) })
		if jobStopped(j) {
			return nil, errf("Задача остановлена")
		}
		if err != nil {
			scan.Error = err.Error()
		}
		if useClam && clamOK && !cfg.Dry {
			scan.Engines = append(scan.Engines, "clamav")
			found = append(found, clam[s.Root]...)
			if clamErr != nil {
				scan.Error = clamErr.Error()
			}
		}
		if maldetOK {
			scan.Engines = append(scan.Engines, "maldet")
			found = append(found, mald[s.Root]...)
			if maldErr != nil && scan.Error == "" {
				scan.Error = maldErr.Error()
			}
		}
		scan.Files = files
		mu.Lock()
		var kept []AVFinding
		for _, f := range found {
			if sum, ok := store.Antivirus.Ignored[avIgnoreKey(s.Domain, f.Path)]; ok && sum == f.SHA256 {
				continue
			}
			kept = append(kept, f)
		}
		mu.Unlock()
		sort.SliceStable(kept, func(a, b int) bool {
			if kept[a].Severity != kept[b].Severity {
				return kept[a].Severity == "high"
			}
			return kept[a].Path < kept[b].Path
		})
		if len(kept) > avMaxFindings {
			scan.More = len(kept) - avMaxFindings
			kept = kept[:avMaxFindings]
		}
		scan.Findings = kept
		if scan.Findings == nil {
			scan.Findings = []AVFinding{}
		}
		scan.Finished = time.Now()
		results[s.Domain] = scan
		total += len(kept) + scan.More
	}
	mu.Lock()
	if store.Antivirus.Scans == nil {
		store.Antivirus.Scans = map[string]*AVScan{}
	}
	for d, scan := range results {
		store.Antivirus.Scans[d] = scan
		if n := len(scan.Findings) + scan.More; n > 0 {
			addNotice("warn", "av:"+d, fmt.Sprintf("Антивирус: на сайте %s найдено подозрительных файлов: %d", d, n))
		}
	}
	if auto || want["*"] {
		store.Antivirus.LastRun = time.Now()
	}
	save()
	mu.Unlock()
	return map[string]any{"info": fmt.Sprintf("Проверено сайтов: %d. Найдено: %d", len(sites), total), "found": total}, nil
}

// avAutoScan — плановая проверка: каждый день или раз в неделю в заданный час.
func avAutoScan(now time.Time) {
	mu.Lock()
	a, sites := store.Antivirus, len(store.Sites)
	mu.Unlock()
	schedule, hour := avSchedule(a)
	if schedule == "off" || now.Hour() != hour || sites == 0 {
		return
	}
	if schedule == "weekly" && (int(now.Weekday()) != a.Weekday || now.Sub(a.LastRun) < 6*24*time.Hour) {
		return
	}
	if schedule == "daily" && now.Sub(a.LastRun) < 20*time.Hour {
		return
	}
	startJob("av:scan", "Антивирус: плановая проверка", "admin", func(j *Job) (any, error) { return runAVScan(j, "*", true) })
}

// ---------- карантин ----------

// avOpenSite открывает папку сайта как os.Root: файлы не открываются по ссылкам за пределы сайта.
func avOpenSite(domain string) (*Site, *os.Root, error) {
	mu.Lock()
	s := findSite(domain)
	var site Site
	if s != nil {
		site = *s
	}
	mu.Unlock()
	if s == nil {
		return nil, nil, errf("Сайт не найден")
	}
	root, err := os.OpenRoot(P(site.Root))
	if err != nil {
		return nil, nil, err
	}
	return &site, root, nil
}

func avCleanRel(rel string) (string, error) {
	rel = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	if rel == "" || rel == "." {
		return "", errf("Неверный путь файла")
	}
	return rel, nil
}

// avQuarantineFile переносит файл сайта в карантин (копия, затем удаление из сайта).
func avQuarantineFile(domain, rel, threat, reason string) (*AVQuarantine, error) {
	rel, err := avCleanRel(rel)
	if err != nil {
		return nil, err
	}
	_, root, err := avOpenSite(domain)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, errf("Файл %s не найден", rel)
	}
	if !info.Mode().IsRegular() {
		return nil, errf("%s — не обычный файл", rel)
	}
	src, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	q := &AVQuarantine{ID: randToken(8), Site: domain, Path: rel, Threat: threat, Time: time.Now(), Size: info.Size(), Mode: uint32(info.Mode().Perm()), Reason: reason}
	q.UID, q.GID = fileOwner(info)
	if err = os.MkdirAll(P(avQuarantineDir), 0700); err != nil {
		return nil, err
	}
	dst, err := os.OpenFile(P(avQuarantineDir+"/"+q.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(dst, src)
	if e := dst.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(P(avQuarantineDir + "/" + q.ID))
		return nil, err
	}
	if reason == "quarantine" {
		if err = root.Remove(rel); err != nil {
			os.Remove(P(avQuarantineDir + "/" + q.ID))
			return nil, err
		}
	}
	mu.Lock()
	store.Antivirus.Quarantine = append(store.Antivirus.Quarantine, q)
	avDropFinding(domain, rel)
	save()
	mu.Unlock()
	return q, nil
}

// avDropFinding убирает файл из последнего результата проверки (под mu).
func avDropFinding(domain, rel string) {
	if scan := store.Antivirus.Scans[domain]; scan != nil {
		kept := scan.Findings[:0]
		for _, f := range scan.Findings {
			if f.Path != rel {
				kept = append(kept, f)
			}
		}
		scan.Findings = kept
	}
}

func avFindQuarantine(id string) *AVQuarantine {
	for _, q := range store.Antivirus.Quarantine {
		if q.ID == id {
			return q
		}
	}
	return nil
}

// avRestoreQuarantine возвращает файл на прежнее место (если там уже есть файл — заменяет его).
func avRestoreQuarantine(id string) error {
	mu.Lock()
	q := avFindQuarantine(id)
	var c AVQuarantine
	if q != nil {
		c = *q
	}
	mu.Unlock()
	if q == nil {
		return errf("Файл в карантине не найден")
	}
	_, root, err := avOpenSite(c.Site)
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := os.ReadFile(P(avQuarantineDir + "/" + c.ID))
	if err != nil {
		return err
	}
	if dir := path.Dir(c.Path); dir != "." {
		if err = root.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	tmp := c.Path + ".itru-restore-" + randToken(3)
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(c.Mode))
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(os.FileMode(c.Mode))
	}
	if err == nil && !cfg.Dry {
		err = f.Chown(c.UID, c.GID)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = root.Rename(tmp, c.Path)
	}
	if err != nil {
		root.Remove(tmp)
		return err
	}
	return avForgetQuarantine(c.ID)
}

func avForgetQuarantine(id string) error {
	mu.Lock()
	defer mu.Unlock()
	kept := store.Antivirus.Quarantine[:0]
	for _, q := range store.Antivirus.Quarantine {
		if q.ID != id {
			kept = append(kept, q)
		}
	}
	store.Antivirus.Quarantine = kept
	os.Remove(P(avQuarantineDir + "/" + filepath.Base(id)))
	return saveChecked()
}

// ---------- установка и обновление ClamAV ----------

func avFreshclamConf() string {
	if isBSD() {
		return bsdPrefix + "/etc/freshclam.conf"
	}
	if osInfo.Family == "rhel" {
		return "/etc/freshclam.conf"
	}
	return "/etc/clamav/freshclam.conf"
}

// Официальный сервер баз (database.clamav.net) отвечает 403 для России и ряда стран. Эти зеркала
// проверены и обновляются ежедневно; базы .cvd подписаны ClamAV, freshclam проверяет подпись,
// поэтому зеркало не может подменить базы — только отдать устаревшие.
var avDefaultMirrors = []string{"https://clamav-mirror.ru", "https://mirror.truenetwork.ru/clamav", "https://packages.microsoft.com/clamav"}

func avMirrors(s string) []string {
	var out []string
	for _, m := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ';' }) {
		if m = strings.TrimRight(m, "/"); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// avApplyMirror прописывает зеркала (PrivateMirror) или возвращает стандартный сервер. Зеркала
// раздают только полные базы без патчей .cdiff, поэтому для них ScriptedUpdates выключается.
func avApplyMirror(mirrors []string) error {
	conf := readFile(avFreshclamConf())
	if conf == "" {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(conf, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "PrivateMirror") || strings.HasPrefix(t, "ScriptedUpdates") || strings.HasPrefix(t, "# ITRUPANEL mirror") {
			continue
		}
		lines = append(lines, l)
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	if len(mirrors) > 0 {
		out += "# ITRUPANEL mirror\n"
		for _, m := range mirrors {
			out += "PrivateMirror " + m + "\n"
		}
		out += "ScriptedUpdates no\n"
	}
	return writeFile(avFreshclamConf(), []byte(out), 0644)
}

func avBlocked(log string) bool {
	return strings.Contains(log, "403") || strings.Contains(log, "Forbidden") || strings.Contains(log, "cool-down") || strings.Contains(log, "429")
}

func avRunFreshclam(j *Job, mirrors []string) (string, error) {
	if err := avApplyMirror(mirrors); err != nil {
		return "", err
	}
	if len(mirrors) > 0 {
		// После отказа официального сервера freshclam запоминает паузу («cool-down») в freshclam.dat;
		// для зеркал она не нужна.
		os.Remove(P(avClamDBDir() + "/freshclam.dat"))
	}
	const marker = "\n--- freshclam ---\n"
	j.Write([]byte(marker))
	err := liveCommand(j, 40*time.Minute, "freshclam", "--stdout")
	jobsMu.Lock()
	log := j.Log
	jobsMu.Unlock()
	if i := strings.LastIndex(log, marker); i >= 0 {
		log = log[i+len(marker):]
	}
	return log, err
}

func avFreshclam(j *Job) error {
	mu.Lock()
	mirrors := avMirrors(store.Antivirus.Mirror)
	mu.Unlock()
	// Служба обновления держит блокировку баз: на время ручного обновления она останавливается.
	// Базы обновляет панель раз в сутки (с переключением на зеркала). Служба clamav-freshclam
	// проверяет их 24 раза в сутки и при блокировке официального сервера только продлевает паузу,
	// поэтому она отключается.
	if isBSD() {
		run("service", "clamav_freshclam", "onestop")
	} else {
		run("systemctl", "disable", "--now", "clamav-freshclam")
	}
	jobStage(j, "Загрузка баз ClamAV (около 120 МБ)")
	log, err := avRunFreshclam(j, mirrors)
	// В режиме паузы после 403 («cool-down») freshclam ничего не скачивает, но завершается
	// успешно, поэтому отказ определяется по выводу, а результат — по наличию баз.
	if len(mirrors) == 0 && avBlocked(log) {
		// Официальный сервер недоступен из страны сервера: зеркала включаются автоматически
		// и остаются в настройках, чтобы базы обновлялись и дальше.
		jobStage(j, "Официальный сервер баз недоступен из вашей страны: переключение на зеркала")
		mirrors = avDefaultMirrors
		log, err = avRunFreshclam(j, mirrors)
		if err == nil && !avBlocked(log) {
			mu.Lock()
			store.Antivirus.Mirror = strings.Join(mirrors, " ")
			save()
			mu.Unlock()
			j.Write([]byte("\nБазы загружены с зеркал: " + strings.Join(mirrors, ", ") + "\n"))
		}
	}
	if avBlocked(log) {
		return errf("Сервер баз ClamAV отклонил загрузку (403). Укажите доступное зеркало в настройках антивируса; встроенная проверка работает без баз")
	}
	if err != nil {
		return err
	}
	if avClamDBDate().IsZero() {
		return errf("Базы ClamAV не загружены. Подробности в журнале задачи")
	}
	return nil
}

func avInstall(j *Job) (any, error) {
	if isBSD() {
		jobStage(j, "Установка ClamAV")
		if err := bsdPkgCheck("clamav"); err != nil {
			return nil, err
		}
		if err := liveCommand(j, 30*time.Minute, "pkg", "install", "-y", "clamav"); err != nil {
			return nil, err
		}
	} else if osInfo.Family == "rhel" {
		if err := ensureEPEL(j); err != nil {
			return nil, err
		}
		jobStage(j, "Установка ClamAV")
		if err := liveCommand(j, 30*time.Minute, "dnf", "--setopt=skip_if_unavailable=True", "install", "-y", "clamav", "clamav-update"); err != nil {
			return nil, err
		}
		// В EPEL freshclam.conf выключен строкой Example.
		if c := readFile(avFreshclamConf()); strings.Contains(c, "\nExample") || strings.HasPrefix(c, "Example") {
			writeFile(avFreshclamConf(), []byte(regexp.MustCompile(`(?m)^Example\s*$`).ReplaceAllString(c, "#Example")), 0644)
		}
	} else {
		jobStage(j, "Установка ClamAV")
		liveCommand(j, 10*time.Minute, "apt-get", "update")
		if err := liveCommand(j, 30*time.Minute, "apt-get", "install", "-y", "--no-install-recommends", "clamav", "clamav-freshclam"); err != nil {
			return nil, err
		}
	}
	if _, err := avUpdateDB(j); err != nil {
		return map[string]string{"info": "ClamAV установлен, но базы не загружены: " + firstLine(err.Error())}, nil
	}
	return map[string]string{"info": "ClamAV установлен, базы загружены"}, nil
}

// avDBUpdateDue — пора ли автоматически обновить базы: не чаще раза в сутки.
func avDBUpdateDue(a AntivirusState, now time.Time) bool {
	return now.Sub(a.DBChecked) >= 23*time.Hour+30*time.Minute
}

// avAutoUpdateDB обновляет базы ClamAV раз в сутки; о сбое сообщает уведомлением.
func avAutoUpdateDB(now time.Time) {
	if !avClamInstalled() || jobRunning("av:update") || jobRunning("av:install") {
		return
	}
	mu.Lock()
	due := avDBUpdateDue(store.Antivirus, now)
	if due {
		store.Antivirus.DBChecked = now
		save()
	}
	mu.Unlock()
	if !due {
		return
	}
	startJob("av:update", "Обновление баз ClamAV (автоматически)", "admin", func(j *Job) (any, error) { return avUpdateDB(j) })
}

// avUpdateDB обновляет базы и запоминает результат для раздела «Антивирус».
func avUpdateDB(j *Job) (any, error) {
	avQueue(j)
	defer avScanMu.Unlock()
	err := avFreshclam(j)
	mu.Lock()
	if err != nil {
		store.Antivirus.DBError = firstLine(err.Error())
		addNotice("warn", "av:db", "Антивирус: базы ClamAV не обновились: "+store.Antivirus.DBError)
	} else {
		store.Antivirus.DBError, store.Antivirus.DBUpdated = "", time.Now()
	}
	save()
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]string{"info": "Базы ClamAV обновлены"}, nil
}

// ---------- API ----------

func avStatus() map[string]any {
	clamOK, clamWhy := avClamUsable()
	rulesInfo, maldetInf := avRulesInfo(), maldetInfo()
	mu.Lock()
	defer mu.Unlock()
	a := store.Antivirus
	sites := []map[string]any{}
	for _, s := range store.Sites {
		row := map[string]any{"domain": s.Domain, "unicode": toUnicode(s.Domain), "root": s.Root}
		if scan := a.Scans[s.Domain]; scan != nil {
			high, medium := 0, 0
			for _, f := range scan.Findings {
				if f.Severity == "high" {
					high++
				} else {
					medium++
				}
			}
			row["scan"] = map[string]any{"finished": scan.Finished, "files": scan.Files, "high": high, "medium": medium, "more": scan.More, "engines": scan.Engines, "error": scan.Error}
		}
		sites = append(sites, row)
	}
	sort.Slice(sites, func(i, k int) bool { return sites[i]["unicode"].(string) < sites[k]["unicode"].(string) })
	schedule, hour := avSchedule(a)
	quarantine := append([]*AVQuarantine{}, a.Quarantine...)
	sort.Slice(quarantine, func(i, k int) bool { return quarantine[i].Time.After(quarantine[k].Time) })
	clam := map[string]any{"installed": avClamInstalled(), "usable": clamOK, "reason": clamWhy, "enabled": !a.NoClam, "mirror": a.Mirror, "memory": avMemTotal()}
	if d := avClamDBDate(); !d.IsZero() {
		clam["db_date"] = d
	}
	if !a.DBChecked.IsZero() {
		clam["db_checked"], clam["db_next"] = a.DBChecked, a.DBChecked.Add(24*time.Hour)
	}
	if a.DBError != "" {
		clam["db_error"] = a.DBError
	}
	return map[string]any{"rules": rulesInfo, "schedule": schedule, "hour": hour, "weekday": a.Weekday, "last_run": a.LastRun, "sites": sites, "quarantine": quarantine, "clamav": clam, "maldet": maldetInf, "running": jobRunning("av:scan") || jobRunning("av:cure") || jobRunning("av:update") || jobRunning("av:rules") || jobRunning("av:maldet") || jobRunning("av:maldet-sigs"), "demo": cfg.Dry}
}

func jobRunning(key string) bool {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	for _, j := range jobs {
		if j.Key == key && j.Status == "running" {
			return true
		}
	}
	return false
}

var reAVMirror = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(?::\d{1,5})?(?:/[A-Za-z0-9._~/-]*)?$`)

func registerAntivirus() {
	registerAntivirusMaldet()
	registerAntivirusRules()
	api("av.status", func(q *Req) (any, error) { return avStatus(), nil })
	api("av.findings", func(q *Req) (any, error) {
		var in struct{ Site string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		scan := store.Antivirus.Scans[in.Site]
		s := findSite(in.Site)
		if s == nil {
			return nil, errf("Сайт не найден")
		}
		return map[string]any{"scan": scan, "root": s.Root}, nil
	})
	apiOp("av.settings", "Антивирус: настройки", func(q *Req) (any, error) {
		var in struct {
			Schedule      string
			Hour, Weekday int
			ClamAV        bool `json:"clamav"`
			Mirror        string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Schedule != "daily" && in.Schedule != "weekly" && in.Schedule != "off" {
			return nil, errf("Неверное расписание")
		}
		if in.Hour < 0 || in.Hour > 23 || in.Weekday < 0 || in.Weekday > 6 {
			return nil, errf("Неверное время проверки")
		}
		mirrors := avMirrors(in.Mirror)
		for _, m := range mirrors {
			if !reAVMirror.MatchString(m) {
				return nil, errf("Зеркало баз: адрес вида https://mirror.example.ru")
			}
		}
		in.Mirror = strings.Join(mirrors, " ")
		mu.Lock()
		store.Antivirus.Schedule, store.Antivirus.Hour, store.Antivirus.Weekday = in.Schedule, in.Hour, in.Weekday
		store.Antivirus.NoClam, store.Antivirus.Mirror = !in.ClamAV, in.Mirror
		err := saveChecked()
		mu.Unlock()
		if err == nil && avClamInstalled() {
			err = avApplyMirror(mirrors)
		}
		return nil, err
	})
	apiOp("av.install", "Антивирус: установка ClamAV", func(q *Req) (any, error) {
		return map[string]string{"job": startJob("av:install", "Установка ClamAV", q.S.User, avInstall)}, nil
	})
	apiOp("av.update", "Антивирус: обновление баз", func(q *Req) (any, error) {
		if !avClamInstalled() {
			return nil, errf("ClamAV не установлен")
		}
		return map[string]string{"job": startJob("av:update", "Обновление баз ClamAV", q.S.User, avUpdateDB)}, nil
	})
	apiOp("av.scan", "Антивирус: проверка", func(q *Req) (any, error) {
		var in struct {
			Site  string
			Sites []string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if in.Site != "" {
			in.Sites = append(in.Sites, in.Site)
		}
		if len(in.Sites) == 0 {
			in.Sites = []string{"*"}
		}
		if len(in.Sites) > 1000 {
			return nil, errf("Слишком много сайтов")
		}
		title := "Антивирус: проверка всех сайтов"
		if in.Sites[0] != "*" {
			title = "Антивирус: проверка " + strings.Join(in.Sites, ", ")
			if len(in.Sites) > 3 {
				title = fmt.Sprintf("Антивирус: проверка выбранных сайтов (%d)", len(in.Sites))
			}
		}
		sites := in.Sites
		return map[string]string{"job": startJob("av:scan", title, q.S.User, func(j *Job) (any, error) { return runAVScanSites(j, sites, false) })}, nil
	})
	apiOp("av.quarantine", "Антивирус: карантин", func(q *Req) (any, error) {
		var in struct{ Site, Path, Threat string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		_, err := avQuarantineFile(in.Site, in.Path, in.Threat, "quarantine")
		return nil, err
	})
	apiOp("av.restore", "Антивирус: возврат из карантина", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, avRestoreQuarantine(in.ID)
	})
	apiOp("av.delete", "Антивирус: удаление из карантина", func(q *Req) (any, error) {
		var in struct{ ID string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		found := avFindQuarantine(in.ID) != nil
		mu.Unlock()
		if !found {
			return nil, errf("Файл в карантине не найден")
		}
		return nil, avForgetQuarantine(in.ID)
	})
	apiOp("av.ignore", "Антивирус: пропуск файла", func(q *Req) (any, error) {
		var in struct{ Site, Path, SHA256 string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		rel, err := avCleanRel(in.Path)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if findSite(in.Site) == nil {
			return nil, errf("Сайт не найден")
		}
		if store.Antivirus.Ignored == nil {
			store.Antivirus.Ignored = map[string]string{}
		}
		store.Antivirus.Ignored[avIgnoreKey(in.Site, rel)] = in.SHA256
		avDropFinding(in.Site, rel)
		return nil, saveChecked()
	})
	registerAntivirusCure()
}

// avLongBlob находит строку в кавычках из символов base64 длиной не меньше min.
func avLongBlob(data []byte, min int) int {
	run, start := 0, -1
	for i, c := range data {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=' || c == '\\' {
			if run == 0 {
				start = i
			}
			run++
			continue
		}
		if run >= min && start > 0 && (data[start-1] == '"' || data[start-1] == '\'') && (c == '"' || c == '\'') {
			return start - 1
		}
		run = 0
	}
	return -1
}

// avImagePHP ищет в картинке рабочую PHP-вставку и возвращает её начало (или -1). Текст вставки
// должен быть корректным UTF-8 (так что русские комментарии и строки в коде не помогают скрыть
// вредонос) без управляющих символов и содержать хотя бы один признак кода — «;», «(», «$» или
// обратную кавычку. Длина вставки не ограничена: отступ из тысяч пробелов не прячет код.
func avImagePHP(data []byte) int {
	for _, m := range reAVImageOpen.FindAllIndex(data, -1) {
		code := false
		for i := m[1]; ; {
			if i >= len(data) {
				if code {
					return m[0]
				}
				break
			}
			rest := data[i:]
			if bytes.HasPrefix(rest, []byte("?>")) || bytes.HasPrefix(rest, []byte("/*")) || len(rest) >= 15 && strings.EqualFold(string(rest[:15]), "__halt_compiler") {
				if code {
					return m[0]
				}
				break
			}
			r, size := utf8.DecodeRune(rest)
			if r == utf8.RuneError || r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f {
				break
			}
			if r == ';' || r == '(' || r == '$' || r == '`' {
				code = true
			}
			i += size
		}
	}
	return -1
}

// avRuleFindings применяет правила к содержимому файла с расширением ext.
func avRuleFindings(rel, ext string, data []byte) []AVFinding {
	var out []AVFinding
	for _, r := range avRules {
		if len(r.exts) > 0 && !avHasExt(r.exts, ext) {
			continue
		}
		if !avCodeRules[r.id] {
			if loc := r.re.FindIndex(data); loc != nil {
				out = append(out, avFindingAt(rel, data, loc[0], r.threat, r.severity))
			}
			continue
		}
		for _, loc := range r.re.FindAllIndex(data, -1) {
			if !avCommented(data, loc[0], ext) {
				out = append(out, avFindingAt(rel, data, loc[0], r.threat, r.severity))
				break
			}
		}
	}
	if p := avPack.Load(); p != nil {
		for _, r := range p.Rules {
			if len(r.exts) > 0 && !avHasExt(r.exts, ext) {
				continue
			}
			if !r.code {
				if loc := r.re.FindIndex(data); loc != nil {
					out = append(out, avFindingAt(rel, data, loc[0], r.threat, r.severity))
				}
				continue
			}
			for _, loc := range r.re.FindAllIndex(data, -1) {
				if !avCommented(data, loc[0], ext) {
					out = append(out, avFindingAt(rel, data, loc[0], r.threat, r.severity))
					break
				}
			}
		}
	}
	return out
}

// avCommented — совпадение стоит в строке, которая целиком закомментирована, и после комментария
// нет «?>» (после него PHP снова выполняется). Строкой считается и конец \r: PHP завершает
// «//»-комментарий и на нём, поэтому код после одиночного \r выполнился бы.
func avCommented(data []byte, pos int, ext string) bool {
	start := 0
	for i := pos - 1; i >= 0; i-- {
		if data[i] == '\n' || data[i] == '\r' {
			start = i + 1
			break
		}
	}
	line := bytes.TrimLeft(data[start:pos], " \t")
	switch {
	case avHasExt(avPHPExts, ext):
		if !(bytes.HasPrefix(line, []byte("//")) || bytes.HasPrefix(line, []byte("#")) && !bytes.HasPrefix(line, []byte("#["))) {
			return false
		}
		return !bytes.Contains(line, []byte("?>"))
	case ext == ".htaccess":
		return bytes.HasPrefix(line, []byte("#"))
	case ext == ".user.ini":
		return bytes.HasPrefix(line, []byte(";")) || bytes.HasPrefix(line, []byte("#"))
	}
	return false
}

// avDescribeBlob расшифровывает длинную строку base64 и пишет в находку, что внутри: PHP-код с
// опасными вызовами — «опасно»; остальное остаётся «подозрительно», но с понятным описанием и
// началом расшифрованного текста, чтобы можно было решить, что это — шаблон или вредонос.
func avDescribeBlob(f *AVFinding, data []byte, at int) {
	i := at + 1
	for i < len(data) && (data[i] >= 'A' && data[i] <= 'Z' || data[i] >= 'a' && data[i] <= 'z' || data[i] >= '0' && data[i] <= '9' || data[i] == '+' || data[i] == '/' || data[i] == '=') {
		i++
	}
	blob := bytes.TrimRight(data[at+1:i], "=")
	dec := make([]byte, base64.RawStdEncoding.DecodedLen(len(blob)))
	n, err := base64.RawStdEncoding.Decode(dec, blob)
	if err != nil || n == 0 {
		return
	}
	dec = dec[:n]
	for _, h := range avRuleFindings("decoded.php", ".php", dec) {
		if h.Severity == "high" {
			f.Threat, f.Severity = "Внутри закодированной строки спрятан вредоносный PHP-код", "high"
			f.Snippet = avPreview(dec)
			return
		}
	}
	switch {
	case reAVPHPOpen.Match(dec):
		f.Threat = "Длинная закодированная строка: внутри PHP-код"
		f.Snippet = avPreview(dec)
	case utf8.Valid(dec) && avPrintableShare(dec) >= 0.95:
		f.Threat = "Длинная закодированная строка: внутри текст или HTML, PHP-кода нет"
		f.Snippet = avPreview(dec)
	default:
		f.Threat = "Длинная закодированная строка: внутри двоичные данные"
		f.Snippet = ""
	}
}

func avPrintableShare(b []byte) float64 {
	printable := 0
	for _, r := range string(b) {
		if r == '\n' || r == '\r' || r == '\t' || r >= 0x20 && r != 0x7f {
			printable++
		}
	}
	return float64(printable) / float64(utf8.RuneCount(b))
}

// avPreview — начало расшифрованного текста в одну строку.
func avPreview(b []byte) string {
	text := strings.Join(strings.Fields(strings.ToValidUTF8(string(b), "?")), " ")
	if r := []rune(text); len(r) > 140 {
		text = string(r[:140]) + "…"
	}
	return "внутри: " + text
}
