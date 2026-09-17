// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAcceptsAnySingleSegmentName pins the rule that makes a custom
// agent work: every name resolves through the same ".<name>/skills"
// convention, so the known list decides suggestions only. A leading dot and
// any casing are accepted because both spellings name the same directory.
func TestResolveAcceptsAnySingleSegmentName(t *testing.T) {
	tests := []struct {
		given string
		want  string
	}{
		{given: "claude", want: "claude"},
		{given: "  Claude ", want: "claude"},
		{given: ".claude", want: "claude"},
		{given: "workbuddy", want: "workbuddy"},
		{given: "Qwen", want: "qwen"},
		{given: "trae-2", want: "trae-2"},
	}
	for _, tt := range tests {
		t.Run(tt.given, func(t *testing.T) {
			target, err := Resolve(tt.given)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.given, err)
			}
			if target.Name != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.given, target.Name, tt.want)
			}
		})
	}
}

// TestResolveRejectsNamesThatAreNotOneSegment keeps a path out of the
// manifest: the directory is derived from the name, so a name carrying a
// separator would not describe the same place on every machine.
func TestResolveRejectsNamesThatAreNotOneSegment(t *testing.T) {
	for _, name := range []string{".claude/skills", "a/b", `a\b`, "a:b", "nope skills", ".", "..", "", strings.Repeat("a", 255)} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(name)
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded, want a shape error", name)
			}
			if !strings.Contains(err.Error(), "skills") {
				t.Fatalf("Resolve(%q) error = %v, want it to state the directory rule", name, err)
			}
		})
	}
}

func TestSuggestedOnlyOffersResolvableDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".agent space", ".中文", "..shadow", ".workbuddy"} {
		if err := os.MkdirAll(filepath.Join(root, dir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, target := range Suggested(root) {
		got = append(got, target.Name)
		resolved, err := Resolve(target.Name)
		if err != nil || resolved != target {
			t.Errorf("Resolve(Suggested(%q) name %q) = %+v, %v, want %+v, nil", root, target.Name, resolved, err, target)
		}
	}
	if want := "claude,codex,workbuddy"; strings.Join(got, ",") != want {
		t.Errorf("Suggested(%q) names = %v, want %s", root, got, want)
	}
}

func TestDirJoinsTheScopeRoot(t *testing.T) {
	target, err := Resolve("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := target.Dir(t.TempDir()); !strings.HasSuffix(filepath.ToSlash(got), "/.codex/skills") {
		t.Fatalf("codex dir = %q", got)
	}
}

// TestKnownIsSortedAndSuggestedAddsExistingDirectories covers the interactive
// list: a name added on the command line has to be offered again next time,
// which is what separates a custom agent from a second-class one.
func TestKnownIsSortedAndSuggestedAddsExistingDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".claude/skills", ".workbuddy/skills", ".empty"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A dot directory without a skills child is not an agent destination.
	if err := os.MkdirAll(filepath.Join(root, ".truncated"), 0o755); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(Suggested(root)))
	for _, target := range Suggested(root) {
		names = append(names, target.Name)
	}
	want := []string{"claude", "codex", "workbuddy"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Suggested = %v, want %v", names, want)
	}
	for _, name := range Known() {
		target, err := Resolve(name)
		if err != nil || target.Name != name {
			t.Errorf("known agent %q resolves to %q (%v), want itself", name, target.Name, err)
		}
	}
}
