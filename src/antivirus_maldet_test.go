// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Вместо настоящего Maldet — скрипт с тем же интерфейсом: -a КАТАЛОГ пишет отчёт сеанса, -u и -p ничего не делают.
const fakeMaldet = `#!/bin/bash
D="$(cd "$(dirname "$0")" && pwd)"
case "$1" in
-u) echo "{sigup} latest signature set already installed"; exit 0;;
-p) rm -f "$D"/sess/*; exit 0;;
-co) path="$4";;
esac
mkdir -p "$D/sess"
if [ ! -f "$path/sub/shell.php" ]; then echo "{scan} scan returned empty file list"; exit 0; fi
printf '#LMD:v1\tscan\t261027-0103.77\n{HEX}php.malware.test.1\t%s/sub/shell.php\t-\tHEX\n{MD5}outside\t/etc/passwd\t-\tMD5\n' "$path" > "$D/sess/session.tsv.261027-0103.77"
echo "maldet(77): {scan} scan report saved, to view run: maldet --report 261027-0103.77"
`

const fakeMaldetInstall = `#!/bin/bash
set -e
mkdir -p "$FAKE_DEST/sigs"
cp maldet "$FAKE_DEST/maldet"
chmod +x "$FAKE_DEST/maldet"
echo 2.0.1 > "$FAKE_DEST/VERSION"
echo 2026052490478 > "$FAKE_DEST/sigs/maldet.sigs.ver"
printf 'autoupdate_signatures="1"\nemail_alert="1"\nscan_ignore_root="1"\n' > "$FAKE_DEST/conf.maldet"
`

func fakeMaldetTarball(t *testing.T) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	add("linux-malware-detect-2.0.1/install.sh", fakeMaldetInstall, 0755)
	add("linux-malware-detect-2.0.1/maldet", fakeMaldet, 0755)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestMaldetReportParsing(t *testing.T) {
	names, paths := maldetHits("#LMD:v1\tscan\tid\n{HEX}php.malware.x.1\t/srv/a/b.php\t-\tHEX\n\nбитая строка\n{MD5}y\trelative.php\t-\n")
	if len(names) != 1 || names[0] != "{HEX}php.malware.x.1" || paths[0] != "/srv/a/b.php" {
		t.Fatalf("%v %v", names, paths)
	}
	if maldetThreat("{HEX}php.malware.x.1") != "Maldet: php.malware.x.1" || maldetThreat("plain") != "Maldet: plain" {
		t.Fatal(maldetThreat("{HEX}php.malware.x.1"))
	}
	v, d := maldetSigDate()
	_ = v
	_ = d
}

func TestMaldetInstallScanRemove(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("нет tar")
	}
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("нет perl")
	}
	mux := testPanel(t)
	cfg.Dry = false
	defer func() { cfg.Dry = true }()
	osInfo.Family = "debian"
	maldetInsecureOK = true
	defer func() { maldetInsecureOK = false }()
	tarball := fakeMaldetTarball(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") {
			w.Write([]byte(`{"tag_name":"v2.0.1"}`))
			return
		}
		w.Write(tarball)
	}))
	defer srv.Close()
	oldAPI, oldFmt := maldetAPIURL, maldetTarballFmt
	maldetAPIURL, maldetTarballFmt = srv.URL+"/latest", srv.URL+"/%s.tar.gz"
	defer func() { maldetAPIURL, maldetTarballFmt = oldAPI, oldFmt }()
	t.Setenv("FAKE_DEST", P(maldetDir))

	if ok, why := maldetUsable(); ok || why == "" {
		t.Fatal("до установки Maldet считается доступным")
	}
	if _, err := maldetInstall(nil); err != nil {
		t.Fatal(err)
	}
	if !maldetInstalled() || maldetVersion() != "2.0.1" {
		t.Fatalf("установлен=%v версия=%q", maldetInstalled(), maldetVersion())
	}
	// install.sh включил письма, ежедневное обновление и пропуск файлов root — панель это выключает.
	conf := readFile(filepath.Join(maldetDir, "conf.maldet"))
	for _, want := range []string{`autoupdate_signatures="0"`, `email_alert="0"`, `scan_ignore_root="0"`, `quarantine_hits="0"`} {
		if !strings.Contains(conf, want) {
			t.Fatalf("в настройках нет %s:\n%s", want, conf)
		}
	}
	if _, d := maldetSigDate(); d.Format("2006-01-02") != "2026-05-24" {
		t.Fatalf("дата сигнатур %v", d)
	}
	if _, err := maldetUpdateSigs(nil); err != nil {
		t.Fatal(err)
	}
	if store.Antivirus.MaldetUpdated.IsZero() || store.Antivirus.MaldetError != "" {
		t.Fatalf("итог обновления: %+v", store.Antivirus)
	}

	root := "/srv/site"
	os.MkdirAll(filepath.Join(P(root), "sub"), 0755)
	os.WriteFile(filepath.Join(P(root), "sub", "shell.php"), []byte("<?php echo 1;"), 0644)
	got, err := maldetScan(nil, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	fs := got[root]
	if len(fs) != 1 || fs[0].Path != "sub/shell.php" || fs[0].Engine != "maldet" || fs[0].Severity != "high" || fs[0].Threat != "Maldet: php.malware.test.1" || fs[0].SHA256 == "" {
		t.Fatalf("находки: %+v", fs)
	}
	if left, _ := filepath.Glob(filepath.Join(P(maldetDir), "sess", "*")); len(left) != 0 {
		t.Fatalf("отчёты сеансов не удалены: %v", left)
	}
	if got, err = maldetScan(nil, []string{"/srv/empty"}); err != nil || len(got["/srv/empty"]) != 0 {
		t.Fatalf("чистый каталог: %v %v", got, err)
	}

	// Автообновление: раз в 4 часа.
	mu.Lock()
	store.Antivirus.MaldetChecked = time.Now().Add(-3 * time.Hour)
	mu.Unlock()
	maldetAutoUpdate(time.Now())
	if jobRunning("av:maldet-sigs") {
		t.Fatal("сигнатуры обновляются раньше срока")
	}
	info := maldetInfo()
	if info["installed"] != true || info["version"] != "2.0.1" || info["next"] == nil {
		t.Fatalf("сведения: %v", info)
	}

	if _, r := testAPI(t, mux, "av.maldet.settings", `{"enabled":false}`); r["error"] != nil || !store.Antivirus.NoMaldet {
		t.Fatalf("настройки: %v", r)
	}
	if _, r := testAPI(t, mux, "av.maldet.remove", `{}`); r["error"] != nil || maldetInstalled() {
		t.Fatalf("удаление: %v", r)
	}
	if _, err := os.Stat(P(maldetDir)); err == nil {
		t.Fatal("каталог Maldet остался")
	}
}

func TestMaldetUnsupportedOnFreeBSD(t *testing.T) {
	old := osInfo.Family
	osInfo.Family = bsdFamily
	defer func() { osInfo.Family = old }()
	if maldetSupported() || maldetInstalled() {
		t.Fatal("Maldet на FreeBSD")
	}
	if _, err := maldetInstall(nil); err == nil {
		t.Fatal("установка на FreeBSD принята")
	}
	if ok, _ := maldetUsable(); ok {
		t.Fatal("доступен на FreeBSD")
	}
}
