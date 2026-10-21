// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const feedbackEndpoint = "https://itrupanel.com/feedback/api.php"

var feedbackRequestID = regexp.MustCompile(`^[a-f0-9-]{32,64}$`)

type feedbackInput struct {
	RequestID      string `json:"request_id"`
	Subject        string `json:"subject"`
	Message        string `json:"message"`
	Email          string `json:"email"`
	Category       string `json:"category"`
	IncludeVersion bool   `json:"include_version"`
	Consent        bool   `json:"consent"`
}
type feedbackPayload struct {
	RequestID string `json:"request_id"`
	Subject   string `json:"subject"`
	Message   string `json:"message"`
	Email     string `json:"email"`
	Category  string `json:"category"`
	Version   string `json:"version"`
}

func validateFeedback(in feedbackInput) (feedbackPayload, error) {
	out := feedbackPayload{RequestID: in.RequestID, Subject: strings.TrimSpace(in.Subject), Message: strings.TrimSpace(in.Message), Email: strings.TrimSpace(in.Email), Category: in.Category}
	if !in.Consent {
		return out, errf("Подтвердите отправку обращения разработчику.")
	}
	if !feedbackRequestID.MatchString(out.RequestID) {
		return out, errf("Обновите форму обращения и повторите.")
	}
	for _, f := range []struct {
		s        string
		max      int
		required bool
	}{{out.Subject, 160, true}, {out.Message, 8000, true}, {out.Email, 254, false}} {
		if !utf8.ValidString(f.s) || utf8.RuneCountInString(f.s) > f.max || (f.required && f.s == "") {
			return out, errf("Проверьте тему, сообщение и e-mail.")
		}
		for _, r := range f.s {
			if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
				return out, errf("Проверьте тему, сообщение и e-mail.")
			}
		}
	}
	if strings.ContainsAny(out.Subject, "\r\n") {
		return out, errf("Проверьте тему, сообщение и e-mail.")
	}
	if out.Email != "" {
		a, e := mail.ParseAddress(out.Email)
		if e != nil || a.Address != out.Email {
			return out, errf("Проверьте тему, сообщение и e-mail.")
		}
	}
	if out.Category != "bug" && out.Category != "question" && out.Category != "idea" {
		return out, errf("Выберите категорию обращения.")
	}
	if in.IncludeVersion {
		out.Version = Version
	}
	return out, nil
}
func deliverFeedback(ctx context.Context, client *http.Client, endpoint string, p feedbackPayload) (string, error) {
	body, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errf("Сервис обратной связи временно недоступен. Повторите позже.")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ITRUPANEL-Feedback")
	resp, err := client.Do(req)
	if err != nil {
		return "", errf("Не удалось подтвердить отправку. Повторите с той же формой: дубликат не будет создан.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return "", errf("Слишком много сообщений. Повторите через час.")
	}
	if resp.StatusCode != http.StatusOK {
		return "", errf("Сервис обратной связи временно недоступен. Повторите позже.")
	}
	var result struct {
		OK   bool `json:"ok"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result) != nil || !result.OK || !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(result.Data.ID) {
		return "", errf("Не удалось подтвердить отправку. Повторите с той же формой: дубликат не будет создан.")
	}
	return result.Data.ID, nil
}
func registerFeedback() {
	// No operation logging: user-authored content and email must not enter the panel journal.
	api("feedback.send", func(q *Req) (any, error) {
		if cfg.Dry {
			return nil, errf("В деморежиме обращения не отправляются.")
		}
		var in feedbackInput
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		p, err := validateFeedback(in)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		id, err := deliverFeedback(q.R.Context(), client, feedbackEndpoint, p)
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id}, nil
	})
}
