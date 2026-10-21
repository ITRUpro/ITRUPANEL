// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Recognize only a server-level prefix location for /. Exact and regex
// locations can coexist with the generated prefix. Ignore comments, strings
// and nested blocks; nginx -t remains the authority for configuration syntax.
func nginxCustomRootLocation(text string) bool {
	var word strings.Builder
	var directive []string
	depth := 0
	var quote byte
	comment, escaped := false, false
	flush := func() {
		if word.Len() > 0 {
			directive = append(directive, word.String())
			word.Reset()
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if comment {
			if c == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			word.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' {
			comment = true
			continue
		}
		// ${variable} braces belong to a token, not a block.
		if c == '$' && i+1 < len(text) && text[i+1] == '{' {
			j := strings.IndexByte(text[i+2:], '}')
			if j >= 0 {
				end := i + 2 + j
				word.WriteString(text[i : end+1])
				i = end
				continue
			}
		}
		switch c {
		case ' ', '\t', '\r', '\n':
			flush()
		case '{':
			flush()
			if depth == 0 && ((len(directive) == 2 && directive[0] == "location" && directive[1] == "/") || (len(directive) == 3 && directive[0] == "location" && directive[1] == "^~" && directive[2] == "/")) {
				return true
			}
			depth++
			directive = nil
		case '}':
			flush()
			if depth > 0 {
				depth--
			}
			directive = nil
		case ';':
			flush()
			directive = nil
		default:
			word.WriteByte(c)
		}
	}
	return false
}

// Called under mu. Both files must agree before nginx tests or reloads them.
func saveNginxCustom(s *Site, text string) (resultErr error) {
	u := findUser(s.User)
	if u == nil {
		return errf("Нет пользователя %s", s.User)
	}
	candidate, err := renderSiteNginx(s, u, text)
	if err != nil {
		return err
	}
	type snapshot struct {
		path   string
		data   []byte
		mode   os.FileMode
		exists bool
	}
	var before []snapshot
	for _, path := range []string{customPath(s.Domain), vhostPath(s.Domain)} {
		data, e := os.ReadFile(P(path))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		sn := snapshot{path: path, data: data, mode: 0644, exists: e == nil}
		if sn.exists {
			info, e := os.Stat(P(path))
			if e != nil {
				return e
			}
			sn.mode = info.Mode().Perm()
		}
		before = append(before, sn)
	}
	changed := false
	defer func() {
		if resultErr == nil || !changed {
			return
		}
		var errs []error
		for _, sn := range before {
			if sn.exists {
				errs = append(errs, writeFile(sn.path, sn.data, sn.mode))
			} else if e := os.Remove(P(sn.path)); e != nil && !os.IsNotExist(e) {
				errs = append(errs, e)
			}
		}
		// Restore the live configuration too if reload failed after validation.
		errs = append(errs, nginxTestReload())
		if e := errors.Join(errs...); e != nil {
			resultErr = fmt.Errorf("%w; восстановление nginx: %v", resultErr, e)
		}
	}()
	if err = writeFile(customPath(s.Domain), []byte(text), before[0].mode); err != nil {
		return err
	}
	changed = true
	if err = writeFile(vhostPath(s.Domain), candidate, before[1].mode); err != nil {
		return err
	}
	return nginxTestReload()
}
