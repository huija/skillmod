// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package fsutil

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/huija/skillmod/internal/i18n"
)

// maxNameBytes is the conservative per-component limit common to supported
// filesystems. Linux's 255-byte NAME_MAX is tighter than the 255 UTF-16-unit
// limit on Windows for non-ASCII names.
const maxNameBytes = 255

// reservedDeviceNames are Windows reserved device names. They are matched
// case-insensitively against the part of a name before its first dot, after
// trimming trailing spaces and dots, so "CON", "con.txt", "Con.tar.gz", and
// "CON .txt" are all rejected.
var reservedDeviceNames = map[string]bool{}

func init() {
	names := []string{
		"con", "prn", "aux", "nul", "conin$", "conout$",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
		"com¹", "com²", "com³", "lpt¹", "lpt²", "lpt³",
	}
	for _, n := range names {
		reservedDeviceNames[foldKey(n)] = true
	}
}

// foldKey is the single canonical identity form for names that denote the
// same directory on case-insensitive filesystems (macOS, Windows). It applies
// Unicode NFC normalization (canonically equivalent spellings collide on
// default macOS filesystems) and then maps every rune to the
// smallest member of its unicode.SimpleFold orbit, which handles pairs that
// strings.ToLower and strings.EqualFold disagree on, such as the Greek sigma
// variants σ/ς and the Kelvin sign K.
func foldKey(s string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(minFoldRune(r))
	}
	return b.String()
}

// FoldKey returns the canonical fold identity of s; two names map to the same
// installation directory on case-insensitive filesystems if and only if their
// FoldKeys are equal.
func FoldKey(s string) string { return foldKey(s) }

func minFoldRune(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < m {
			m = f
		}
	}
	return m
}

// IsReservedDeviceName reports whether base is a Windows reserved device name.
// base must already be a single name without directory separators; it is
// fold-compared so caller-provided casing does not matter.
func IsReservedDeviceName(base string) bool {
	return reservedDeviceNames[foldKey(base)]
}

// ValidName reports whether name is a single path component that can be
// created on every platform skillmod supports. It returns a descriptive,
// localized error for the first violated rule. Names must be non-empty valid
// UTF-8 of at most 255 bytes; they must not be "." or "..", contain any of
// `/ \ : * ? " < > |` or control characters, be a Windows reserved device
// name (with or without an extension, ignoring spaces before the extension),
// or end with a dot or space. Non-ASCII letters are allowed.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s", i18n.Text("name must not be empty"))
	case !utf8.ValidString(name):
		return fmt.Errorf("%s", i18n.Text("name is not valid UTF-8"))
	case name == "." || name == "..":
		return fmt.Errorf(i18n.Text("name must not be %q"), name)
	case len(name) > maxNameBytes:
		return fmt.Errorf(i18n.Text("name is longer than %d bytes"), maxNameBytes)
	}
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7F:
			return fmt.Errorf("%s", i18n.Text("name contains a control character"))
		case strings.ContainsRune(`/:*?"<>|\`, r):
			return fmt.Errorf(i18n.Text("name contains a character that is illegal on Windows: %q"), r)
		}
	}
	base, _, _ := strings.Cut(name, ".")
	base = strings.TrimRight(base, " .")
	if IsReservedDeviceName(base) {
		return fmt.Errorf(i18n.Text("name uses the reserved Windows device name %q"), base)
	}
	if last := name[len(name)-1]; last == '.' || last == ' ' {
		return fmt.Errorf("%s", i18n.Text("name must not end with a dot or space"))
	}
	return nil
}

// ValidAlias reports whether s is acceptable as a user-chosen alias and
// installation directory. Aliases use the strict ASCII charset
// [A-Za-z0-9._-] on top of the portable name rules.
func ValidAlias(s string) error {
	if s == "" {
		return fmt.Errorf("%s", i18n.Text("name must not be empty"))
	}
	for _, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf(i18n.Text("alias %q contains invalid characters (only letters, digits, '.', '_', and '-' are allowed)"), s)
		}
	}
	return ValidName(s)
}

// ValidPath reports whether the slash-separated path can be materialized on
// every supported platform: no backslash anywhere, and every component passes
// ValidName (which also rejects empty components from "//" or a leading or
// trailing slash, and "." or ".." components).
func ValidPath(p string) error {
	if strings.Contains(p, `\`) {
		return fmt.Errorf(i18n.Text("path contains a backslash: %q"), p)
	}
	for comp := range strings.SplitSeq(p, "/") {
		if err := ValidName(comp); err != nil {
			return fmt.Errorf(i18n.Text("path component %q is invalid: %w"), comp, err)
		}
	}
	return nil
}
