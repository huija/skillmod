// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Declaration tests cover the per-skill agents list in SKILL.mod and the
// lifecycle commands that consume it: share records the agents on an entry,
// sync realigns drifted links, verify reports them, and remove/prune take the
// links down with the managed copies they point at.
package engine_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/modfile"
)

// setupSharedSkill writes one local skill, records it with init, and shares it
// to claude so the agents declaration and the link are both in place.
func setupSharedSkill(t *testing.T, eng *engine.Engine, root, dirName string) {
	t.Helper()
	writeLocalSkill(t, root, dirName, "")
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
}

func loadMod(t *testing.T, root string) *modfile.Mod {
	t.Helper()
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod: %v", err)
	}
	return m
}

// agentsOf returns the agents declared on the entry whose installation
// directory matches, or nil when the manifest declares no such entry.
func agentsOf(m *modfile.Mod, dirName string) []string {
	for _, sk := range m.Skills {
		if sk.DirName() == dirName {
			return sk.Agents
		}
	}
	return nil
}

func TestShareRecordsAgentsOnTheSkillEntry(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude", "codex"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
	got := agentsOf(loadMod(t, root), "hello")
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("agents on hello = %v, want [claude codex]", got)
	}
}

// TestShareRecordsPerSkillAgents is the case target-level declarations cannot
// express: one skill goes to claude, another to codex, in the same manifest.
func TestShareRecordsPerSkillAgents(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "only-claude", "")
	writeLocalSkill(t, root, "only-codex", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"only-claude"}, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share(only-claude): %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"only-codex"}, Agents: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(only-codex): %v", err)
	}
	m := loadMod(t, root)
	if got := agentsOf(m, "only-claude"); len(got) != 1 || got[0] != "claude" {
		t.Errorf("agents on only-claude = %v, want [claude]", got)
	}
	if got := agentsOf(m, "only-codex"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("agents on only-codex = %v, want [codex]", got)
	}
	// The links match the declaration, not the union of every agent.
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "only-codex")); !os.IsNotExist(err) {
		t.Errorf("claude received a link the declaration never named: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "codex"), "only-claude")); !os.IsNotExist(err) {
		t.Errorf("codex received a link the declaration never named: %v", err)
	}
}

// TestShareIsAdditiveAndRemoveSubtracts pins the list semantics: --agent adds
// to what an entry already names, and --remove is the only way one comes off.
func TestShareIsAdditiveAndRemoveSubtracts(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	// Adding an agent that is already declared must not duplicate it.
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share(claude again): %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 2 {
		t.Fatalf("agents after re-adding claude = %v, want the original two", got)
	}
	// Subtracting one leaves the other in place.
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"hello"}, Remove: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove codex): %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("agents after --remove codex = %v, want [claude]", got)
	}
}

func TestShareDirectoryDestinationIsNotRecorded(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	custom := filepath.Join(root, "custom-skills")
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Dirs: []string{custom}}, testIO()); err != nil {
		t.Fatalf("Share(--dir): %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 0 {
		t.Fatalf("Share(--dir) recorded agents: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(custom, "hello")); err != nil {
		t.Fatalf("Share(--dir) did not link into the custom directory: %v", err)
	}
}

func TestShareIsIdempotentInManifest(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
			t.Fatalf("Share #%d: %v", i+1, err)
		}
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("agents after two runs = %v, want one claude entry", got)
	}
}

func TestSyncRecreatesDriftedShareLink(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")

	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove share link: %v", err)
	}

	rep, err := eng.Sync(ctx, engine.SyncOptions{}, testIO())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	src := filepath.Join(root, ".agents", "skills", "hello")
	assertManagedLink(t, src, link)
	found := false
	for _, en := range rep.Entries {
		if en.Name == "hello" && en.Action == engine.ActionInstall {
			found = true
		}
	}
	if !found {
		t.Errorf("Sync report = %+v, want the recreated share link counted as an install", rep.Entries)
	}
}

func TestVerifyReportsMissingShareLinkAsDrift(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")

	// Consistent state passes.
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("Verify(consistent) = %v, want nil", err)
	}

	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove share link: %v", err)
	}
	_, err := eng.Verify(ctx, testIO())
	var de *engine.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("Verify(missing share link) = %v (%T), want DriftError", err, err)
	}
}

func TestVerifySkipsAgentDirectoryAbsentFromMachine(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")

	// Simulate a machine where Claude Code is not installed: no .claude dir.
	if err := os.RemoveAll(filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("Verify(absent agent dir) = %v, want nil (declaration is intent, not a machine requirement)", err)
	}
}

func TestRemoveCleansShareLinkAndKeepsDeclaration(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "aa", "")
	writeLocalSkill(t, root, "bb", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}

	if _, err := eng.Remove(ctx, []string{"aa"}, testIO()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "aa")); !os.IsNotExist(err) {
		t.Errorf("Remove kept the share link for the removed skill: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "bb")); err != nil {
		t.Errorf("Remove took down an unrelated share link: %v", err)
	}
	if got := agentsOf(loadMod(t, root), "bb"); len(got) != 1 || got[0] != "claude" {
		t.Errorf("Remove dropped an unrelated entry's agents: %v", got)
	}
}

func TestPruneCleansShareLinkForStaleSkill(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "aa", "")
	writeLocalSkill(t, root, "bb", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}

	// Hand-edit the mod to drop bb, leaving its lock record and files behind.
	m := loadMod(t, root)
	var kept []modfile.ModSkill
	for _, sk := range m.Skills {
		if sk.Name != "bb" {
			kept = append(kept, sk)
		}
	}
	m.Skills = kept
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatalf("SaveMod: %v", err)
	}

	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "bb")); !os.IsNotExist(err) {
		t.Errorf("Prune kept the share link for the stale skill: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "aa")); err != nil {
		t.Errorf("Prune took down an unrelated share link: %v", err)
	}
}

// TestLockCarriesAgentsSoCleanupSurvivesManifestEdits is the case the lock's
// agents field exists for. Dropping a manifest entry erases the intent to
// share, but the links it created stay on disk; the lock is the surviving
// record, so prune can still find them after the declaration is gone — and
// even after a sync has rewritten the lock from the edited manifest.
func TestLockCarriesAgentsSoCleanupSurvivesManifestEdits(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "aa", "")
	writeLocalSkill(t, root, "bb", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
	if got := lockAgentsOf(t, root, "bb"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("lock agents for bb = %v, want [claude]", got)
	}

	// Hand-edit the mod to drop bb, then sync: sync marks bb stale and
	// rewrites the lock from the surviving entries. The agents recorded for
	// bb must ride along, or the link becomes unreachable garbage.
	m := loadMod(t, root)
	var kept []modfile.ModSkill
	for _, sk := range m.Skills {
		if sk.Name != "bb" {
			kept = append(kept, sk)
		}
	}
	m.Skills = kept
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatalf("SaveMod: %v", err)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Fatalf("Sync after dropping the entry: %v", err)
	}
	if got := lockAgentsOf(t, root, "bb"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("lock agents for bb after sync = %v, want [claude] to survive the rewrite", got)
	}

	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "bb")); !os.IsNotExist(err) {
		t.Errorf("Prune kept the share link whose declaration was dropped: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "aa")); err != nil {
		t.Errorf("Prune took down an unrelated share link: %v", err)
	}
}

// lockAgentsOf returns the agents recorded on a lock entry.
func lockAgentsOf(t *testing.T, root, dirName string) []string {
	t.Helper()
	l, err := modfile.LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	for _, lk := range l.Skills {
		if lk.InstallDir() == dirName {
			return lk.Agents
		}
	}
	return nil
}

// TestLockAgentsSurviveAVersionChange covers the upsertLock path: a version
// change rebuilds the lock record from resolved facts, and the recorded agents
// — which the resolver knows nothing about — must ride along. Losing them here
// silently orphans the links, because cleanup reads the lock.
func TestLockAgentsSurviveAVersionChange(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
	if got := lockAgentsOf(t, root, "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("lock agents after share = %v, want [claude]", got)
	}

	r.Write("v1.md", "new\n")
	r.CommitAll("v1.1")
	r.Evolve("v1.1.0", false)
	if _, err := eng.Update(ctx, nil, engine.UpdateOptions{}, testIO()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if lk := lockAgentsOf(t, root, "hello"); len(lk) != 1 || lk[0] != "claude" {
		t.Fatalf("lock agents after a version change = %v, want [claude] preserved", lk)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("manifest agents after a version change = %v, want [claude] preserved", got)
	}
}

func TestShareRejectsUnknownAgentInDeclaration(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// A hand-edited manifest naming an unregistered agent must fail loudly on
	// sync rather than silently skipping the declaration.
	m := loadMod(t, root)
	m.Skills[0].Agents = []string{"not-an-agent"}
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatalf("SaveMod: %v", err)
	}
	_, err := eng.Sync(ctx, engine.SyncOptions{}, testIO())
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("Sync(unknown declared agent) = %v, want the registered names", err)
	}
}

func setupTwoAgents(t *testing.T, root string) *engine.Engine {
	t.Helper()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude", "codex"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
	return eng
}

func TestShareRemoveUnlinksAndUndeclares(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"hello"}, Remove: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "codex"), "hello")); !os.IsNotExist(err) {
		t.Errorf("Share(--remove codex) kept the codex link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); err != nil {
		t.Errorf("Share(--remove codex) took down the claude link: %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("agents after --remove codex = %v, want [claude]", got)
	}
}

// TestShareRemoveIsSkillScoped pins the granularity: unsharing one skill must
// not touch a second skill that names the same agent.
func TestShareRemoveIsSkillScoped(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "aa", "")
	writeLocalSkill(t, root, "bb", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}

	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"aa"}, Remove: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove claude --skill aa): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "aa")); !os.IsNotExist(err) {
		t.Errorf("--remove kept the named skill's link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "bb")); err != nil {
		t.Errorf("--remove took down an unnamed skill's link: %v", err)
	}
	m := loadMod(t, root)
	if got := agentsOf(m, "aa"); len(got) != 0 {
		t.Errorf("agents on aa = %v, want none", got)
	}
	if got := agentsOf(m, "bb"); len(got) != 1 || got[0] != "claude" {
		t.Errorf("agents on bb = %v, want [claude]", got)
	}
}

// TestShareRemoveRequiresASkillSelection pins that removing agents names the
// entries it edits, because the list belongs to an entry.
func TestShareRemoveRequiresASkillSelection(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	_, err := eng.Share(ctx, engine.ShareOptions{Remove: []string{"codex"}}, testIO())
	if err == nil {
		t.Fatal("Share(--remove without --skill or --all) = nil error, want a failure")
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 2 {
		t.Fatalf("failed remove changed the declaration: %v", got)
	}
}

func TestShareRemoveDropsEmptyAgentsField(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "hello", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !os.IsNotExist(err) {
		t.Errorf("Share(--remove claude) kept the link: %v", err)
	}
	// An entry left with no agents loses the field rather than keeping an
	// empty list: "not shared" is the absence of a declaration.
	m := loadMod(t, root)
	if got := agentsOf(m, "hello"); len(got) != 0 {
		t.Fatalf("Share(--remove) left agents on the entry: %v", got)
	}
	data, err := modfile.MarshalMod(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agents") {
		t.Errorf("serialized manifest still names agents:\n%s", data)
	}
}

func TestShareRemoveKeepsForeignContent(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	foreign := filepath.Join(agentSkillsDir(root, "codex"), "hello")
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove): %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "notes.txt")); err != nil {
		t.Errorf("Share(--remove) discarded foreign content: %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("agents after --remove codex = %v, want [claude]", got)
	}
}

func TestShareRemoveDryRunLeavesEverything(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}}, testIO(), engine.MutationOptions{DryRun: true}); err != nil {
		t.Fatalf("Share(--remove --dry-run): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "codex"), "hello")); err != nil {
		t.Errorf("dry run removed the link: %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 2 {
		t.Fatalf("dry run changed the declaration: %v", got)
	}
}

func TestShareRemoveRejectsUndeclaredAgent(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	_, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"claude", "unknown-agent"}}, testIO())
	if err == nil {
		t.Fatal("Share(--remove unknown-agent) = nil error, want a failure")
	}
	// Nothing was taken down: the failed run leaves links and declaration alone.
	if _, statErr := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); statErr != nil {
		t.Errorf("failed remove took down links: %v", statErr)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 2 {
		t.Fatalf("failed remove changed the declaration: %v", got)
	}
}

// TestShareRemoveRejectsAgentTheSkillDoesNotName is the per-skill counterpart
// of the typo check: the agent exists, but this entry never named it.
func TestShareRemoveRejectsAgentTheSkillDoesNotName(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill(t, root, "aa", "")
	writeLocalSkill(t, root, "bb", "")
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"aa"}, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatalf("Share(aa): %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"bb"}, Agents: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(bb): %v", err)
	}

	_, err := eng.Share(ctx, engine.ShareOptions{Skills: []string{"aa"}, Remove: []string{"codex"}}, testIO())
	if err == nil {
		t.Fatal("Share(--remove codex on a skill that names only claude) = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "aa") {
		t.Errorf("error = %v, want it to name the skill whose declaration lacks the agent", err)
	}
	if got := agentsOf(loadMod(t, root), "aa"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("failed remove changed aa's agents: %v", got)
	}
}

func TestShareRemoveRejectsMixedOptions(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	_, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}, Agents: []string{"codex"}}, testIO())
	if err == nil {
		t.Fatal("Share(--remove --agent) = nil error, want a failure")
	}
	_, err = eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}, Dirs: []string{filepath.Join(root, "elsewhere")}}, testIO())
	if err == nil {
		t.Fatal("Share(--remove --dir) = nil error, want a failure")
	}
}

func TestShareRemoveAbsentAgentDirectory(t *testing.T) {
	root := t.TempDir()
	eng := setupTwoAgents(t, root)

	// A machine without codex installed still un-declares it.
	if err := os.RemoveAll(filepath.Join(root, ".codex")); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Remove: []string{"codex"}}, testIO()); err != nil {
		t.Fatalf("Share(--remove, absent dir): %v", err)
	}
	if got := agentsOf(loadMod(t, root), "hello"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("agents after --remove codex = %v, want [claude]", got)
	}
}

// makeShareLinkSymlink replaces the share destination with the symlink auto
// install mode produces by default on this filesystem, so dangling-link
// cleanup can be exercised against a link whose target is about to vanish.
func makeShareLinkSymlink(t *testing.T, root, dirName string) {
	t.Helper()
	dst := filepath.Join(agentSkillsDir(root, "claude"), dirName)
	managed := filepath.Join(root, ".agents", "skills", dirName)
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managed, dst); err != nil {
		t.Fatal(err)
	}
}

// makeFallbackCopy replaces the share destination with a real directory
// holding the same content, the representation share produces where the
// filesystem forbids symlinks. Unlike a link it may hold user edits.
func makeFallbackCopy(t *testing.T, root, dirName string) {
	t.Helper()
	managed := filepath.Join(root, ".agents", "skills", dirName)
	dst := filepath.Join(agentSkillsDir(root, "claude"), dirName)
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(managed)); err != nil {
		t.Fatal(err)
	}
}

// dropManagedSkill deletes the managed copy, leaving every share link that
// pointed at it dangling.
func dropManagedSkill(t *testing.T, root, dirName string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, ".agents", "skills", dirName)); err != nil {
		t.Fatal(err)
	}
}

func TestPruneCleansDanglingShareLink(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	makeShareLinkSymlink(t, root, "hello")
	dropManagedSkill(t, root, "hello")

	// Hand-edit the mod to drop hello, so its lock record goes stale.
	m := loadMod(t, root)
	m.Skills = nil
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatalf("SaveMod: %v", err)
	}

	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !os.IsNotExist(err) {
		t.Errorf("Prune kept the share link whose managed copy was gone: %v", err)
	}
}

func TestPruneKeepsFallbackCopyWhenManagedVanishes(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	// A fallback copy holds real content skillmod cannot re-create, so when
	// the managed copy vanishes it stays rather than being discarded.
	makeFallbackCopy(t, root, "hello")
	dropManagedSkill(t, root, "hello")

	m := loadMod(t, root)
	m.Skills = nil
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatalf("SaveMod: %v", err)
	}

	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentSkillsDir(root, "claude"), "hello", "SKILL.md")); err != nil {
		t.Errorf("Prune discarded the fallback copy: %v", err)
	}
}

func TestRemoveCleansDanglingShareLinkOnMissing(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	makeShareLinkSymlink(t, root, "hello")
	dropManagedSkill(t, root, "hello")

	if _, err := eng.Remove(ctx, []string{"hello"}, testIO()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentSkillsDir(root, "claude"), "hello")); !os.IsNotExist(err) {
		t.Errorf("Remove kept the dangling share link: %v", err)
	}
}

// A share destination holding foreign content is a conflict sync settles with
// the same per-conflict policy as its own install targets. These three cases
// cover the paths that run through shareConflict.asConflict: the default ask
// policy with no one to answer, overwrite, and skip.
func TestSyncShareConflictAsksAndAbortsWithoutAnAnswerer(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	foreignShareContent(t, root, "hello")

	// A non-interactive run that may not overwrite must name the conflict and
	// abort rather than discard the foreign content.
	_, err := eng.Sync(ctx, engine.SyncOptions{}, engine.IO{Out: io.Discard})
	if err == nil {
		t.Fatal("Sync(share conflict, no answerer) = nil, want the conflict reported")
	}
	var de *engine.DriftError
	if errors.As(err, &de) {
		t.Fatalf("Sync returned %v, want the conflict list rather than a drift result", err)
	}
	if !strings.Contains(err.Error(), "hello") {
		t.Errorf("Sync error = %v, want it to name the conflicting share destination", err)
	}
	if got := readFile(t, filepath.Join(agentSkillsDir(root, "claude"), "hello", "SKILL.md")); !strings.Contains(got, "foreign") {
		t.Errorf("Sync discarded the foreign share content: %q", got)
	}
}

func TestSyncShareConflictOverwritesAndRelinks(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	foreignShareContent(t, root, "hello")

	// testIO answers --yes, which skips every conflict before the interactive
	// policy is consulted, so clear it to exercise the per-conflict prompts.
	ioOverwrite := testIO()
	ioOverwrite.Yes = false
	ioOverwrite.Confirm = &conflictPolicyChooser{choice: 0}
	rep, err := eng.Sync(ctx, engine.SyncOptions{}, ioOverwrite)
	if err != nil {
		t.Fatalf("Sync(overwrite share conflict): %v", err)
	}
	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	assertManagedLink(t, filepath.Join(root, ".agents", "skills", "hello"), link)
	// applyShareLinks records a completed link as "installed", the same
	// vocabulary get uses for a target it wrote.
	found := false
	for _, en := range rep.Entries {
		if en.Name != "hello" {
			continue
		}
		for _, tr := range en.TargetResults {
			if tr.Path == link && tr.Action == engine.ActionInstalled {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("Sync report = %+v, want the overwritten share destination reported as installed", rep.Entries)
	}
}

func TestSyncShareConflictSkipKeepsForeignContentAndReportsPartial(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	foreignShareContent(t, root, "hello")

	// Choice 1 is "skip", so the run completes partially: the conflicting
	// destination is preserved and exit code 3 reaches automation.
	ioSkip := testIO()
	ioSkip.Yes = false
	ioSkip.Confirm = &conflictPolicyChooser{choice: 1}
	rep, err := eng.Sync(ctx, engine.SyncOptions{}, ioSkip)
	var partial *engine.PartialError
	if err == nil || !errors.As(err, &partial) {
		t.Fatalf("Sync(skip share conflict) = %v (%T), want a PartialError so exit code 3 reaches automation", err, err)
	}
	link := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	found := false
	for _, en := range rep.Entries {
		if en.Name != "hello" {
			continue
		}
		for _, tr := range en.TargetResults {
			if tr.Path == link && tr.Action == engine.ActionSkip {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("Sync report = %+v, want the preserved share destination reported as a skip", rep.Entries)
	}
	if got := readFile(t, filepath.Join(link, "SKILL.md")); !strings.Contains(got, "foreign") {
		t.Errorf("Sync discarded the skipped share content: %q", got)
	}
}

func TestSyncShareConflictAbortLeavesForeignContent(t *testing.T) {
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	setupSharedSkill(t, eng, root, "hello")
	foreignShareContent(t, root, "hello")

	// Choice 2 is "abort", which stops the run before anything is written.
	ioAbort := testIO()
	ioAbort.Yes = false
	ioAbort.Confirm = &conflictPolicyChooser{choice: 2}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, ioAbort); err == nil {
		t.Fatal("Sync(abort share conflict) = nil, want the abort reported")
	}
	if got := readFile(t, filepath.Join(agentSkillsDir(root, "claude"), "hello", "SKILL.md")); !strings.Contains(got, "foreign") {
		t.Errorf("Sync discarded the share content an abort must leave alone: %q", got)
	}
}

// conflictPolicyChooser answers every conflict prompt with one fixed index, so
// a sync test can select overwrite, skip, or abort without a terminal.
// resolveConflicts asks through Choose; the multi-select ChooseMany is not on
// this path.
type conflictPolicyChooser struct{ choice int }

func (*conflictPolicyChooser) Confirm(string) (bool, error) { return false, nil }

func (c *conflictPolicyChooser) Choose(string, []string) (int, error) { return c.choice, nil }

// foreignShareContent replaces the share destination with a real directory
// holding unmatched content, the shape a conflict is detected from.
func foreignShareContent(t *testing.T, root, dirName string) {
	t.Helper()
	dst := filepath.Join(agentSkillsDir(root, "claude"), dirName)
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: " + dirName + "\ndescription: foreign skill\n---\n# foreign\n"
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
