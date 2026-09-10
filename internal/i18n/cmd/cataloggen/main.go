// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// cataloggen collects the message keys used by Go source, merges them with the
// hand-written English and Chinese catalogs, and writes deterministic gettext
// PO files.
//
// Message keys are short, stable identifiers such as "cli.get.long"; the
// translatable text lives in the catalogs, with en_US.po holding the English
// source wording. See locales/README.md for the key naming rule.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/huija/skillmod/internal/i18n/msgkey"
	"github.com/huija/skillmod/internal/i18n/pofile"
)

const poLineWidth = 76

type message struct {
	key         string
	english     string
	translation string
}

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "check catalogs without writing them")
	list := flag.Bool("list", false, "print the message key table")
	flag.Parse()

	if err := run(filepath.Clean(*root), *check, *list); err != nil {
		fmt.Fprintln(os.Stderr, "cataloggen:", err)
		os.Exit(1)
	}
}

func run(root string, check, list bool) error {
	enPath := filepath.Join(root, "locales", "en_US.po")
	zhPath := filepath.Join(root, "locales", "zh_CN.po")
	english, err := readExisting(enPath)
	if err != nil {
		return err
	}
	chinese, err := readExisting(zhPath)
	if err != nil {
		return err
	}
	messages, err := scan(root, english, chinese)
	if err != nil {
		return err
	}

	en := render(messages, "en_US")
	zh := render(messages, "zh_CN")

	if list {
		if _, err := os.Stdout.Write(listing(messages)); err != nil {
			return err
		}
	}
	if check {
		if err := equalFile(enPath, en); err != nil {
			return err
		}
		return equalFile(zhPath, zh)
	}

	if err := os.MkdirAll(filepath.Dir(zhPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(enPath, en, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(zhPath, zh, 0o644); err != nil {
		return err
	}

	var missingEn, missingZh []string
	for _, msg := range messages {
		if msg.english == "" {
			missingEn = append(missingEn, msg.key)
		}
		if msg.translation == "" {
			missingZh = append(missingZh, msg.key)
		}
	}
	pruned := 0
	for id := range english {
		if _, ok := messages[id]; !ok {
			pruned++
		}
	}
	if pruned > 0 {
		fmt.Fprintf(os.Stderr, "cataloggen: pruned %d message(s) no longer used by the source\n", pruned)
	}
	if len(missingEn) != 0 || len(missingZh) != 0 {
		return untranslatedError(missingEn, missingZh)
	}
	return nil
}

func untranslatedError(missingEn, missingZh []string) error {
	var parts []string
	if len(missingEn) != 0 {
		parts = append(parts, fmt.Sprintf("%d messages need en_US translations (first: %q)", len(missingEn), missingEn[0]))
	}
	if len(missingZh) != 0 {
		parts = append(parts, fmt.Sprintf("%d messages need zh_CN translations (first: %q)", len(missingZh), missingZh[0]))
	}
	return fmt.Errorf("%s; fill the empty msgstr values in locales/ and run again", strings.Join(parts, "; "))
}

func readExisting(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return pofile.Parse(data)
}

// scan collects every key used by i18n.Text and i18n.Format call sites, merging
// the existing catalog values for those keys. Messages the source no longer
// references are dropped.
func scan(root string, english, chinese map[string]string) (map[string]*message, error) {
	messages := map[string]*message{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "locales" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		var inspectErr error
		ast.Inspect(file, func(n ast.Node) bool {
			if inspectErr != nil {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !isI18nCall(call.Fun) {
				return true
			}
			msgKey, ok := literal(call.Args[0])
			if !ok {
				pos := fset.Position(call.Pos())
				inspectErr = fmt.Errorf("%s:%d: i18n message key must be a string literal", path, pos.Line)
				return false
			}
			pos := fset.Position(call.Pos())
			if err := msgkey.Validate(msgKey); err != nil {
				inspectErr = fmt.Errorf("%s:%d: %w", path, pos.Line, err)
				return false
			}
			if _, exists := messages[msgKey]; !exists {
				messages[msgKey] = &message{
					key:         msgKey,
					english:     english[msgKey],
					translation: chinese[msgKey],
				}
			}
			return true
		})
		return inspectErr
	})
	return messages, err
}

func isI18nCall(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Text" && sel.Sel.Name != "Format") {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "i18n"
}

func literal(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func render(messages map[string]*message, language string) []byte {
	var out bytes.Buffer
	out.WriteString("# Generated by go generate ./internal/i18n.\n")
	out.WriteString("# Message keys are identifiers; translatable text lives in msgstr.\n")
	out.WriteString("# The en_US catalog holds the English source wording and is hand written.\n")
	out.WriteString("# Do not reorder or rename keys by hand; run go generate instead.\n")
	out.WriteString("msgid \"\"\nmsgstr \"\"\n")
	out.WriteString("\"Project-Id-Version: skillmod\\n\"\n")
	out.WriteString("\"PO-Revision-Date: 2026-09-02 00:00+0800\\n\"\n")
	out.WriteString("\"Last-Translator: skillmod contributors\\n\"\n")
	if language == "zh_CN" {
		out.WriteString("\"Language-Team: Simplified Chinese\\n\"\n")
		out.WriteString("\"Language: zh_CN\\n\"\n")
	} else {
		out.WriteString("\"Language-Team: English (United States)\\n\"\n")
		out.WriteString("\"Language: en_US\\n\"\n")
	}
	out.WriteString("\"MIME-Version: 1.0\\n\"\n")
	out.WriteString("\"Content-Type: text/plain; charset=UTF-8\\n\"\n")
	out.WriteString("\"Content-Transfer-Encoding: 8bit\\n\"\n\n")

	keys := make([]string, 0, len(messages))
	for id := range messages {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		msg := messages[id]
		value := msg.english
		if language == "zh_CN" {
			value = msg.translation
		}
		if strings.Contains(value, "%") {
			out.WriteString("#, go-format\n")
		}
		writeEntry(&out, "msgid", msg.key)
		writeEntry(&out, "msgstr", value)
		out.WriteString("\n")
	}
	return out.Bytes()
}

// writeEntry writes a PO field, folding long values over continuation lines so
// no single physical line grows to several hundred characters. The first chunk
// stays on the field line, matching gettext's own wrapping.
func writeEntry(out *bytes.Buffer, field, value string) {
	body := strconv.Quote(value)
	body = body[1 : len(body)-1]
	head := poLineWidth - len(field) - 3 // field, separating space, surrounding quotes
	if len(body) <= head {
		out.WriteString(field + " " + strconv.Quote(value) + "\n")
		return
	}
	chunks := foldBody(body, head)
	out.WriteString(field + " \"" + chunks[0] + "\"\n")
	for _, chunk := range chunks[1:] {
		out.WriteString("\"" + chunk + "\"\n")
	}
}

// foldBody splits an escaped string body into chunks whose quoted form fits
// poLineWidth, the first one leaving room for the field prefix. Escape sequences
// and multi-byte characters are never split.
func foldBody(body string, headLimit int) []string {
	var chunks []string
	for len(body) > 0 {
		limit := poLineWidth - 2 // surrounding quotes
		if len(chunks) == 0 && headLimit < limit {
			limit = headLimit
		}
		if limit >= len(body) {
			chunks = append(chunks, body)
			break
		}
		cut := 0
		for cut < limit {
			var size int
			if body[cut] == '\\' {
				size = escapeLen(body[cut:])
			} else {
				_, size = utf8.DecodeRuneInString(body[cut:])
			}
			if cut+size > limit {
				break
			}
			cut += size
		}
		chunks = append(chunks, body[:cut])
		body = body[cut:]
	}
	if len(chunks) == 0 {
		chunks = append(chunks, "")
	}
	return chunks
}

// escapeLen returns the length of the Go string escape starting at body[0].
func escapeLen(body string) int {
	if len(body) < 2 {
		return 1
	}
	rest := body[1:]
	switch c := rest[0]; {
	case c >= '0' && c <= '7':
		n := 0
		for n < 3 && n < len(rest) && rest[n] >= '0' && rest[n] <= '7' {
			n++
		}
		return 1 + n
	case c == 'x':
		return 1 + 1 + min(2, hexRun(rest[1:]))
	case c == 'u':
		return 1 + 1 + min(4, hexRun(rest[1:]))
	case c == 'U':
		return 1 + 1 + min(8, hexRun(rest[1:]))
	default:
		return 2
	}
}

func hexRun(s string) int {
	n := 0
	for n < len(s) && strings.ContainsRune("0123456789abcdefABCDEF", rune(s[n])) {
		n++
	}
	return n
}

// listing renders the key inventory: every message key with its English and
// Chinese fill state, plus the missing values themselves.
func listing(messages map[string]*message) []byte {
	keys := make([]string, 0, len(messages))
	for id := range messages {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	fmt.Fprintf(&out, "%-34s %-9s %s\n", "KEY", "EN", "ZH")
	for _, id := range keys {
		msg := messages[id]
		enState, zhState := "ok", "ok"
		if msg.english == "" {
			enState = "MISSING"
		}
		if msg.translation == "" {
			zhState = "MISSING"
		}
		fmt.Fprintf(&out, "%-34s %-9s %s\n", id, enState, zhState)
		if enState == "MISSING" || zhState == "MISSING" {
			fmt.Fprintf(&out, "    en: %s\n    zh: %s\n", strconv.Quote(msg.english), strconv.Quote(msg.translation))
		}
	}
	return out.Bytes()
}

func equalFile(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s is stale; run go generate ./internal/i18n", path)
	}
	return nil
}
