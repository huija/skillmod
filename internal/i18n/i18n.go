// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package i18n selects user-facing text for skillmod commands from the shared
// gettext catalogs in the repository-level locales directory. Call sites pass
// short message keys (see locales/README.md); the wording lives in the catalogs,
// with en_US.po holding the English source text.
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

// Text returns the active locale catalog's text for a message key such as
// "cli.get.long". Keys missing from the active locale fall back to the English
// catalog, and keys missing everywhere fall back to the key itself, so catalog
// mistakes never hide the message entirely.
func Text(key string) string {
	return lookup(catalogs, Language(), key)
}

// Format formats the text for the active language.
func Format(key string, args ...any) string {
	return fmt.Sprintf(Text(key), args...)
}

func lookup(all map[string]map[string]string, language, key string) string {
	if translated := all[language][key]; translated != "" {
		return translated
	}
	if english := all["en_US"][key]; english != "" {
		return english
	}
	return key
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
