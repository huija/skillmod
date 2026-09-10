// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/i18n/pofile"
)

func TestCatalogsAreCurrent(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	enPath := filepath.Join(root, "locales", "en_US.po")
	zhPath := filepath.Join(root, "locales", "zh_CN.po")
	english, err := readExisting(enPath)
	if err != nil {
		t.Fatal(err)
	}
	chinese, err := readExisting(zhPath)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := scan(root, english, chinese)
	if err != nil {
		t.Fatal(err)
	}
	if err := equalFile(enPath, render(messages, "en_US")); err != nil {
		t.Fatal(err)
	}
	if err := equalFile(zhPath, render(messages, "zh_CN")); err != nil {
		t.Fatal(err)
	}
}

func TestRunWritesAndChecksCatalogs(t *testing.T) {
	root := t.TempDir()
	source := `package demo

import "github.com/huija/skillmod/internal/i18n"

func messages() {
	_ = i18n.Text("demo.greeting")
	_ = i18n.Format("demo.count", 2)
}
`
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(source))
	writeTestFile(t, filepath.Join(root, "locales", "en_US.po"), []byte(`msgid "demo.greeting"
msgstr "hello"

msgid "demo.count"
msgstr "count %d"
`))
	writeTestFile(t, filepath.Join(root, "locales", "zh_CN.po"), []byte(`msgid "demo.greeting"
msgstr "translated hello"

msgid "demo.count"
msgstr "translated count %d"
`))

	if err := run(root, false, false); err != nil {
		t.Fatal(err)
	}
	enPath := filepath.Join(root, "locales", "en_US.po")
	zhPath := filepath.Join(root, "locales", "zh_CN.po")
	en, err := os.ReadFile(enPath)
	if err != nil {
		t.Fatal(err)
	}
	updatedZh, err := os.ReadFile(zhPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(en), `msgstr "count %d"`) ||
		!strings.Contains(string(updatedZh), `msgstr "translated count %d"`) {
		t.Fatalf("generated catalogs are incomplete:\nEN:\n%s\nZH:\n%s", en, updatedZh)
	}
	// The key must never stand in for the English wording.
	if strings.Contains(string(en), `msgstr "demo.count"`) {
		t.Fatalf("English catalog echoed the key instead of the wording:\n%s", en)
	}
	if !strings.Contains(string(en), `#, go-format`) {
		t.Fatalf("English catalog is missing the go-format marker:\n%s", en)
	}
	if strings.Contains(string(en), "#:") || strings.Contains(string(updatedZh), "#:") {
		t.Fatal("generated catalogs must not contain source references")
	}
	if err := run(root, true, false); err != nil {
		t.Fatalf("fresh catalogs failed check mode: %v", err)
	}

	if err := os.WriteFile(enPath, []byte("msgid \"demo.greeting\"\nmsgstr \"stale wording\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true, false); err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("stale catalog check error = %v", err)
	}
}

func TestRunPrunesKeysTheSourceNoLongerUses(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo

import "github.com/huija/skillmod/internal/i18n"

var message = i18n.Text("demo.greeting")
`))
	for _, locale := range []string{"en_US", "zh_CN"} {
		writeTestFile(t, filepath.Join(root, "locales", locale+".po"), []byte(`msgid "demo.greeting"
msgstr "hello"

msgid "demo.removed"
msgstr "dropped"
`))
	}

	if err := run(root, false, false); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en_US", "zh_CN"} {
		data, err := os.ReadFile(filepath.Join(root, "locales", locale+".po"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "demo.removed") {
			t.Errorf("%s kept a message the source no longer uses:\n%s", locale, data)
		}
	}
}

func TestRunReportsMissingTranslations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo
import "github.com/huija/skillmod/internal/i18n"
var message = i18n.Text("demo.untranslated")
`))
	err := run(root, false, false)
	if err == nil {
		t.Fatal("run succeeded with a message missing from both catalogs")
	}
	for _, want := range []string{"need en_US translations", "need zh_CN translations"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %q", err, want)
		}
	}
	for _, name := range []string{"en_US.po", "zh_CN.po"} {
		if _, err := os.Stat(filepath.Join(root, "locales", name)); err != nil {
			t.Fatalf("catalog %s was not written before reporting missing translations: %v", name, err)
		}
	}
}

func TestScanRejectsNonLiteralMessageKey(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo
import "github.com/huija/skillmod/internal/i18n"
func message(value string) string { return i18n.Text(value) }
`))
	if _, err := scan(root, nil, nil); err == nil || !strings.Contains(err.Error(), "must be a string literal") {
		t.Fatalf("non-literal scan error = %v", err)
	}
}

func TestScanRejectsKeysBreakingTheRule(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "uppercase segment", key: "demo.Greeting"},
		{name: "single segment", key: "greeting"},
		{name: "too many segments", key: "demo.greeting.extra.more"},
		{name: "dash instead of underscore", key: "demo.greeting-line"},
		{name: "leading digit", key: "9demo.greeting"},
		{name: "trailing dot", key: "demo.greeting."},
		{name: "space", key: "demo.greeting line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo
import "github.com/huija/skillmod/internal/i18n"
var message = i18n.Text("`+tt.key+`")
`))
			_, err := scan(root, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "i18n key") {
				t.Fatalf("invalid key %q scan error = %v", tt.key, err)
			}
			if !strings.Contains(err.Error(), "demo.go:3") {
				t.Errorf("error %v does not point at the call site", err)
			}
		})
	}
}

func TestWriteEntryFoldsLongValuesWithoutSplittingEscapes(t *testing.T) {
	value := strings.Repeat("α→", 60) + "\n" + strings.Repeat("x", 40) + ` "quoted" \`
	var buf bytes.Buffer
	writeEntry(&buf, "msgstr", value)
	entry := buf.String()
	lines := strings.Split(strings.TrimSuffix(entry, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("long value was not folded: %q", entry)
	}
	// The value must start on the field line: an empty leading chunk reads like
	// an untranslated entry to anyone skimming the catalog.
	if lines[0] == `msgstr ""` {
		t.Fatalf("folded entry opens with an empty msgstr: %q", entry)
	}
	for _, line := range lines {
		if len(line) > poLineWidth {
			t.Errorf("line is %d characters, want at most %d: %q", len(line), poLineWidth, line)
		}
	}
	parsed, err := pofile.Parse([]byte("msgid \"demo.long\"\n" + entry + "\n"))
	if err != nil {
		t.Fatalf("folded entry does not parse: %v\n%s", err, entry)
	}
	if got := parsed["demo.long"]; got != value {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, value)
	}
}

func TestWriteEntryKeepsShortValuesOnOneLine(t *testing.T) {
	var buf bytes.Buffer
	writeEntry(&buf, "msgid", "demo.short")
	if got, want := buf.String(), "msgid \"demo.short\"\n"; got != want {
		t.Fatalf("writeEntry = %q, want %q", got, want)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
