// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
)

// Пароли почтовых ящиков хранятся зашифрованными (AES-GCM), чтобы администратор мог скопировать
// их кнопкой в таблице ящиков. Ключ лежит отдельно от state.json: утечка состояния или резервной
// копии настроек пароли не раскрывает. Dovecot по-прежнему проверяет только bcrypt-хеш.

func mailSecretKeyPath() string { return filepath.Join(cfg.Home, "etc", "mail-secret.key") }

func mailSecretKey() ([]byte, error) {
	p := mailSecretKeyPath()
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		// Ключ создан параллельно — читаем его, а не перезаписываем.
		if b, e := os.ReadFile(p); e == nil && len(b) == 32 {
			return b, nil
		}
		return nil, errf("Повреждён ключ паролей почты")
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(p)
		return nil, err
	}
	return key, nil
}

func mailSealPassword(pass string) (string, error) {
	key, err := mailSecretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(pass), nil)), nil
}

func mailOpenPassword(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", errf("Пароль ящика повреждён")
	}
	key, err := mailSecretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errf("Пароль ящика повреждён")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errf("Пароль ящика не расшифровывается: задайте его заново")
	}
	return string(plain), nil
}

func registerMailSecrets() {
	apiOp("mail.box.password", "Почта: копирование пароля ящика", func(q *Req) (any, error) {
		var in struct{ Address string }
		if err := strictBind(q, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		sealed, found := "", false
		for _, b := range store.Mail.Boxes {
			if b.Address == in.Address {
				sealed, found = b.Secret, true
			}
		}
		mu.Unlock()
		if !found {
			return nil, errf("Ящик не найден")
		}
		if sealed == "" {
			return nil, errf("Пароль этого ящика панель не знает: задайте его заново, после этого он будет копироваться")
		}
		pass, err := mailOpenPassword(sealed)
		if err != nil {
			return nil, err
		}
		return map[string]string{"password": pass}, nil
	})
}
