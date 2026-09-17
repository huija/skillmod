// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/ui"
)

func TestShareSkillDefaultsAreScopedToRequestedAgents(t *testing.T) {
	tests := []struct {
		name   string
		agents []string
		want   map[string]bool
	}{
		{name: "workbuddy", agents: []string{"workbuddy"}, want: map[string]bool{"beta": true, "gamma": true}},
		{name: "codex", agents: []string{"codex"}, want: map[string]bool{"alpha": true, "beta": true}},
		{name: "normalized workbuddy", agents: []string{".Workbuddy"}, want: map[string]bool{"beta": true, "gamma": true}},
		{name: "both agents", agents: []string{"codex", "workbuddy"}, want: map[string]bool{"beta": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"alpha", "beta", "gamma", "unshared"} {
				writeLocalSkill(t, root, name, "")
			}
			eng := newEngine(t, root, t.TempDir())
			if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"alpha", "beta"}, Agents: []string{"codex"}}, testIO()); err != nil {
				t.Fatal(err)
			}
			if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"beta", "gamma"}, Agents: []string{"workbuddy"}}, testIO()); err != nil {
				t.Fatal(err)
			}
			chooser := &shareDefaultsChooser{}
			rep, err := eng.Share(ctx, engine.ShareOptions{Agents: tt.agents}, engine.IO{Out: io.Discard, Confirm: chooser})
			if err != nil {
				t.Fatal(err)
			}
			if chooser.calls != 1 {
				t.Fatalf("Share(--agent %v) picker calls = %d, want one skill picker", tt.agents, chooser.calls)
			}
			for _, option := range chooser.options[0] {
				if option.Selected != tt.want[option.Label] {
					t.Errorf("Share(--agent %v) skill %s selected = %v, want %v", tt.agents, option.Label, option.Selected, tt.want[option.Label])
				}
			}
			if len(rep.Entries) != len(tt.want) {
				t.Errorf("Share(--agent %v, unchanged defaults) entries = %+v, want %d already-shared skills", tt.agents, rep.Entries, len(tt.want))
			}
			for _, entry := range rep.Entries {
				if !tt.want[entry.Name] || entry.Action != engine.ActionKeep {
					t.Errorf("Share(--agent %v, unchanged defaults) entry = %+v, want an already-shared skill kept", tt.agents, entry)
				}
			}
			for agent, name := range map[string]string{"workbuddy": "alpha", "codex": "gamma"} {
				path := filepath.Join(agentSkillsDir(root, agent), name)
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("Share(--agent %v, unchanged defaults) unrelated %s link = %v, want not exist", tt.agents, path, err)
				}
			}
		})
	}
}

func TestShareNewAgentDoesNotPreselectSkillsSharedElsewhere(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"alpha"}, Agents: []string{"codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	chooser := &shareDefaultsChooser{}
	if _, err := eng.Share(ctx, engine.ShareOptions{Agents: []string{"workbuddy"}}, engine.IO{Out: io.Discard, Confirm: chooser}); err == nil {
		t.Error("Share(new --agent workbuddy, unchanged defaults) = nil, want empty-selection error")
	}
	for _, option := range chooser.options[0] {
		if option.Selected {
			t.Errorf("Share(new --agent workbuddy) skill %s selected = true, want false", option.Label)
		}
	}
	if _, err := os.Stat(agentSkillsDir(root, "workbuddy")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Share(new --agent workbuddy, unchanged defaults) directory = %v, want not exist", err)
	}
}

func TestShareUnrecordedLinkDefaultsAreScopedToRequestedAgent(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		writeLocalSkill(t, root, name, "")
	}
	for agent, name := range map[string]string{"codex": "alpha", "workbuddy": "beta"} {
		_, commit, err := install.Link(installedDir(root, name), filepath.Join(agentSkillsDir(root, agent), name))
		if err != nil {
			t.Fatal(err)
		}
		commit()
	}
	eng := newEngine(t, root, t.TempDir())
	chooser := &shareDefaultsChooser{}
	rep, err := eng.Share(ctx, engine.ShareOptions{Agents: []string{"workbuddy"}}, engine.IO{Out: io.Discard, Confirm: chooser})
	if err != nil {
		t.Fatal(err)
	}
	if choices := chooser.options[0]; len(choices) != 2 || choices[0].Selected || !choices[1].Selected {
		t.Errorf("Share(unrecorded links, --agent workbuddy) choices = %+v, want only beta checked", choices)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "beta" {
		t.Errorf("Share(unrecorded links, unchanged defaults) entries = %+v, want beta only", rep.Entries)
	}
	if m := loadMod(t, root); len(m.Skills) != 1 || m.Skills[0].Name != "beta" {
		t.Errorf("Share(unrecorded links, unchanged defaults) declarations = %+v, want only beta adopted", m.Skills)
	}
}

// shareDefaultsChooser models confirming a multi-select without toggling items.
type shareDefaultsChooser struct{ shareChooser }

func (c *shareDefaultsChooser) ChooseMany(prompt string, options []ui.Option) ([]int, error) {
	var selected []int
	for i, option := range options {
		if option.Selected {
			selected = append(selected, i)
		}
	}
	c.selections = append(c.selections, selected)
	return c.shareChooser.ChooseMany(prompt, options)
}
