// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The accepted document is bundled with the executable: no external page can
// change what the user agrees to or prevent first login while offline.
//
//go:embed TERMS.txt
var termsDocument string

const termsRevision = "2.0"

var termsDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(termsDocument)))

type TermsAcceptance struct {
	Revision     string    `json:"revision"`
	SHA256       string    `json:"sha256"`
	AcceptedAt   time.Time `json:"accepted_at"`
	PanelVersion string    `json:"panel_version"`
	Language     string    `json:"language"`
}

func termsAcceptedLocked(user string) bool {
	a, ok := store.TermsAcceptance[user]
	// Consent given to the earlier free-use terms (edition 1.0) stays valid: version 0.2.1
	// replaced them with the GPL-3.0, which grants more rights and needs no new acceptance.
	legacy := a.Revision == "1.0" && (a.SHA256 == "cf8e463d91e56c86ace48bba7e4378286c8f7b4c24306442c7c9023a0d964a78" || a.SHA256 == "7d4220e0a0301f7b9a94ad9df79862884334d536739e9a380b3b82e4131b5245")
	current := a.Revision == termsRevision && a.SHA256 == termsDigest
	return ok && (current || legacy) && !a.AcceptedAt.IsZero()
}
func termsAccepted(user string) bool {
	mu.Lock()
	defer mu.Unlock()
	return termsAcceptedLocked(user)
}
func requireTerms(w http.ResponseWriter, user string) bool {
	if termsAccepted(user) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "Для работы с панелью необходимо принять условия использования", "code": "terms_required"})
	return false
}
func registerTerms() {
	api("terms.get", func(q *Req) (any, error) {
		lang := requestLanguage(q.R)
		text := strings.TrimSpace(strings.SplitN(termsDocument, "ENGLISH TRANSLATION", 2)[0])
		if lang == "en" {
			parts := strings.SplitN(termsDocument, "ENGLISH TRANSLATION", 2)
			if len(parts) == 2 {
				text = "ENGLISH TRANSLATION" + parts[1]
			}
		}
		return map[string]any{"revision": termsRevision, "sha256": termsDigest, "text": text, "language": lang}, nil
	})
	apiOp("terms.accept", "Принятие условий использования", func(q *Req) (any, error) {
		var in struct {
			Agree    bool   `json:"agree"`
			Revision string `json:"revision"`
			SHA256   string `json:"sha256"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		if !in.Agree {
			return nil, errf("Подтвердите согласие с условиями использования")
		}
		if in.Revision != termsRevision || in.SHA256 != termsDigest {
			return nil, errf("Условия изменились. Обновите страницу и ознакомьтесь с текущей редакцией")
		}
		mu.Lock()
		defer mu.Unlock()
		if termsAcceptedLocked(q.S.User) {
			return map[string]any{"accepted": true}, nil
		}
		old, existed := store.TermsAcceptance[q.S.User]
		if store.TermsAcceptance == nil {
			store.TermsAcceptance = map[string]TermsAcceptance{}
		}
		store.TermsAcceptance[q.S.User] = TermsAcceptance{termsRevision, termsDigest, time.Now().UTC(), Version, requestLanguage(q.R)}
		if err := saveChecked(); err != nil {
			if existed {
				store.TermsAcceptance[q.S.User] = old
			} else {
				delete(store.TermsAcceptance, q.S.User)
			}
			return nil, errf("Не удалось сохранить согласие. Повторите попытку")
		}
		return map[string]any{"accepted": true}, nil
	})
}
