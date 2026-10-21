// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

// Signs the exact release payload. Private key stays outside distributable source.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("sign PRIVATE_KEY PAYLOAD EXPECTED_PUBLIC_KEY")
	}
	b, e := os.ReadFile(os.Args[1])
	if e != nil {
		log.Fatal(e)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		log.Fatal("Invalid PEM")
	}
	key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		log.Fatal(e)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok || hex.EncodeToString(priv.Public().(ed25519.PublicKey)) != os.Args[3] {
		log.Fatal("Release signing key does not match trusted public key")
	}
	payload, e := os.ReadFile(os.Args[2])
	if e != nil {
		log.Fatal(e)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))})
}
