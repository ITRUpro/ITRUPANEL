// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ftpGuardJail = "itrupanel-ftp"

var ftpGuardPath = "/etc/fail2ban/jail.d/itrupanel-ftp.local"

const ftpGuardHeader = "# Managed by ITRUPANEL login protection\n"

var ftpGuardMu sync.Mutex

// A successful systemctl start only confirms that the process was launched.
// Type=simple units can return before Fail2ban has opened its control socket.
func waitFTPGuard(timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("Fail2ban не ответил за отведённое время: %w", last)
		}
		if remaining > 2*time.Second {
			remaining = 2 * time.Second
		}
		if _, last = runTimeout(remaining, "", "fail2ban-client", "ping"); last == nil {
			return nil
		}
		state := ""
		if !isBSD() {
			state, _ = runTimeout(2*time.Second, "", "systemctl", "is-active", "fail2ban.service")
		}
		if strings.TrimSpace(state) == "failed" {
			return fmt.Errorf("Служба Fail2ban завершилась с ошибкой: %w", last)
		}
		pause := interval
		if left := time.Until(deadline); pause > left {
			pause = left
		}
		if pause > 0 {
			time.Sleep(pause)
		}
	}
}

func ftpGuardStartupError(cause error) error {
	if isBSD() {
		tail, _ := runTimeout(5*time.Second, "", "tail", "-n", "40", "/var/log/fail2ban.log")
		return fmt.Errorf("Не удалось запустить защиту FTP: %w\n%s", cause, strings.TrimSpace(tail))
	}
	state, _ := runTimeout(5*time.Second, "", "systemctl", "show", "fail2ban.service", "--property=ActiveState,SubState,Result,ExecMainStatus")
	journal, _ := runTimeout(5*time.Second, "", "journalctl", "-u", "fail2ban.service", "-n", "40", "--no-pager", "-o", "cat")
	if len(journal) > 12000 {
		journal = journal[len(journal)-12000:]
	}
	return fmt.Errorf("Не удалось запустить защиту FTP: %w\n%s\n%s", cause, strings.TrimSpace(state), strings.TrimSpace(journal))
}

func ftpGuardConfig(port int) string {
	if isBSD() {
		return bsdFTPGuardConfig(port)
	}
	return fmt.Sprintf(`%s[%s]
enabled = true
filter = pure-ftpd
backend = systemd
journalmatch = _SYSTEMD_UNIT=pure-ftpd.service + _COMM=pure-ftpd
port = %d
protocol = tcp
maxretry = 5
findtime = 600
bantime = 900
ignoreip = 127.0.0.0/8 ::1
ignoreself = true
action = nftables-multiport[name=itrupanel-ftp, port="%%(port)s", protocol=tcp, table=itrupanel_f2b, chain=itrupanel_ftp]
`, ftpGuardHeader, ftpGuardJail, port)
}
func ftpGuardStatus() map[string]any {
	ftpGuardMu.Lock()
	defer ftpGuardMu.Unlock()
	d := map[string]any{"active": false, "installed": have("fail2ban-client"), "bans": []string{}, "port": 21}
	b := readFile(ftpGuardPath)
	for _, l := range strings.Split(b, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "port" {
			if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil {
				d["port"] = n
			}
		}
	}
	if cfg.Dry {
		d["active"] = strings.Contains(b, "enabled = true")
		return d
	}
	if !have("fail2ban-client") {
		return d
	}
	out, e := runTimeout(5*time.Second, "", "fail2ban-client", "get", ftpGuardJail, "banip")
	if e != nil {
		d["warning"] = out
		return d
	}
	d["active"] = true
	ips := []string{}
	for _, s := range strings.Fields(out) {
		if a, e := netip.ParseAddr(s); e == nil {
			ips = append(ips, a.String())
		}
	}
	d["bans"] = ips
	return d
}
func ftpNoResolve(conf string) string {
	lines := []string{}
	for _, line := range strings.Split(strings.TrimRight(conf, "\n"), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) > 0 && strings.EqualFold(fields[0], "DontResolve") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\nDontResolve yes\n"
}
func enableFTPGuard(j *Job, port int) (err error) {
	ftpGuardMu.Lock()
	defer ftpGuardMu.Unlock()
	if isBSD() {
		return enableFTPGuardBSD(j, port)
	}
	if !cfg.Dry && (!osInfo.Systemd || !have("pure-pw")) {
		return errf("Требуются Pure-FTPd и systemd")
	}
	old, e := os.ReadFile(P(ftpGuardPath))
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if exists && !strings.HasPrefix(string(old), ftpGuardHeader) {
		return errf("Конфигурация FTP-защиты создана вручную; требуется проверка администратора")
	}
	phpInstallMu.Lock()
	defer phpInstallMu.Unlock()
	jobStage(j, "Проверка и установка Fail2ban")
	// Installing nftables must not load a distribution-wide default ruleset.
	if !have("nft") {
		before, _ := run("systemctl", "is-enabled", "nftables.service")
		if !strings.HasPrefix(strings.TrimSpace(before), "masked") {
			if _, e = run("systemctl", "mask", "nftables.service"); e != nil {
				return e
			}
			defer run("systemctl", "unmask", "nftables.service")
		}
	}
	pkgs := []string{"fail2ban", "nftables", "python3-systemd"}
	if osInfo.Family == "rhel" {
		pkgs = []string{"fail2ban", "nftables", "python3-systemd"}
	}
	if !cfg.Dry {
		if e = installPackagesJob(j, pkgs...); e != nil {
			return e
		}
	}
	// Keep the shipped filter, with numeric addresses in the daemon's journal.
	path := "/etc/pure-ftpd/conf/DontResolve"
	if osInfo.Family == "rhel" {
		path = "/etc/pure-ftpd/pure-ftpd.conf"
	}
	oldFTP, e := os.ReadFile(P(path))
	ftpExists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if osInfo.Family == "rhel" && !ftpExists && !cfg.Dry {
		return fmt.Errorf("Не найден конфигурационный файл Pure-FTPd")
	}
	nextFTP := []byte("yes\n")
	if osInfo.Family == "rhel" {
		nextFTP = []byte(ftpNoResolve(string(oldFTP)))
	}
	ftpChanged := string(oldFTP) != string(nextFTP)
	restore := func(path string, b []byte, exists bool) error {
		if exists {
			return writeFile(path, b, 0644)
		}
		e := os.Remove(P(path))
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	changed := false
	defer func() {
		if err != nil && changed {
			errors := []string{}
			if e := restore(ftpGuardPath, old, exists); e != nil {
				errors = append(errors, e.Error())
			}
			if ftpChanged {
				if e := restore(path, oldFTP, ftpExists); e != nil {
					errors = append(errors, e.Error())
				}
				if _, e := run("systemctl", "restart", "pure-ftpd"); e != nil {
					errors = append(errors, e.Error())
				}
			}
			if exists {
				if _, e := runTimeout(15*time.Second, "", "fail2ban-client", "reload", "--restart", "--if-exists", ftpGuardJail); e != nil {
					errors = append(errors, e.Error())
				}
			} else {
				runTimeout(15*time.Second, "", "fail2ban-client", "stop", ftpGuardJail)
			}
			if len(errors) > 0 {
				err = fmt.Errorf("%w; ошибка отката: %s", err, strings.Join(errors, "; "))
			}
		}
	}()
	changed = true
	if ftpChanged {
		if err = writeFile(path, nextFTP, 0644); err != nil {
			return err
		}
	}
	if err = writeFile(ftpGuardPath, []byte(ftpGuardConfig(port)), 0644); err != nil {
		return err
	}
	jobStage(j, "Проверка конфигурации и запуск защиты")
	if _, err = runTimeout(30*time.Second, "", "fail2ban-client", "-t"); err != nil {
		return err
	}
	if ftpChanged {
		if _, err = run("systemctl", "restart", "pure-ftpd"); err != nil {
			return err
		}
	}
	if _, err = run("systemctl", "enable", "--now", "fail2ban"); err != nil {
		return ftpGuardStartupError(err)
	}
	jobStage(j, "Ожидание готовности Fail2ban")
	if err = waitFTPGuard(30*time.Second, 250*time.Millisecond); err != nil {
		return ftpGuardStartupError(err)
	}
	jobStage(j, "Применение и проверка защиты FTP")
	// --if-exists skips the missing-jail precondition, not the config stream:
	// Fail2ban will create a new jail or restart the existing one. Without it,
	// targeted reload fails after a previous installation was rolled back.
	if _, err = runTimeout(30*time.Second, "", "fail2ban-client", "reload", "--restart", "--if-exists", ftpGuardJail); err != nil {
		return ftpGuardStartupError(err)
	}
	if _, err = runTimeout(10*time.Second, "", "fail2ban-client", "status", ftpGuardJail); err != nil {
		return ftpGuardStartupError(err)
	}
	return nil
}
