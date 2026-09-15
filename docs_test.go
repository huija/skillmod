// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
)

// Documentation that automation depends on lives with the Agent Skill, and the
// README pair is the front door that links to it. These enumerations are checked
// against both, so a new action constant fails the build until the documented
// vocabulary carries it.
var (
	documentedCommands = []string{
		string(engine.CommandGet), string(engine.CommandInit), string(engine.CommandList),
		string(engine.CommandPrune), string(engine.CommandRemove), string(engine.CommandShare), string(engine.CommandSync),
		string(engine.CommandUpdate), string(engine.CommandVerify), string(engine.CommandWhy),
	}
	documentedEntryActions = []string{
		engine.ActionConflict, engine.ActionDrift, engine.ActionInstall, engine.ActionInstalled,
		engine.ActionKeep, engine.ActionLocal, engine.ActionLocalDrift, engine.ActionMatched,
		engine.ActionMissing, engine.ActionPartial, engine.ActionPrune, engine.ActionRemove,
		engine.ActionSkip, engine.ActionStale, engine.ActionUnlocked, engine.ActionUnresolved,
		engine.ActionUnverifiable, engine.ActionUpdate,
	}
	documentedTargetActions = []string{
		engine.ActionDrift, engine.ActionInstall, engine.ActionInstalled, engine.ActionKeep,
		engine.ActionMissing, engine.ActionRemove, engine.ActionSkip, engine.ActionUnlocked,
		engine.ActionUnverifiable,
	}
)

// vocabularyDocument states the JSON report vocabulary that scripts branch on.
const vocabularyDocument = "skills/skillmod/references/automation.md"

// vocabularyRowLabels keys the tables by their untranslated first cell, which is
// the JSON field path, and holds the values the row must enumerate.
var vocabularyRowLabels = map[string][]string{
	"`action`":                           documentedCommands,
	"`entries[].action`":                 documentedEntryActions,
	"`entries[].targetResults[].action`": documentedTargetActions,
}

// linkedReferences hold detail that used to sit in the README. Both languages
// must still point at them, so moving documentation cannot orphan it.
var linkedReferences = []string{
	"skills/skillmod/references/manifests.md",
	"skills/skillmod/references/storage.md",
	vocabularyDocument,
}

// TestDocumentedReportVocabulary requires the reference documentation to
// enumerate exactly the actions the command emits, since automation branches on
// those strings rather than on translated prose.
func TestDocumentedReportVocabulary(t *testing.T) {
	rows := tableRowsByLabel(t, vocabularyDocument)
	for label, want := range vocabularyRowLabels {
		got, ok := rows[label]
		if !ok {
			t.Fatalf("%s: no table row labeled %s documenting action values", vocabularyDocument, label)
		}
		if !reflect.DeepEqual(sortedCopy(got), sortedCopy(want)) {
			t.Errorf("%s: %s documents %v, the report model defines %v", vocabularyDocument, label, sortedCopy(got), sortedCopy(want))
		}
	}
}

// TestReportVocabularyMatchesModel ties the enumerations above to the constants
// they describe, so documenting a new action and adding it to the report model
// cannot happen independently.
func TestReportVocabularyMatchesModel(t *testing.T) {
	model := reportConstantStrings(t)
	documented := append(sortedCopy(documentedCommands), documentedEntryActions...)
	if !reflect.DeepEqual(sortedCopy(model), sortedCopy(documented)) {
		t.Errorf("internal/engine/report.go defines %v, the test enumerates %v", sortedCopy(model), sortedCopy(documented))
	}
}

// TestReadmesLinkToReferenceDocumentation keeps the moved detail reachable from
// both language front doors.
func TestReadmesLinkToReferenceDocumentation(t *testing.T) {
	for _, readme := range []string{"README.md", "README_CN.md"} {
		data, err := os.ReadFile(readme)
		if err != nil {
			t.Fatalf("read %s: %v", readme, err)
		}
		for _, reference := range linkedReferences {
			if !strings.Contains(string(data), reference) {
				t.Errorf("%s does not link to %s", readme, reference)
			}
		}
	}
}

// TestReadmesStayStructurallyParallel keeps the translation in step with the
// English original: a section that exists in only one language is a defect.
func TestReadmesStayStructurallyParallel(t *testing.T) {
	english, chinese := markdownHeadingLevels(t, "README.md"), markdownHeadingLevels(t, "README_CN.md")
	if !reflect.DeepEqual(english, chinese) {
		t.Errorf("README.md exposes heading levels %v, README_CN.md %v; both files must keep the same outline", english, chinese)
	}
}

// tableRowsByLabel maps the first cell of every markdown table row to the
// backticked values of its last cell, skipping fenced code blocks.
func tableRowsByLabel(t *testing.T, name string) map[string][]string {
	t.Helper()
	rows := map[string][]string{}
	for _, line := range markdownLines(t, name) {
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "| ---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		label := strings.TrimSpace(cells[0])
		if _, tracked := vocabularyRowLabels[label]; !tracked {
			continue
		}
		rows[label] = backticked(strings.TrimSpace(cells[len(cells)-1]))
	}
	return rows
}

func markdownHeadingLevels(t *testing.T, name string) []int {
	t.Helper()
	var levels []int
	for _, line := range markdownLines(t, name) {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		levels = append(levels, len(line)-len(strings.TrimLeft(line, "#")))
	}
	return levels
}

// markdownLines returns the file's lines with fenced code blocks removed.
func markdownLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var lines []string
	fenced := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			lines = append(lines, line)
		}
	}
	return lines
}

// reportConstantStrings returns every string constant declared in the report
// model, which holds the command identifiers and the shared action vocabulary.
func reportConstantStrings(t *testing.T) []string {
	t.Helper()
	const path = "internal/engine/report.go"
	file, err := parser.ParseFile(token.NewFileSet(), filepath.FromSlash(path), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var values []string
	blocks := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		blocks++
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, literal := range value.Values {
				lit, ok := literal.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				values = append(values, unquoted)
			}
		}
	}
	if blocks != 1 {
		t.Fatalf("%s declares %d const blocks, want the report vocabulary in exactly one", path, blocks)
	}
	return values
}

func backticked(cell string) []string {
	var values []string
	for {
		start := strings.Index(cell, "`")
		if start < 0 {
			return values
		}
		rest := cell[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			return values
		}
		if value := rest[:end]; value != "" {
			values = append(values, value)
		}
		cell = rest[end+1:]
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
