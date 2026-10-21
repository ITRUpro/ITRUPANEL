// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunCertbotWaitsForOtherInstance(t *testing.T) {
	oldExec, oldDelay, oldRetries := certbotExec, certbotRetryDelay, certbotRetries
	defer func() { certbotExec, certbotRetryDelay, certbotRetries = oldExec, oldDelay, oldRetries }()
	certbotRetryDelay, certbotRetries = time.Millisecond, 3
	calls := 0
	certbotExec = func(time.Duration, ...string) (string, error) {
		calls++
		if calls < 3 {
			return certbotBusy + ".", errors.New("exit status 1")
		}
		return "ok", nil
	}
	if out, err := runCertbot(time.Second, "renew"); err != nil || out != "ok" || calls != 3 {
		t.Fatalf("busy certbot must be retried: out=%q err=%v calls=%d", out, err, calls)
	}
	calls = 0
	certbotExec = func(time.Duration, ...string) (string, error) {
		calls++
		return certbotBusy, errors.New("exit status 1")
	}
	if out, err := runCertbot(time.Second, "renew"); err == nil || !strings.Contains(out, "pgrep -a certbot") || calls != 4 {
		t.Fatalf("long busy certbot must stop with a hint: out=%q err=%v calls=%d", out, err, calls)
	}
	calls = 0
	certbotExec = func(time.Duration, ...string) (string, error) {
		calls++
		return "Detail: 404", errors.New("exit status 1")
	}
	if out, err := runCertbot(time.Second, "certonly"); err == nil || out != "Detail: 404" || calls != 1 {
		t.Fatalf("other certbot errors must not be retried: out=%q err=%v calls=%d", out, err, calls)
	}
}
