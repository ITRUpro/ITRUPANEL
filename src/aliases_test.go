// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestAliasEditsOverrideLegacyWWWDefault(t *testing.T) {
	for _, backend := range []string{"", "apache", "openlitespeed"} {
		t.Run(backend, func(t *testing.T) {
			testPanel(t)
			in := siteIn{Domain: "aliases.test", User: "alice", Mode: "php", PHP: "8.3", Backend: backend, AddWWW: true, Aliases: "other.aliases.test"}
			s, err := createOrUpdateSite(in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.Aliases, []string{"www.aliases.test", "other.aliases.test"}) {
				t.Fatal(s.Aliases)
			}
			in.Edit = true // AddWWW stays true to reproduce requests from older clients.
			for _, aliases := range []string{"other.aliases.test", "", "new.aliases.test"} {
				in.Aliases = aliases
				s, err = createOrUpdateSite(in)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Join(s.Aliases, " ") != aliases {
					t.Fatalf("saved %v, wanted %q", s.Aliases, aliases)
				}
				for _, line := range strings.Split(readFile(vhostPath(s.Domain)), "\n") {
					if strings.Contains(line, "server_name") && strings.Contains(line, "www.aliases.test") {
						t.Fatal("nginx still serves the deleted alias")
					}
				}
				// Reopen persisted state, as happens after restart, not only the returned object.
				mustLoad()
				if got := strings.Join(findSite(in.Domain).Aliases, " "); got != aliases {
					t.Fatalf("reloaded aliases %q, wanted %q", got, aliases)
				}
			}
		})
	}
}
