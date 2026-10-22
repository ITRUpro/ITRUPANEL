// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// «Вылечить сайт». Для каждого найденного файла панель готовит план и ничего не меняет до
// подтверждения:
//   restore    — последняя версия файла из резервных копий, которая проходит проверку;
//   clean      — вредоносная вставка вырезана, остальной код файла сохранён;
//   quarantine — файла нет ни в одной копии (его подбросили) или он целиком вредоносный;
//   manual     — автоматически исправить нельзя.
// Перед заменой текущая версия файла сохраняется в карантин, поэтому всё можно вернуть.

type AVCureItem struct {
	Path       string    `json:"path"`
	Threat     string    `json:"threat"`
	Action     string    `json:"action"`
	Detail     string    `json:"detail"`
	BackupID   string    `json:"backup_id,omitempty"`
	BackupDate time.Time `json:"backup_date,omitempty"`
	SHA256     string    `json:"sha256"` // текущая версия на момент плана
}

type avCurePlan struct {
	ID      string
	Site    string
	Staging string
	Created time.Time
	Items   []AVCureItem
}

var (
	avPlansMu sync.Mutex
	avPlans   = map[string]*avCurePlan{}
)

func fileOwner(info os.FileInfo) (int, int) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return 0, 0
}

// ---------- вырезание вставок ----------

const avDecoders = `(?:base64_decode|gzinflate|gzuncompress|gzdecode|str_rot13|strrev|rawurldecode|hex2bin)`

var (
	// <?php …вредоносная строка… ?> в самом начале файла перед настоящим кодом.
	reAVHeadBlock = regexp.MustCompile(`\A<\?php[^\n]*?\b(?:eval|assert)\s*\([^\n]*?\?>[ \t]*\r?\n?`)
	// Отдельная строка вида @eval(base64_decode('…'));
	reAVEvalLine = regexp.MustCompile(`(?m)^[ \t]*@?(?:eval|assert)\s*\(\s*@?` + avDecoders + `\s*\([^\n]*\)\s*;[ \t]*\r?\n?`)
	// <?php …вредоносный блок… ?> в конце файла после настоящего содержимого.
	reAVTailBlock = regexp.MustCompile(`\r?\n?<\?php[^\n]*?\b(?:eval|assert)\s*\([^\n]*?(?:\?>)?\s*\z`)
	// <script>…скрытый код…</script> в начале или конце HTML/PHP-файла.
	reAVScriptTag = regexp.MustCompile(`(?is)<script[^>]*>[^<]*?(?:String\.fromCharCode\(\s*(?:\d{2,3}\s*,\s*){40,}|document\.write\s*\(\s*unescape\s*\(\s*["']%3C)[^<]*?</script>`)
	// В .js — одна строка с такой вставкой.
	reAVJSLine = regexp.MustCompile(`(?m)^[^\n]*(?:String\.fromCharCode\(\s*(?:\d{2,3}\s*,\s*){40,}|document\.write\s*\(\s*unescape\s*\(\s*["']%3C)[^\n]*\r?\n?`)
)

// avCleanInjection вырезает типичные вставки. Возвращает false, если файл после очистки всё
// ещё подозрителен, стал пустым или вставка не похожа на отдельный блок.
func avCleanInjection(rel string, data []byte) ([]byte, bool) {
	text := string(data)
	ext := avExt(path.Base(rel))
	orig := text
	switch {
	case avHasExt(avPHPExts, ext):
		text = reAVHeadBlock.ReplaceAllString(text, "")
		text = reAVEvalLine.ReplaceAllString(text, "")
		if loc := reAVTailBlock.FindStringIndex(text); loc != nil && loc[0] > 0 {
			text = text[:loc[0]] + "\n"
		}
		text = reAVScriptTag.ReplaceAllString(text, "")
	case ext == ".html" || ext == ".htm" || ext == ".shtml":
		text = reAVScriptTag.ReplaceAllString(text, "")
	case ext == ".js":
		text = reAVJSLine.ReplaceAllString(text, "")
	default:
		return nil, false
	}
	if text == orig || strings.TrimSpace(text) == "" {
		return nil, false
	}
	for _, f := range avScanContent(rel, []byte(text)) {
		if f.Severity == "high" || f.Threat != "PHP-файл в папке загрузок" {
			return nil, false
		}
	}
	if avHasExt(avPHPExts, ext) && !avPHPSyntaxOK(text) {
		return nil, false
	}
	return []byte(text), true
}

// avPHPSyntaxOK проверяет синтаксис очищенного PHP-файла, если на сервере есть PHP.
func avPHPSyntaxOK(text string) bool {
	if cfg.Dry {
		return true
	}
	var bin string
	for _, v := range phpInstalled() {
		if fileExists(phpCLI(v)) {
			bin = phpCLI(v)
		}
	}
	if bin == "" {
		return true
	}
	f, err := os.CreateTemp("", "itru-av-*.php")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	f.WriteString(text)
	f.Close()
	_, err = runTimeout(30*time.Second, "", bin, "-l", f.Name())
	return err == nil
}

// ---------- план лечения ----------

func avSiteBackups(domain string) []*Backup {
	mu.Lock()
	defer mu.Unlock()
	var out []*Backup
	for _, b := range store.Backups {
		if b.Site == domain && backupHasFiles(b) && backupRestoreIssue(b) == "" {
			c := *b
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Created.After(out[k].Created) })
	return out
}

func avCleanVersion(rel string, data []byte) bool {
	for _, f := range avScanContent(rel, data) {
		if f.Severity == "high" || f.Threat != "PHP-файл в папке загрузок" {
			return false
		}
	}
	return true
}

func avStagePut(staging, rel string, data []byte, mode os.FileMode) error {
	p := filepath.Join(P(staging), "files", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, data, mode)
}

func avPlanCure(j *Job, domain string, paths []string) (map[string]any, error) {
	avQueue(j)
	defer avScanMu.Unlock()
	mu.Lock()
	scan := store.Antivirus.Scans[domain]
	var findings []AVFinding
	if scan != nil {
		findings = append(findings, scan.Findings...)
	}
	mu.Unlock()
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	_, root, err := avOpenSite(domain)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	type cur struct {
		f    AVFinding
		data []byte
		mode os.FileMode
	}
	current := map[string]*cur{}
	var order []string
	for _, f := range findings {
		if len(want) > 0 && !want[f.Path] || current[f.Path] != nil {
			continue
		}
		info, err := root.Lstat(f.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := root.ReadFile(f.Path)
		if err != nil {
			continue
		}
		current[f.Path] = &cur{f: f, data: data, mode: info.Mode().Perm()}
		order = append(order, f.Path)
	}
	if len(order) == 0 {
		return nil, errf("Нет файлов для лечения: сначала проверьте сайт")
	}
	plan := &avCurePlan{ID: randToken(8), Site: domain, Created: time.Now(), Staging: backupDir + "/.cure-" + randToken(6)}
	if err = os.MkdirAll(P(plan.Staging+"/files"), 0700); err != nil {
		return nil, err
	}
	items := map[string]*AVCureItem{}
	for _, rel := range order {
		c := current[rel]
		items[rel] = &AVCureItem{Path: rel, Threat: c.f.Threat, SHA256: avSHA256(c.data)}
	}
	needed := map[string]bool{}
	for _, rel := range order {
		needed[rel] = true
	}
	seen := map[string]bool{} // файл встречался хотя бы в одной копии
	bad := map[string]bool{}  // путь+хеш заражённой версии
	backups := avSiteBackups(domain)
	indexed := 0
	for i, b := range backups {
		if len(needed) == 0 {
			break
		}
		if jobStopped(j) {
			os.RemoveAll(P(plan.Staging))
			return nil, errf("Задача остановлена")
		}
		jobStage(j, fmt.Sprintf("Поиск чистых версий: копия %d из %d (%s)", i+1, len(backups), b.Created.Format("02.01.2006 15:04")))
		idx := backupBrowseIndex(b.ID)
		var cand []string
		for rel := range needed {
			if idx != nil {
				st, ok := idx.Files[rel]
				if !ok {
					continue
				}
				seen[rel] = true
				if st.Hash == items[rel].SHA256 || bad[rel+":"+st.Hash] {
					continue
				}
			}
			cand = append(cand, rel)
		}
		if idx != nil {
			indexed++
		}
		if len(cand) == 0 {
			continue
		}
		work, err := assembleBackup(j, b)
		if err != nil {
			jobStage(j, "Копия "+b.ID+" недоступна: "+firstLine(err.Error()))
			continue
		}
		for _, rel := range cand {
			p := filepath.Join(P(work), "files", filepath.FromSlash(rel))
			info, err := os.Lstat(p)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[rel] = true
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if !avCleanVersion(rel, data) {
				bad[rel+":"+avSHA256(data)] = true
				continue
			}
			if err = avStagePut(plan.Staging, rel, data, current[rel].mode); err != nil {
				os.RemoveAll(P(work))
				return nil, err
			}
			it := items[rel]
			it.Action, it.BackupID, it.BackupDate = "restore", b.ID, b.Created
			it.Detail = "Версия из копии от " + b.Created.Format("02.01.2006 15:04")
			delete(needed, rel)
		}
		os.RemoveAll(P(work))
	}
	for rel := range needed {
		it, c := items[rel], current[rel]
		if cleaned, ok := avCleanInjection(rel, c.data); ok {
			if err := avStagePut(plan.Staging, rel, cleaned, c.mode); err != nil {
				return nil, err
			}
			it.Action = "clean"
			it.Detail = fmt.Sprintf("Вырезать вставку: удаляется %d байт, остальной код сохраняется", len(c.data)-len(cleaned))
			continue
		}
		switch {
		case indexed > 0 && !seen[rel]:
			it.Action, it.Detail = "quarantine", "Файла нет ни в одной резервной копии — вероятно, его подбросили"
		case c.f.Severity == "high" && (len(backups) == 0 || seen[rel]) && avWholeFileMalware(rel, c.data):
			it.Action, it.Detail = "quarantine", "Файл целиком вредоносный"
		case seen[rel]:
			it.Action, it.Detail = "manual", "Во всех резервных копиях этот файл тоже заражён. Проверьте его вручную"
		default:
			it.Action, it.Detail = "manual", "Резервных копий с этим файлом нет, а вставку нельзя безопасно вырезать. Проверьте вручную"
		}
	}
	for _, rel := range order {
		plan.Items = append(plan.Items, *items[rel])
	}
	avPlansMu.Lock()
	for id, p := range avPlans {
		if time.Since(p.Created) > 24*time.Hour {
			os.RemoveAll(P(p.Staging))
			delete(avPlans, id)
		}
	}
	avPlans[plan.ID] = plan
	avPlansMu.Unlock()
	return map[string]any{"plan": plan.ID, "site": domain, "items": plan.Items, "backups": len(backups), "info": "План лечения готов"}, nil
}

// avWholeFileMalware — небольшой файл, в котором почти весь код — сама угроза (типичный веб-шелл).
func avWholeFileMalware(rel string, data []byte) bool {
	if avHasExt(avImageExts, avExt(path.Base(rel))) {
		return true
	}
	if len(data) > 64<<10 {
		return false
	}
	for _, f := range avScanContent(rel, data) {
		if strings.HasPrefix(f.Threat, "Известный веб-шелл") || strings.HasPrefix(f.Threat, "Выполнение команд из запроса") || strings.HasPrefix(f.Threat, "Вызов функции, переданной") {
			return true
		}
	}
	return false
}

// avApplyCure выполняет выбранные пункты плана. Изменившиеся после плана файлы пропускаются.
func avApplyCure(j *Job, planID string, paths []string) (map[string]any, error) {
	avQueue(j)
	defer avScanMu.Unlock()
	avPlansMu.Lock()
	plan := avPlans[planID]
	avPlansMu.Unlock()
	if plan == nil {
		return nil, errf("План лечения устарел: составьте его заново")
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	mu.Lock()
	s := findSite(plan.Site)
	var site Site
	if s != nil {
		site = *s
	}
	mu.Unlock()
	if s == nil {
		return nil, errf("Сайт не найден")
	}
	var done, skipped []string
	for _, it := range plan.Items {
		if len(want) > 0 && !want[it.Path] || it.Action == "manual" {
			continue
		}
		if jobStopped(j) {
			skipped = append(skipped, "остановлено до "+it.Path)
			break
		}
		jobStage(j, "Лечение: "+it.Path)
		data, err := avReadSiteFile(plan.Site, it.Path)
		if err != nil || avSHA256(data) != it.SHA256 {
			skipped = append(skipped, it.Path+": файл изменился после составления плана")
			continue
		}
		switch it.Action {
		case "quarantine":
			if _, err = avQuarantineFile(plan.Site, it.Path, it.Threat, "quarantine"); err != nil {
				skipped = append(skipped, it.Path+": "+firstLine(err.Error()))
				continue
			}
		case "restore", "clean":
			// Текущая версия сохраняется в карантин (без удаления), затем заменяется.
			q, err := avQuarantineFile(plan.Site, it.Path, it.Threat, "cure")
			if err != nil {
				skipped = append(skipped, it.Path+": "+firstLine(err.Error()))
				continue
			}
			if err = restoreSitePath(&site, plan.Staging+"/files", it.Path); err != nil {
				skipped = append(skipped, it.Path+": "+firstLine(err.Error()))
				continue
			}
			_ = q
		}
		done = append(done, it.Path)
		mu.Lock()
		avDropFinding(plan.Site, it.Path)
		mu.Unlock()
	}
	avPlansMu.Lock()
	delete(avPlans, planID)
	avPlansMu.Unlock()
	os.RemoveAll(P(plan.Staging))
	mu.Lock()
	save()
	mu.Unlock()
	info := fmt.Sprintf("Вылечено файлов: %d", len(done))
	if len(skipped) > 0 {
		info += ". Пропущено: " + strings.Join(skipped, "; ")
	}
	return map[string]any{"info": info, "done": done, "skipped": skipped}, nil
}

func registerAntivirusCure() {
	apiOp("av.cure.plan", "Антивирус: план лечения", func(q *Req) (any, error) {
		var in struct {
			Site  string
			Paths []string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		mu.Lock()
		ok := findSite(in.Site) != nil
		mu.Unlock()
		if !ok {
			return nil, errf("Сайт не найден")
		}
		return map[string]string{"job": startJob("av:cure", "Антивирус: план лечения "+in.Site, q.S.User, func(j *Job) (any, error) { return avPlanCure(j, in.Site, in.Paths) })}, nil
	})
	apiOp("av.cure.apply", "Антивирус: лечение", func(q *Req) (any, error) {
		var in struct {
			Plan  string
			Paths []string
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return map[string]string{"job": startJob("av:cure", "Антивирус: лечение", q.S.User, func(j *Job) (any, error) { return avApplyCure(j, in.Plan, in.Paths) })}, nil
	})
}

func avReadSiteFile(domain, rel string) ([]byte, error) {
	_, root, err := avOpenSite(domain)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errf("Файл %s не найден", rel)
	}
	return root.ReadFile(rel)
}
