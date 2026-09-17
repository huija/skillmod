// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Share tests exercise installed directories and destination conflicts without
// needing a repository fixture.
package engine_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/agents"
	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/ui"
)

// writeLocalSkill installs one skill directory without a manifest or lock.
// An empty content leaves a SKILL.md-only skill.
func writeLocalSkill(t *testing.T, root, dirName, extra string) {
	t.Helper()
	dir := filepath.Join(root, ".agents", "skills", dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: " + dirName + "\ndescription: test skill\n---\n# " + dirName + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	if extra != "" {
		if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(extra), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// assertManagedLink verifies that dst is a symlink resolving to the managed
// skill directory src, the representation share exists to produce.
func assertManagedLink(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %o)", dst, info.Mode())
	}
	resolved, err := filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := filepath.EvalSymlinks(src)
	if err != nil {
		t.Fatal(err)
	}
	if fsutil.FoldKey(resolved) != fsutil.FoldKey(managed) {
		t.Fatalf("%s links to %s, want the managed copy %s", dst, resolved, managed)
	}
}

func agentSkillsDir(root, agent string) string {
	target, _ := agents.Lookup(agent)
	return target.Dir(root)
}

func TestShareLinksEveryAgentToTheManagedCopy(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "#!/bin/sh\necho hello\n")
	writeLocalSkill(t, root, "world", "")

	rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
		All: true, Agents: []string{"claude", "codex"},
	}, testIO())
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if rep.Action != engine.CommandShare || len(rep.Entries) != 2 {
		t.Fatalf("report = %+v, want two shared entries", rep)
	}
	for agent := range map[string]bool{"claude": true, "codex": true} {
		for _, name := range []string{"hello", "world"} {
			src := filepath.Join(root, ".agents", "skills", name)
			dst := filepath.Join(agentSkillsDir(root, agent), name)
			assertManagedLink(t, src, dst)
			data, readErr := os.ReadFile(filepath.Join(dst, "SKILL.md"))
			if readErr != nil {
				t.Fatalf("%s/%s: %v", agent, name, readErr)
			}
			if !strings.Contains(string(data), "name: "+name) {
				t.Fatalf("%s/%s SKILL.md = %q", agent, name, data)
			}
		}
	}
	for _, entry := range rep.Entries {
		if entry.Action != engine.ActionInstall || len(entry.TargetResults) != 2 {
			t.Fatalf("entry %s = %+v, want install across two targets", entry.Name, entry)
		}
		for _, target := range entry.TargetResults {
			if target.Action != engine.ActionInstall {
				t.Fatalf("target %s action = %s, want install", target.Path, target.Action)
			}
		}
	}
}

func TestShareKeepsLinkedAndIdenticalDestinations(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "content\n")
	writeLocalSkill(t, root, "twin", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("first Share: %v", err)
	}
	// A foreign real directory with the same content is kept too: it holds
	// nothing worth discarding and replacing it would only churn.
	twin := filepath.Join(agentSkillsDir(root, "claude"), "twin")
	if err := os.RemoveAll(twin); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(twin, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: twin\ndescription: test skill\n---\n# twin\n"
	if err := os.WriteFile(filepath.Join(twin, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO())
	if err != nil {
		t.Fatalf("second Share: %v", err)
	}
	if rep.Entries[0].Action != engine.ActionKeep || rep.Entries[0].TargetResults[0].Action != engine.ActionKeep {
		t.Fatalf("linked destination = %+v, want keep", rep.Entries[0])
	}
	if rep.Entries[1].Action != engine.ActionKeep || rep.Entries[1].TargetResults[0].Action != engine.ActionKeep {
		t.Fatalf("identical real destination = %+v, want keep", rep.Entries[1])
	}
}

func TestShareConflictPolicies(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		yes        bool
		wantErr    bool
		wantAction engine.TargetStatus
	}{
		{name: "overwrite replaces the content with a link", policy: engine.ConflictOverwrite, wantAction: engine.ActionInstall},
		{name: "skip keeps the content", policy: engine.ConflictSkip, wantErr: true, wantAction: engine.ActionSkip},
		{name: "ask aborts when nobody answers", policy: engine.ConflictAsk, wantErr: true, wantAction: engine.ActionConflict},
		{name: "ask skips under --yes", policy: engine.ConflictAsk, yes: true, wantErr: true, wantAction: engine.ActionSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeLocalSkill(t, root, "hello", "current\n")
			dst := filepath.Join(agentSkillsDir(root, "claude"), "hello")
			if err := os.MkdirAll(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("destination\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			io := engine.IO{Out: io.Discard, Yes: tt.yes}
			rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
				All: true, Agents: []string{"claude"}, OnConflict: tt.policy,
			}, io)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Share(%s) succeeded, want an error", tt.policy)
				}
			} else if err != nil {
				t.Fatalf("Share(%s): %v", tt.policy, err)
			}
			// An aborted run resolves nothing, so there is no report to inspect;
			// the kept content below is the assertion that matters.
			if rep != nil {
				target := rep.Entries[0].TargetResults[0]
				if target.Action != tt.wantAction {
					t.Fatalf("target action = %s, want %s", target.Action, tt.wantAction)
				}
				if tt.wantErr && tt.wantAction == engine.ActionSkip {
					var partial *engine.PartialError
					if !errors.As(err, &partial) {
						t.Fatalf("Share error = %v, want a PartialError for skipped targets", err)
					}
				}
			}
			if tt.wantAction == engine.ActionInstall {
				assertManagedLink(t, filepath.Join(root, ".agents", "skills", "hello"), dst)
			} else {
				data, readErr := os.ReadFile(filepath.Join(dst, "SKILL.md"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(data) != "destination\n" {
					t.Fatalf("destination SKILL.md = %q, want it kept", data)
				}
			}
		})
	}
}

func TestShareTreatsAForeignLinkAsAConflict(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "current\n")
	dst := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, dst); err != nil {
		t.Fatal(err)
	}
	// Under --yes the foreign link is skipped, not silently replaced.
	rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
		All: true, Agents: []string{"claude"},
	}, engine.IO{Out: io.Discard, Yes: true})
	var partial *engine.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("Share error = %v, want a PartialError for the foreign link", err)
	}
	if got := rep.Entries[0].TargetResults[0].Action; got != engine.ActionSkip {
		t.Fatalf("foreign link action = %s, want skip", got)
	}
	// Overwrite replaces it with the managed link.
	rep, err = newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
		All: true, Agents: []string{"claude"}, OnConflict: engine.ConflictOverwrite,
	}, engine.IO{Out: io.Discard})
	if err != nil {
		t.Fatalf("Share(overwrite): %v", err)
	}
	if got := rep.Entries[0].TargetResults[0].Action; got != engine.ActionInstall {
		t.Fatalf("overwritten link action = %s, want install", got)
	}
	assertManagedLink(t, filepath.Join(root, ".agents", "skills", "hello"), dst)
}

func TestShareAskPolicyResolvesPerConflictInteractively(t *testing.T) {
	for _, tt := range []struct {
		choice     int
		wantAction engine.TargetStatus
		wantErr    bool
	}{
		{choice: 0, wantAction: engine.ActionInstall},
		{choice: 1, wantAction: engine.ActionSkip, wantErr: true},
		{choice: 2, wantErr: true, wantAction: engine.ActionConflict},
	} {
		root := t.TempDir()
		writeLocalSkill(t, root, "hello", "current\n")
		dst := filepath.Join(agentSkillsDir(root, "claude"), "hello")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("destination\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		chooser := &shareChooser{conflicts: []int{tt.choice}}
		rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
			All: true, Agents: []string{"claude"},
		}, engine.IO{Out: io.Discard, Confirm: chooser})
		if tt.wantErr && err == nil {
			t.Fatalf("choice %d succeeded, want an error", tt.choice)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("choice %d: %v", tt.choice, err)
		}
		// An abort resolves nothing and produces no report.
		if rep == nil {
			continue
		}
		if got := rep.Entries[0].TargetResults[0].Action; got != tt.wantAction {
			t.Fatalf("choice %d target action = %s, want %s", tt.choice, got, tt.wantAction)
		}
	}
}

func TestShareExplicitSelectorsMatchNamesAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "dir-name", "")
	eng := newEngine(t, root, t.TempDir())
	for _, selector := range []string{"dir-name"} {
		if _, err := eng.Share(ctx, engine.ShareOptions{
			Skills: []string{selector}, Agents: []string{"claude"},
		}, testIO()); err != nil {
			t.Fatalf("Share(%q): %v", selector, err)
		}
	}
	assertManagedLink(t, filepath.Join(root, ".agents", "skills", "dir-name"),
		filepath.Join(agentSkillsDir(root, "claude"), "dir-name"))
	_, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"missing"}, Agents: []string{"claude"}}, testIO())
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown selector error = %v, want it to name the request", err)
	}
}

func TestShareUnknownAgentNamesTheRegistry(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	_, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
		All: true, Agents: []string{"nope"},
	}, testIO())
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("unknown agent error = %v, want the registered names", err)
	}
}

func TestShareRejectsDestinationsOverlappingTheManagedDirectory(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	for _, dir := range []string{".agents", ".agents/skills", ".agents/skills/hello"} {
		if _, err := eng.Share(ctx, engine.ShareOptions{
			All: true, Dirs: []string{dir},
		}, testIO()); err == nil || !strings.Contains(err.Error(), "overlaps") {
			t.Fatalf("Share(--dir %s) error = %v, want a managed-overlap diagnostic", dir, err)
		}
	}
}

func TestShareDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "content\n")
	rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
		All: true, Agents: []string{"claude"},
	}, testIO(), engine.MutationOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Share(dry run): %v", err)
	}
	if _, err := os.Stat(agentSkillsDir(root, "claude")); !os.IsNotExist(err) {
		t.Fatalf("dry run created the agent directory: %v", err)
	}
	if rep.Entries[0].Action != engine.ActionInstall || rep.Entries[0].TargetResults[0].Action != engine.ActionInstall {
		t.Fatalf("dry run report = %+v, want the planned installs", rep)
	}
	if len(rep.Notes) == 0 {
		t.Fatalf("dry run report carries no note: %+v", rep)
	}
}

func TestShareRequiresSkillsAndDestinations(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	writeLocalSkill(t, root, "world", "")
	eng := newEngine(t, root, t.TempDir())
	// Without --yes and without a terminal, an underspecified share must ask
	// for explicit selections instead of guessing.
	mute := engine.IO{Out: io.Discard}
	if _, err := eng.Share(ctx, engine.ShareOptions{Agents: []string{"claude"}}, mute); err == nil {
		t.Fatal("Share without a skill request succeeded")
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true}, mute); err == nil {
		t.Fatal("Share without a destination request succeeded")
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{}, mute); err == nil {
		t.Fatal("non-interactive Share without any request succeeded")
	}
}

func TestShareInteractiveSelectionChoosesSkillsAndTargets(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	writeLocalSkill(t, root, "world", "")
	chooser := &shareChooser{selections: [][]int{{1}, {0}}}
	out := &bytes.Buffer{}
	rep, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{},
		engine.IO{Out: out, Confirm: chooser})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if chooser.calls != 2 {
		t.Fatalf("selector calls = %d, want one for skills and one for targets", chooser.calls)
	}
	if len(chooser.options[0]) != 2 || chooser.options[0][1].Label != "world" {
		t.Fatalf("skill options = %+v", chooser.options[0])
	}
	if len(chooser.options[1]) != 2 || chooser.options[1][0].Label != "claude" {
		t.Fatalf("target options = %+v", chooser.options[1])
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "world" {
		t.Fatalf("report = %+v, want only the selected skill", rep)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "world")); err != nil {
		t.Fatalf("selected skill was not linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill was linked: %v", err)
	}
}

func TestShareInteractiveSelectionRejectsOutOfRangeIndex(t *testing.T) {
	// A selector answering with an index past the list must be refused. The
	// destination path indexes the same list, so trusting the answer would panic
	// before the report is built.
	for _, tc := range []struct {
		name       string
		selections [][]int
	}{
		{name: "skill selection", selections: [][]int{{7}}},
		{name: "destination selection", selections: [][]int{{0}, {9}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLocalSkill(t, root, "hello", "")
			writeLocalSkill(t, root, "world", "")
			_, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{},
				engine.IO{Out: io.Discard, Confirm: &shareChooser{selections: tc.selections}})
			if err == nil {
				t.Fatal("Share accepted an out-of-range selection")
			}
			if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !os.IsNotExist(err) {
				t.Fatalf("out-of-range selection linked something: %v", err)
			}
		})
	}
}

// shareChooser is a ui.MultiSelector fake that answers the skill selection,
// the target selection, and each conflict prompt from scripted choices.
type shareChooser struct {
	selections [][]int // ChooseMany answers, in call order
	conflicts  []int   // Choose answers, in call order; 0 (overwrite) once exhausted
	calls      int
	options    [][]ui.Option
}

func (c *shareChooser) Confirm(string) (bool, error) { return true, nil }

func (c *shareChooser) Choose(string, []string) (int, error) {
	if len(c.conflicts) == 0 {
		return 0, nil
	}
	answer := c.conflicts[0]
	c.conflicts = c.conflicts[1:]
	return answer, nil
}

func (c *shareChooser) ChooseMany(_ string, options []ui.Option) ([]int, error) {
	c.options = append(c.options, options)
	index := c.calls
	c.calls++
	if index >= len(c.selections) {
		return nil, nil
	}
	return c.selections[index], nil
}
