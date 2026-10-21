// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"strings"
	"testing"
)

func TestRecycleBin(t *testing.T) {
	mux := testPanel(t)
	os.MkdirAll(P("/var/www/alice/data/www/site/sub"), 0755)
	os.WriteFile(P("/var/www/alice/data/www/site/index.php"), []byte("<?php echo 1;"), 0644)
	os.WriteFile(P("/var/www/alice/data/www/site/sub/a.txt"), []byte("A"), 0644)

	// Удаление переносит в корзину.
	if code, r := testAPI(t, mux, "files.delete", `{"paths":["/var/www/alice/data/www/site/index.php","/var/www/alice/data/www/site/sub"]}`); code != 200 || r["error"] != nil {
		t.Fatalf("delete: %v", r)
	}
	if fileExists("/var/www/alice/data/www/site/index.php") || fileExists("/var/www/alice/data/www/site/sub") {
		t.Fatal("files must leave the site")
	}
	_, r := testAPI(t, mux, "files.recycle.list", `{}`)
	items := r["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("bin: %v", items)
	}
	ids := map[string]string{}
	for _, x := range items {
		it := x.(map[string]any)
		ids[it["path"].(string)] = it["id"].(string)
	}
	// Восстановление на прежнее место; занятое имя не перезаписывается.
	os.WriteFile(P("/var/www/alice/data/www/site/index.php"), []byte("new"), 0644)
	if _, r := testAPI(t, mux, "files.recycle.restore", `{"ids":["`+ids["/var/www/alice/data/www/site/sub"]+`","`+ids["/var/www/alice/data/www/site/index.php"]+`"]}`); r["error"] != nil {
		t.Fatalf("restore: %v", r)
	}
	if readFile("/var/www/alice/data/www/site/sub/a.txt") != "A" || readFile("/var/www/alice/data/www/site/index.php") != "new" {
		t.Fatal("restore must bring back files without overwriting")
	}
	entries, _ := os.ReadDir(P("/var/www/alice/data/www/site"))
	restoredCopy := false
	for _, e := range entries {
		restoredCopy = restoredCopy || strings.HasPrefix(e.Name(), "index (восстановлен")
	}
	if !restoredCopy {
		t.Fatalf("occupied name must get a restored copy: %v", entries)
	}
	// Удаление навсегда, минуя корзину, и выключенная корзина.
	testAPI(t, mux, "files.delete", `{"paths":["/var/www/alice/data/www/site/sub"],"permanent":true}`)
	if fileExists("/var/www/alice/data/www/site/sub") || len(recycleLoad()) != 0 {
		t.Fatal("permanent delete")
	}
	testAPI(t, mux, "files.recycle.settings", `{"enabled":false}`)
	testAPI(t, mux, "files.delete", `{"paths":["/var/www/alice/data/www/site/index.php"]}`)
	if fileExists("/var/www/alice/data/www/site/index.php") || len(recycleLoad()) != 0 {
		t.Fatal("disabled bin must delete immediately")
	}
	testAPI(t, mux, "files.recycle.settings", `{"enabled":true}`)
	os.WriteFile(P("/var/www/alice/data/www/site/x.txt"), []byte("x"), 0644)
	testAPI(t, mux, "files.delete", `{"paths":["/var/www/alice/data/www/site/x.txt"]}`)
	if _, r := testAPI(t, mux, "files.delete", `{"paths":["`+recycleDir+`"]}`); r["error"] == nil {
		t.Fatal("the bin folder itself must not be deletable")
	}
	testAPI(t, mux, "files.recycle.empty", `{}`)
	if entries, _ := os.ReadDir(P(recycleDir)); len(entries) != 1 || len(recycleLoad()) != 0 {
		t.Fatalf("empty: %v", entries)
	}
}
