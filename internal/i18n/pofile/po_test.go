// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package pofile

import "testing"

func TestParse(t *testing.T) {
	data := []byte(`# comment
msgid ""
msgstr ""
"Language: zh_CN\n"

#: source.go:1
msgid "hello "
"world"
msgstr "translated "
"value"
`)
	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got["hello world"] != "translated value" {
		t.Fatalf("catalog = %#v", got)
	}
}

func TestParseDuplicate(t *testing.T) {
	_, err := Parse([]byte("msgid \"x\"\nmsgstr \"a\"\n\nmsgid \"x\"\nmsgstr \"b\"\n"))
	if err == nil {
		t.Fatal("duplicate msgid was accepted")
	}
}
