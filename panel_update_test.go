// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRelease() PanelRelease {
	return PanelRelease{Schema: 1, Version: "0.1.15", MinimumVersion: "0.1.1", Released: "2026-10-08", URL: "https://itrupanel.com/panel/itrupanel-full-0.1.15.zip", SHA256: strings.Repeat("a", 64), Size: 10}
}
func signedRelease(t *testing.T, r PanelRelease, key ed25519.PrivateKey) []byte {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	out, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(b), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))})
	return out
}
func TestReleaseSignatureAndConstraints(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	r := testRelease()
	valid := signedRelease(t, r, key)
	if _, e := verifyPanelRelease(valid, pub); e != nil {
		t.Fatal(e)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, e := verifyPanelRelease(valid, other); e == nil {
		t.Fatal("wrong key accepted")
	}
	var env map[string]string
	json.Unmarshal(valid, &env)
	env["payload"] = base64.StdEncoding.EncodeToString([]byte(`{"version":"999.0.0"}`))
	bad, _ := json.Marshal(env)
	if _, e := verifyPanelRelease(bad, pub); e == nil {
		t.Fatal("tampered payload accepted")
	}
	for _, change := range []func(*PanelRelease){func(r *PanelRelease) { r.URL = "https://evil.test/a.zip" }, func(r *PanelRelease) { r.Version = "0.1.15;rm" }, func(r *PanelRelease) { r.Size = maxReleaseSize + 1 }, func(r *PanelRelease) { r.SHA256 = "bad" }, func(r *PanelRelease) { r.Schema = 2 }} {
		r := testRelease()
		change(&r)
		if _, e := verifyPanelRelease(signedRelease(t, r, key), pub); e == nil {
			t.Fatal("invalid signed release accepted", r)
		}
	}
}
func TestVersionsCompareNumerically(t *testing.T) {
	for _, pair := range [][2]string{{"0.1.15", "0.1.1"}, {"0.1.10", "0.1.9"}, {"1.0.0", "0.99.99"}} {
		cmp, e := compareVersions(pair[0], pair[1])
		if e != nil || cmp != 1 {
			t.Fatal(pair, cmp, e)
		}
	}
	if _, e := compareVersions("01.1.2", "0.1.2"); e == nil {
		t.Fatal("malformed version")
	}
}
func TestDownloadRequiresExactHashAndSize(t *testing.T) {
	b := []byte("a complete release")
	sum := sha256.Sum256(b)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	defer s.Close()
	release := testRelease()
	release.URL = s.URL
	release.Size = int64(len(b))
	release.SHA256 = hex.EncodeToString(sum[:])
	for _, kind := range []string{"valid", "truncated", "extra", "tampered"} {
		r := release
		switch kind {
		case "truncated":
			r.Size++
		case "extra":
			r.Size--
		case "tampered":
			r.SHA256 = strings.Repeat("0", 64)
		}
		e := downloadPanelArchive(context.Background(), s.Client(), &r, filepath.Join(t.TempDir(), "archive"), nil)
		if (e == nil) != (kind == "valid") {
			t.Fatal(kind, e)
		}
	}
}
func makeReleaseZIP(t *testing.T, names []string, symlink bool) string {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(0644)
		if symlink && n == "itrupanel/escape" {
			h.SetMode(os.ModeSymlink | 0777)
		}
		f, e := z.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte("content"))
	}
	z.Close()
	p := filepath.Join(t.TempDir(), "release.zip")
	os.WriteFile(p, buf.Bytes(), 0600)
	return p
}
func TestReleaseArchiveRejectsUnsafePaths(t *testing.T) {
	required := []string{"itrupanel/update.sh", "itrupanel/bin/itrupanel-linux-amd64"}
	if e := unpackPanelRelease(makeReleaseZIP(t, required, false), t.TempDir(), "amd64"); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"itrupanel/../escape", "/itrupanel/escape", "itrupanel/escape/../../outside", "itrupanel/\\escape", "itrupanel/update.sh"} {
		names := append(append([]string{}, required...), n)
		if e := unpackPanelRelease(makeReleaseZIP(t, names, false), t.TempDir(), "amd64"); e == nil {
			t.Fatal("unsafe archive accepted", n)
		}
	}
	if e := unpackPanelRelease(makeReleaseZIP(t, append(required, "itrupanel/escape"), true), t.TempDir(), "amd64"); e == nil {
		t.Fatal("symlink accepted")
	}
	if e := unpackPanelRelease(makeReleaseZIP(t, required[:1], false), t.TempDir(), "amd64"); e == nil {
		t.Fatal("missing binary accepted")
	}
}
func TestCumulativeMigrationsSkipCompletedAndRollback(t *testing.T) {
	testPanel(t)
	saveChecked()
	path := filepath.Join(cfg.Home, "etc", "fixture")
	os.WriteFile(path, []byte("original"), 0640)
	var ran []string
	mk := func(id string, fail bool) updateMigration {
		return updateMigration{id, func() ([]string, error) { return []string{path}, nil }, func() error {
			ran = append(ran, id)
			os.WriteFile(path, []byte(id), 0600)
			if fail {
				return errors.New("broken migration")
			}
			return nil
		}}
	}
	b1 := filepath.Join(t.TempDir(), "backup")
	if e := runUpdateMigrations(b1, []updateMigration{mk("one", false)}); e != nil {
		t.Fatal(e)
	}
	b2 := filepath.Join(t.TempDir(), "backup")
	if e := runUpdateMigrations(b2, []updateMigration{mk("one", false), mk("two", false), mk("three", false)}); e != nil {
		t.Fatal(e)
	}
	if strings.Join(ran, ",") != "one,two,three" {
		t.Fatal(ran)
	}
	b3 := filepath.Join(t.TempDir(), "backup")
	if e := runUpdateMigrations(b3, []updateMigration{mk("four", true)}); e == nil {
		t.Fatal("migration should fail")
	}
	if e := rollbackUpdateMigrations(b3); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "three" {
		t.Fatal("rollback lost settings", string(b))
	}
	ledger, e := loadMigrationLedger()
	if e != nil || ledger.Applied["three"] == "" || ledger.Applied["four"] != "" {
		t.Fatal(ledger, e)
	}
	if e := rollbackUpdateMigrations(b1); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(path)
	st, _ := os.Stat(path)
	if string(b) != "original" || st.Mode().Perm() != 0640 {
		t.Fatal("initial snapshot not restored")
	}
	if _, e := os.Stat(migrationLedgerPath()); !os.IsNotExist(e) {
		t.Fatal("new ledger was not removed")
	}
}
func TestUpdatingBlocksMutationsButAllowsStatus(t *testing.T) {
	mux := testPanel(t)
	called := false
	apiOp("test.mutate", "test", func(q *Req) (any, error) { called = true; return nil, nil })
	writeUpdateJSON(updateStatusPath(), PanelUpdateRun{State: "running", Updated: time.Now()})
	code, _ := testAPI(t, mux, "test.mutate", "{}")
	if code != 503 || called {
		t.Fatal("mutation ran during update")
	}
	code, result := testAPI(t, mux, "updates.status", "{}")
	if code != 200 || result["ok"] != true {
		t.Fatal(result)
	}
	r := httptest.NewRequest("POST", "/api/updates.start", strings.NewReader("{}"))
	r.Header.Set("X-Itrupanel", "1")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated update allowed")
	}
}
func TestSnapshotRefusesSymlinks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.Symlink("/etc/passwd", p)
	if _, e := snapshotUpdate([]string{p}, nil); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestInterruptedUpdateCanRecoverAfterReboot(t *testing.T) {
	testPanel(t)
	cfg.Dry = false
	t.Cleanup(func() { cfg.Dry = true })
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho LoadState=not-found\necho ActiveState=inactive\nexit 1\n"), 0700)
	t.Setenv("PATH", bin)
	writeUpdateJSON(updateStatusPath(), PanelUpdateRun{ID: strings.Repeat("a", 24), Unit: "itrupanel-online-update-" + strings.Repeat("a", 24), State: "running", Updated: time.Now().Add(-5 * time.Minute)})
	r := reconcilePanelUpdate()
	if r == nil || r.State != "error" || panelUpdating() {
		t.Fatal("orphaned job did not release update lock", r)
	}
}

func TestPreparedUpdateRequiresExplicitApply(t *testing.T) {
	testPanel(t)
	id := strings.Repeat("a", 24)
	os.MkdirAll(filepath.Dir(updateApplyPath(id)), 0700)
	w := &panelUpdateWriter{r: &PanelUpdateRun{ID: id, State: "running", Total: 100, Downloaded: 100}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPanelUpdateApply(ctx, w) }()
	deadline := time.Now().Add(time.Second)
	for {
		r, _ := readPanelUpdate()
		if r != nil && r.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never ready")
		}
		time.Sleep(time.Millisecond)
	}
	if panelUpdating() {
		t.Fatal("prepared update blocks normal panel operations")
	}
	select {
	case e := <-done:
		t.Fatalf("applied without consent: %v", e)
	case <-time.After(300 * time.Millisecond):
	}
	if _, e := signalPanelUpdateApply(strings.Repeat("b", 24)); e == nil {
		t.Fatal("wrong run applied")
	}
	if _, e := signalPanelUpdateApply(id); e != nil {
		t.Fatal(e)
	}
	if !panelUpdating() {
		t.Fatal("apply must block mutations before worker sees signal")
	}
	if _, e := signalPanelUpdateApply(id); e != nil {
		t.Fatal("retry not idempotent", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	r, _ := readPanelUpdate()
	if r.State != "running" {
		t.Fatal(r.State)
	}
	if _, e := signalPanelUpdateApply(id); e != nil {
		t.Fatal("running apply retry failed", e)
	}
}
func TestPreparedUpdateTimeoutNeverApplies(t *testing.T) {
	testPanel(t)
	w := &panelUpdateWriter{r: &PanelUpdateRun{ID: strings.Repeat("a", 24)}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := waitPanelUpdateApply(ctx, w); e == nil {
		t.Fatal("expired preparation applied")
	}
	if _, e := os.Stat(updateApplyPath(w.r.ID)); !os.IsNotExist(e) {
		t.Fatal("created apply signal without request")
	}
}
func TestFastDownloadPersistsFinalBytes(t *testing.T) {
	testPanel(t)
	body := []byte("small fast archive")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer server.Close()
	r := &PanelRelease{URL: server.URL, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	progress := &panelUpdateWriter{r: &PanelUpdateRun{}, lastProgress: time.Now()}
	if e := downloadPanelArchive(context.Background(), server.Client(), r, filepath.Join(t.TempDir(), "archive"), progress); e != nil {
		t.Fatal(e)
	}
	saved, e := readPanelUpdate()
	if e != nil || saved == nil || saved.Downloaded != r.Size {
		t.Fatalf("missing final bytes: %+v %v", saved, e)
	}
}
