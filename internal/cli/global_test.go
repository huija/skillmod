// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/huija/skillmod/internal/modfile"
)

func TestGlobalScopeIsIndependentOfProject(t *testing.T) {
	project, storeRoot := isolateCLI(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(home, ".agents", "skills", "personal", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: personal\n---\n# personal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := NewRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("skillmod %v: %v", args, err)
		}
	}
	globalRoot := filepath.Join(storeRoot, "global")
	run("--global", "init", "--yes", "--dry-run")
	if _, err := os.Stat(globalRoot); !os.IsNotExist(err) {
		t.Fatalf("global dry-run created declaration directory: %v", err)
	}
	run("--global", "init", "--yes")
	m, err := modfile.LoadMod(globalRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Name != "personal" || !m.Skills[0].Local {
		t.Fatalf("global declarations = %+v, want personal local skill", m)
	}
	for _, command := range []string{"sync", "verify", "list", "update", "prune"} {
		run(command, "--global", "--yes")
	}
	for _, name := range []string{modfile.ModFileName, modfile.LockFileName} {
		if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
			t.Errorf("global commands touched project %s: %v", name, err)
		}
	}
	run("init", "--yes")
	projectMod, err := modfile.LoadMod(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectMod.Skills) != 0 {
		t.Errorf("project declarations = %+v, want empty", projectMod)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("global skill disappeared: %v", err)
	}
}

func TestInstallModeOverrideAndRelinkFlags(t *testing.T) {
	isolateCLI(t)
	eng, err := (&rootOptions{installMode: "copy"}).newEngine()
	if err != nil {
		t.Fatal(err)
	}
	if eng.Config.InstallMode != "copy" {
		t.Errorf("CLI mode = %q, want copy", eng.Config.InstallMode)
	}
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"sync", "--check", "--relink"})
	if err := cmd.Execute(); err == nil {
		t.Error("sync accepted --check and --relink together")
	}
}
