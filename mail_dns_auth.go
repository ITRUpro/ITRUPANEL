// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Проверка почтовых DNS-записей спрашивает NS-серверы домена напрямую, как это видит интернет:
// резолвер сервера отдаёт локальные зоны BIND, кэш и /etc/hosts.

var mailDNSPublicServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

type dnsClient struct{ servers []string }

func dnsNotFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (c dnsClient) exchange(ctx context.Context, server string, q []byte, tcp bool) (*dnsmessage.Message, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	network := "udp"
	if tcp {
		network = "tcp"
	}
	conn, err := d.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	buf := make([]byte, 65535)
	n := 0
	if tcp {
		frame := make([]byte, 2+len(q))
		binary.BigEndian.PutUint16(frame, uint16(len(q)))
		copy(frame[2:], q)
		if _, err = conn.Write(frame); err != nil {
			return nil, err
		}
		var size [2]byte
		if _, err = io.ReadFull(conn, size[:]); err != nil {
			return nil, err
		}
		n = int(binary.BigEndian.Uint16(size[:]))
		if _, err = io.ReadFull(conn, buf[:n]); err != nil {
			return nil, err
		}
	} else {
		if _, err = conn.Write(q); err != nil {
			return nil, err
		}
		if n, err = conn.Read(buf); err != nil {
			return nil, err
		}
	}
	var m dnsmessage.Message
	if err = m.Unpack(buf[:n]); err != nil {
		return nil, err
	}
	return &m, nil
}

// query возвращает ответы нужного типа. NXDOMAIN и пустой ответ — «запись не найдена».
func (c dnsClient) query(ctx context.Context, name string, t dnsmessage.Type) ([]dnsmessage.Resource, error) {
	fqdn := strings.TrimSuffix(name, ".") + "."
	qn, err := dnsmessage.NewName(fqdn)
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{Header: dnsmessage.Header{ID: uint16(rand.Intn(65536)), RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: qn, Type: t, Class: dnsmessage.ClassINET}}}
	q, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	var last error = errors.New("нет DNS-серверов")
	for _, server := range c.servers {
		m, err := c.exchange(ctx, server, q, false)
		if err == nil && m.Truncated {
			m, err = c.exchange(ctx, server, q, true)
		}
		if err != nil {
			last = err
			continue
		}
		switch m.RCode {
		case dnsmessage.RCodeSuccess:
			var out []dnsmessage.Resource
			for _, a := range m.Answers {
				if a.Header.Type == t {
					out = append(out, a)
				}
			}
			if len(out) == 0 {
				return nil, dnsNotFound(name)
			}
			return out, nil
		case dnsmessage.RCodeNameError:
			return nil, dnsNotFound(name)
		default:
			last = errors.New(server + ": " + m.RCode.String())
		}
	}
	return nil, &net.DNSError{Err: last.Error(), Name: name}
}

// mailDNSNet — резолвер проверки: имена внутри зоны домена — у её NS-серверов, остальное и PTR — у публичного DNS.
type mailDNSNet struct {
	zone         string
	auth, public dnsClient
}

func (r mailDNSNet) pick(name string) dnsClient {
	n := dnsHost(name)
	if len(r.auth.servers) > 0 && (n == r.zone || strings.HasSuffix(n, "."+r.zone)) {
		return r.auth
	}
	return r.public
}

func (r mailDNSNet) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	rows, err := r.pick(name).query(ctx, name, dnsmessage.TypeMX)
	if err != nil {
		return nil, err
	}
	var out []*net.MX
	for _, a := range rows {
		if mx, ok := a.Body.(*dnsmessage.MXResource); ok {
			out = append(out, &net.MX{Host: mx.MX.String(), Pref: mx.Pref})
		}
	}
	return out, nil
}

func (r mailDNSNet) LookupIP(ctx context.Context, _ string, name string) ([]net.IP, error) {
	rows, err := r.pick(name).query(ctx, name, dnsmessage.TypeA)
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range rows {
		if v, ok := a.Body.(*dnsmessage.AResource); ok {
			out = append(out, net.IP(v.A[:]))
		}
	}
	return out, nil
}

func (r mailDNSNet) LookupTXT(ctx context.Context, name string) ([]string, error) {
	rows, err := r.pick(name).query(ctx, name, dnsmessage.TypeTXT)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range rows {
		if v, ok := a.Body.(*dnsmessage.TXTResource); ok {
			out = append(out, strings.Join(v.TXT, ""))
		}
	}
	return out, nil
}

func (r mailDNSNet) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	ip := net.ParseIP(addr).To4()
	if ip == nil {
		return nil, dnsNotFound(addr)
	}
	name := net.IPv4(ip[3], ip[2], ip[1], ip[0]).To4().String() + ".in-addr.arpa"
	rows, err := r.public.query(ctx, name, dnsmessage.TypePTR)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range rows {
		if v, ok := a.Body.(*dnsmessage.PTRResource); ok {
			out = append(out, v.PTR.String())
		}
	}
	return out, nil
}

// mailDNSAuthority находит зону домена и адреса её NS-серверов через публичный DNS.
func mailDNSAuthority(ctx context.Context, domain string) mailDNSNet {
	r := mailDNSNet{public: dnsClient{mailDNSPublicServers}}
	labels := strings.Split(dnsHost(domain), ".")
	for i := 0; i < len(labels)-1; i++ {
		zone := strings.Join(labels[i:], ".")
		rows, err := r.public.query(ctx, zone, dnsmessage.TypeNS)
		if err != nil {
			continue
		}
		for _, a := range rows {
			ns, ok := a.Body.(*dnsmessage.NSResource)
			if !ok {
				continue
			}
			ips, err := r.public.query(ctx, ns.NS.String(), dnsmessage.TypeA)
			if err != nil {
				continue
			}
			for _, ip := range ips {
				if v, ok := ip.Body.(*dnsmessage.AResource); ok {
					r.auth.servers = append(r.auth.servers, net.JoinHostPort(net.IP(v.A[:]).String(), "53"))
				}
			}
		}
		r.zone = zone
		return r
	}
	return r
}

// mailDNSNameServers — имена NS-серверов для подписи «где проверено».
func mailDNSNameServers(ctx context.Context, zone string) []string {
	rows, err := dnsClient{mailDNSPublicServers}.query(ctx, zone, dnsmessage.TypeNS)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range rows {
		if ns, ok := a.Body.(*dnsmessage.NSResource); ok {
			out = append(out, dnsHost(ns.NS.String()))
		}
	}
	return out
}

// mailDNSCachedPublicIPv4 — внешний IPv4 из кэша serverIPs (сервер за NAT или с несколькими адресами).
func mailDNSCachedPublicIPv4() string {
	pubIPMu.Lock()
	defer pubIPMu.Unlock()
	found := ""
	for _, s := range pubIPs {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil && mailDNSPublicIPv4(ip) {
			if found != "" && found != s {
				return ""
			}
			found = s
		}
	}
	return found
}
