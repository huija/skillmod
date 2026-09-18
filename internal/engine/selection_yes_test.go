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
	"github.com/huija/skillmod/internal/testutil"
)

func TestShareYesWithoutSelectionDoesNotChooseEverySkill(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{Agents: []string{"claude"}}, testIO()); err == nil {
		t.Error("Share(--yes without skills or --all) = nil, want selection error")
	}
	for _, name := range []string{modfile.ModFileName, modfile.LockFileName, ".claude"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Share(--yes without selection) Lstat(%q) = %v, want not exist", name, err)
		}
	}
}

func TestShareYesCanStillChooseSkillsAndAgentsInteractively(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	chooser := &shareChooser{selections: [][]int{{0}, {0}}}
	rep, err := eng.Share(ctx, engine.ShareOptions{}, engine.IO{Out: io.Discard, Confirm: chooser, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if chooser.calls != 2 || len(rep.Entries) != 1 || rep.Entries[0].Name != "alpha" {
		t.Errorf("Share(interactive --yes) calls=%d entries=%+v, want 2 selections and alpha only", chooser.calls, rep.Entries)
	}
}

func TestRemoveYesCanChooseWithoutChoosingEverything(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "alpha", "")
	writeLocalSkill(t, root, "beta", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	chooser := &shareChooser{selections: [][]int{{0}}}
	if _, err := eng.Remove(ctx, nil, engine.IO{Out: io.Discard, Confirm: chooser, Yes: true}); err != nil {
		t.Fatal(err)
	}
	m := loadMod(t, root)
	if len(m.Skills) != 1 || m.Skills[0].Name != "beta" {
		t.Errorf("Remove(interactive --yes) remaining entries = %+v, want beta only", m.Skills)
	}
}

func TestGetYesCanStillChooseOneSkillInteractively(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("skills/alpha", "alpha")
	r.WriteSkill("skills/beta", "beta")
	r.CommitAll("collection")
	r.Tag("v1.0.0")
	r.Finish()
	eng := newEngine(t, t.TempDir(), t.TempDir())
	chooser := &shareChooser{selections: [][]int{{0}}}
	rep, err := eng.Get(ctx, r.URL+"@v1.0.0", "", engine.IO{Out: io.Discard, Confirm: chooser, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if chooser.calls != 1 || len(rep.Entries) != 1 || rep.Entries[0].Name != "alpha" {
		t.Errorf("Get(interactive --yes) calls=%d entries=%+v, want one selection and alpha only", chooser.calls, rep.Entries)
	}
}

func TestGetRejectsInvalidAliasBeforeChoosingCollectionSkills(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("skills/alpha", "alpha")
	r.WriteSkill("skills/beta", "beta")
	r.CommitAll("collection")
	r.Tag("v1.0.0")
	r.Finish()
	eng := newEngine(t, t.TempDir(), t.TempDir())
	chooser := &shareChooser{selections: [][]int{{0}}}
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "bad alias", engine.IO{Out: io.Discard, Confirm: chooser}); err == nil {
		t.Error("Get(alias=bad alias) = nil, want validation error")
	}
	if chooser.calls != 0 {
		t.Errorf("Get(alias=bad alias) picker calls = %d, want 0", chooser.calls)
	}
}
