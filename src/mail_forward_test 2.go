// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"strings"
	"testing"
)

func TestForwardingDeliveryAndPersistence(t *testing.T) {
	mux := testPanel(t)
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	call := func(name, body string, ok bool) {
		t.Helper()
		_, r := testAPI(t, mux, name, body)
		if (r["ok"] == true) != ok {
			t.Fatalf("%s: %v", name, r)
		}
	}
	call("mail.domain.add", `{"name":"example.test"}`, true)
	call("mail.box.save", `{"address":"a@example.test"}`, true)
	call("mail.box.save", `{"address":"b@example.test"}`, true)
	call("mail.forward.save", `{"address":"a@example.test","targets":["b@example.test","outside@external.test"],"keep_copy":true,"enabled":true}`, true)
	line := "a@example.test b@example.test, outside@external.test, a@example.test"
	if !strings.Contains(readFile("/etc/postfix/itrupanel/aliases"), line) {
		t.Fatal("copy or recipients missing")
	}
	mustLoad()
	if len(store.Mail.Forwards) != 1 || !store.Mail.Forwards[0].KeepCopy {
		t.Fatal("forwarding lost after restart")
	}
	call("mail.box.save", `{"address":"a@example.test","edit":true,"enabled":false}`, true)
	if strings.Contains(readFile("/etc/postfix/itrupanel/aliases"), "a@example.test") {
		t.Fatal("disabled mailbox still forwarding")
	}
	call("mail.box.save", `{"address":"a@example.test","edit":true,"enabled":true}`, true)
	call("mail.forward.save", `{"address":"a@example.test","targets":["outside@external.test"],"keep_copy":false,"enabled":true,"edit":true}`, true)
	if strings.TrimSpace(readFile("/etc/postfix/itrupanel/aliases")) != "a@example.test outside@external.test" {
		t.Fatal("copy wasn't disabled")
	}
	call("mail.forward.save", `{"address":"a@example.test","targets":["outside@external.test"],"enabled":false,"edit":true}`, true)
	if strings.Contains(readFile("/etc/postfix/itrupanel/aliases"), "a@example.test") {
		t.Fatal("disabled forward remains")
	}
	if !strings.Contains(readFile("/etc/postfix/itrupanel/boxes"), "a@example.test OK") {
		t.Fatal("disabling forward disabled local delivery")
	}
	call("mail.box.delete", `{"address":"a@example.test"}`, false)
	call("mail.forward.delete", `{"address":"a@example.test"}`, true)
	call("mail.box.delete", `{"address":"a@example.test"}`, true)
	call("mail.forward.save", `{"address":"sales@example.test","targets":["b@example.test"],"enabled":true}`, true)
	call("mail.box.delete", `{"address":"b@example.test"}`, false)
	call("mail.alias.save", `{"address":"sales@example.test","target":"b@example.test"}`, false)
	call("mail.domain.delete", `{"name":"example.test"}`, false)
}

func TestForwardingRejectsLoopsAndInvalidTargets(t *testing.T) {
	mux := testPanel(t)
	if err := setupMail("mail.example.test", "", nil); err != nil {
		t.Fatal(err)
	}
	testAPI(t, mux, "mail.domain.add", `{"name":"example.test"}`)
	testAPI(t, mux, "mail.box.save", `{"address":"a@example.test"}`)
	testAPI(t, mux, "mail.alias.save", `{"address":"alias@example.test","target":"a@example.test"}`)
	before := readFile("/etc/postfix/itrupanel/aliases")
	for _, body := range []string{
		`{"address":"a@example.test","targets":["a@example.test"],"enabled":true}`,
		`{"address":"a@example.test","targets":["alias@example.test"],"enabled":true}`,
		`{"address":"a@example.test","targets":["alias@example.test"],"enabled":false}`,
		`{"address":"a@example.test","targets":["missing@example.test"],"enabled":true}`,
		`{"address":"other@example.test","targets":["a@example.test"],"keep_copy":true,"enabled":true}`,
		`{"address":"other@external.test","targets":["a@example.test"],"enabled":true}`,
		`{"address":"a@example.test","targets":["bad@external.test\nBcc:x@test.ru"],"enabled":true}`,
		`{"address":"a@example.test","targets":[],"enabled":true}`,
		`{"address":"a@example.test","targets":["x@external.test","x@external.test"],"enabled":true}`,
	} {
		_, r := testAPI(t, mux, "mail.forward.save", body)
		if r["ok"] == true {
			t.Fatal("invalid forward accepted", body)
		}
	}
	if len(store.Mail.Forwards) != 0 || readFile("/etc/postfix/itrupanel/aliases") != before {
		t.Fatal("invalid forward changed state or files")
	}
}
