// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A local FTP protocol fixture exercises real curl, not a stub of the scanner.
func discoveryFTPFixture(t *testing.T, files map[string][]byte) (BackupFTP, *[]string, *sync.Mutex) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var calls []string
	var lock sync.Mutex
	var wg sync.WaitGroup
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				reply := func(s string) { fmt.Fprint(c, s+"\r\n") }
				reply("220 Test FTP")
				var data net.Listener
				defer func() {
					if data != nil {
						data.Close()
					}
				}()
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					line := scanner.Text()
					command, arg, _ := strings.Cut(line, " ")
					command = strings.ToUpper(command)
					lock.Lock()
					calls = append(calls, command)
					lock.Unlock()
					switch command {
					case "USER":
						reply("331 Password required")
					case "PASS":
						reply("230 Logged in")
					case "PWD":
						reply(`257 "/"`)
					case "CWD":
						reply("250 Directory changed")
					case "TYPE":
						reply("200 Type set")
					case "EPSV":
						if data != nil {
							data.Close()
						}
						data, e = net.Listen("tcp", "127.0.0.1:0")
						if e != nil {
							return
						}
						reply(fmt.Sprintf("229 Entering Extended Passive Mode (|||%d|)", data.Addr().(*net.TCPAddr).Port))
					case "SIZE":
						if b, ok := files[path.Base(arg)]; ok {
							reply(fmt.Sprintf("213 %d", len(b)))
						} else {
							reply("550 Not found")
						}
					case "NLST", "RETR":
						if data == nil {
							reply("425 No data connection")
							continue
						}
						reply("150 Opening data connection")
						dc, e := data.Accept()
						if e != nil {
							return
						}
						dc.SetDeadline(time.Now().Add(10 * time.Second))
						if command == "NLST" {
							for name := range files {
								fmt.Fprintf(dc, "%s\r\n", name)
							}
						} else {
							io.Copy(dc, bytes.NewReader(files[path.Base(arg)]))
						}
						dc.Close()
						data.Close()
						data = nil
						reply("226 Transfer complete")
					case "QUIT":
						reply("221 Goodbye")
						return
					default:
						reply("502 Unsupported")
					}
				}
			}()
		}
	}()
	port, _ := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
	return BackupFTP{Host: "127.0.0.1", Port: port, User: "fixture", Pass: "fixture-password", Dir: "/old"}, &calls, &lock
}
func TestBackupDiscoveryThroughFTPAndRestart(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl required")
	}
	backupTestSite(t)
	m := discoveryMeta()
	baseName := "example.test_20261013-120000-aabb.tar.gz"
	base := discoveryArchive(t, m)
	m.ParentID = "20261013-120000-aabb"
	nextName := "example.test_20261013-130000-aabb.tar.gz"
	f, calls, lock := discoveryFTPFixture(t, map[string][]byte{baseName: base, nextName: discoveryArchive(t, m), "example.test_20261013-140000-aabb.tar.gz": []byte("broken gzip"), "readme.txt": []byte("ignore")})
	store.Settings.BackupFTP = f
	cfg.Dry = false
	for run := 0; run < 2; run++ {
		result, err := scanBackupFTP(&Job{})
		if err != nil {
			t.Fatal(err)
		}
		r := result.(map[string]any)
		if r["skipped"] != 1 || (run == 0 && r["added"] != 2) || (run == 1 && (r["added"] != 0 || r["existing"] != 2)) {
			t.Fatal(r)
		}
	}
	store = State{}
	mustLoad()
	if len(store.Backups) != 2 {
		t.Fatalf("not persisted: %d", len(store.Backups))
	}
	for _, b := range store.Backups {
		if issue := backupRestoreIssue(b); issue != "" {
			t.Fatal(issue)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	for _, cmd := range *calls {
		switch cmd {
		case "DELE", "STOR", "RMD", "MKD", "RNFR", "RNTO":
			t.Fatal("discovery modified FTP:", cmd)
		}
	}
}
