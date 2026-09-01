// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogsAreCurrent(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	enPath := filepath.Join(root, "locales", "en_US.po")
	zhPath := filepath.Join(root, "locales", "zh_CN.po")
	existing, err := readExisting(zhPath)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := scan(root, existing)
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
	_ = i18n.Text("hello")
	_ = i18n.Format("count %d", 2)
}
`
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(source))
	zh := `msgid "hello"
msgstr "translated hello"

msgid "count %d"
msgstr "translated count %d"
`
	writeTestFile(t, filepath.Join(root, "locales", "zh_CN.po"), []byte(zh))

	if err := run(root, false); err != nil {
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
	if strings.Contains(string(en), "#:") || strings.Contains(string(updatedZh), "#:") {
		t.Fatal("generated catalogs must not contain source references")
	}
	if err := run(root, true); err != nil {
		t.Fatalf("fresh catalogs failed check mode: %v", err)
	}

	if err := os.WriteFile(enPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("stale catalog check error = %v", err)
	}
}

func TestRunReportsMissingTranslations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo
import "github.com/huija/skillmod/internal/i18n"
var message = i18n.Text("untranslated")
`))
	if err := run(root, false); err == nil || !strings.Contains(err.Error(), "need zh_CN translations") {
		t.Fatalf("missing translation error = %v", err)
	}
	for _, name := range []string{"en_US.po", "zh_CN.po"} {
		if _, err := os.Stat(filepath.Join(root, "locales", name)); err != nil {
			t.Fatalf("catalog %s was not written before reporting missing translations: %v", name, err)
		}
	}
}

func TestScanRejectsNonLiteralMessage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo.go"), []byte(`package demo
import "github.com/huija/skillmod/internal/i18n"
func message(value string) string { return i18n.Text(value) }
`))
	if _, err := scan(root, nil); err == nil || !strings.Contains(err.Error(), "must be a string literal") {
		t.Fatalf("non-literal scan error = %v", err)
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
