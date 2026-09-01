// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/dirhash"
)

func TestValidDirName(t *testing.T) {
	for _, name := range []string{"demo", "Demo-1.2_skill"} {
		if !validDirName(name) {
			t.Errorf("validDirName(%q) = false", name)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "with space", "café"} {
		if validDirName(name) {
			t.Errorf("validDirName(%q) = true", name)
		}
	}
}

func TestSourceComparisonAndParsing(t *testing.T) {
	repo, subdir, err := splitSource("https://example.com/acme/skills//tools/demo")
	if err != nil || repo != "https://example.com/acme/skills" || subdir != "tools/demo" {
		t.Fatalf("splitSource = %q, %q, %v", repo, subdir, err)
	}
	if _, _, err := splitSource("https://example.com/acme/skills@v1.0.0"); err == nil {
		t.Fatal("source field with a version was accepted")
	}
	if _, _, err := splitSource("https://example.com/acme/skills//../demo"); err == nil {
		t.Fatal("source field with a non-canonical subdirectory was accepted")
	}

	if !sameRemoteSource("https://example.com/acme/skills//demo", "git@example.com:acme/skills.git//demo") {
		t.Error("equivalent HTTPS and SSH sources were considered different")
	}
	if sameRemoteSource("https://example.com/acme/skills//demo", "https://example.com/acme/skills//other") {
		t.Error("different subdirectories were considered the same source")
	}
	if !sameRemoteSource("invalid source//", "invalid source//") {
		// Exact strings are intentionally equal without reparsing.
		t.Error("identical source strings were considered different")
	}
	if sameRemoteSource("invalid source//", "https://example.com/acme/skills") {
		t.Error("an invalid source was considered equivalent to a valid source")
	}
}

func TestResolveConflicts(t *testing.T) {
	conflicts := []conflict{{name: "a", dir: "/skills/a"}, {name: "b", dir: "/skills/b"}}

	if skip, err := resolveConflicts(IO{}, nil); err != nil || len(skip) != 0 {
		t.Fatalf("no conflicts = %v, %v", skip, err)
	}
	if _, err := resolveConflicts(IO{}, conflicts); err == nil || !strings.Contains(err.Error(), "/skills/a") {
		t.Fatalf("non-interactive conflict error = %v", err)
	}

	var out bytes.Buffer
	skip, err := resolveConflicts(IO{Yes: true, Out: &out}, conflicts)
	if err != nil || !skip["/skills/a"] || !skip["/skills/b"] || !strings.Contains(out.String(), "/skills/a") {
		t.Fatalf("--yes conflicts = %v, %v, output %q", skip, err, out.String())
	}

	chooser := &choiceConfirmer{choices: []int{0, 1}}
	skip, err = resolveConflicts(IO{Confirm: chooser}, conflicts)
	if err != nil || skip["/skills/a"] || !skip["/skills/b"] {
		t.Fatalf("interactive conflicts = %v, %v", skip, err)
	}
	if chooser.calls != 2 {
		t.Fatalf("Choose calls = %d, want 2", chooser.calls)
	}

	if _, err := resolveConflicts(IO{Confirm: &choiceConfirmer{choices: []int{2}}}, conflicts[:1]); err == nil {
		t.Fatal("abort choice did not return an error")
	}
}

func TestClassifyTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	currentHash, err := dirhash.HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got := classifyTarget(filepath.Join(t.TempDir(), "missing"), "h1:new", ""); got != "install" {
		t.Fatalf("missing target = %q", got)
	}
	if got := classifyTarget(dir, currentHash, ""); got != "keep" {
		t.Fatalf("matching target = %q", got)
	}
	if got := classifyTarget(dir, "h1:new", currentHash); got != "install" {
		t.Fatalf("clean previous target = %q", got)
	}
	if got := classifyTarget(dir, "h1:new", "h1:other"); got != "conflict" {
		t.Fatalf("modified target = %q", got)
	}
}

func TestDisplayListAction(t *testing.T) {
	for action, want := range map[string]string{
		"installed": "installed",
		"unlocked":  "unlocked",
		"missing":   "missing",
		"drift":     "drift",
		"local":     "local",
	} {
		if got := displayListAction(action); got != want {
			t.Errorf("displayListAction(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestEngineErrorDiagnostics(t *testing.T) {
	tests := []struct {
		err  error
		want []string
	}{
		{err: &DriftError{}, want: []string{"drift detected"}},
		{err: &TamperError{Name: "demo", Want: "h1:want", Got: "h1:got"}, want: []string{"demo", "h1:want", "h1:got"}},
		{err: &NameConflictError{Name: "demo", Existing: "old", Incoming: "new"}, want: []string{"demo", "old", "new", "--alias"}},
		{err: &skillSubdirError{Subdir: "tools/demo", Version: "v1.0.0"}, want: []string{"tools/demo", "v1.0.0"}},
	}
	for _, tt := range tests {
		message := tt.err.Error()
		for _, want := range tt.want {
			if !strings.Contains(message, want) {
				t.Errorf("%T message %q is missing %q", tt.err, message, want)
			}
		}
	}
}

func TestApplyInstallsCommitAndRollback(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("commit", func(t *testing.T) {
		target := newInstallationTarget(t, "old")
		finalize, err := applyInstalls([]plannedInstall{{name: "demo", contentDir: src, targets: []string{target}}})
		if err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, target, "new")
		if err := finalize(true); err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, target, "new")
		if _, err := os.Stat(target + ".skillmod-bak"); !os.IsNotExist(err) {
			t.Fatalf("backup remains after commit: %v", err)
		}
	})

	t.Run("explicit rollback", func(t *testing.T) {
		first := newInstallationTarget(t, "old-first")
		second := newInstallationTarget(t, "old-second")
		finalize, err := applyInstalls([]plannedInstall{{name: "demo", contentDir: src, targets: []string{first, second}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := finalize(false); err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, first, "old-first")
		assertInstallationContent(t, second, "old-second")
	})

	t.Run("later failure rolls back earlier target", func(t *testing.T) {
		target := newInstallationTarget(t, "old")
		plans := []plannedInstall{
			{name: "good", contentDir: src, targets: []string{target}},
			{name: "bad", contentDir: filepath.Join(t.TempDir(), "missing"), targets: []string{filepath.Join(t.TempDir(), "bad")}},
		}
		if _, err := applyInstalls(plans); err == nil || !strings.Contains(err.Error(), "all changes were rolled back") {
			t.Fatalf("applyInstalls error = %v", err)
		}
		assertInstallationContent(t, target, "old")
	})
}

type choiceConfirmer struct {
	choices []int
	calls   int
}

func (*choiceConfirmer) Confirm(string) bool { return false }

func (c *choiceConfirmer) Choose(string, []string) int {
	choice := c.choices[c.calls]
	c.calls++
	return choice
}

func newInstallationTarget(t *testing.T, content string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return target
}

func assertInstallationContent(t *testing.T, target, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("installation content = %q, want %q", data, want)
	}
}
