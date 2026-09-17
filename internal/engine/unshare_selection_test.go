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
	"github.com/huija/skillmod/internal/modfile"
)

func TestRemoveAgentPicksOnlyMatchingSkillsAndKeepsManagedCopies(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"alpha"}, Agents: []string{"claude", "codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"beta"}, Agents: []string{"codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	chooser := &shareChooser{selections: [][]int{{0}}}
	rep, err := eng.Remove(ctx, nil, engine.IO{Out: io.Discard, Confirm: chooser}, engine.RemoveOptions{Agents: []string{".Claude", "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != engine.CommandRemove || len(rep.Entries) != 1 || len(rep.Entries[0].TargetResults) != 1 {
		t.Errorf("Remove(--agent claude repeated) report = %+v, want one removal target", rep)
	}
	if choices := chooser.options[0]; len(choices) != 1 || choices[0].Label != "alpha" || choices[0].Selected {
		t.Errorf("Remove(--agent claude) choices = %+v, want only alpha unchecked", choices)
	}
	if got := agentsOf(loadMod(t, root), "alpha"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("Remove(--agent claude) alpha agents = %v, want [codex]", got)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Remove(--agent claude) alpha link = %v, want not exist", err)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(after selected unsharing) = %v, want both managed copies and codex links intact", err)
	}
}

func TestRemoveAgentAllIgnoresSkillsSharedOnlyToOtherAgents(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"alpha"}, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"beta"}, Agents: []string{"codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Remove(ctx, nil, testIO(), engine.RemoveOptions{All: true, Agents: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "alpha" {
		t.Errorf("Remove(--all --agent claude) entries = %+v, want alpha only", rep.Entries)
	}
	if got := agentsOf(loadMod(t, root), "beta"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("Remove(--all --agent claude) beta agents = %v, want [codex]", got)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(after --all unsharing) = %v, want success", err)
	}
}

func TestShareRemovePicksSkillsWithoutNames(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)
	chooser := &shareChooser{selections: [][]int{{0}}}
	if _, err := eng.Share(ctx, engine.ShareOptions{Remove: []string{"claude"}}, engine.IO{Out: io.Discard, Confirm: chooser}); err != nil {
		t.Errorf("Share(--remove with interactive selection) = %v, want success", err)
	}
	if chooser.calls != 1 {
		t.Errorf("Share(--remove) picker calls = %d, want 1", chooser.calls)
	}
}

func TestUnshareAgentDirectoriesThatResolveToTheSameLocation(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	if err := os.MkdirAll(agentSkillsDir(root, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".claude", filepath.Join(root, ".workbuddy")); err != nil {
		t.Skipf("agent directory symlinks unavailable: %v", err)
	}
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"hello"}, Agents: []string{"claude", "workbuddy"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Remove(ctx, []string{"hello"}, testIO(), engine.RemoveOptions{Agents: []string{"claude", "workbuddy"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || len(rep.Entries[0].TargetResults) != 2 {
		t.Errorf("Remove(aliased agent directories) report = %+v, want both requested agents", rep)
	}
	for _, agent := range []string{"claude", "workbuddy"} {
		if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, agent), "hello")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Remove(aliased agent directories) %s link = %v, want not exist", agent, err)
		}
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 0 {
		t.Errorf("Remove(aliased agent directories) agents = %v, want none", got)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(after removing aliased agent directories) = %v, want managed copy intact", err)
	}
}

func TestUnshareMissingManagedCopyCleansDanglingLink(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)
	makeShareLinkSymlink(t, root, "hello")
	dropManagedSkill(t, root, "hello")
	if _, err := eng.Remove(ctx, []string{"hello"}, testIO(), engine.RemoveOptions{Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Remove(--agent claude, missing managed hello) link = %v, want not exist", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("Remove(--agent claude, missing managed hello) agents = %v, want [codex]", got)
	}
}

func TestUnshareRollsBackLinksWhenStateSaveFails(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)
	modPath := filepath.Join(root, modfile.ModFileName)
	before := readFile(t, modPath)
	blocked := false
	w := &shareWriteHook{hook: func([]byte) error {
		if !blocked {
			blocked = true
			if err := os.Remove(modPath); err != nil {
				return err
			}
			return os.Mkdir(modPath, 0o755)
		}
		return nil
	}}
	if _, err := eng.Remove(ctx, []string{"hello"}, engine.IO{Out: w}, engine.RemoveOptions{Agents: []string{"claude"}}); err == nil {
		t.Error("Remove(--agent claude, blocked state save) = nil, want error")
	}
	if err := os.Remove(modPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modPath, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(after failed unshare) = %v, want original links and lock restored", err)
	}
}

func TestUnshareRechecksChangedDestinationContent(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)
	dst := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	changed := false
	w := &shareWriteHook{hook: func([]byte) error {
		if !changed {
			changed = true
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, "notes.txt"), []byte("new foreign content"), 0o644)
		}
		return nil
	}}
	if _, err := eng.Remove(ctx, []string{"hello"}, engine.IO{Out: w}, engine.RemoveOptions{Agents: []string{"claude"}}); err == nil {
		t.Error("Remove(--agent claude, changed destination) = nil, want recheck error")
	}
	if got := readFile(t, filepath.Join(dst, "notes.txt")); got != "new foreign content" {
		t.Errorf("Remove(changed destination) content = %q, want new foreign content", got)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 2 {
		t.Errorf("Remove(changed destination) agents = %v, want original [claude codex]", got)
	}
}

func TestUnshareWithoutSelectionAndMixedAllNamesDoNotMutate(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)
	for _, run := range []engine.RemoveOptions{{Agents: []string{"claude"}}, {All: true}, {All: true, Agents: []string{"claude"}}} {
		var names []string
		if run.All {
			names = []string{"hello"}
		}
		if _, err := eng.Remove(ctx, names, testIO(), run); err == nil {
			t.Errorf("Remove(names=%v, options=%+v) = nil, want selection error", names, run)
		}
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(after refused removals) = %v, want intact state", err)
	}
}
