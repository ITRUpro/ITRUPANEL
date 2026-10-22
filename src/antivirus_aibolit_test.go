// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Вместо настоящего сканера (его панель не поставляет) — файл с тем же интерфейсом командной строки.
const fakeAIBolit = `<?php
// AI-BOLIT fake scanner for tests
$o = getopt('', array('path:', 'json_report:', 'mode:', 'memory:', 'delay:'));
$bad = $o['path'] . '/shell.php';
file_put_contents($o['json_report'], json_encode(array(
  'php_malware' => array(array('fn' => $bad, 'sn' => 'Backdoor.PHP.Test')),
  'vulners' => array(array('fn' => $o['path'] . '/old.php', 'sn' => 'Outdated library')),
  'adware' => array(array('fn' => '/etc/passwd', 'sn' => 'outside the site')),
  'version' => '32.0',
)));
`

func TestAIBolitReportParsing(t *testing.T) {
	hits, err := avAIBolitParse([]byte(`{"php_malware":[{"fn":"/x/a.php","sn":"Shell"}],"vulners":[{"fn":"/x/b.php"}],"version":"1","total":3}`))
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if hits[0].Group != "php_malware" || hits[0].Name != "Shell" || avAIBolitSeverity(hits[0].Group) != "high" || avAIBolitSeverity("vulners") != "medium" {
		t.Fatalf("%+v", hits)
	}
	if _, err := avAIBolitParse([]byte("not json")); err == nil {
		t.Fatal("мусор принят за отчёт")
	}
}

func TestAIBolitInstallAndScan(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("нет PHP")
	}
	mux := testPanel(t)
	cfg.Dry = false
	defer func() { cfg.Dry = true }()
	avAIBolitInsecureOK = true
	defer func() { avAIBolitInsecureOK = false }()
	body := fakeAIBolit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	if ok, why := avAIBolitUsable(); ok || why == "" {
		t.Fatal("до подключения сканер считается доступным")
	}
	// Чужой файл не принимается.
	body = "<?php echo 1;"
	if _, err := avAIBolitInstall(context.Background(), srv.URL); err == nil {
		t.Fatal("файл не из AI-Bolit принят")
	}
	body = fakeAIBolit
	if _, err := avAIBolitInstall(context.Background(), "ftp://x/ai-bolit.php"); err == nil {
		t.Fatal("адрес не http(s) принят")
	}
	if _, err := avAIBolitInstall(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if ok, why := avAIBolitUsable(); !ok {
		t.Fatal(why)
	}
	root := "/srv/site"
	os.MkdirAll(P(root), 0755)
	os.WriteFile(filepath.Join(P(root), "shell.php"), []byte("<?php echo 1;"), 0644)
	got, err := avAIBolitScan(nil, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	fs := got[root]
	if len(fs) != 2 {
		t.Fatalf("находки: %+v", fs)
	}
	var shell, old *AVFinding
	for i := range fs {
		switch fs[i].Path {
		case "shell.php":
			shell = &fs[i]
		case "old.php":
			old = &fs[i]
		}
	}
	if shell == nil || shell.Severity != "high" || !strings.HasPrefix(shell.Threat, "AI-Bolit: Backdoor") || shell.Engine != "aibolit" || shell.SHA256 == "" {
		t.Fatalf("shell: %+v", shell)
	}
	if old == nil || old.Severity != "medium" {
		t.Fatalf("old: %+v", old)
	}
	// Выключатель и сведения для раздела.
	if _, r := testAPI(t, mux, "av.aibolit.settings", `{"enabled":false,"path":""}`); r["error"] != nil || !store.Antivirus.NoAIBolit {
		t.Fatalf("настройки: %v", r)
	}
	if _, r := testAPI(t, mux, "av.aibolit.settings", `{"enabled":true,"path":"/etc/passwd"}`); r["error"] == nil {
		t.Fatal("путь не к ai-bolit.php принят")
	}
	if _, r := testAPI(t, mux, "av.aibolit.remove", `{}`); r["error"] != nil || avAIBolitScript() != "" {
		t.Fatalf("удаление: %v", r)
	}
}
