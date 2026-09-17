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
	"github.com/huija/skillmod/internal/modfile"
)

func TestShareAdoptsOnlySelectedSkillsAndCleansThemOnRemove(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"alpha"}, Agents: []string{"claude"}}, testIO())
	if err != nil {
		t.Fatal(err)
	}
	m := loadMod(t, root)
	if len(m.Skills) != 1 || m.Skills[0].Name != "alpha" || !m.Skills[0].Local {
		t.Fatalf("Share(alpha) declarations = %+v, want only local alpha", m.Skills)
	}
	if !rep.Entries[0].Local || rep.Entries[0].Directory != "alpha" {
		t.Errorf("Share(alpha) report = %+v, want local alpha directory", rep.Entries[0])
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(adopted alpha) = %v, want success", err)
	}
	if _, err := eng.Remove(ctx, []string{"alpha"}, testIO()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{installedDir(root, "alpha"), filepath.Join(agentSkillsDir(root, "claude"), "alpha")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Remove(adopted alpha) Lstat(%q) = %v, want not exist", path, err)
		}
	}
	if _, err := os.Stat(installedDir(root, "beta")); err != nil {
		t.Errorf("Remove(alpha) unselected beta = %v, want kept", err)
	}
}

func TestShareAdoptsExistingUnrecordedLinksAndOpensChecked(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	dst := filepath.Join(agentSkillsDir(root, "claude"), "alpha")
	_, commit, err := install.Link(installedDir(root, "alpha"), dst)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	eng := newEngine(t, root, t.TempDir())
	chooser := &shareChooser{selections: [][]int{{0}, {0}}}
	if _, err := eng.Share(ctx, engine.ShareOptions{}, engine.IO{Out: io.Discard, Confirm: chooser}); err != nil {
		t.Fatal(err)
	}
	if !chooser.options[0][0].Selected || chooser.options[0][1].Selected {
		t.Errorf("Share(unrecorded alpha link) skill choices = %+v, want only alpha checked", chooser.options[0])
	}
	if !chooser.options[1][0].Selected || chooser.options[1][0].Label != "claude" {
		t.Errorf("Share(unrecorded alpha link) agent choices = %+v, want claude checked", chooser.options[1])
	}
	if got := agentsOf(loadMod(t, root), "alpha"); len(got) != 1 || got[0] != "claude" {
		t.Errorf("Share(unrecorded alpha link) agents = %v, want [claude]", got)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Errorf("Sync(adopted alpha link) = %v, want success", err)
	}
}

func TestShareAdoptionPreservesRemoteProvenanceAndAlias(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "review", testIO()); err != nil {
		t.Fatal(err)
	}
	before := loadLockSkill(t, root, "hello")
	m := loadMod(t, root)
	m.Skills = nil
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"review"}, Agents: []string{"workbuddy"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	sk := loadMod(t, root).Skills[0]
	if sk.Local || sk.Source != before.Source || sk.Version != before.Version || sk.Alias != "review" {
		t.Errorf("Share(undeclared remote review) declaration = %+v, want provenance %+v and alias review", sk, before)
	}
	after := loadLockSkill(t, root, "hello")
	if after.Commit != before.Commit || after.Dirhash != before.Dirhash || after.InstallDir() != "review" {
		t.Errorf("Share(undeclared remote review) lock = %+v, want baseline %+v", after, before)
	}
}

func TestShareAdoptionDryRunLeavesNoStateOrLinks(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO(), engine.MutationOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Entries[0].Local {
		t.Errorf("Share(undeclared hello, dry-run) entry = %+v, want local adoption in plan", rep.Entries[0])
	}
	for _, name := range []string{modfile.ModFileName, modfile.LockFileName, ".claude"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Share(dry-run) Lstat(%q) = %v, want not exist", name, err)
		}
	}
}

func TestShareOffersDeclaredCustomAgentWhenDirectoryIsMissing(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"workbuddy"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".workbuddy")); err != nil {
		t.Fatal(err)
	}
	chooser := &shareChooser{selections: [][]int{{2}}}
	if _, err := eng.Share(ctx, engine.ShareOptions{}, engine.IO{Out: io.Discard, Confirm: chooser}); err != nil {
		t.Fatal(err)
	}
	if got := chooser.options[0][2]; got.Label != "workbuddy" || !got.Selected {
		t.Errorf("Share(missing custom directory) choice = %+v, want workbuddy checked", got)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify(restored custom directory) = %v, want success", err)
	}
}

func TestShareRollsBackOverwriteWhenStateCannotBeSaved(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	dst := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := false
	w := &shareWriteHook{hook: func([]byte) error {
		_, err := os.Stat(filepath.Join(dst, "notes.txt"))
		if errors.Is(err, os.ErrNotExist) && !blocked {
			blocked = true
			return os.Mkdir(filepath.Join(root, modfile.LockFileName), 0o755)
		}
		return nil
	}}
	eng := newEngine(t, root, t.TempDir())
	_, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}, OnConflict: engine.ConflictOverwrite}, engine.IO{Out: w})
	if !blocked {
		t.Fatal("Share(overwrite) never reached the staged destination, want simulated save failure")
	}
	if err == nil {
		t.Error("Share(overwrite with blocked lock path) = nil, want save error")
	}
	if got := readFile(t, filepath.Join(dst, "notes.txt")); got != "keep me" {
		t.Errorf("Share(failed state save) restored content = %q, want keep me", got)
	}
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Share(failed state save) SKILL.mod = %v, want not exist", err)
	}
}

func TestShareDoesNotResetDeclaredDriftBaseline(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "old\n")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	before := loadLockSkill(t, root, "hello")
	if err := os.WriteFile(filepath.Join(installedDir(root, "hello"), "run.sh"), []byte("edited\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if got := loadLockSkill(t, root, "hello").Dirhash; got != before.Dirhash {
		t.Errorf("Share(declared modified hello) baseline = %q, want %q", got, before.Dirhash)
	}
	_, err := eng.Verify(ctx, testIO())
	var drift *engine.DriftError
	if !errors.As(err, &drift) {
		t.Errorf("Verify(after Share of modified hello) = %v, want DriftError", err)
	}
}

func TestShareRejectsUnportableAdoptionBeforeLinking(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	if err := os.Rename(installedDir(root, "hello"), installedDir(root, "bad alias")); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err == nil {
		t.Error("Share(unportable alias) = nil, want adoption validation error")
	}
	for _, name := range []string{modfile.ModFileName, modfile.LockFileName, ".claude"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Share(unportable alias) Lstat(%q) = %v, want not exist", name, err)
		}
	}
}

type shareWriteHook struct{ hook func([]byte) error }

func (w *shareWriteHook) Write(p []byte) (int, error) {
	if err := w.hook(p); err != nil {
		return 0, err
	}
	return len(p), nil
}
