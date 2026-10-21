// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Импорт данных: подключение к старому серверу по SSH. Старый сервер только читается: на нём
// выполняются команды просмотра, tar и mysqldump, ничего не устанавливается и не изменяется.

// impRunner выполняет команды на старом сервере. Интерфейс нужен, чтобы сканер проверялся тестами
// без настоящего сервера.
type impRunner interface {
	Run(ctx context.Context, cmd string) (string, error)
	Stream(ctx context.Context, cmd string, w io.Writer) error
	Close() error
}

type impSSHConf struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Password    string `json:"password"`
	Key         string `json:"key"`         // закрытый ключ в формате PEM
	Fingerprint string `json:"fingerprint"` // SHA256:… — если задан, ключ сервера должен совпасть
}

type impSSH struct {
	client      *ssh.Client
	fingerprint string
	sudo        bool
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func impDialSSH(ctx context.Context, cf impSSHConf) (*impSSH, error) {
	cf.Host = strings.TrimSpace(cf.Host)
	if cf.Host == "" || strings.ContainsAny(cf.Host, " /\\@") {
		return nil, errf("Укажите адрес сервера")
	}
	if cf.Port == 0 {
		cf.Port = 22
	}
	if cf.Port < 1 || cf.Port > 65535 {
		return nil, errf("Порт SSH от 1 до 65535")
	}
	if cf.User == "" {
		cf.User = "root"
	}
	var auth []ssh.AuthMethod
	if strings.TrimSpace(cf.Key) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cf.Key))
		if err != nil {
			return nil, errf("Не удалось прочитать ключ SSH (поддерживаются ключи без пароля): %v", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if cf.Password != "" {
		auth = append(auth, ssh.Password(cf.Password), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			out := make([]string, len(qs))
			for i := range qs {
				out[i] = cf.Password
			}
			return out, nil
		}))
	}
	if len(auth) == 0 {
		return nil, errf("Укажите пароль или ключ SSH")
	}
	var got string
	conf := &ssh.ClientConfig{
		User: cf.User, Auth: auth, Timeout: 20 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = ssh.FingerprintSHA256(key)
			if cf.Fingerprint != "" && cf.Fingerprint != got {
				return errf("Ключ сервера изменился (%s вместо %s). Подключение отменено", got, cf.Fingerprint)
			}
			return nil
		},
	}
	addr := net.JoinHostPort(cf.Host, strconv.Itoa(cf.Port))
	d := net.Dialer{Timeout: 20 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, errf("Не удалось подключиться к %s: %v", addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, conf)
	if err != nil {
		conn.Close()
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errf("Сервер %s отклонил логин или пароль SSH", cf.Host)
		}
		return nil, errf("SSH: %v", err)
	}
	s := &impSSH{client: ssh.NewClient(cc, chans, reqs), fingerprint: got}
	// Не root: команды выполняются через sudo без пароля, если он настроен.
	if out, err := s.Run(ctx, "id -u"); err != nil {
		s.Close()
		return nil, err
	} else if strings.TrimSpace(out) != "0" {
		if _, err := s.Run(ctx, "sudo -n true"); err != nil {
			s.Close()
			return nil, errf("Для переноса нужен root или пользователь с sudo без пароля: войдите под root")
		}
		s.sudo = true
	}
	return s, nil
}

func (s *impSSH) wrap(cmd string) string {
	if s.sudo {
		return "sudo -n sh -c " + shQuote(cmd)
	}
	return cmd
}

func (s *impSSH) Run(ctx context.Context, cmd string) (string, error) {
	var out, errb bytes.Buffer
	if err := s.exec(ctx, cmd, &out, &errb); err != nil {
		return out.String(), errf("%s", firstLine(strings.TrimSpace(errb.String()+" "+err.Error())))
	}
	return out.String(), nil
}

func (s *impSSH) Stream(ctx context.Context, cmd string, w io.Writer) error {
	var errb bytes.Buffer
	if err := s.exec(ctx, cmd, w, &errb); err != nil {
		return errf("%s", firstLine(strings.TrimSpace(errb.String()+" "+err.Error())))
	}
	return nil
}

func (s *impSSH) exec(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdout, sess.Stderr = stdout, stderr
	if !strings.HasPrefix(cmd, "id -u") && !strings.HasPrefix(cmd, "sudo -n true") {
		cmd = s.wrap(cmd)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		sess.Close()
		return errf("Операция остановлена")
	}
}

func (s *impSSH) Close() error { return s.client.Close() }

func (s *impSSH) Fingerprint() string { return s.fingerprint }

var _ impRunner = (*impSSH)(nil)

func impFmtSize(n int64) string { return fmtGB(n) }
