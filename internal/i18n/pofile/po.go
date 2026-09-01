// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package pofile parses the small, standard gettext PO subset used by
// skillmod catalogs. It supports comments, headers, and continued strings.
package pofile

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Parse returns msgid-to-msgstr entries from a gettext PO file. The header
// entry (empty msgid) is omitted.
func Parse(data []byte) (map[string]string, error) {
	entries := map[string]string{}
	var id, value strings.Builder
	field := ""
	haveEntry := false

	flush := func() error {
		if !haveEntry {
			return nil
		}
		msgid := id.String()
		if msgid != "" {
			if _, exists := entries[msgid]; exists {
				return fmt.Errorf("duplicate msgid %q", msgid)
			}
			entries[msgid] = value.String()
		}
		id.Reset()
		value.Reset()
		field = ""
		haveEntry = false
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			if err := flush(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "msgid "):
			if haveEntry {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			s, err := quoted(strings.TrimSpace(strings.TrimPrefix(line, "msgid")))
			if err != nil {
				return nil, fmt.Errorf("parse msgid: %w", err)
			}
			id.WriteString(s)
			field = "msgid"
			haveEntry = true
		case strings.HasPrefix(line, "msgstr "):
			if !haveEntry {
				return nil, fmt.Errorf("msgstr without msgid")
			}
			s, err := quoted(strings.TrimSpace(strings.TrimPrefix(line, "msgstr")))
			if err != nil {
				return nil, fmt.Errorf("parse msgstr: %w", err)
			}
			value.WriteString(s)
			field = "msgstr"
		case strings.HasPrefix(line, "\""):
			s, err := quoted(line)
			if err != nil {
				return nil, fmt.Errorf("parse continued %s: %w", field, err)
			}
			switch field {
			case "msgid":
				id.WriteString(s)
			case "msgstr":
				value.WriteString(s)
			default:
				return nil, fmt.Errorf("continued string without msgid or msgstr")
			}
		default:
			return nil, fmt.Errorf("unsupported PO line %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return entries, nil
}

func quoted(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("expected quoted string, got %q", s)
	}
	return strconv.Unquote(s)
}
