// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// Ответы настоящего FASTPANEL (Ubuntu 22.04, консольная программа и файлы ProFTPD и Dovecot) лежат в testdata/fastpanel.
type fpFakeRunner struct {
	t     *testing.T
	files map[string]string // часть команды → имя файла из testdata
	fail  map[string]bool
}

func (f *fpFakeRunner) Run(ctx context.Context, cmd string) (string, error) {
	for part, file := range f.files {
		if strings.Contains(cmd, part) {
			if f.fail[part] {
				return "", errors.New("сбой")
			}
			if file == "yes" {
				return "yes\n", nil
			}
			b, err := os.ReadFile("testdata/fastpanel/" + file)
			if err != nil {
				f.t.Fatal(err)
			}
			return string(b), nil
		}
	}
	return "", errors.New("нет такой команды: " + cmd)
}
func (f *fpFakeRunner) Stream(ctx context.Context, cmd string, w io.Writer) error { return nil }
func (f *fpFakeRunner) Close() error                                              { return nil }

func newFPRunner(t *testing.T) *fpFakeRunner {
	return &fpFakeRunner{t: t, fail: map[string]bool{}, files: map[string]string{
		"[ -x " + fpBin:               "yes",
		"sites list":                  "sites.json",
		"databases list":              "databases.json",
		"ftp_account list":            "ftp.json",
		"emails domains list":         "maildomains.json",
		"emails boxes list --domain=": "boxes.json",
		"cat " + fpFTPPasswd:          "ftpd.passwd",
		"cat " + fpMailPass:           "dovecot.passwd",
	}}
}

func TestFastpanelEnrichInventory(t *testing.T) {
	r := newFPRunner(t)
	// Результат общего просмотра: в базах есть служебные базы FASTPANEL.
	inv := &ImpInventory{Services: map[string]bool{}, DBs: []ImpDB{{Name: "blog_db"}, {Name: "fp2phpmyadmin"}, {Name: "fp2roundcube"}, {Name: "shop_db", Users: []string{"shop_user"}}}}
	impFastpanelEnrich(context.Background(), r, inv)
	if inv.Panel != "fastpanel" {
		t.Fatal("панель не определена")
	}
	if len(inv.Sites) != 2 || inv.Sites[0].Domain != "blog.example.test" || inv.Sites[1].Domain != "shop.example.test" {
		t.Fatalf("сайты: %+v", inv.Sites)
	}
	shop := inv.Sites[1]
	if shop.Owner != "alice" || shop.Root != "/var/www/alice/data/www/shop.example.test" || shop.PHP != "8.1" || shop.Mode != "php" || shop.SSL || len(shop.Aliases) != 1 || shop.Aliases[0] != "www.shop.example.test" {
		t.Fatalf("сайт: %+v", shop)
	}
	if len(inv.DBs) != 2 || inv.DBs[0].Name != "blog_db" || inv.DBs[1].Name != "shop_db" || len(inv.DBs[1].Users) != 1 {
		t.Fatalf("базы без служебных: %+v", inv.DBs)
	}
	if len(inv.FTP) != 1 || inv.FTP[0].Name != "alice_ftp" || inv.FTP[0].Owner != "alice" || inv.FTP[0].Home != "/var/www/alice/data/www/shop.example.test" {
		t.Fatalf("FTP: %+v", inv.FTP)
	}
	if len(inv.MailDomains) != 1 || inv.MailDomains[0].Name != "shop.example.test" || inv.MailDomains[0].Boxes != 2 {
		t.Fatalf("почта: %+v", inv.MailDomains)
	}
	if len(inv.DNS) != 0 {
		t.Fatal("DNS у FASTPANEL внешний, переносить нечего")
	}
}

func TestFastpanelEnrichFallsBackWhenCLIFails(t *testing.T) {
	r := newFPRunner(t)
	r.fail["sites list"], r.fail["databases list"] = true, true
	inv := &ImpInventory{Services: map[string]bool{}, Sites: []ImpSite{{Domain: "old.example.test", Root: "/var/www/u/data/www/old.example.test"}}, DBs: []ImpDB{{Name: "fp2roundcube"}, {Name: "x_db"}}}
	impFastpanelEnrich(context.Background(), r, inv)
	if len(inv.Sites) != 1 || inv.Sites[0].Domain != "old.example.test" {
		t.Fatalf("при сбое сайты должны остаться из просмотра nginx: %+v", inv.Sites)
	}
	if len(inv.DBs) != 1 || inv.DBs[0].Name != "x_db" {
		t.Fatalf("служебные базы должны скрываться и без CLI: %+v", inv.DBs)
	}
	if len(inv.Warnings) == 0 || !strings.Contains(strings.Join(inv.Warnings, " "), "nginx") {
		t.Fatalf("нет предупреждения: %v", inv.Warnings)
	}
	// Программы FASTPANEL нет совсем.
	delete(r.files, "[ -x "+fpBin)
	r.files["[ -x "+fpBin] = "none"
	r.fail["[ -x "+fpBin] = true
	inv = &ImpInventory{Services: map[string]bool{}}
	impFastpanelEnrich(context.Background(), r, inv)
	if inv.Panel != "fastpanel" || len(inv.Warnings) == 0 {
		t.Fatalf("без CLI: %+v", inv)
	}
}

func TestFastpanelLoadFTPAndMail(t *testing.T) {
	r := newFPRunner(t)
	ftp, err := fpLoadFTP(context.Background(), r)
	if err != nil || len(ftp) != 1 || ftp[0].Name != "alice_ftp" || !strings.HasPrefix(ftp[0].Hash, "$1$") || ftp[0].Owner != "alice" {
		t.Fatalf("FTP: %+v %v", ftp, err)
	}
	if !ftpHashSupported(ftp[0].Hash) {
		t.Fatal("хеш md5-crypt должен поддерживаться")
	}
	src, err := fpLoadMail(context.Background(), r)
	if err != nil || len(src.export.Domains) != 1 || len(src.export.Boxes) != 2 {
		t.Fatalf("почта: %+v %v", src, err)
	}
	for _, b := range src.export.Boxes {
		if !strings.HasPrefix(b.Hash, "{CRAM-MD5}") || !b.Enabled {
			t.Fatalf("ящик: %+v", b)
		}
		want := "/var/www/alice/data/email/shop.example.test/" + strings.SplitN(b.Address, "@", 2)[0] + "/.maildir"
		if src.homes[b.Address] != want {
			t.Fatalf("путь писем %s: %q", b.Address, src.homes[b.Address])
		}
	}
	// Ящик без пароля в файле Dovecot или с подозрительным путём пропускается.
	r.files["cat "+fpMailPass] = "ftp.json" // в этом файле нет строк «адрес:хеш:…»
	src, _ = fpLoadMail(context.Background(), r)
	if len(src.export.Boxes) != 0 || len(src.notes) == 0 {
		t.Fatalf("ящики без пароля: %+v", src)
	}
}

func TestFPPHPVersion(t *testing.T) {
	for in, want := range map[string]string{"81": "8.1", "74": "7.4", "83": "8.3", "8.2": "8.2", "": "", "x": "", "100": ""} {
		if got := fpPHP(in); got != want {
			t.Fatalf("%q → %q, ожидалось %q", in, got, want)
		}
	}
}
