// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/modfile"
)

// writeInstalledSkill places one skill directly into the managed directory;
// sharing works on installed directories and needs no Git or manifest.
func writeInstalledSkill(t *testing.T, project, dirName string) {
	t.Helper()
	dir := filepath.Join(project, ".agents", "skills", dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: " + dirName + "\ndescription: test skill\n---\n# " + dirName + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShareCommandJSONReportAndPositionalSkills(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")
	writeInstalledSkill(t, project, "world")

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"share", "hello", "--agent", "claude,codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("share: %v", err)
	}
	// --json keeps the summary on stderr and stdout carries one document.
	var rep engine.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not one JSON report: %v\n%s", err, stdout.String())
	}
	if rep.Action != engine.CommandShare || len(rep.Entries) != 1 {
		t.Fatalf("report = %+v, want one entry for the positional skill", rep)
	}
	if rep.Entries[0].Name != "hello" || rep.Entries[0].Action != engine.ActionInstall {
		t.Fatalf("entry = %+v", rep.Entries[0])
	}
	if len(rep.Entries[0].TargetResults) != 2 {
		t.Fatalf("targets = %+v, want one per agent", rep.Entries[0].TargetResults)
	}
	for _, agent := range []string{"claude", "codex"} {
		if _, err := os.Stat(filepath.Join(project, "."+agent, "skills", "hello", "SKILL.md")); err != nil {
			t.Fatalf("%s copy: %v", agent, err)
		}
	}
	// The unselected skill stays where it is.
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "world")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill was copied: %v", err)
	}
}

func TestShareCommandSkipsConflictsUnderYesAndExitsPartial(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")
	dst := filepath.Join(project, ".claude", "skills", "hello")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	os.Args = []string{"skillmod", "--yes", "share", "--all", "--agent", "claude"}
	if got := Execute(); got != ExitPartial {
		t.Fatalf("share exit = %d, want %d", got, ExitPartial)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "different\n" {
		t.Fatalf("destination content = %q, want it kept under --yes", data)
	}
}

func TestShareCommandDryRunWritesNothing(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"share", "--all", "--agent", "claude", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("share: %v", err)
	}
	if !strings.Contains(stdout.String(), `"action": "install"`) {
		t.Fatalf("dry run report = %s, want the planned install", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "hello")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote to the agent directory: %v", err)
	}
}

func TestShareCommandRejectsAnUnusableAgentAndConflictPolicy(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")

	// Any single-segment agent name is accepted, so the rejection left is a
	// name that is not a directory segment at all.
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--all", "--agent", ".claude/skills"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "skills") {
		t.Fatalf("unusable agent name error = %v, want the directory rule stated", err)
	}

	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--all", "--agent", "claude", "--on-conflict", "maybe"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "ask") {
		t.Fatalf("unknown policy error = %v, want the supported policies", err)
	}
}

// The --skill flag documents comma-separated values, so it must split them
// exactly like --agent does. A positional argument is one shell word:
// a name containing a comma stays whole rather than being read as two.
func TestShareCommandSplitsCommaSeparatedSkillFlagsOnly(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")
	writeInstalledSkill(t, project, "world")

	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--skill", "hello,world", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("share --skill a,b: %v", err)
	}
	for _, name := range []string{"hello", "world"} {
		if _, err := os.Stat(filepath.Join(project, ".claude", "skills", name, "SKILL.md")); err != nil {
			t.Fatalf("comma-separated --skill did not share %s: %v", name, err)
		}
	}

	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "hello,world", "--agent", "claude"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"hello,world"`) {
		t.Fatalf("positional comma error = %v, want the whole word reported as one selector", err)
	}
}

// TestShareCommandRemoveNeedsASkillSelection pins the flag contract: --remove
// edits the agents of named entries, so it must be told which ones.
func TestShareCommandRemoveNeedsASkillSelection(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")

	// The agents list lives on a manifest entry, so hello must be declared
	// before a share can record claude on it: an undeclared directory links
	// without becoming declarable. init is the declaration's front door.
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"init", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--all", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("share: %v", err)
	}

	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--remove", "--agent", "claude"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("share --remove without --skill or --all = nil, want a diagnostic")
	}
	if _, statErr := os.Lstat(filepath.Join(project, ".claude", "skills", "hello")); statErr != nil {
		t.Errorf("the rejected run took down a link: %v", statErr)
	}

	// Naming the skill lets the same invocation through.
	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "hello", "--remove", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("share hello --remove --agent claude: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "hello")); !os.IsNotExist(err) {
		t.Errorf("--remove kept the link: %v", err)
	}

	// The same run also drops the agents field from hello's entry: the
	// manifest says "not shared" by omitting the field, not by an empty list.
	m, err := modfile.LoadMod(project)
	if err != nil {
		t.Fatalf("LoadMod: %v", err)
	}
	for _, entry := range m.Skills {
		if entry.DirName() == "hello" && len(entry.Agents) != 0 {
			t.Errorf("agents on hello after --remove = %v, want the field gone", entry.Agents)
		}
	}
}
