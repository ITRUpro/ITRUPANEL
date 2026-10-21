// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPMASELinuxContexts(t *testing.T) {
	for _, state := range []string{"Disabled", "Enforcing", "Permissive"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", state, existing), func(t *testing.T) {
				var calls []string
				labeled := false
				command := func(name string, args ...string) (string, error) {
					line := strings.Join(append([]string{name}, args...), " ")
					calls = append(calls, line)
					switch name {
					case "getenforce":
						return state + "\n", nil
					case "semanage":
						if existing && args[1] == "-a" {
							return "", errors.New("already defined")
						}
						return "", nil
					case "restorecon":
						// An unlabeled tree must receive a complete context, including user/role/range.
						if !reflect.DeepEqual(args, []string{"-RF", "/var/lib/itrupanel/phpmyadmin"}) {
							t.Fatalf("unsafe relabel: %v", args)
						}
						labeled = true
						return "", nil
					default:
						t.Fatalf("unexpected command: %s", line)
						return "", nil
					}
				}
				if err := applyPMASELinuxContext(pmaDataDir, command); err != nil {
					t.Fatal(err)
				}
				if state == "Disabled" {
					if len(calls) != 1 || labeled {
						t.Fatal(calls)
					}
					return
				}
				if !labeled {
					t.Fatal("enabled SELinux was left unlabeled")
				}
				want := []string{"getenforce", "semanage fcontext -a -t httpd_sys_rw_content_t /var/lib/itrupanel/phpmyadmin(/.*)?"}
				if existing {
					want = append(want, "semanage fcontext -m -t httpd_sys_rw_content_t /var/lib/itrupanel/phpmyadmin(/.*)?")
				}
				want = append(want, "restorecon -RF /var/lib/itrupanel/phpmyadmin")
				if !reflect.DeepEqual(calls, want) {
					t.Fatal(calls)
				}
			})
		}
	}
}

func TestPMASELinuxDoesNotHideFailures(t *testing.T) {
	for _, failure := range []string{"getenforce", "semanage", "restorecon", "unknown"} {
		t.Run(failure, func(t *testing.T) {
			restoreCalled := false
			err := applyPMASELinuxContext(pmaDataDir, func(name string, args ...string) (string, error) {
				if name == "restorecon" {
					restoreCalled = true
				}
				if name == failure {
					return "", errors.New("fixture failure")
				}
				if name == "getenforce" {
					if failure == "unknown" {
						return "", nil
					}
					return "Enforcing", nil
				}
				return "", nil
			})
			if err == nil {
				t.Fatal("failure treated as success")
			}
			if failure != "restorecon" && restoreCalled {
				t.Fatal("relabel after failed status or policy update")
			}
		})
	}
}

func TestPMASELinuxEscapesCustomHome(t *testing.T) {
	err := applyPMASELinuxContext("/srv/panel.v1/www", func(name string, args ...string) (string, error) {
		if name == "getenforce" {
			return "Permissive", nil
		}
		if name == "semanage" && args[len(args)-1] != `/srv/panel\.v1/www(/.*)?` {
			t.Fatal(args)
		}
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
