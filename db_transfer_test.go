// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSQLDumpFormats(t *testing.T) {
	sql := "CREATE TABLE t (id int);\nCREATE DEFINER=`root`@`localhost` VIEW v AS SELECT 1;\n"
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write([]byte(sql))
	gw.Close()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("dump/site.sql")
	w.Write([]byte(sql))
	zw.Close()
	for name, data := range map[string][]byte{"a.sql": []byte(sql), "a.sql.gz": gz.Bytes(), "a.zip": zb.Bytes()} {
		dst := filepath.Join(t.TempDir(), "out.sql")
		if err := prepareSQLDump(bytes.NewReader(data), name, dst); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, _ := os.ReadFile(dst)
		if string(out) != sql {
			t.Fatalf("%s: %q", name, out)
		}
	}
	if err := prepareSQLDump(strings.NewReader("x"), "a.tar", filepath.Join(t.TempDir(), "o")); err == nil {
		t.Fatal("неподдерживаемый формат принят")
	}
	var empty bytes.Buffer
	ez := zip.NewWriter(&empty)
	ew, _ := ez.Create("readme.txt")
	ew.Write([]byte("hi"))
	ez.Close()
	if err := prepareSQLDump(bytes.NewReader(empty.Bytes()), "a.zip", filepath.Join(t.TempDir(), "o")); err == nil {
		t.Fatal("ZIP без .sql принят")
	}
}

func uploadRequest(t *testing.T, url, user, name, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write([]byte(content))
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, url, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-Itrupanel", "1")
	r.AddCookie(&http.Cookie{Name: sessCookie, Value: user})
	return r
}
