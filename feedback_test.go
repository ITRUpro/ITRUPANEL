// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedbackPrivacyAndValidation(t *testing.T) {
	in := feedbackInput{RequestID: strings.Repeat("a", 48), Subject: "Question", Message: "Test", Category: "question", Consent: true}
	p, err := validateFeedback(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "" || p.Email != "" {
		t.Fatal("unexpected identifying fields")
	}
	in.IncludeVersion = true
	p, err = validateFeedback(in)
	if err != nil || p.Version != Version {
		t.Fatal("opt-in version missing")
	}
	for _, mutate := range []func(*feedbackInput){func(i *feedbackInput) { i.Consent = false }, func(i *feedbackInput) { i.RequestID = "bad" }, func(i *feedbackInput) { i.Email = "Name <user@example.com>" }, func(i *feedbackInput) { i.Message = strings.Repeat("я", 8001) }, func(i *feedbackInput) { i.Subject = "hello\r\nBcc: test@example.com" }, func(i *feedbackInput) { i.Category = "smtp" }} {
		v := in
		mutate(&v)
		if _, err := validateFeedback(v); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
}
func TestFeedbackDeliveryOnlyExplicitFields(t *testing.T) {
	p := feedbackPayload{RequestID: strings.Repeat("a", 48), Subject: "Question", Message: "Test", Category: "question"}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials or method")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 6 {
			t.Errorf("unexpected payload fields: %v", body)
		}
		for _, k := range []string{"hostname", "ip", "logs", "user", "password"} {
			if _, ok := body[k]; ok {
				t.Errorf("leaked %s", k)
			}
		}
		w.Write([]byte(`{"ok":true,"data":{"id":"abcdef123456abcdef123456"}}`))
	}))
	defer s.Close()
	id, err := deliverFeedback(context.Background(), s.Client(), s.URL, p)
	if err != nil || id != "abcdef123456abcdef123456" {
		t.Fatalf("%s %v", id, err)
	}
}
func TestFeedbackDoesNotExposeRemoteErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("secret smtp password"))
	}))
	defer s.Close()
	_, err := deliverFeedback(context.Background(), s.Client(), s.URL, feedbackPayload{})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
