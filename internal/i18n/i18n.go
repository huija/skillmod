// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package i18n selects user-facing text for skillmod commands from the shared
// gettext catalogs in the repository-level locales directory.
package i18n

import (
	"fmt"
	"os"
	"strings"

	"github.com/huija/skillmod/internal/i18n/pofile"
	"github.com/huija/skillmod/locales"
)

//go:generate go run ./cmd/cataloggen -root ../..

// Env is the environment variable used to select command output language.
// Values beginning with "en" select en_US; values beginning with "zh" select
// zh_CN. When unset, the system locale is read from LC_ALL, LC_MESSAGES, then
// LANG. Unsupported and missing locales fall back to English.
const Env = "SKILLMOD_LANG"

var catalogs = mustCatalogs()

// Language returns the normalized active locale ("en_US" or "zh_CN").
func Language() string {
	if override := strings.TrimSpace(os.Getenv(Env)); override != "" {
		return normalizeLanguage(override)
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale := strings.TrimSpace(os.Getenv(key)); locale != "" {
			return normalizeLanguage(locale)
		}
	}
	return "en_US"
}

func normalizeLanguage(locale string) string {
	lang := strings.ToLower(strings.TrimSpace(locale))
	if strings.HasPrefix(lang, "en") {
		return "en_US"
	}
	if strings.HasPrefix(lang, "zh") {
		return "zh_CN"
	}
	return "en_US"
}

// Text returns the active locale catalog's translation for msgid. A missing
// entry falls back to the msgid so catalog mistakes never hide the message.
func Text(msgid string) string {
	if translated := catalogs[Language()][msgid]; translated != "" {
		return translated
	}
	return msgid
}

// Format formats the translation for the active language.
func Format(msgid string, args ...any) string {
	return fmt.Sprintf(Text(msgid), args...)
}

func mustCatalogs() map[string]map[string]string {
	paths := map[string]string{"en_US": "en_US.po", "zh_CN": "zh_CN.po"}
	loaded := make(map[string]map[string]string, len(paths))
	for language, path := range paths {
		data, err := locales.FS.ReadFile(path)
		if err != nil {
			panic("read embedded " + language + " catalog: " + err.Error())
		}
		catalog, err := pofile.Parse(data)
		if err != nil {
			panic("parse embedded " + language + " catalog: " + err.Error())
		}
		loaded[language] = catalog
	}
	return loaded
}
