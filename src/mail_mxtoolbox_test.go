// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mxTestTransport func(*http.Request) (*http.Response, error)

func (f mxTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const mxTestKey = "00000000-0000-0000-0000-000000000001"

func mxTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestMXToolboxSettingsSecretAndPersistence(t *testing.T) {
	mux := testPanel(t)
	_, r := testAPI(t, mux, "mail.mxtoolbox.save", `{"key":"`+mxTestKey+`"}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	mustLoad()
	if store.Mail.MXToolboxKey != mxTestKey {
		t.Fatal("key not persisted")
	}
	st, e := os.Stat(statePath())
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("state is not private", e)
	}
	for _, method := range []string{"mail.mxtoolbox.settings", "mail.list"} {
		_, r = testAPI(t, mux, method, `{}`)
		encoded, _ := json.Marshal(r)
		if strings.Contains(string(encoded), mxTestKey) {
			t.Fatal("secret returned", method)
		}
	}
	ops, _ := os.ReadFile(cfg.Home + "/var/ops.jsonl")
	if strings.Contains(string(ops), mxTestKey) {
		t.Fatal("secret logged")
	}
	_, r = testAPI(t, mux, "mail.mxtoolbox.save", `{"key":"bad-key"}`)
	if r["ok"] == true {
		t.Fatal("bad key accepted")
	}
	if store.Mail.MXToolboxKey != mxTestKey {
		t.Fatal("failed save changed key")
	}
	_, r = testAPI(t, mux, "mail.mxtoolbox.save", `{"remove":true}`)
	if r["ok"] != true {
		t.Fatal(r)
	}
	mustLoad()
	if store.Mail.MXToolboxKey != "" {
		t.Fatal("key not removed")
	}
}
func TestMXToolboxVendorResults(t *testing.T) {
	body := `{"Command":"blacklist","CommandArgument":"example.test","Failed":[{"Name":"List A","Info":"<b>Listed</b>"}],"Warnings":[{"Name":"List B","Info":"Warning"}],"Timeouts":[{"Name":"List C","Info":"Timeout"}],"Passed":[{"Name":"List D","Info":"Not listed"}]}`
	client := &http.Client{Transport: mxTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != mxLookupURL+"?argument=example.test" || r.Header.Get("Authorization") != mxTestKey {
			t.Error("incorrect vendor request")
		}
		if strings.Contains(r.URL.String(), mxTestKey) {
			t.Error("key in URL")
		}
		return mxTestResponse(200, body), nil
	})}
	rows := mxLookup(context.Background(), client, mxTestKey, "domain", "example.test")
	if len(rows) != 4 {
		t.Fatal(rows)
	}
	for i, want := range []string{"failed", "unknown", "unknown", "clear"} {
		if rows[i].Status != want {
			t.Fatal(rows)
		}
	}
	if rows[0].Details != "Listed" {
		t.Fatal(rows[0])
	}
	for _, bad := range []string{`{}`, `null`, `{"Command":"blacklist","CommandArgument":"example.test","Passed":[]}`, `{"Command":"dns","CommandArgument":"example.test","Passed":[{"Name":"wrong"}]}`, `{"Command":"blacklist","CommandArgument":"other.test","Passed":[{"Name":"wrong"}]}`, `<html>upstream unavailable</html>`, strings.Repeat("x", (1<<20)+1)} {
		body = bad
		rows = mxLookup(context.Background(), client, mxTestKey, "domain", "example.test")
		if len(rows) != 1 || rows[0].Status != "unknown" {
			t.Fatal("false success on malformed response", rows)
		}
	}
}
func TestMXToolboxUnavailableNotClean(t *testing.T) {
	for _, status := range []int{301, 401, 402, 403, 429, 500} {
		client := &http.Client{Transport: mxTestTransport(func(r *http.Request) (*http.Response, error) { return mxTestResponse(status, mxTestKey), nil })}
		rows := mxLookup(context.Background(), client, mxTestKey, "ip", "8.8.8.8")
		if len(rows) != 1 || rows[0].Status != "unknown" || strings.Contains(rows[0].Message, mxTestKey) {
			t.Fatal(rows)
		}
	}
	if e := mxHTTP.CheckRedirect(nil, nil); e != http.ErrUseLastResponse {
		t.Fatal("redirect can leak credentials")
	}
	if mxHTTP.Timeout <= 0 || mxHTTP.Timeout > 30*time.Second {
		t.Fatal("unbounded network call")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: mxTestTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	if rows := mxLookup(ctx, client, mxTestKey, "domain", "example.test"); rows[0].Status != "unknown" {
		t.Fatal(rows)
	}
	if s := mxPlain("<script>secret</script><b>"+mxTestKey+"</b>", mxTestKey); s != "[redacted]" {
		t.Fatal("unsafe text", s)
	}
}
func TestMXToolboxCacheAndKeyIsolation(t *testing.T) {
	old := mxHTTP
	defer func() { mxHTTP = old }()
	mxCache.Lock()
	mxCache.data = map[string]reputationResult{}
	mxCache.Unlock()
	var calls atomic.Int32
	mxHTTP = &http.Client{Transport: mxTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		data, _ := json.Marshal(mxResponse{Command: "blacklist", CommandArgument: r.URL.Query().Get("argument"), Passed: []mxCheck{{Name: "Test list", Info: "Not listed"}}})
		return mxTestResponse(200, string(data)), nil
	})}
	v := mailDNSView{Domain: "example.test", Hostname: "mail.example.test", Options: MailDNSOptions{IPv4: "8.8.8.8"}}
	a, e := mxReputation(context.Background(), v, mxTestKey)
	if e != nil || len(a.Rows) != 3 {
		t.Fatal(a, e)
	}
	b, e := mxReputation(context.Background(), v, mxTestKey)
	if e != nil || !b.Checked.Equal(a.Checked) || calls.Load() != 3 {
		t.Fatal("cache missed", e, calls.Load())
	}
	_, e = mxReputation(context.Background(), v, "00000000-0000-0000-0000-000000000002")
	if e != nil || calls.Load() != 6 {
		t.Fatal("cache leaked between keys", e)
	}
	_, e = mxReputation(context.Background(), v, "")
	if e == nil {
		t.Fatal("missing key accepted")
	}
	v.Options.IPv4 = "127.0.0.1"
	a, e = mxReputation(context.Background(), v, mxTestKey)
	if e != nil || a.Rows[0].Status != "unknown" || calls.Load() != 8 {
		t.Fatal("private IP sent", a, e)
	}
	v.Domain = "other.test"
	mxRunGate <- struct{}{}
	_, e = mxReputation(context.Background(), v, mxTestKey)
	<-mxRunGate
	if e == nil {
		t.Fatal("concurrent paid lookups accepted")
	}
}
