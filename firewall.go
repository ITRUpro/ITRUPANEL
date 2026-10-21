// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const fwTable = "itrupanel_fw"
const fwConfig = "/etc/itrupanel/firewall.nft"
const fwUnit = "/etc/systemd/system/itrupanel-firewall.service"
const fwUnitName = "itrupanel-firewall.service"

var fwMu sync.Mutex

type FirewallRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Action    string   `json:"action"`
	Protocol  string   `json:"protocol"`
	Port      string   `json:"port"`
	Source    string   `json:"source"`
	Addresses []string `json:"addresses"`
	Enabled   bool     `json:"enabled"`
}
type FirewallState struct {
	Ready    bool           `json:"ready"`
	Enabled  bool           `json:"enabled"`
	Revision uint64         `json:"revision"`
	Rules    []FirewallRule `json:"rules"`
}

func fwCopy(s FirewallState) FirewallState {
	s.Rules = append([]FirewallRule{}, s.Rules...)
	for i := range s.Rules {
		s.Rules[i].Addresses = append([]string{}, s.Rules[i].Addresses...)
	}
	return s
}
func fwPorts(raw string) (int, int, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(strings.ReplaceAll(raw, ":", "-"), "-")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, errf("Порт: число 1–65535 или диапазон 8000-8100")
	}
	a, e := strconv.Atoi(parts[0])
	if e != nil || a < 1 || a > 65535 {
		return 0, 0, errf("Порт должен быть от 1 до 65535")
	}
	b := a
	if len(parts) == 2 {
		b, e = strconv.Atoi(parts[1])
		if e != nil || b < a || b > 65535 {
			return 0, 0, errf("Неверный диапазон портов")
		}
	}
	return a, b, nil
}
func fwPrefix(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if a, e := netip.ParseAddr(raw); e == nil && a.Zone() == "" {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, e := netip.ParsePrefix(raw)
	if e != nil || p.Addr().Is4In6() {
		return netip.Prefix{}, errf("Неверный IP-адрес или сеть: %s", raw)
	}
	return p.Masked(), nil
}
func normalizeFWRule(r FirewallRule) (FirewallRule, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 160 || strings.ContainsAny(r.Name, "\r\n\x00") {
		return r, errf("Название: от 1 до 160 байт, без переноса строк")
	}
	switch r.Action {
	case "allow", "deny", "allow_selected", "deny_selected":
	default:
		return r, errf("Выберите действие правила")
	}
	switch r.Protocol {
	case "tcp", "udp":
		a, b, e := fwPorts(r.Port)
		if e != nil {
			return r, e
		}
		r.Port = strconv.Itoa(a)
		if a != b {
			r.Port += "-" + strconv.Itoa(b)
		}
	case "icmp", "icmpv6", "any":
		if strings.TrimSpace(r.Port) != "" {
			return r, errf("Для выбранного протокола порт не указывается")
		}
	default:
		return r, errf("Протокол: TCP, UDP, ICMP, ICMPv6 или все")
	}
	r.Source = strings.TrimSpace(r.Source)
	var scope netip.Prefix
	if r.Source == "" || r.Source == "0.0.0.0" || r.Source == "*" {
		r.Source = "*"
	} else {
		p, e := fwPrefix(r.Source)
		if e != nil {
			return r, e
		}
		scope = p
		r.Source = p.String()
		if (r.Protocol == "icmp" && !p.Addr().Is4()) || (r.Protocol == "icmpv6" && p.Addr().Is4()) {
			return r, errf("Семейство IP не соответствует протоколу ICMP")
		}
	}
	if r.Action == "allow_selected" || r.Action == "deny_selected" {
		if len(r.Addresses) == 0 || len(r.Addresses) > 64 {
			return r, errf("Укажите от 1 до 64 IP-адресов или сетей")
		}
		seen := map[string]bool{}
		list := []string{}
		for _, raw := range r.Addresses {
			p, e := fwPrefix(raw)
			if e != nil {
				return r, e
			}
			if scope.IsValid() && (!scope.Contains(p.Addr()) || p.Bits() < scope.Bits()) {
				return r, errf("Адрес %s находится вне сети источника %s", raw, r.Source)
			}
			if (r.Protocol == "icmp" && !p.Addr().Is4()) || (r.Protocol == "icmpv6" && p.Addr().Is4()) {
				return r, errf("Адрес %s не соответствует протоколу", raw)
			}
			if !seen[p.String()] {
				seen[p.String()] = true
				list = append(list, p.String())
			}
		}
		r.Addresses = list
	} else {
		r.Addresses = []string{}
	}
	return r, nil
}
func fwMatch(r FirewallRule, source string) string {
	var words []string
	if source != "*" {
		p, _ := netip.ParsePrefix(source)
		family := "ip6"
		if p.Addr().Is4() {
			family = "ip"
		}
		words = append(words, family, "saddr", source)
	}
	switch r.Protocol {
	case "tcp", "udp":
		words = append(words, r.Protocol, "dport", r.Port)
	case "icmp":
		words = append(words, "meta", "l4proto", "icmp")
	case "icmpv6":
		words = append(words, "meta", "l4proto", "ipv6-icmp")
	}
	return strings.Join(words, " ")
}
func fwScript(state FirewallState, protected []int) (string, error) {
	var b strings.Builder
	// A single nft transaction replaces only the dedicated table, never the ruleset.
	fmt.Fprintf(&b, "add table inet %s\ndelete table inet %s\n", fwTable, fwTable)
	if !state.Enabled {
		return b.String(), nil
	}
	fmt.Fprintf(&b, "table inet %s {\n chain input {\n type filter hook input priority 10; policy accept;\n iifname \"lo\" accept\n ct state established,related accept\n", fwTable)
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
	fmt.Fprintf(&b, " tcp dport { %s } accept comment \"ITRUPANEL management\"\n", strings.Join(ports, ", "))
	b.WriteString(" meta l4proto ipv6-icmp icmpv6 type { nd-router-solicit, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept\n")
	b.WriteString(" ip protocol udp udp sport 67 udp dport 68 accept\n meta nfproto ipv6 udp sport 547 udp dport 546 accept\n")
	for i, raw := range state.Rules {
		r, e := normalizeFWRule(raw)
		if e != nil {
			return "", fmt.Errorf("Правило %d: %w", i+1, e)
		}
		if !r.Enabled {
			continue
		}
		emit := func(src, verdict string) {
			fmt.Fprintf(&b, " %s counter %s comment \"ITRUPANEL rule %d\"\n", fwMatch(r, src), verdict, i+1)
		}
		switch r.Action {
		case "allow":
			emit(r.Source, "accept")
		case "deny":
			emit(r.Source, "drop")
		case "allow_selected":
			for _, p := range r.Addresses {
				emit(p, "accept")
			}
			emit(r.Source, "drop")
		case "deny_selected":
			for _, p := range r.Addresses {
				emit(p, "drop")
			}
			emit(r.Source, "accept")
		}
	}
	b.WriteString(" }\n}\n")
	return b.String(), nil
}
func fwProtectedPorts() ([]int, error) {
	ports := map[int]bool{22: true, 3333: true}
	if _, s, e := net.SplitHostPort(cfg.Listen); e == nil {
		if p, e := strconv.Atoi(s); e == nil && p > 0 && p <= 65535 {
			ports[p] = true
		}
	}
	if !cfg.Dry && have("sshd") {
		out, e := runTimeout(10*time.Second, "", "sshd", "-T")
		if e != nil {
			return nil, errf("Не удалось определить порты SSH: %v", e)
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "port" {
				p, e := strconv.Atoi(f[1])
				if e == nil && p > 0 && p <= 65535 {
					ports[p] = true
					found = true
				}
			}
		}
		if !found {
			return nil, errf("sshd -T не вернул порт SSH; правила не применены")
		}
	}
	// Socket activation can bind ports different from sshd_config, even
	// before sshd starts and appears in ss process output.
	if !cfg.Dry && have("systemctl") {
		for _, unit := range []string{"ssh.socket", "sshd.socket"} {
			out, e := runTimeout(5*time.Second, "", "systemctl", "show", unit, "--property=Listen", "--property=LoadState")
			if strings.Contains(out, "LoadState=not-found") {
				continue
			}
			if e != nil {
				return nil, fmt.Errorf("Проверка сокета SSH: %w", e)
			}
			listen := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "Listen=") {
					listen = strings.TrimPrefix(line, "Listen=")
				}
			}
			fields := strings.Fields(listen)
			for i, f := range fields {
				if f != "(Stream)" || i == 0 {
					continue
				}
				address := fields[i-1]
				port := address
				if _, p, e := net.SplitHostPort(address); e == nil {
					port = p
				}
				n, e := strconv.Atoi(port)
				if e != nil || n < 1 || n > 65535 {
					return nil, errf("Не удалось определить порт %s: %s", unit, address)
				}
				ports[n] = true
			}
		}
	}
	if !cfg.Dry && have("ss") {
		out, e := runTimeout(10*time.Second, "", "ss", "-H", "-lntp")
		if e != nil {
			return nil, e
		}
		for _, line := range strings.Split(out, "\n") {
			if !strings.Contains(line, "sshd") {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			_, s, e := net.SplitHostPort(f[3])
			if e == nil {
				p, e := strconv.Atoi(s)
				if e == nil && p > 0 && p <= 65535 {
					ports[p] = true
				}
			}
		}
	}
	list := []int{}
	for p := range ports {
		list = append(list, p)
	}
	sort.Ints(list)
	return list, nil
}
func fwMarker() string { return filepath.Join(cfg.Home, "etc", "firewall-owned") }
func fwExists() (bool, error) {
	if cfg.Dry {
		return store.Firewall.Enabled, nil
	}
	out, e := runTimeout(10*time.Second, "", "nft", "-j", "list", "tables")
	if e != nil {
		return false, e
	}
	var doc struct {
		NFTables []struct {
			Table *struct{ Name, Family string } `json:"table"`
		} `json:"nftables"`
	}
	if e = json.Unmarshal([]byte(out), &doc); e != nil {
		return false, e
	}
	for _, v := range doc.NFTables {
		if v.Table != nil && v.Table.Name == fwTable && v.Table.Family == "inet" {
			return true, nil
		}
	}
	return false, nil
}
func fwSnapshot() (string, error) {
	exists, e := fwExists()
	if e != nil {
		return "", e
	}
	remove, _ := fwScript(FirewallState{}, nil)
	if !exists {
		return remove, nil
	}
	if _, e := os.Stat(fwMarker()); e != nil {
		return "", errf("Таблица %s уже существует и не принадлежит панели", fwTable)
	}
	if cfg.Dry {
		p, _ := fwProtectedPorts()
		return fwScript(store.Firewall, p)
	}
	out, e := runTimeout(10*time.Second, "", "nft", "-s", "list", "table", "inet", fwTable)
	if e != nil {
		return "", e
	}
	return remove + out + "\n", nil
}
func fwNft(script string, check bool) error {
	args := []string{"-f", "-"}
	if check {
		args = append([]string{"-c"}, args...)
	}
	_, e := runTimeout(15*time.Second, script, "nft", args...)
	return e
}

// fwMu and mu are held by callers. Restore both runtime rules and the boot file
// if persistence fails. All unrelated firewall tables stay intact.
func fwApplyLocked(next FirewallState) error {
	if !store.Firewall.Ready {
		return errf("Сначала установите модуль брандмауэра")
	}
	var ports []int
	var e error
	if next.Enabled {
		ports, e = fwProtectedPorts()
		if e != nil {
			return e
		}
	}
	script, e := fwScript(next, ports)
	if e != nil {
		return e
	}
	previous, e := fwSnapshot()
	if e != nil {
		return e
	}
	if e = fwNft(script, true); e != nil {
		return fmt.Errorf("Проверка правил: %w", e)
	}
	oldFile, fileErr := os.ReadFile(P(fwConfig))
	if fileErr != nil && !os.IsNotExist(fileErr) {
		return fileErr
	}
	if e = fwNft(script, false); e != nil {
		return fmt.Errorf("Применение правил: %w", e)
	}
	old := store.Firewall
	rollback := func(cause error) error {
		store.Firewall = old
		var fileRollback error
		if os.IsNotExist(fileErr) {
			fileRollback = os.Remove(P(fwConfig))
			if os.IsNotExist(fileRollback) {
				fileRollback = nil
			}
		} else {
			fileRollback = writeFile(fwConfig, oldFile, 0600)
		}
		runtimeRollback := fwNft(previous, false)
		if fileRollback != nil || runtimeRollback != nil {
			return fmt.Errorf("%w; откат файла: %v; откат правил: %v", cause, fileRollback, runtimeRollback)
		}
		return cause
	}
	if e = writeFile(fwConfig, []byte(script), 0600); e != nil {
		return rollback(e)
	}
	next.Revision = old.Revision + 1
	store.Firewall = next
	if e = saveChecked(); e != nil {
		return rollback(e)
	}
	return nil
}
func fwInstall(j *Job) error {
	fwMu.Lock()
	defer fwMu.Unlock()
	if !cfg.Dry && !osInfo.Systemd {
		return errf("Модуль требует Linux с systemd")
	}
	mu.Lock()
	ready := store.Firewall.Ready
	mu.Unlock()
	if ready {
		return nil
	}
	if !have("nft") {
		jobStage(j, "Установка nftables")
		phpInstallMu.Lock()
		defer phpInstallMu.Unlock()
		// Prevent the distribution service from loading its default global ruleset.
		before, _ := run("systemctl", "is-enabled", "nftables.service")
		wasMasked := strings.HasPrefix(strings.TrimSpace(before), "masked")
		if !wasMasked {
			if _, e := run("systemctl", "mask", "nftables.service"); e != nil {
				return e
			}
			defer run("systemctl", "unmask", "nftables.service")
		}
		if e := installPackagesJob(j, "nftables"); e != nil {
			return e
		}
	}
	jobStage(j, "Подготовка отдельной таблицы и автозагрузки")
	mu.Lock()
	defer mu.Unlock()
	exists, e := fwExists()
	if e != nil {
		return e
	}
	if exists {
		return errf("Таблица %s уже существует при ненастроенном модуле. Правила не изменены; требуется проверка администратора", fwTable)
	}
	for _, path := range []string{fwUnit, fwConfig} {
		if _, e = os.Stat(P(path)); e == nil {
			if _, owned := os.Stat(fwMarker()); owned != nil {
				return errf("Файл %s уже существует; конфигурация не изменена", path)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if e = os.WriteFile(fwMarker(), []byte("ITRUPANEL\n"), 0600); e != nil {
		return e
	}
	empty, _ := fwScript(FirewallState{}, nil)
	if e = fwNft(empty, true); e != nil {
		return fmt.Errorf("Проверка доступа к nftables: %w", e)
	}
	if e = writeFile(fwConfig, []byte(empty), 0600); e != nil {
		return e
	}
	nftPath := "/usr/sbin/nft"
	if !cfg.Dry {
		p, e := exec.LookPath("nft")
		if e != nil {
			return e
		}
		nftPath = p
	}
	unit := fmt.Sprintf(`[Unit]
Description=ITRUPANEL firewall rules
After=network-pre.target nftables.service ufw.service firewalld.service
Before=network.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s -f %s

[Install]
WantedBy=multi-user.target
`, nftPath, fwConfig)
	if e = writeFile(fwUnit, []byte(unit), 0644); e != nil {
		return e
	}
	if _, e = run("systemctl", "daemon-reload"); e != nil {
		return e
	}
	if _, e = run("systemctl", "enable", fwUnitName); e != nil {
		return e
	}
	next := fwCopy(store.Firewall)
	next.Ready = true
	next.Enabled = false
	next.Revision++
	old := store.Firewall
	store.Firewall = next
	if e = saveChecked(); e != nil {
		store.Firewall = old
		run("systemctl", "disable", fwUnitName)
		return e
	}
	return nil
}
func fwChange(revision uint64, fn func(*FirewallState) error) error {
	fwMu.Lock()
	defer fwMu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if store.Firewall.Revision != revision {
		return errf("Правила изменились в другой вкладке. Обновите список и повторите")
	}
	next := fwCopy(store.Firewall)
	if e := fn(&next); e != nil {
		return e
	}
	return fwApplyLocked(next)
}
func registerFirewall() {
	api("firewall.list", func(q *Req) (any, error) {
		fwMu.Lock()
		defer fwMu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		s := fwCopy(store.Firewall)
		ports, guardErr := fwProtectedPorts()
		active := false
		runtimeErr := ""
		if s.Ready {
			var e error
			active, e = fwExists()
			if e != nil {
				runtimeErr = e.Error()
			}
		}
		if guardErr != nil {
			runtimeErr = guardErr.Error()
		}
		other := []string{}
		if !cfg.Dry {
			if have("ufw") {
				out, _ := runTimeout(5*time.Second, "", "ufw", "status")
				if strings.Contains(out, "Status: active") {
					other = append(other, "UFW")
				}
			}
			if have("firewall-cmd") {
				out, _ := runTimeout(5*time.Second, "", "firewall-cmd", "--state")
				if strings.TrimSpace(out) == "running" {
					other = append(other, "firewalld")
				}
			}
		}
		return map[string]any{"state": s, "active": active, "protected_ports": ports, "other_firewalls": other, "error": runtimeErr}, nil
	})
	apiOp("firewall.install", "Брандмауэр: установка модуля", func(q *Req) (any, error) {
		id := startJob("firewall:install", "Настройка брандмауэра", q.S.User, func(j *Job) (any, error) {
			e := fwInstall(j)
			return map[string]string{"info": "Модуль готов. Добавьте правила и включите фильтрацию."}, e
		})
		return map[string]string{"job": id}, nil
	})
	apiOp("firewall.save", "Брандмауэр: сохранение правила", func(q *Req) (any, error) {
		var in struct {
			Rule     FirewallRule
			Revision uint64
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		r, e := normalizeFWRule(in.Rule)
		if e != nil {
			return nil, e
		}
		e = fwChange(in.Revision, func(s *FirewallState) error {
			if r.ID == "" {
				if len(s.Rules) >= 200 {
					return errf("Максимум 200 правил")
				}
				r.ID = randToken(12)
				s.Rules = append(s.Rules, r)
				return nil
			}
			for i := range s.Rules {
				if s.Rules[i].ID == r.ID {
					s.Rules[i] = r
					return nil
				}
			}
			return errf("Правило не найдено")
		})
		return nil, e
	})
	apiOp("firewall.delete", "Брандмауэр: удаление правила", func(q *Req) (any, error) {
		var in struct {
			ID       string
			Revision uint64
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return nil, fwChange(in.Revision, func(s *FirewallState) error {
			for i, r := range s.Rules {
				if r.ID == in.ID {
					s.Rules = append(s.Rules[:i], s.Rules[i+1:]...)
					return nil
				}
			}
			return errf("Правило не найдено")
		})
	})
	apiOp("firewall.move", "Брандмауэр: порядок правил", func(q *Req) (any, error) {
		var in struct {
			ID        string
			Direction int
			Revision  uint64
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		if in.Direction != 1 && in.Direction != -1 {
			return nil, errf("Неверное направление")
		}
		return nil, fwChange(in.Revision, func(s *FirewallState) error {
			for i, r := range s.Rules {
				if r.ID == in.ID {
					j := i + in.Direction
					if j < 0 || j >= len(s.Rules) {
						return errf("Правило уже на краю списка")
					}
					s.Rules[i], s.Rules[j] = s.Rules[j], s.Rules[i]
					return nil
				}
			}
			return errf("Правило не найдено")
		})
	})
	apiOp("firewall.enable", "Брандмауэр: состояние фильтрации", func(q *Req) (any, error) {
		var in struct {
			Enabled  bool
			Revision uint64
		}
		if e := q.Bind(&in); e != nil {
			return nil, e
		}
		return nil, fwChange(in.Revision, func(s *FirewallState) error { s.Enabled = in.Enabled; return nil })
	})
	api("firewall.preview", func(q *Req) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		ports, e := fwProtectedPorts()
		if e != nil {
			return nil, e
		}
		s := fwCopy(store.Firewall)
		s.Enabled = true
		script, e := fwScript(s, ports)
		return map[string]string{"script": script}, e
	})
}
