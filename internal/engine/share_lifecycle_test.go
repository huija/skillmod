// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
)

func TestSyncRestoresSharedSkillsOnFirstRun(t *testing.T) {
	r := newHelloRepo(t)
	original := t.TempDir()
	eng := newEngine(t, original, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude", "codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	m := loadMod(t, original)
	l, err := modfile.LoadLock(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, withLock := range []bool{true, false} {
		t.Run(map[bool]string{true: "mod and lock", false: "mod only"}[withLock], func(t *testing.T) {
			root := t.TempDir()
			if err := modfile.SaveMod(root, m); err != nil {
				t.Fatal(err)
			}
			if withLock {
				if err := modfile.SaveLock(root, l); err != nil {
					t.Fatal(err)
				}
			}
			eng := newEngine(t, root, t.TempDir())
			var output bytes.Buffer
			runIO := engine.IO{Out: &output, Yes: true}
			rep, err := eng.Sync(ctx, engine.SyncOptions{DryRun: true}, runIO)
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Entries) != 1 || len(rep.Entries[0].TargetResults) != 3 {
				t.Fatalf("first Sync(dry-run) report = %+v, want managed and two shared targets", rep)
			}
			for _, tr := range rep.Entries[0].TargetResults {
				if tr.Action != engine.ActionInstall {
					t.Errorf("Sync(dry-run) target %q action = %q, want install", tr.Path, tr.Action)
				}
				if _, err := os.Lstat(tr.Path); !os.IsNotExist(err) {
					t.Errorf("Sync(dry-run) wrote %q: %v, want absent", tr.Path, err)
				}
			}
			if _, err := eng.Sync(ctx, engine.SyncOptions{}, runIO); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"claude", "codex"} {
				shared := filepath.Join(agentSkillsDir(root, agent), "hello")
				if got := readFile(t, filepath.Join(shared, "SKILL.md")); !strings.Contains(got, "name: hello") {
					t.Errorf("first Sync shared content for %s = %q, want hello skill", agent, got)
				}
			}
			if got := lockAgentsOf(t, root, "hello"); !slices.Equal(got, []string{"claude", "codex"}) {
				t.Errorf("first Sync lock agents = %v, want [claude codex]", got)
			}
			if _, err := eng.Verify(ctx, testIO()); err != nil {
				t.Errorf("Verify after first Sync = %v, want consistency", err)
			}
			output.Reset()
			rep, err = eng.Sync(ctx, engine.SyncOptions{}, runIO)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Entries[0].Action != engine.ActionKeep || !strings.Contains(output.String(), "no changes") {
				t.Errorf("second Sync = %+v, output %q, want unchanged keep", rep, output.String())
			}
			if _, err := eng.Remove(ctx, []string{"hello"}, testIO()); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"claude", "codex"} {
				shared := filepath.Join(agentSkillsDir(root, agent), "hello")
				if _, err := os.Lstat(shared); !os.IsNotExist(err) {
					t.Errorf("Remove after first Sync left %q: %v, want absent", shared, err)
				}
			}
		})
	}
}

func TestSyncReportsRestoredRemoteShareLink(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		var output bytes.Buffer
		rep, err := eng.Sync(ctx, engine.SyncOptions{DryRun: dryRun}, engine.IO{Out: &output, Yes: true})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Entries[0].Action != engine.ActionInstall || !strings.Contains(output.String(), "1 entries") {
			t.Errorf("Sync(dryRun=%v) = %+v, output %q, want one changed entry", dryRun, rep, output.String())
		}
	}
}

func TestSyncRestoresSourceOfRelativeDanglingShareLink(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	m := &modfile.Mod{SchemaVersion: modfile.SchemaVersion, Skills: []modfile.ModSkill{
		{Name: "hello", Source: r.URL, Version: "v1.0.0", Agents: []string{"claude"}},
	}}
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}
	agentDir := agentSkillsDir(root, "claude")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Rel(agentDir, installedDir(root, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agentDir, "hello")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("native directory symlinks unavailable: %v", err)
	}
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Fatalf("Sync(relative dangling share link) = %v, want restored source without conflict", err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Errorf("share link after Sync = %q, %v, want original relative link %q", got, err, target)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify after restoring shared source = %v, want consistency", err)
	}
}

func TestSyncUpdatesCleanFallbackCopyWithManagedVersion(t *testing.T) {
	r := newHelloRepo(t)
	r.Write("new.md", "new version\n")
	r.CommitAll("new version")
	r.Evolve("v1.1.0", false)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.RemoveAll(shared); err != nil {
		t.Fatal(err)
	}
	if err := install.CopyDir(installedDir(root, "hello"), shared); err != nil {
		t.Fatal(err)
	}
	m := loadMod(t, root)
	m.Skills[0].Version = "v1.1.0"
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Fatalf("Sync(clean fallback with version change) = %v, want successful refresh", err)
	}
	if got := readFile(t, filepath.Join(shared, "new.md")); got != "new version\n" {
		t.Errorf("shared fallback content after version change = %q, want new version", got)
	}
}

func TestRegetPreservesSharedAgents(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("agents after re-get = %v, want [claude]", got)
	}
	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(link, "SKILL.md")); err != nil {
		t.Errorf("Sync after re-get did not restore shared skill: %v", err)
	}
}

func TestSyncRecordsHandEditedAgentsAndKeepsCleanupHistory(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		m := loadMod(t, root)
		m.Skills[0].Agents = []string{agent}
		if err := modfile.SaveMod(root, m); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
			t.Fatal(err)
		}
	}
	if got := lockAgentsOf(t, root, "hello"); !slices.Equal(got, []string{"claude", "codex"}) {
		t.Errorf("lock cleanup history after hand edits = %v, want [claude codex]", got)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	if got := lockAgentsOf(t, root, "hello"); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("lock cleanup history after unsharing codex = %v, want [claude]", got)
	}
	if _, err := eng.Remove(ctx, []string{"hello"}, testIO()); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, agent), "hello")); !os.IsNotExist(err) {
			t.Errorf("Remove kept %s's recorded link: %v, want absent", agent, err)
		}
	}
}

func TestLifecycleRejectsAgentAliasIntoManagedDirectory(t *testing.T) {
	for _, command := range []string{"unshare", "sync", "remove", "prune"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			eng := newEngine(t, root, t.TempDir())
			setupSharedSkill(t, eng, root, "hello")
			agentDir := agentSkillsDir(root, "claude")
			if err := os.RemoveAll(agentDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, ".agents", "skills"), agentDir); err != nil {
				t.Skipf("native directory symlinks unavailable: %v", err)
			}
			var err error
			switch command {
			case "unshare":
				_, err = eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"claude"}}, testIO())
			case "sync":
				_, err = eng.Sync(ctx, engine.SyncOptions{}, testIO())
			case "remove":
				_, err = eng.Remove(ctx, []string{"hello"}, testIO())
			case "prune":
				m := loadMod(t, root)
				m.Skills = nil
				if err := modfile.SaveMod(root, m); err != nil {
					t.Fatal(err)
				}
				_, err = eng.Prune(ctx, testIO())
			}
			if err == nil {
				t.Errorf("%s accepted agent alias into managed directory, want error", command)
			}
			if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "SKILL.md")); err != nil {
				t.Errorf("%s deleted managed content: %v, want preserved", command, err)
			}
		})
	}
}

type sharePlanWriter func([]byte) (int, error)

func (w sharePlanWriter) Write(p []byte) (int, error) { return w(p) }

func TestUnshareRechecksParentBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	agentDir := agentSkillsDir(root, "claude")
	probe := filepath.Join(root, "probe")
	if err := os.Symlink(filepath.Join(root, ".agents", "skills"), probe); err != nil {
		t.Skipf("native directory symlinks unavailable: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	writer := sharePlanWriter(func(p []byte) (int, error) {
		if err := os.RemoveAll(agentDir); err != nil {
			return 0, err
		}
		if err := os.Symlink(filepath.Join(root, ".agents", "skills"), agentDir); err != nil {
			return 0, err
		}
		return len(p), nil
	})
	_, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"claude"}}, engine.IO{Out: writer, Yes: true})
	if err == nil {
		t.Error("unshare deleted through a changed parent, want error")
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "SKILL.md")); err != nil {
		t.Errorf("unshare with changed parent deleted managed content: %v, want preserved", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("failed unshare changed declaration to %v, want [claude]", got)
	}
}
