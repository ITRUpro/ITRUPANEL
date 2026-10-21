// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import "strings"

type MailForward struct {
	Address  string   `json:"address"`
	Targets  []string `json:"targets"`
	KeepCopy bool     `json:"keep_copy"`
	Enabled  bool     `json:"enabled"`
}

func checkMailForwards(m MailState) error {
	boxes := map[string]bool{}
	recipients := map[string]bool{}
	edges := map[string][]string{}
	for _, b := range m.Boxes {
		boxes[b.Address] = true
		recipients[b.Address] = true
	}
	for _, a := range m.Aliases {
		recipients[a.Address] = true
		edges[a.Address] = []string{a.Target}
	}
	seen := map[string]bool{}
	for _, f := range m.Forwards {
		address, err := mailAddress(f.Address)
		if err != nil || address != f.Address {
			return errf("Неверный адрес пересылки")
		}
		if seen[address] || edges[address] != nil {
			return errf("Адрес пересылки уже занят")
		}
		seen[address] = true
		if !mailDomainExists(m, strings.SplitN(address, "@", 2)[1]) {
			return errf("Сначала добавьте почтовый домен пересылки")
		}
		if f.KeepCopy && !boxes[address] {
			return errf("Для сохранения копии сначала создайте ящик с этим адресом")
		}
		if len(f.Targets) == 0 || len(f.Targets) > 20 {
			return errf("Укажите от 1 до 20 получателей")
		}
		recipients[address] = true
		edges[address] = f.Targets
	}
	for _, f := range m.Forwards {
		seenTargets := map[string]bool{}
		for _, target := range f.Targets {
			address, err := mailAddress(target)
			if err != nil || address != target {
				return errf("Неверный адрес получателя пересылки")
			}
			if seenTargets[target] {
				return errf("Получатели пересылки повторяются")
			}
			seenTargets[target] = true
			if mailDomainExists(m, strings.SplitN(target, "@", 2)[1]) && !recipients[target] {
				return errf("Локальный получатель %s не существует", target)
			}
		}
	}
	// Include disabled rules: enabling one later must not create a latent loop.
	marks := map[string]int{}
	var visit func(string) bool
	visit = func(a string) bool {
		if marks[a] == 1 {
			return false
		}
		if marks[a] == 2 {
			return true
		}
		marks[a] = 1
		for _, target := range edges[a] {
			if !visit(target) {
				return false
			}
		}
		marks[a] = 2
		return true
	}
	for a := range edges {
		if !visit(a) {
			return errf("Пересылка создаёт цикл между почтовыми адресами")
		}
	}
	return nil
}

func mailForwardMap(m MailState) string {
	boxes := map[string]bool{}
	for _, b := range m.Boxes {
		boxes[b.Address] = b.Enabled
	}
	var out strings.Builder
	for _, f := range m.Forwards {
		if !f.Enabled {
			continue
		}
		if enabled, exists := boxes[f.Address]; exists && !enabled {
			continue
		}
		targets := append([]string{}, f.Targets...)
		// Postfix stops alias expansion at the address itself and delivers locally.
		if f.KeepCopy {
			targets = append(targets, f.Address)
		}
		out.WriteString(f.Address + " " + strings.Join(targets, ", ") + "\n")
	}
	return out.String()
}

func registerMailForwards() {
	apiOp("mail.forward.save", "Почта: пересылка", func(q *Req) (any, error) {
		var in struct {
			MailForward
			Edit bool `json:"edit"`
		}
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		var err error
		in.Address, err = mailAddress(in.Address)
		if err != nil {
			return nil, err
		}
		for i := range in.Targets {
			in.Targets[i], err = mailAddress(in.Targets[i])
			if err != nil {
				return nil, err
			}
		}
		return nil, changeMail(func(m *MailState) error {
			for i := range m.Forwards {
				if m.Forwards[i].Address == in.Address {
					if !in.Edit {
						return errf("Пересылка уже существует")
					}
					m.Forwards[i] = in.MailForward
					return nil
				}
			}
			if in.Edit {
				return errf("Пересылка не найдена")
			}
			m.Forwards = append(m.Forwards, in.MailForward)
			return nil
		})
	})
	apiOp("mail.forward.delete", "Почта: удаление пересылки", func(q *Req) (any, error) {
		var in struct{ Address string }
		if err := q.Bind(&in); err != nil {
			return nil, err
		}
		return nil, changeMail(func(m *MailState) error {
			keep := []MailForward{}
			for _, f := range m.Forwards {
				if f.Address != in.Address {
					keep = append(keep, f)
				}
			}
			m.Forwards = keep
			return nil
		})
	})
}
