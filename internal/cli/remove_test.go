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
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/modfile"
)

func TestAgentRemovalCommandsKeepSkillDeclarationAndManagedCopy(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want engine.Command
	}{
		{name: "remove --agent", args: []string{"remove", "hello", "--agent", ".Claude", "--json"}, want: engine.CommandRemove},
		{name: "share --remove --agent", args: []string{"share", "hello", "--remove", "--agent", "claude", "--json"}, want: engine.CommandShare},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, _ := isolateCLI(t)
			writeInstalledSkill(t, project, "hello")
			cmd := NewRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"share", "hello", "--agent", "claude"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			cmd = NewRootCmd()
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute(%v) = %v, want success", tt.args, err)
			}
			var rep engine.Report
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Action != tt.want {
				t.Errorf("Execute(%v) JSON action = %q, want %q", tt.args, rep.Action, tt.want)
			}
			m, err := modfile.LoadMod(project)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Skills) != 1 || len(m.Skills[0].Agents) != 0 {
				t.Errorf("Execute(%v) declarations = %+v, want unshared hello kept", tt.args, m.Skills)
			}
			if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "hello", "SKILL.md")); err != nil {
				t.Errorf("Execute(%v) managed copy = %v, want kept", tt.args, err)
			}
		})
	}
}

func TestEmptyAgentCannotBecomeFullSkillRemoval(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "hello", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", ",", " , "} {
		cmd = NewRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"remove", "hello", "--agent", value, "--yes"})
		if err := cmd.Execute(); err == nil {
			t.Errorf("Execute(remove hello --agent %q --yes) = nil, want argument error", value)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "hello", "SKILL.md")); err != nil {
		t.Errorf("Remove(empty agent) managed copy = %v, want kept", err)
	}
	m, err := modfile.LoadMod(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || len(m.Skills[0].Agents) != 1 {
		t.Errorf("Remove(empty agent) declarations = %+v, want original hello and agent", m.Skills)
	}
}

func TestShareRemoveRequiresAgent(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--remove"})
	if err := cmd.Execute(); err == nil {
		t.Error("Execute(share --remove) = nil, want missing agent error")
	}
}

// TestRemoveSkillFlagSpellsTheSameSelection pins remove's --skill as the flag
// spelling of the positional names, split on commas exactly like share's, and
// merged with the positionals rather than replacing them.
func TestRemoveSkillFlagSpellsTheSameSelection(t *testing.T) {
	project, _ := isolateCLI(t)
	writeInstalledSkill(t, project, "hello")
	writeInstalledSkill(t, project, "world")
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"share", "--all", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cmd = NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"remove", "--skill", "hello", "--agent", "claude", "world", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(remove --skill hello world --agent claude) = %v, want success", err)
	}
	m, err := modfile.LoadMod(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 2 {
		t.Fatalf("declarations = %+v, want both entries kept", m.Skills)
	}
	for _, sk := range m.Skills {
		if len(sk.Agents) != 0 {
			t.Errorf("declaration %q agents = %v, want unshared", sk.Name, sk.Agents)
		}
	}
	for _, name := range []string{"hello", "world"} {
		if _, err := os.Stat(filepath.Join(project, ".agents", "skills", name, "SKILL.md")); err != nil {
			t.Errorf("managed copy %s = %v, want kept", name, err)
		}
	}
}
