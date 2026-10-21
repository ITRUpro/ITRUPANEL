// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/base64"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Образцы — только сигнатурные фрагменты, не рабочий код.
func TestAntivirusRules(t *testing.T) {
	bad := map[string]string{
		"a/shell.php":                   `<?php @eval(base64_decode($_POST['x'])); ?>`,
		"b/cmd.php":                     `<?php system($_GET['c']); ?>`,
		"c/call.php":                    `<?php $_POST['f']($_POST['a']); ?>`,
		"d/wso.php":                     `<?php $auth_pass = "63a9f0ea7bb98050796b649e85481845"; // FilesMan`,
		"e/inject.php":                  "<?php eval(gzinflate(base64_decode('AAAA'))); ?><?php\necho 'site';\n",
		"f/hex.php":                     `<?php $f="\x65\x76\x61\x6c"; ?>`,
		"wp-content/uploads/2024/x.jpg": "\xff\xd8\xff<?php echo 1; ?>",
		"img/short.jpg":                 "\xff\xd8\xff\xe0\x10<?=`$_GET[c]`?>\xff\xd9",
		"img/halt.png":                  "\x89PNG\r\n<?php system($_GET['c']); __halt_compiler();\x00\x9a\xff",
		"img/tail.gif":                  "GIF89a\x01\x00<?php echo 1;",
		"img/comment.jpg":               "\xff\xd8<?php system($_GET['c']); /*\x00\x9a\xff*/",
		"img/padded.png":                "\x89PNG<?php" + strings.Repeat(" ", 5000) + "system($_GET['c']); ?>\x00",
		"files/up/shell.php":            `<?php system($_GET['c']); ?>`,
		"files/x.php":                   "<?php @eval(base64_decode('ZWNobyAxOw==')); ?>",
		"img/utf8.jpg":                  "\xff\xd8\xff\xfe<?php system($_GET['c']); // привет\n?>\xff\xd9",
		"img/utf8-string.gif":           "GIF89a<?php $m = 'вход'; eval($_POST['x']); __halt_compiler();\x00\xff",
		"img/upper.png":                 "\x89PNG<?PHP\r\npassthru($_REQUEST['c']);\r\n?>\x00\x01",
		"img/exif.jpg":                  "\xff\xd8\xff\xe1\x00\x40Exif\x00\x00<?php\n$a = $_POST['p'];\neval($a);\n?>\xff\xdb\x00\x43",
		"wp-content/uploads/2024/y.php": "<?php echo 'hi';",
		"sites/default/files/z.php":     "<?php echo 'hi';",
		".htaccess":                     "AddHandler application/x-httpd-php .jpg\n",
		"g/.user.ini":                   "auto_prepend_file=/tmp/x.php\n",
		"h/inj.js":                      "var a=1;document.write(unescape('%3Cscript src=x%3E'));\n",
	}
	for rel, text := range bad {
		if len(avScanContent(rel, []byte(text))) == 0 {
			t.Errorf("not detected: %s", rel)
		}
	}
	// Обычный код CMS: одиночный base64_decode, eval в шаблонизаторе, exec без данных запроса.
	good := map[string]string{
		"wp-includes/class.php": `<?php $data = base64_decode($row['payload']); $out = json_decode($data);`,
		"lib/tpl.php":           `<?php $this->evaluate($tpl); exec('ls', $out); $x = $_GET['page'] ?? 1;`,
		"lib/strings.php":       `<?php $s = chr(65) . chr(66);`,
		"assets/app.js":         `var s=String.fromCharCode(72,101,108,108,111); document.write('<p>ok</p>');`,
		"img/logo.png":          "\x89PNG\r\n\x1a\nIDAT",
		// Случайные байты сжатого JPEG, как в реальном ложном срабатывании.
		"x3disk/uploads/l/a.jpg":         "\xff\xd8\xff\xe0=?c?\xcc\xa2'?h?/,<?=\x8f\xa1c-\xd7\x9e+#?3g \x00\xc8O(*9?I*\xff\xd9",
		"img/xmp.jpg":                    "\xff\xd8\xff\xe1<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?>\xff\xd9",
		"files/index.php":                "<?php echo 'files gallery';",
		"files/_files/config/config.php": "<?php return array('root' => '');",
		"uploads/readme.txt":             "<?php is mentioned in a text file",
		".htaccess":                      "RewriteEngine On\nAddType image/webp .webp\n",
	}
	for rel, text := range good {
		if f := avScanContent(rel, []byte(text)); len(f) > 0 {
			t.Errorf("false positive %s: %v", rel, f)
		}
	}
}

// В 64 МБ случайных байт «<?=» встречается несколько раз — это не должно считаться вставкой.
func TestAntivirusRandomImageBytes(t *testing.T) {
	data := make([]byte, 64<<20)
	rand.New(rand.NewSource(1)).Read(data)
	if !reAVPHPOpen.Match(data) {
		t.Skip("в образце нет «<?=», проверять нечего")
	}
	if f := avScanContent("photo.jpg", data); len(f) > 0 {
		t.Fatalf("false positive on random image bytes: %v", f)
	}
}

func avTestSite(t *testing.T) (*Site, func(string, string)) {
	s := backupTestSite(t)
	put := func(rel, text string) {
		t.Helper()
		p := filepath.Join(P(s.Root), rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return s, put
}

func TestAntivirusScanQuarantineIgnore(t *testing.T) {
	mux := testPanel(t)
	s, put := avTestSite(t)
	mux = testPanel(t)
	store.Sites = []*Site{s}
	put("index.php", "<?php echo 'ok';")
	put("wp-content/uploads/shell.php", `<?php system($_GET['c']);`)
	put("lib/odd.php", `<?php $x = "`+strings.Repeat("QUJD", 2000)+`";`)
	if _, err := runAVScan(&Job{}, s.Domain, false); err != nil {
		t.Fatal(err)
	}
	scan := store.Antivirus.Scans[s.Domain]
	if scan.Files != 3 || len(scan.Findings) != 2 || scan.Findings[0].Severity != "high" || scan.Findings[0].Path != "wp-content/uploads/shell.php" {
		t.Fatalf("scan: %+v", scan)
	}
	if code, r := testAPI(t, mux, "av.quarantine", `{"site":"`+s.Domain+`","path":"wp-content/uploads/shell.php","threat":"x"}`); code != 200 || r["error"] != nil {
		t.Fatalf("quarantine: %v", r)
	}
	if fileExists(s.Root+"/wp-content/uploads/shell.php") || len(store.Antivirus.Quarantine) != 1 {
		t.Fatal("file must move to quarantine")
	}
	if _, r := testAPI(t, mux, "av.quarantine", `{"site":"`+s.Domain+`","path":"../../etc/passwd"}`); r["error"] == nil {
		t.Fatal("path outside the site accepted")
	}
	if err := avRestoreQuarantine(store.Antivirus.Quarantine[0].ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(s.Root+"/wp-content/uploads/shell.php"), "system") || len(store.Antivirus.Quarantine) != 0 {
		t.Fatal("restore from quarantine")
	}
	odd := store.Antivirus.Scans[s.Domain].Findings
	var sum string
	for _, f := range odd {
		if f.Path == "lib/odd.php" {
			sum = f.SHA256
		}
	}
	testAPI(t, mux, "av.ignore", `{"site":"`+s.Domain+`","path":"lib/odd.php","sha256":"`+sum+`"}`)
	runAVScan(&Job{}, s.Domain, false)
	for _, f := range store.Antivirus.Scans[s.Domain].Findings {
		if f.Path == "lib/odd.php" {
			t.Fatal("ignored file reported again")
		}
	}
	put("lib/odd.php", `<?php $x = "`+strings.Repeat("QUJE", 2000)+`";`)
	runAVScan(&Job{}, s.Domain, false)
	again := false
	for _, f := range store.Antivirus.Scans[s.Domain].Findings {
		again = again || f.Path == "lib/odd.php"
	}
	if !again {
		t.Fatal("a changed ignored file must be reported again")
	}
}

// «Вылечить»: заражённый файл возвращается из последней чистой копии, подброшенный файл
// уходит в карантин, вставка без чистой копии вырезается. Остальные файлы не меняются.
func TestAntivirusCure(t *testing.T) {
	s, put := avTestSite(t)
	put("index.php", "<?php\necho 'home v1';\n")
	put("about.php", "<?php\necho 'about';\n")
	if _, err := makeBackup(s.Domain, "files", false, BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	put("index.php", "<?php\necho 'home v2';\n")
	if _, err := makeBackup(s.Domain, "files", false, BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	// Заражение после последней копии.
	put("index.php", "<?php @eval(base64_decode('ZWNobyAxOw==')); ?><?php\necho 'home v2';\n")
	put("about.php", "<?php\necho 'about';\n@eval(base64_decode('ZWNobyAxOw=='));\n")
	put("cache/x.php", `<?php system($_GET['c']);`)
	put("news.php", "<?php\necho 'news, edited today';\n") // обычная правка — не трогается
	// about.php заражён и в копии — значит, чистой версии нет, остаётся вырезать вставку.
	idx, _ := readBackupIndex(store.Backups[1].ID)
	_ = idx
	if _, err := runAVScan(&Job{}, s.Domain, false); err != nil {
		t.Fatal(err)
	}
	if n := len(store.Antivirus.Scans[s.Domain].Findings); n != 3 {
		t.Fatalf("findings: %+v", store.Antivirus.Scans[s.Domain].Findings)
	}
	r, err := avPlanCure(&Job{}, s.Domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, it := range r["items"].([]AVCureItem) {
		actions[it.Path] = it.Action
	}
	if actions["index.php"] != "restore" || actions["cache/x.php"] != "quarantine" {
		t.Fatalf("plan: %v", actions)
	}
	if actions["about.php"] != "restore" {
		t.Fatalf("about.php is clean in the backups and must be restored: %v", actions)
	}
	res, err := avApplyCure(&Job{}, r["plan"].(string), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(s.Root + "/index.php"); got != "<?php\necho 'home v2';\n" {
		t.Fatalf("index.php: %q (%v)", got, res)
	}
	if fileExists(s.Root+"/cache/x.php") || !strings.Contains(readFile(s.Root+"/news.php"), "edited today") {
		t.Fatal("quarantine or untouched files")
	}
	cured := 0
	for _, q := range store.Antivirus.Quarantine {
		if q.Reason == "cure" {
			cured++
		}
	}
	if cured != 2 {
		t.Fatalf("infected versions must be kept in quarantine: %+v", store.Antivirus.Quarantine)
	}
}

func TestAntivirusCleanInjectionWithoutBackups(t *testing.T) {
	s, put := avTestSite(t)
	put("page.php", "<?php\n$title = 'Page';\n@eval(base64_decode('ZWNobyAxOw=='));\necho $title;\n")
	put("tail.html", "<html><body>Hello</body></html>\n<script>document.write(unescape('%3Cscript src=x%3E'));</script>")
	runAVScan(&Job{}, s.Domain, false)
	r, err := avPlanCure(&Job{}, s.Domain, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r["items"].([]AVCureItem) {
		if it.Action != "clean" {
			t.Fatalf("%s: %s %s", it.Path, it.Action, it.Detail)
		}
	}
	if _, err := avApplyCure(&Job{}, r["plan"].(string), nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(s.Root + "/page.php"); got != "<?php\n$title = 'Page';\necho $title;\n" {
		t.Fatalf("page.php: %q", got)
	}
	if got := readFile(s.Root + "/tail.html"); strings.Contains(got, "unescape") || !strings.Contains(got, "Hello") {
		t.Fatalf("tail.html: %q", got)
	}
}

func TestAntivirusMirrorsAndSelectedScan(t *testing.T) {
	s, put := avTestSite(t)
	os.MkdirAll(P("/etc/clamav"), 0755)
	os.WriteFile(P(avFreshclamConf()), []byte("DatabaseMirror database.clamav.net\nScriptedUpdates yes\n"), 0644)
	if got := avMirrors(" https://a.example/ ,https://b.example/clamav\n"); strings.Join(got, "|") != "https://a.example|https://b.example/clamav" {
		t.Fatalf("mirrors: %v", got)
	}
	if err := avApplyMirror(avDefaultMirrors); err != nil {
		t.Fatal(err)
	}
	conf := readFile(avFreshclamConf())
	if strings.Count(conf, "PrivateMirror ") != len(avDefaultMirrors) || !strings.Contains(conf, "ScriptedUpdates no") || strings.Contains(conf, "ScriptedUpdates yes") {
		t.Fatalf("freshclam.conf:\n%s", conf)
	}
	avApplyMirror(nil)
	if conf = readFile(avFreshclamConf()); strings.Contains(conf, "PrivateMirror") || strings.Contains(conf, "ScriptedUpdates") || !strings.Contains(conf, "DatabaseMirror") {
		t.Fatalf("mirrors must be removed:\n%s", conf)
	}
	if !avBlocked("WARNING: You are still on cool-down until after: 2026-10-19") || avBlocked("Database updated") {
		t.Fatal("blocked detection")
	}
	b := &Site{Domain: "b.test", User: "alice", Root: "/var/www/alice/data/www/b.test", Mode: "static"}
	c := &Site{Domain: "c.test", User: "alice", Root: "/var/www/alice/data/www/c.test", Mode: "static"}
	store.Sites = append(store.Sites, b, c)
	for _, x := range []*Site{b, c} {
		os.MkdirAll(P(x.Root), 0755)
	}
	put("index.php", "<?php echo 1;")
	if _, err := runAVScanSites(&Job{}, []string{s.Domain, "c.test"}, false); err != nil {
		t.Fatal(err)
	}
	if store.Antivirus.Scans[s.Domain] == nil || store.Antivirus.Scans["c.test"] == nil || store.Antivirus.Scans["b.test"] != nil || !store.Antivirus.LastRun.IsZero() {
		t.Fatalf("selected scan: %v", store.Antivirus.Scans)
	}
}

func TestAntivirusDailyDBUpdate(t *testing.T) {
	now := time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)
	if !avDBUpdateDue(AntivirusState{}, now) {
		t.Fatal("first update must run right away")
	}
	if avDBUpdateDue(AntivirusState{DBChecked: now.Add(-3 * time.Hour)}, now) {
		t.Fatal("databases must not be updated more than once a day")
	}
	if !avDBUpdateDue(AntivirusState{DBChecked: now.Add(-24 * time.Hour)}, now) {
		t.Fatal("daily update missed")
	}
}

// Закомментированный код не выполняется и не считается угрозой, но комментарий не должен
// становиться способом спрятать настоящий вредонос.
func TestAntivirusCommentedCode(t *testing.T) {
	clean := map[string]string{
		"lib/Input.php":  "<?php\n//return preg_replace('~&#([0-9]{2,4})~e', 'chr(\\\\1)', $_str);\nreturn $_str;\n",
		"lib/hash.php":   "<?php\n    # eval(base64_decode($x));\n",
		"lib/indent.php": "<?php\r\n\t// system($_GET['c']);\r\n",
		".htaccess":      "# AddHandler application/x-httpd-php .jpg\nRewriteEngine On\n",
		"sub/.user.ini":  "; auto_prepend_file=/tmp/x.php\n",
	}
	for rel, text := range clean {
		if f := avScanContent(rel, []byte(text)); len(f) > 0 {
			t.Errorf("commented code flagged %s: %v", rel, f)
		}
	}
	bad := map[string]string{
		"a.php":     "<?php\n//return preg_replace('~a~e', 'x', $s);\nreturn preg_replace('~a~e', 'x', $s);\n",
		"b.php":     "<?php // x ?><?php system($_GET['c']);",
		"c.php":     "<?php\n// note ?> <?php system($_GET['c']);\n",
		"d.php":     "<?php\n// note\r<?php eval(base64_decode($x));",
		"e.php":     "<?php $a = 1; // ok\n$b = 2; system($_GET['c']);",
		"f.php":     "<?php\n$x = 1; // eval(base64_decode($y));\n",
		"g.php":     "<?php\n// FilesMan\n",
		"h.php":     "<?php\n#[Attribute] system($_GET['c']);\n",
		".htaccess": "# comment\nAddHandler application/x-httpd-php .jpg\n",
		"s.html":    "<p>// x <script>document.write(unescape('%3Cscript src=x%3E'));</script>",
	}
	for rel, text := range bad {
		if len(avScanContent(rel, []byte(text))) == 0 {
			t.Errorf("not detected: %s %q", rel, text)
		}
	}
}

// Длинная строка base64 расшифровывается: шаблон HTML остаётся «подозрительно» с понятным
// описанием, а закодированный вредонос становится «опасно».
func TestAntivirusBlobDescribed(t *testing.T) {
	html := "<!DOCTYPE HTML PUBLIC \"-//W3C//DTD HTML 4.01//EN\">\n<html><body>" + strings.Repeat("<p>Привет, мир</p>\n", 400) + "</body></html>"
	cases := []struct {
		name, payload, severity, threat, snippet string
	}{
		{"html", html, "medium", "внутри текст или HTML, PHP-кода нет", "внутри: <!DOCTYPE HTML"},
		{"php", "<?php echo 'hello'; " + strings.Repeat("// padding\n", 700), "medium", "внутри PHP-код", "внутри: <?php echo"},
		{"shell", "<?php system($_GET['c']); " + strings.Repeat(" ", 6000), "high", "спрятан вредоносный PHP-код", "внутри: <?php system"},
		{"binary", string([]byte{0x7f, 'E', 'L', 'F', 0, 1, 2, 3, 0xff, 0xfe}) + strings.Repeat("\x00\x01\x02\xff", 3000), "medium", "внутри двоичные данные", ""},
	}
	for _, c := range cases {
		enc := base64.StdEncoding.EncodeToString([]byte(c.payload))
		file := "<?php\n$tpl = '" + enc + "';\n"
		f := avScanContent("tpl/"+c.name+".php", []byte(file))
		if len(f) != 1 || f[0].Severity != c.severity || !strings.Contains(f[0].Threat, c.threat) || !strings.HasPrefix(f[0].Snippet, c.snippet) {
			t.Errorf("%s: %+v", c.name, f)
		}
	}
}
