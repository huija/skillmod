// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package agents

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAllReturnsTargetsInNameOrder(t *testing.T) {
	targets := All()
	if len(targets) < 2 {
		t.Fatalf("registry holds %d targets, want at least the two shipped ones", len(targets))
	}
	for i := 1; i < len(targets); i++ {
		if targets[i-1].Name >= targets[i].Name {
			t.Fatalf("targets are not sorted by name: %v", targets)
		}
	}
	for _, target := range targets {
		if !strings.HasSuffix(filepath.ToSlash(target.SkillsDir), "/skills") {
			t.Errorf("target %s points at %q, want a skills directory", target.Name, target.SkillsDir)
		}
	}
}

func TestLookupIsCaseInsensitiveAndNamesTheRegistry(t *testing.T) {
	target, err := Lookup("  Claude ")
	if err != nil {
		t.Fatalf("Lookup(claude): %v", err)
	}
	if target.SkillsDir != ".claude/skills" {
		t.Fatalf("claude target = %+v", target)
	}
	_, err = Lookup("nope")
	if err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("unknown agent error = %v, want it to name the registered agents", err)
	}
}

func TestDirJoinsTheScopeRoot(t *testing.T) {
	target, err := Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := target.Dir(t.TempDir()); !strings.HasSuffix(filepath.ToSlash(got), "/.codex/skills") {
		t.Fatalf("codex dir = %q", got)
	}
}
