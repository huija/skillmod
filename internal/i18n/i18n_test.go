// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/huija/skillmod/internal/i18n/msgkey"
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
	const key = "engine.prune.pruned_stale_entries"

	setLocaleEnv(t, "en", "", "", "")
	if got := Format(key, 2); got != "pruned 2 stale entries" {
		t.Fatalf("English Format = %q", got)
	}

	setLocaleEnv(t, "zh", "", "", "")
	if got := Format(key, 2); got == "" || got == "pruned 2 stale entries" {
		t.Fatalf("zh_CN Format = %q, want a non-empty translation", got)
	}
}

func TestLookupFallsBackToEnglishThenKey(t *testing.T) {
	all := map[string]map[string]string{
		"en_US": {"demo.both": "english both", "demo.english_only": "english only"},
		"zh_CN": {"demo.both": "中文两者"},
	}
	tests := []struct {
		name     string
		language string
		key      string
		want     string
	}{
		{name: "active locale wins", language: "zh_CN", key: "demo.both", want: "中文两者"},
		{name: "English catalog fills a gap", language: "zh_CN", key: "demo.english_only", want: "english only"},
		{name: "unknown key is returned verbatim", language: "zh_CN", key: "demo.unknown", want: "demo.unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lookup(all, tt.language, tt.key); got != tt.want {
				t.Fatalf("lookup(%q, %q) = %q, want %q", tt.language, tt.key, got, tt.want)
			}
		})
	}
}

// TestCatalogsCompleteAndFormatCompatible guards the contract between the two
// catalogs: same keys, valid key shape, English and Chinese text everywhere,
// identical printf verbs in the same order, and the same line layout.
func TestCatalogsCompleteAndFormatCompatible(t *testing.T) {
	english := englishCatalog(t)
	chinese := catalogs["zh_CN"]
	if len(english) != len(chinese) {
		t.Fatalf("catalog size: en=%d zh_CN=%d", len(english), len(chinese))
	}
	if len(catalogs["en_US"]) != len(english) {
		t.Fatalf("embedded en_US catalog has %d entries, locales/en_US.po has %d", len(catalogs["en_US"]), len(english))
	}
	for key, englishText := range english {
		if err := msgkey.Validate(key); err != nil {
			t.Errorf("%v", err)
			continue
		}
		if englishText == "" {
			t.Errorf("missing English text for %q", key)
			continue
		}
		translated, ok := chinese[key]
		if !ok || translated == "" {
			t.Errorf("missing zh_CN translation for %q", key)
			continue
		}
		wantVerbs := formatDirective.FindAllString(englishText, -1)
		gotVerbs := formatDirective.FindAllString(translated, -1)
		if !reflect.DeepEqual(gotVerbs, wantVerbs) {
			t.Errorf("format directives for %q: en=%v zh_CN=%v", key, wantVerbs, gotVerbs)
		}
		if err := checkEdges(key, englishText, translated); err != nil {
			t.Errorf("%v", err)
		}
		if want, got := strings.Count(englishText, "\n"), strings.Count(translated, "\n"); want != got {
			t.Errorf("line breaks for %q: en=%d zh_CN=%d; a translation must keep the multi-line layout", key, want, got)
		}
	}
}

// englishCatalog reads the on-disk English catalog, the source of truth for the
// wording.
func englishCatalog(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "locales", "en_US.po"))
	if err != nil {
		t.Fatal(err)
	}
	english, err := pofile.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return english
}

// TestContractWordingIsPinned pins the wording other artifacts depend on: the
// lock rejection used by the CLI, the command usage lines published in
// help and the README, and the canonical missing-manifest errors. Rewording any
// of them is a deliberate act that must update this table in the same change.
func TestContractWordingIsPinned(t *testing.T) {
	english := englishCatalog(t)
	pinned := map[string]string{
		"resolve.branch_not_lockable":           "branches cannot be locked; use a tag or commit SHA (%q is a branch name)",
		"engine.skill_mod_found_advice":         "SKILL.mod not found\nAdvice: run skillmod init or skillmod get first",
		"engine.verify.skill_lock_found_advice": "SKILL.lock not found\nAdvice: run skillmod sync first to generate the lock file",
		"cli.get.use":                           "get <repository>[//<subdirectory-or-skill-name>][@<version>]",
		"cli.remove.use":                        "remove <names…>",
		"cli.update.use":                        "update [names…]",
		"cli.why.use":                           "why <name-or-alias>",
		// Fragments a call site concatenates with a value. The edge whitespace is
		// load-bearing, and checkEdges below cannot notice its loss.
		"engine.list.list":                         " (%s)",
		"engine.list.upgrade_available":            "upgrade available → ",
		"engine.prune.locally_modified_kept_files": "locally modified; kept files and removed only the lock record: ",
		"engine.remove.locally_modified_kept":      "locally modified; kept installed files: ",
		"engine.sync.missing":                      "missing: ",
		"engine.why.commit":                        "  commit: %s",
		"engine.why.dirhash":                       "  dirhash: %s",
		"engine.why.why":                           "  %s: %s",
		"source.fetch.missing_skill_md":            "skill package is missing SKILL.md or has an invalid frontmatter name (contact the author to fix it): ",
		"store.version_dir_unreadable":             "version directory is unreadable: ",
		"store.version_dir_unverifiable":           "version directory cannot be verified: ",
		"store.version_metadata_prefix":            "cannot parse version metadata: ",
		"store.version_metadata_unreadable":        "version metadata is unreadable: ",
		"ui.choose":                                "choose [1-%d]: ",
	}
	for key, want := range pinned {
		got, ok := english[key]
		if !ok {
			t.Errorf("contract message %q is missing from locales/en_US.po", key)
			continue
		}
		if got != want {
			t.Errorf("wording for %q changed:\n got: %q\nwant: %q", key, got, want)
		}
	}
}

// checkEdges covers message fragments that a call site concatenates with a
// value. English carries the separating space; a translation may use a
// full-width character instead, but must never glue a word onto the value.
func checkEdges(key, englishText, translated string) error {
	if strings.HasSuffix(englishText, " ") && gluesToPrevious(translated) {
		return fmt.Errorf("%q ends with a word character, but the English text ends with a space used to separate a concatenated value", key)
	}
	if strings.HasPrefix(englishText, " ") && gluesToNext(translated) {
		return fmt.Errorf("%q starts with a word character, but the English text starts with a space used to separate a concatenated value", key)
	}
	return nil
}

func gluesToPrevious(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return isWordRune(r)
}

func gluesToNext(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return isWordRune(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func TestMessageKeysAreUniqueAndBounded(t *testing.T) {
	chinese := catalogs["zh_CN"]
	seen := map[string]bool{}
	for key := range chinese {
		if seen[key] {
			t.Fatalf("duplicate message key %q", key)
		}
		seen[key] = true
		if len(key) > msgkey.MaxLen {
			t.Errorf("key %q exceeds %d characters", key, msgkey.MaxLen)
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
