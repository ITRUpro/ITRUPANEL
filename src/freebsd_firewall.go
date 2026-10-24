// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Брандмауэр на FreeBSD: pf. Правила панели лежат в отдельном якоре (anchor) itrupanel_fw, остальные
// правила pf.conf не затрагиваются. Правила идут по порядку с quick: первое подошедшее решает.

const bsdPFAnchor = "itrupanel_fw"
const bsdPFConf = "/etc/pf.conf"

func fwPFMatch(r FirewallRule, source string) string {
	src := "any"
	if source != "*" {
		src = source
	}
	proto := ""
	switch r.Protocol {
	case "tcp", "udp":
		port := strings.ReplaceAll(r.Port, "-", ":")
		return fmt.Sprintf("proto %s from %s to any port %s", r.Protocol, src, port)
	case "icmp":
		proto = "proto icmp "
	case "icmpv6":
		proto = "proto icmp6 "
	}
	return fmt.Sprintf("%sfrom %s to any", proto, src)
}

func fwScriptPF(state FirewallState, protected []int) (string, error) {
	var b strings.Builder
	b.WriteString("# ITRUPANEL: правила брандмауэра (якорь " + bsdPFAnchor + "). Файл перезаписывается панелью.\n")
	if !state.Enabled {
		return b.String(), nil
	}
	ports := []string{}
	for _, p := range protected {
		if p < 1 || p > 65535 {
			return "", errf("Неверный защищённый порт")
		}
		ports = append(ports, strconv.Itoa(p))
	}
	if len(ports) == 0 {
		return "", errf("Не определены порты управления")
	}
	b.WriteString("pass in quick on lo0 all\n")
	fmt.Fprintf(&b, "pass in quick proto tcp from any to any port { %s } keep state\n", strings.Join(ports, ", "))
	b.WriteString("pass in quick inet6 proto icmp6 icmp6-type { routersol routeradv neighbrsol neighbradv }\n")
	b.WriteString("pass in quick proto udp from any port 67 to any port 68\npass in quick inet6 proto udp from any port 547 to any port 546\n")
	for i, raw := range state.Rules {
		r, e := normalizeFWRule(raw)
		if e != nil {
			return "", fmt.Errorf("Правило %d: %w", i+1, e)
		}
		if !r.Enabled {
			continue
		}
		fmt.Fprintf(&b, "# правило %d: %s\n", i+1, strings.NewReplacer("\n", " ", "\r", " ").Replace(r.Name))
		emit := func(src, verdict string) {
			if verdict == "pass" {
				fmt.Fprintf(&b, "pass in quick %s keep state\n", fwPFMatch(r, src))
			} else {
				fmt.Fprintf(&b, "block drop in quick %s\n", fwPFMatch(r, src))
			}
		}
		switch r.Action {
		case "allow":
			emit(r.Source, "pass")
		case "deny":
			emit(r.Source, "block")
		case "allow_selected":
			for _, p := range r.Addresses {
				emit(p, "pass")
			}
			emit(r.Source, "block")
		case "deny_selected":
			for _, p := range r.Addresses {
				emit(p, "block")
			}
			emit(r.Source, "pass")
		}
	}
	return b.String(), nil
}

func fwPFApply(script string, check bool) error {
	args := []string{"-a", bsdPFAnchor, "-f", "-"}
	if check {
		args = append([]string{"-n"}, args...)
	}
	if strings.Count(script, "\n") <= 1 && !check {
		// пустой набор: просто очищаем якорь
		_, e := runTimeout(15*time.Second, "", "pfctl", "-a", bsdPFAnchor, "-F", "rules")
		return e
	}
	_, e := runTimeout(15*time.Second, script, "pfctl", args...)
	return e
}

// bsdFirewallPrepare включает pf и добавляет якорь панели в pf.conf (остальные правила не меняются).
func bsdFirewallPrepare() error {
	conf := readFile(bsdPFConf)
	anchor := "anchor \"" + bsdPFAnchor + "\"\nload anchor \"" + bsdPFAnchor + "\" from \"" + fwConfig + "\"\n"
	if !strings.Contains(conf, "anchor \""+bsdPFAnchor+"\"") {
		if conf != "" && !fileExists(bsdPFConf+".itrupanel-orig") {
			writeFile(bsdPFConf+".itrupanel-orig", []byte(conf), 0644)
		}
		if conf != "" && !strings.HasSuffix(conf, "\n") {
			conf += "\n"
		}
		if err := writeFile(bsdPFConf, []byte(conf+"# ITRUPANEL\n"+anchor), 0600); err != nil {
			return err
		}
	}
	// якорь f2b/* нужен fail2ban: он блокирует адреса правилами внутри него
	if c := readFile(bsdPFConf); !strings.Contains(c, "anchor \"f2b/*\"") {
		if err := writeFile(bsdPFConf, []byte(strings.TrimRight(c, "\n")+"\nanchor \"f2b/*\"\n"), 0600); err != nil {
			return err
		}
	}
	if _, e := run("pfctl", "-n", "-f", bsdPFConf); e != nil {
		return errf("pf.conf не прошёл проверку: %s", e)
	}
	if _, e := run("sysrc", "pf_enable=YES", "pflog_enable=YES"); e != nil {
		return e
	}
	return nil
}

// bsdFirewallStart запускает pf после того, как правила панели записаны в файл.
func bsdFirewallStart() error {
	if out, _ := run("pfctl", "-s", "info"); strings.Contains(out, "Status: Enabled") {
		_, e := run("pfctl", "-f", bsdPFConf)
		return e
	}
	_, e := run("sh", "-c", "service pf onestart </dev/null >/dev/null 2>&1")
	return e
}

func fwInstallBSD(j *Job) error {
	jobStage(j, "Подготовка pf и якоря панели")
	mu.Lock()
	defer mu.Unlock()
	// файл правил должен существовать до проверки pf.conf: в нём есть load anchor … from файл
	empty, _ := fwScript(FirewallState{}, nil)
	if e := writeFile(fwConfig, []byte(empty), 0600); e != nil {
		return e
	}
	if err := bsdFirewallPrepare(); err != nil {
		return err
	}
	if e := os.WriteFile(fwMarker(), []byte("ITRUPANEL\n"), 0600); e != nil {
		return e
	}
	if e := bsdFirewallStart(); e != nil {
		return e
	}
	next := fwCopy(store.Firewall)
	next.Ready = true
	next.Enabled = false
	next.Revision++
	old := store.Firewall
	store.Firewall = next
	if e := saveChecked(); e != nil {
		store.Firewall = old
		return e
	}
	return nil
}
