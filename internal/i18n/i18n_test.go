// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package i18n

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/huija/skillmod/internal/i18n/pofile"
)

var formatDirective = regexp.MustCompile(`%(?:\[[0-9]+\])?[-+#0-9 .'']*[a-zA-Z]`)

func TestLanguage(t *testing.T) {
	tests := []struct {
		name       string
		override   string
		lcAll      string
		lcMessages string
		lang       string
		want       string
	}{
		{name: "missing locale defaults to English", want: "en_US"},
		{name: "unsupported locale defaults to English", lang: "de_DE.UTF-8", want: "en_US"},
		{name: "C locale defaults to English", lang: "C.UTF-8", want: "en_US"},
		{name: "LANG selects English", lang: "en_GB.UTF-8", want: "en_US"},
		{name: "LANG selects Chinese", lang: "zh_CN.UTF-8", want: "zh_CN"},
		{name: "LC_MESSAGES precedes LANG", lcMessages: "zh_TW", lang: "en_US", want: "zh_CN"},
		{name: "LC_ALL precedes LC_MESSAGES", lcAll: "en_US", lcMessages: "zh_CN", lang: "zh_CN", want: "en_US"},
		{name: "unsupported LC_ALL falls back to English", lcAll: "fr_FR", lcMessages: "zh_CN", want: "en_US"},
		{name: "explicit English override", override: "EN-us.UTF-8", lcAll: "zh_CN", want: "en_US"},
		{name: "explicit Chinese override", override: "zh-Hans", lcAll: "en_US", want: "zh_CN"},
		{name: "unsupported override falls back to English", override: "ja_JP", lcAll: "zh_CN", want: "en_US"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLocaleEnv(t, tt.override, tt.lcAll, tt.lcMessages, tt.lang)
			if got := Language(); got != tt.want {
				t.Fatalf("Language = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextUsesSelectedCatalog(t *testing.T) {
	setLocaleEnv(t, "en", "", "", "")
	if got := Format("pruned %d stale entries", 2); got != "pruned 2 stale entries" {
		t.Fatalf("English Format = %q", got)
	}

	setLocaleEnv(t, "zh", "", "", "")
	if got := Format("pruned %d stale entries", 2); got == "" || got == "pruned 2 stale entries" {
		t.Fatalf("zh_CN Format = %q, want a non-empty translation", got)
	}
}

func TestMissingTranslationFallsBackToMsgID(t *testing.T) {
	t.Setenv(Env, "zh")
	const msgid = "message not present in the catalog"
	if got := Text(msgid); got != msgid {
		t.Fatalf("Text = %q, want msgid fallback", got)
	}
}

func TestCatalogsCompleteAndFormatCompatible(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "locales", "en_US.po"))
	if err != nil {
		t.Fatal(err)
	}
	english, err := pofile.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	chinese := catalogs["zh_CN"]
	if len(english) != len(chinese) {
		t.Fatalf("catalog size: en=%d zh_CN=%d", len(english), len(chinese))
	}
	for msgid, englishText := range english {
		if englishText != msgid {
			t.Errorf("English msgstr for %q = %q", msgid, englishText)
		}
		translated, ok := chinese[msgid]
		if !ok || translated == "" {
			t.Errorf("missing zh_CN translation for %q", msgid)
			continue
		}
		wantVerbs := formatDirective.FindAllString(msgid, -1)
		gotVerbs := formatDirective.FindAllString(translated, -1)
		if !reflect.DeepEqual(gotVerbs, wantVerbs) {
			t.Errorf("format directives for %q: source=%v zh_CN=%v", msgid, wantVerbs, gotVerbs)
		}
	}
}

func setLocaleEnv(t *testing.T, override, lcAll, lcMessages, lang string) {
	t.Helper()
	t.Setenv(Env, override)
	t.Setenv("LC_ALL", lcAll)
	t.Setenv("LC_MESSAGES", lcMessages)
	t.Setenv("LANG", lang)
}
