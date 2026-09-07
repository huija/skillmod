// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Engine integration tests cover the acceptance criteria and use local bare repositories through file://.
package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/huija/skillmod/internal/config"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/testutil"
	"github.com/huija/skillmod/internal/ui"
)

var ctx = context.Background()

func TestMain(m *testing.M) { testutil.RunMain(m) }

func newEngine(t *testing.T, root, storeDir string) *engine.Engine {
	t.Helper()
	s := store.New(storeDir)
	return &engine.Engine{
		Root:   root,
		Source: &source.Source{VCSRoot: s.VCSRoot()},
		Store:  s,
		Config: &config.Config{Agents: []string{"agents"}},
	}
}

func testIO() engine.IO {
	return engine.IO{Out: io.Discard, Err: io.Discard, Yes: true}
}

// Standard single-skill repository with a root hello skill tagged v1.0.0.
func newHelloRepo(t *testing.T) *testutil.Repo {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "hello", "run.sh")
	r.CommitAll("init")
	r.Tag("v1.0.0")
	r.Finish()
	return r
}

func installedDir(root, name string) string {
	return filepath.Join(root, ".agents", "skills", name)
}

func loadLockSkill(t *testing.T, root, name string) modfile.LockSkill {
	t.Helper()
	l, err := modfile.LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	for _, s := range l.Skills {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("lock has no entry for %s: %+v", name, l.Skills)
	return modfile.LockSkill{}
}

func assertStateDirs(t *testing.T, root string, want ...string) {
	t.Helper()
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod: %v", err)
	}
	l, err := modfile.LoadLock(root)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	modDirs := make(map[string]bool, len(m.Skills))
	for _, skill := range m.Skills {
		modDirs[skill.DirName()] = true
	}
	lockDirs := make(map[string]bool, len(l.Skills))
	for _, skill := range l.Skills {
		lockDirs[skill.InstallDir()] = true
	}
	if len(modDirs) != len(want) || len(lockDirs) != len(want) {
		t.Fatalf("state directory counts = mod %v, lock %v; want %v", modDirs, lockDirs, want)
	}
	for _, dir := range want {
		if !modDirs[dir] || !lockDirs[dir] {
			t.Errorf("state directories = mod %v, lock %v; want both to contain %q", modDirs, lockDirs, dir)
		}
	}
}

func addRedundantLockDir(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, modfile.LockFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("name = '%s'\n", name)
	redundant := marker + fmt.Sprintf("dir = '%s'\n", name)
	updated := strings.Replace(string(data), marker, redundant, 1)
	if updated == string(data) {
		t.Fatalf("addRedundantLockDir(%q) could not find lock entry in:\n%s", name, data)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertCanonicalLockDir(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, modfile.LockFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), fmt.Sprintf("dir = '%s'", name)) {
		t.Errorf("%s retains redundant dir for %q:\n%s", modfile.LockFileName, name, data)
	}
	if got := loadLockSkill(t, root, name).Dir; got != "" {
		t.Errorf("LoadLock(%q).Dir = %q, want empty", name, got)
	}
}

// AC-1, first half: exercise the complete get flow and match installed content to the source.
func TestGet_Tag(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())

	_, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod: %v", err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Name != "hello" || m.Skills[0].Version != "v1.0.0" {
		t.Fatalf("mod = %+v", m.Skills)
	}
	lk := loadLockSkill(t, root, "hello")
	if !strings.HasPrefix(lk.Dirhash, "h1:") || lk.Commit == "" {
		t.Errorf("lock entry is missing dirhash or commit: %+v", lk)
	}
	if lk.Dir != "" {
		t.Errorf("lock entry Dir = %q, want omitted when no alias is used", lk.Dir)
	}
	// Installed bytes match the Git source.
	got, err := os.ReadFile(filepath.Join(installedDir(root, "hello"), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(r.Work, "SKILL.md"))
	if string(got) != string(want) {
		t.Error("installed content differs from the source")
	}
	// Recomputing the installation directory produces the locked dirhash.
	h, err := dirhash.HashDir(installedDir(root, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if h != lk.Dirhash {
		t.Errorf("recomputed installation hash %s != lock %s", h, lk.Dirhash)
	}
}

func TestGet_RedundantAliasIsCanonical(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "hello", testIO()); err != nil {
		t.Fatalf("Get(alias equal to published name) error = %v, want nil", err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Skills[0].Alias; got != "" {
		t.Errorf("Get(alias equal to published name) Alias = %q, want empty", got)
	}
	assertCanonicalLockDir(t, root, "hello")
	assertStateDirs(t, root, "hello")
}

func TestGet_DiscoversSingleSkillInSkillsDirectory(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("README.md", "skill collection\n")
	r.WriteSkill("skills/only", "only")
	r.CommitAll("add skill collection")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get collection root: %v", err)
	}
	mod, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Skills) != 1 || mod.Skills[0].Name != "only" || mod.Skills[0].Source != r.URL+"//skills/only" {
		t.Fatalf("discovered mod entry = %+v", mod.Skills)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "only"), "SKILL.md")); err != nil {
		t.Fatalf("discovered skill was not installed: %v", err)
	}
}

func TestGet_DiscoversSkillsDirectoryWithSubdirectoryTag(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("README.md", "skill collection\n")
	r.WriteSkill("skills/only", "only")
	r.CommitAll("add skill collection")
	r.Tag("skills/only/v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get collection root with subdirectory tag: %v", err)
	}
	mod, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Skills) != 1 || mod.Skills[0].Source != r.URL+"//skills/only" || mod.Skills[0].Version != "skills/only/v1.0.0" {
		t.Fatalf("discovered mod entry = %+v", mod.Skills)
	}
}

func TestGet_SelectsFromRootAndSkillsDirectory(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "root-skill")
	r.WriteSkill("skills/nested", "nested-skill")
	r.CommitAll("add root and nested skills")
	r.Tag("v1.0.0")
	r.Finish()

	chooser := &getSkillChooser{choices: []int{1}}
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	io := engine.IO{Out: io.Discard, Err: io.Discard, Confirm: chooser}
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", io); err != nil {
		t.Fatalf("Get collection root: %v", err)
	}
	if chooser.calls != 1 {
		t.Fatalf("candidate selector calls = %d, want 1", chooser.calls)
	}
	if len(chooser.options) != 2 || chooser.options[1].Label != "nested-skill" ||
		!strings.Contains(chooser.options[1].Description, "test skill") ||
		!strings.Contains(chooser.options[1].Detail, "//nested-skill") ||
		strings.Contains(chooser.options[1].Detail, "//skills/nested") {
		t.Fatalf("candidate options = %+v", chooser.options)
	}
	mod, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Skills) != 1 || mod.Skills[0].Name != "nested-skill" || mod.Skills[0].Source != r.URL+"//skills/nested" {
		t.Fatalf("selected mod entry = %+v", mod.Skills)
	}
}

func TestGet_ResolvesSingleSegmentSubdirectoryAsSkillName(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("README.md", "skill collection\n")
	r.WriteSkill("skills/.curated/gh-fix-ci", "gh-fix-ci", "check.go")
	r.CommitAll("add curated skill")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//gh-fix-ci@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get(skill-name shorthand) error = %v, want nil", err)
	}
	mod, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Skills) != 1 || mod.Skills[0].Source != r.URL+"//skills/.curated/gh-fix-ci" {
		t.Errorf("Get(skill-name shorthand) source = %+v, want full matched subdirectory", mod.Skills)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "gh-fix-ci"), "check.go")); err != nil {
		t.Errorf("Get(skill-name shorthand) installed file error = %v, want nil", err)
	}
}

func TestGet_RejectsAmbiguousSkillNameShorthand(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("README.md", "skill collection\n")
	r.WriteSkill("skills/.curated/demo", "demo")
	r.WriteSkill("skills/.system/demo", "demo")
	r.CommitAll("add duplicate skill names")
	r.Tag("v1.0.0")
	r.Finish()

	eng := newEngine(t, t.TempDir(), t.TempDir())
	_, err := eng.Get(ctx, r.URL+"//demo@v1.0.0", "", testIO())
	if err == nil || !strings.Contains(err.Error(), "multiple subdirectories") ||
		!strings.Contains(err.Error(), "skills/.curated/demo") ||
		!strings.Contains(err.Error(), "skills/.system/demo") {
		t.Errorf("Get(ambiguous skill-name shorthand) error = %v, want both matching subdirectories", err)
	}
}

func TestGet_RequiresExplicitSelectionForMultipleSkills(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "root-skill")
	r.WriteSkill("skills/nested", "nested-skill")
	r.CommitAll("add root and nested skills")
	r.Tag("v1.0.0")
	r.Finish()

	eng := newEngine(t, t.TempDir(), t.TempDir())
	_, err := eng.Get(ctx, r.URL+"@v1.0.0", "", engine.IO{Out: io.Discard, Err: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "select one or more") ||
		!strings.Contains(err.Error(), "//nested-skill") || strings.Contains(err.Error(), "//skills/nested") {
		t.Fatalf("Get multiple candidates error = %v", err)
	}
}

func TestGet_YesInstallsAllDiscoveredNestedSkills(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("README.md", "skill collection\n")
	r.WriteSkill("skills/.curated/ci", "ci")
	r.WriteSkill("skills/.system/review", "review")
	r.CommitAll("add nested skill collection")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get nested collection with --yes: %v", err)
	}
	mod, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Skills) != 2 {
		t.Fatalf("discovered skill count = %d, want 2: %+v", len(mod.Skills), mod.Skills)
	}
	byName := map[string]modfile.ModSkill{}
	for _, skill := range mod.Skills {
		byName[skill.Name] = skill
	}
	if byName["ci"].Source != r.URL+"//skills/.curated/ci" || byName["review"].Source != r.URL+"//skills/.system/review" {
		t.Fatalf("discovered nested entries = %+v", mod.Skills)
	}
}

type getSkillChooser struct {
	choices []int
	calls   int
	options []ui.Option
}

func (*getSkillChooser) Confirm(string) bool { return false }

func (c *getSkillChooser) Choose(string, []string) int {
	c.calls++
	return 0
}

func (c *getSkillChooser) ChooseMany(_ string, options []ui.Option) []int {
	c.calls++
	c.options = append([]ui.Option(nil), options...)
	return c.choices
}

func TestGet_DefaultTargetIsGenericAgents(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "hello", "SKILL.md")); err != nil {
		t.Fatalf("generic target was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("default target unexpectedly created .claude; stat error = %v", err)
	}
}

func TestGet_MultiTargetAgentsAndClaude(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	eng.Config.Agents = []string{"agents", "claude-code"}
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	generic := filepath.Join(root, ".agents", "skills", "hello")
	claude := filepath.Join(root, ".claude", "skills", "hello")
	hGeneric, err := dirhash.HashDir(generic)
	if err != nil {
		t.Fatal(err)
	}
	hClaude, err := dirhash.HashDir(claude)
	if err != nil {
		t.Fatal(err)
	}
	if hGeneric != hClaude {
		t.Fatalf("target dirhash values differ: agents=%s claude=%s", hGeneric, hClaude)
	}
}

func TestUpdate_MovedTagConflictsWithImmutableSnapshot(t *testing.T) {
	r := newHelloRepo(t)
	sharedStore := t.TempDir()
	root := t.TempDir()
	eng := newEngine(t, root, sharedStore)
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	r.WriteSkill("", "hello", "changed.md")
	r.CommitAll("rewrite v1")
	r.Evolve("v1.0.0", true)

	_, err := eng.Update(ctx, []string{"hello"}, testIO())
	var conflict *store.SnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v (%T), want SnapshotConflictError", err, err)
	}
}

func TestGet_ExactSnapshotSkipsGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell wrapper")
	}
	r := newHelloRepo(t)
	sharedStore := t.TempDir()
	eng := newEngine(t, t.TempDir(), sharedStore)
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "git-called")
	wrapper := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\n: > \"$SKILLMOD_TEST_GIT_CALLED\"\nexit 99\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKILLMOD_TEST_GIT_CALLED", marker)
	eng = newEngine(t, t.TempDir(), sharedStore)
	eng.Source.Git = wrapper
	rep, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO())
	if err != nil {
		t.Fatalf("exact version snapshot hit failed: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("exact version snapshot hit still invoked git: %v", err)
	}
	if rep.Entries[0].Note != i18n.Text("from a local version snapshot; not verified online") {
		t.Fatalf("Note = %q", rep.Entries[0].Note)
	}
}

// AC-1: cross-machine consistency; get on machine A, copy mod and lock to machine B with an independent store, then sync to identical hashes.
func TestSync_CrossMachine(t *testing.T) {
	r := newHelloRepo(t)
	rootA := t.TempDir()
	engA := newEngine(t, rootA, t.TempDir())
	if _, err := engA.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	rootB := t.TempDir()
	for _, f := range []string{modfile.ModFileName, modfile.LockFileName} {
		data, err := os.ReadFile(filepath.Join(rootA, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rootB, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engB := newEngine(t, rootB, t.TempDir()) // An independent store represents a clean machine.
	if _, err := engB.Sync(ctx, false, testIO()); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	hA, _ := dirhash.HashDir(installedDir(rootA, "hello"))
	hB, _ := dirhash.HashDir(installedDir(rootB, "hello"))
	lk := loadLockSkill(t, rootB, "hello")
	if hA != hB || hB != lk.Dirhash {
		t.Errorf("cross-machine mismatch: A=%s B=%s lock=%s", hA, hB, lk.Dirhash)
	}
}

// AC-2: idempotency; repeated sync reports no changes and performs no file writes.
func TestSync_Idempotent(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, modfile.LockFileName)
	st1, _ := os.Stat(lockPath)

	rep, err := eng.Sync(ctx, false, testIO())
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range rep.Entries {
		if en.Action == "install" {
			t.Errorf("second sync still reports an install action: %+v", en)
		}
	}
	st2, _ := os.Stat(lockPath)
	if st1.ModTime() != st2.ModTime() {
		t.Error("SKILL.lock was rewritten without changes")
	}
}

func TestSync_RepairsRedundantLockDirectory(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	addRedundantLockDir(t, root, "hello")

	if _, err := eng.Sync(ctx, false, testIO()); err != nil {
		t.Fatalf("Sync(redundant lock dir) error = %v, want nil", err)
	}
	assertCanonicalLockDir(t, root, "hello")
}

// AC-3: tamper protection; changing a locked dirhash makes sync fail without modifying the filesystem.
func TestSync_TamperedLock(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Tamper with the locked dirhash.
	l, _ := modfile.LoadLock(root)
	l.Skills[0].Dirhash = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := modfile.SaveLock(root, l); err != nil {
		t.Fatal(err)
	}
	// Switch to an empty store to force the refetch path.
	storeDir := t.TempDir()
	eng = newEngine(t, root, storeDir)

	_, err := eng.Sync(ctx, false, testIO())
	var te *engine.TamperError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want TamperError", err, err)
	}
}

// AC-4: reject branch names.
func TestGet_BranchRejected(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	_, err := eng.Get(ctx, r.URL+"@main", "", testIO())
	var be *resolve.BranchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want BranchError", err, err)
	}
	wantMessage := i18n.Format("branches cannot be locked; use a tag or commit SHA (%q is a branch name)", "main")
	if err.Error() != wantMessage {
		t.Errorf("message = %q", err)
	}
	// No files were written.
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName)); !os.IsNotExist(err) {
		t.Error("SKILL.mod was written after the request was rejected")
	}
}

// AC-5: commit addressing records a pseudo-version in the lock and repeated runs are reproducible.
func TestGet_ByCommitSHA(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "noskill-tag")
	r.CommitAll("init") // Repository with no tags.
	r.Finish()
	sha := r.SHA("main")

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@"+sha, "", testIO()); err != nil {
		t.Fatalf("Get by SHA: %v", err)
	}
	lk := loadLockSkill(t, root, "noskill-tag")
	if !resolve.IsPseudoVersion(lk.Version) {
		t.Errorf("locked version %q is not a pseudo-version", lk.Version)
	}
	if lk.Commit != sha {
		t.Errorf("lock commit = %s, want %s", lk.Commit, sha)
	}

	// Repeated execution resolves to the same version.
	root2 := t.TempDir()
	eng2 := newEngine(t, root2, t.TempDir())
	if _, err := eng2.Get(ctx, r.URL+"@"+sha, "", testIO()); err != nil {
		t.Fatal(err)
	}
	lk2 := loadLockSkill(t, root2, "noskill-tag")
	if lk2.Version != lk.Version {
		t.Errorf("pseudo-version is not reproducible: %q vs %q", lk2.Version, lk.Version)
	}
}

// AC-6: a monorepo subdirectory automatically prefixes a bare version and hashes only the subtree.
func TestGet_MonorepoSubdir(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("code-review", "code-review", "checklist.md")
	r.WriteSkill("pdf", "pdf")
	r.CommitAll("init")
	r.Tag("code-review/v1.2.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//code-review@v1.2.0", "", testIO()); err != nil {
		t.Fatalf("Get monorepo: %v", err)
	}
	lk := loadLockSkill(t, root, "code-review")
	if lk.Version != "code-review/v1.2.0" {
		t.Errorf("version = %q, want code-review/v1.2.0", lk.Version)
	}
	// The installation contains only subtree content, not other repository content.
	if _, err := os.Stat(filepath.Join(installedDir(root, "code-review"), "checklist.md")); err != nil {
		t.Error("subtree file is missing")
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "code-review"), "pdf")); !os.IsNotExist(err) {
		t.Error("installation directory contains unrelated repository content")
	}
	h, _ := dirhash.HashDir(installedDir(root, "code-review"))
	if h != lk.Dirhash {
		t.Error("subtree hash differs from the lock")
	}
}

func TestGet_SameRepoVersionSecondSkillIsFullyLocal(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("alpha", "alpha", "alpha.txt")
	r.WriteSkill("beta", "beta", "beta.txt")
	r.CommitAll("two skills")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	storeDir := t.TempDir()
	eng := newEngine(t, root, storeDir)
	if _, err := eng.Get(ctx, r.URL+"//alpha@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	eng.Source.Git = filepath.Join(t.TempDir(), "git-must-not-run")
	rep, err := eng.Get(ctx, r.URL+"//beta@v1.0.0", "", testIO())
	if err != nil {
		t.Fatalf("second skill at the same repo@version did not use a local-only hit: %v", err)
	}
	if rep.Entries[0].Note != i18n.Text("from a local repository version snapshot; not verified online") {
		t.Fatalf("same-repository cache note = %q", rep.Entries[0].Note)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "beta.txt")); err != nil {
		t.Fatalf("second skill was not installed: %v", err)
	}
	vcsEntries, err := os.ReadDir(filepath.Join(storeDir, "pkg", "mod", "cache", "vcs"))
	if err != nil {
		t.Fatal(err)
	}
	vcsRepos := 0
	for _, entry := range vcsEntries {
		if entry.IsDir() {
			vcsRepos++
		}
	}
	if vcsRepos != 1 {
		t.Fatalf("two skills from one repository created %d bare repositories, want 1", vcsRepos)
	}
	snapshotPath, err := eng.Store.SnapshotPath(r.URL, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snapshotPath, "alpha", "alpha.txt")); err != nil {
		t.Fatalf("repository snapshot is missing alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshotPath, "beta", "beta.txt")); err != nil {
		t.Fatalf("repository snapshot is missing beta: %v", err)
	}
}

func TestGet_SameRepoCommitSecondSkillIsFullyLocal(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("alpha", "alpha", "alpha.txt")
	r.WriteSkill("beta", "beta", "beta.txt")
	r.CommitAll("two skills")
	r.Finish()
	commit := r.SHA("main")

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//alpha@"+commit, "", testIO()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r.Bare, r.Bare+".gone"); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Get(ctx, r.URL+"//beta@"+commit, "", testIO())
	if err != nil {
		t.Fatalf("second skill at the same repo@commit did not use a local-only hit: %v", err)
	}
	if rep.Entries[0].Note != i18n.Text("from a local repository commit snapshot; not verified online") {
		t.Fatalf("commit cache note = %q", rep.Entries[0].Note)
	}
	alpha := loadLockSkill(t, root, "alpha")
	beta := loadLockSkill(t, root, "beta")
	if alpha.Commit != commit || beta.Commit != commit || alpha.Version != beta.Version {
		t.Fatalf("skills at the same commit did not share a version: alpha=%+v beta=%+v", alpha, beta)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "beta.txt")); err != nil {
		t.Fatalf("second skill was not installed: %v", err)
	}
}

func TestGet_ExplicitLocalRepoVersionWinsOverNewSubdirTag(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "root")
	r.WriteSkill("beta", "beta", "old.txt")
	r.CommitAll("root release")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	// After the first get, the complete repo@v1.0.0 is local. Even if the remote later adds
	// beta/v1.0.0, an explicit @v1.0.0 still uses the local immutable version directly;
	// update owns refresh semantics.
	r.WriteSkill("beta", "beta", "new.txt")
	r.CommitAll("beta release")
	r.Tag("beta/v1.0.0")
	r.Evolve("", false)
	if _, err := eng.Get(ctx, r.URL+"//beta@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	lk := loadLockSkill(t, root, "beta")
	if lk.Version != "v1.0.0" {
		t.Fatalf("version = %q, want local root version v1.0.0", lk.Version)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "old.txt")); err != nil {
		t.Fatalf("beta was not installed from the local repository version: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "new.txt")); !os.IsNotExist(err) {
		t.Fatal("explicit local version unexpectedly used a later remote subdirectory tag")
	}
}

// AC-8: the same name from different sources conflicts, and --alias resolves it.
func TestGet_NameConflict(t *testing.T) {
	r1 := testutil.NewRepo(t)
	r1.WriteSkill("", "dup")
	r1.CommitAll("init")
	r1.Tag("v1.0.0")
	r1.Finish()

	r2 := testutil.NewRepo(t)
	r2.WriteSkill("", "dup")
	r2.Write("extra.txt", "different content\n")
	r2.CommitAll("init")
	r2.Tag("v1.0.0")
	r2.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r1.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	_, err := eng.Get(ctx, r2.URL+"@v1.0.0", "", testIO())
	var nc *engine.NameConflictError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v (%T), want NameConflictError", err, err)
	}
	// Resolve with an alias.
	if _, err := eng.Get(ctx, r2.URL+"@v1.0.0", "dup-b", testIO()); err != nil {
		t.Fatalf("alias get: %v", err)
	}
	if _, err := os.Stat(installedDir(root, "dup-b")); err != nil {
		t.Error("alias directory was not installed")
	}
	assertStateDirs(t, root, "dup", "dup-b")
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("Verify same-name aliases: %v", err)
	}

	// Removing one of two same-name entries must prune only its installation
	// directory and lock record.
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range m.Skills {
		if skill.DirName() == "dup" {
			m.Skills = []modfile.ModSkill{skill}
			break
		}
	}
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune aliased duplicate: %v", err)
	}
	if _, err := os.Stat(installedDir(root, "dup")); err != nil {
		t.Errorf("Prune removed retained same-name entry: %v", err)
	}
	if _, err := os.Stat(installedDir(root, "dup-b")); !os.IsNotExist(err) {
		t.Errorf("Prune kept removed aliased entry: %v", err)
	}
	assertStateDirs(t, root, "dup")
}

func TestGet_AliasChangeReportsRetainedDirectory(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "old-hello", testIO()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	commandIO := testIO()
	commandIO.Out = &out
	rep, err := eng.Get(ctx, r.URL+"@v1.0.0", "new-hello", commandIO)
	if err != nil {
		t.Fatalf("Get(same source with new alias) error = %v, want nil", err)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, want := range []string{"old-hello", "new-hello", "skillmod prune"} {
		if !strings.Contains(notes, want) {
			t.Errorf("Get(same source with new alias) notes = %q, want substring %q", notes, want)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("Get(same source with new alias) output = %q, want substring %q", out.String(), want)
		}
	}
	for _, dirName := range []string{"old-hello", "new-hello"} {
		if _, err := os.Stat(installedDir(root, dirName)); err != nil {
			t.Errorf("Get(same source with new alias) retained directory %q error = %v, want nil", dirName, err)
		}
	}
}

// Spellings that differ only in letter case map to one directory on Windows
// and macOS; a second get from a different source must fail with both
// spellings named. Runs on every OS because the conflict fires before any
// directory is written.
func TestGet_CaseOnlyNameConflictAcrossSources(t *testing.T) {
	rA := testutil.NewRepo(t)
	rA.WriteSkill("", "demo")
	rA.CommitAll("init")
	rA.Tag("v1.0.0")
	urlA := rA.FinishNamed("repo-a")
	rB := testutil.NewRepo(t)
	rB.WriteSkill("", "Demo")
	rB.CommitAll("init")
	rB.Tag("v1.0.0")
	urlB := rB.FinishNamed("repo-b")

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, urlA+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Re-getting the same source stays idempotent.
	if _, err := eng.Get(ctx, urlA+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("idempotent re-get: %v", err)
	}
	_, err := eng.Get(ctx, urlB+"@v1.0.0", "", testIO())
	var conflict *engine.NameConflictError
	if !errors.As(err, &conflict) || conflict.OtherName == "" {
		t.Fatalf("second Get error = %v (%T), want case-only NameConflictError", err, err)
	}
	if conflict.Name != "demo" || conflict.OtherName != "Demo" {
		t.Fatalf("conflict spellings = %q, %q; want demo, Demo", conflict.Name, conflict.OtherName)
	}
	// A case-variant alias collides too, while a distinct alias installs.
	_, err = eng.Get(ctx, urlB+"@v1.0.0", "DEMO", testIO())
	if !errors.As(err, &conflict) || conflict.OtherName == "" {
		t.Fatalf("alias DEMO error = %v (%T), want case-only NameConflictError", err, err)
	}
	if _, err := eng.Get(ctx, urlB+"@v1.0.0", "capital-demo", testIO()); err != nil {
		t.Fatalf("distinct alias get: %v", err)
	}
	assertStateDirs(t, root, "demo", "capital-demo")
}

// Two skills in one repository whose frontmatter names differ only by case
// map to one installation directory and collide within one batch. The
// directories themselves must not collide (that would be rejected earlier by
// snapshot path validation), so they live in differently named subdirs. The
// fixture needs a case-sensitive filesystem to build both directories.
func TestGet_CaseOnlyNameConflictWithinOneRepo(t *testing.T) {
	if caseInsensitiveFS(t.TempDir()) {
		t.Skip("requires a case-sensitive filesystem")
	}
	r := testutil.NewRepo(t)
	r.Write("README.md", "collection\n")
	r.WriteSkill("skills/one", "Demo")
	r.WriteSkill("skills/two", "demo")
	r.CommitAll("init")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	_, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO())
	var conflict *engine.NameConflictError
	if !errors.As(err, &conflict) || conflict.OtherName == "" {
		t.Fatalf("batch Get error = %v (%T), want case-only NameConflictError", err, err)
	}
	// Convention: Name is the existing directory spelling, OtherName the
	// incoming one. The first discovered skill (Demo, in skills/one) is the
	// existing entry, so it is Name.
	if conflict.Name != "Demo" || conflict.OtherName != "demo" {
		t.Fatalf("conflict spellings = %q, %q; want Demo, demo", conflict.Name, conflict.OtherName)
	}
}

func caseInsensitiveFS(dir string) bool {
	probe := filepath.Join(dir, "skillmod-case-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return true // fail safe: skip when the probe cannot be created
	}
	defer func() { _ = os.Remove(probe) }()
	_, err := os.Stat(filepath.Join(dir, "SKILLMOD-CASE-PROBE"))
	return err == nil
}

// AC-9: protect local modifications; sync does not overwrite them and reports a conflict, while --yes keeps and skips automatically.
func TestSync_LocalModification(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Simulate a user's manual edit.
	target := filepath.Join(installedDir(root, "hello"), "SKILL.md")
	if err := os.WriteFile(target, []byte("user modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.Sync(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	found := false
	for _, en := range rep.Entries {
		if en.Name == "hello" && en.Action == "conflict" {
			found = true
		}
	}
	if !found {
		t.Error("conflict was not reported")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "user modified\n" {
		t.Error("user modification was overwritten")
	}
}

// AC-10: the lock is authoritative and does not upgrade when a new remote tag appears.
func TestSync_LockWins(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Upstream publishes v1.1.0.
	r.Write("new-feature.md", "v1.1 content\n")
	r.CommitAll("v1.1")
	r.Evolve("v1.1.0", false)

	rep, err := eng.Sync(ctx, false, testIO())
	if err != nil {
		t.Fatal(err)
	}
	lk := loadLockSkill(t, root, "hello")
	if lk.Version != "v1.0.0" {
		t.Errorf("version after sync = %s, want authoritative locked version v1.0.0", lk.Version)
	}
	for _, en := range rep.Entries {
		if en.Name == "hello" && en.Action == "install" {
			t.Error("unchanged lock should not trigger reinstallation")
		}
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "new-feature.md")); !os.IsNotExist(err) {
		t.Error("newer version content was installed")
	}
}

// AC-12: CI can consume verify results; detected drift returns DriftError, which the CLI maps to exit code 2.
func TestVerify_Drift(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Matching content passes.
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("verify returned an error for a consistent installation: %v", err)
	}
	// Changing installed content causes drift.
	if err := os.WriteFile(filepath.Join(installedDir(root, "hello"), "SKILL.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := eng.Verify(ctx, testIO())
	var de *engine.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want DriftError", err, err)
	}
	// sync --check uses the same implementation.
	_, err = eng.Sync(ctx, true, testIO())
	if !errors.As(err, &de) {
		t.Errorf("sync --check err = %v, want DriftError", err)
	}
}

// AC-7: zero-migration init scans, suggests ls-remote matches, falls back to local entries, and does not modify original files.
func TestInit_ScanAndMatch(t *testing.T) {
	// Monorepo source containing a pdf/v1.0.0 tag.
	mono := testutil.NewRepo(t)
	mono.WriteSkill("pdf", "pdf")
	mono.CommitAll("init")
	mono.Tag("pdf/v1.0.0")
	monoURL := mono.Finish()
	// Single-repository source whose repository and directory are both named solo.
	solo := testutil.NewRepo(t)
	solo.WriteSkill("", "solo")
	solo.CommitAll("init")
	solo.Tag("v1.0.0")
	soloURL := solo.FinishNamed("solo")

	root := t.TempDir()
	for _, d := range []string{"pdf", "solo", "scratch"} {
		dir := installedDir(root, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+d+"\n---\n# "+d+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scratchMD, _ := os.ReadFile(filepath.Join(installedDir(root, "scratch"), "SKILL.md"))

	eng := newEngine(t, root, t.TempDir())
	eng.Config.KnownSources = []string{monoURL, soloURL}
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]modfile.ModSkill{}
	for _, sk := range m.Skills {
		byName[sk.Name] = sk
	}
	if byName["pdf"].Source != monoURL+"//pdf" || byName["pdf"].Version != "pdf/v1.0.0" {
		t.Errorf("pdf entry = %+v", byName["pdf"])
	}
	if byName["solo"].Source != soloURL || byName["solo"].Version != "v1.0.0" {
		t.Errorf("solo entry = %+v", byName["solo"])
	}
	if !byName["scratch"].Local {
		t.Errorf("scratch should be local: %+v", byName["scratch"])
	}
	// Lock baseline for a local entry.
	lk := loadLockSkill(t, root, "scratch")
	if !strings.HasPrefix(lk.Dirhash, "h1:") {
		t.Error("local entry is missing its dirhash baseline")
	}
	// Original files remain unchanged.
	after, _ := os.ReadFile(filepath.Join(installedDir(root, "scratch"), "SKILL.md"))
	if string(after) != string(scratchMD) {
		t.Error("init modified the original file")
	}
	_ = rep
}

// init refuses to run when SKILL.mod exists; --force backs it up and rebuilds.
func TestInit_RefuseExisting(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Init(ctx, false, testIO()); err == nil {
		t.Error("init should reject an existing SKILL.mod")
	}
	if _, err := eng.Init(ctx, true, testIO()); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName+".bak")); err != nil {
		t.Error(".bak backup was not created")
	}
}

func TestInit_ScansAllKnownAdapters(t *testing.T) {
	root := t.TempDir()
	writeLocalSkill := func(base, name string) {
		dir := filepath.Join(root, base, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: local\n---\n# local\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLocalSkill(".agents", "generic-local")
	writeLocalSkill(".claude", "claude-local")

	eng := newEngine(t, root, t.TempDir())
	eng.Config.Agents = []string{"agents"}
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, sk := range m.Skills {
		found[sk.Name] = true
	}
	for _, name := range []string{"generic-local", "claude-local"} {
		if !found[name] {
			t.Fatalf("init did not discover %s: %+v", name, m.Skills)
		}
	}
}

func TestInit_PreservesSameNameAliasDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dirName := range []string{"dup", "dup-b"} {
		dir := filepath.Join(root, ".agents", "skills", dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: dup\ndescription: local\n---\n# local\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	assertStateDirs(t, root, "dup", "dup-b")
}

func TestInit_SkipsDirectoriesThatCannotBeAliases(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(dirName, skillName string) {
		t.Helper()
		dir := filepath.Join(root, ".agents", "skills", dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + skillName + "\ndescription: local\n---\n# local\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("kept", "kept")
	writeSkill("café", "unicode-alias")
	writeSkill("a b", "space-alias")

	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init(with directories that cannot be aliases) error = %v, want nil", err)
	}
	assertStateDirs(t, root, "kept")
	notes := strings.Join(rep.Notes, "\n")
	for _, want := range []string{"café", "a b", "alias"} {
		if !strings.Contains(notes, want) {
			t.Errorf("Init(with directories that cannot be aliases) notes = %q, want substring %q", notes, want)
		}
	}
	for _, dirName := range []string{"café", "a b"} {
		if _, err := os.Stat(installedDir(root, dirName)); err != nil {
			t.Errorf("Init(with directories that cannot be aliases) source directory %q error = %v, want nil", dirName, err)
		}
	}
}

// update advances tagged entries to latest and pseudo-version entries to a new pseudo-version at HEAD.
func TestUpdate(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	r.Write("v1.md", "new\n")
	r.CommitAll("v1.1")
	r.Evolve("v1.1.0", false)

	rep, err := eng.Update(ctx, nil, testIO())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	lk := loadLockSkill(t, root, "hello")
	if lk.Version != "v1.1.0" {
		t.Errorf("version after update = %s, want v1.1.0", lk.Version)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "v1.md")); err != nil {
		t.Error("new version content was not installed")
	}
	_ = rep

	// A second update is already current.
	rep, err = eng.Update(ctx, nil, testIO())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Note != i18n.Text("already up to date") {
		t.Errorf("second update = %+v, want already up to date", rep.Entries[0])
	}
}

func TestUpdate_RepairsRedundantLockDirectory(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	addRedundantLockDir(t, root, "hello")

	if _, err := eng.Update(ctx, nil, testIO()); err != nil {
		t.Fatalf("Update(redundant lock dir) error = %v, want nil", err)
	}
	assertCanonicalLockDir(t, root, "hello")
}

func TestUpdate_SameNameAliasesRemainIndependent(t *testing.T) {
	newRepo := func(file string) *testutil.Repo {
		t.Helper()
		r := testutil.NewRepo(t)
		r.WriteSkill("", "dup", file)
		r.CommitAll("v1.0")
		r.Tag("v1.0.0")
		r.Finish()
		return r
	}
	r1, r2 := newRepo("first.txt"), newRepo("second.txt")
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r1.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Get(ctx, r2.URL+"@v1.0.0", "dup-b", testIO()); err != nil {
		t.Fatal(err)
	}

	r1.Write("first-v1.1.txt", "first update\n")
	r1.CommitAll("v1.1")
	r1.Evolve("v1.1.0", false)
	r2.Write("second-v1.2.txt", "second update\n")
	r2.CommitAll("v1.2")
	r2.Evolve("v1.2.0", false)

	if _, err := eng.Update(ctx, []string{"dup-b"}, testIO()); err != nil {
		t.Fatalf("Update(alias): %v", err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, skill := range m.Skills {
		versions[skill.DirName()] = skill.Version
	}
	if versions["dup"] != "v1.0.0" || versions["dup-b"] != "v1.2.0" {
		t.Fatalf("versions after alias update = %v, want dup=v1.0.0 and dup-b=v1.2.0", versions)
	}

	if _, err := eng.Update(ctx, []string{"dup"}, testIO()); err != nil {
		t.Fatalf("Update(published name): %v", err)
	}
	m, err = modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	versions = map[string]string{}
	for _, skill := range m.Skills {
		versions[skill.DirName()] = skill.Version
	}
	if versions["dup"] != "v1.1.0" || versions["dup-b"] != "v1.2.0" {
		t.Fatalf("versions after name update = %v, want dup=v1.1.0 and dup-b=v1.2.0", versions)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("Verify same-name updates: %v", err)
	}
}

func TestUpdateQueriesDistinctRepositoriesConcurrently(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell wrapper")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}

	newRepo := func(name string) *testutil.Repo {
		t.Helper()
		r := testutil.NewRepo(t)
		r.WriteSkill("", name)
		r.CommitAll("init " + name)
		r.Tag("v1.0.0")
		r.FinishNamed(name)
		return r
	}
	firstRepo, secondRepo := newRepo("first"), newRepo("second")
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	for _, repo := range []*testutil.Repo{firstRepo, secondRepo} {
		if _, err := eng.Get(ctx, repo.URL+"@v1.0.0", "", testIO()); err != nil {
			t.Fatalf("Get(%q) error = %v, want nil", repo.URL, err)
		}
	}

	markerDir := t.TempDir()
	wrapper := filepath.Join(t.TempDir(), "git")
	body := `#!/bin/sh
if [ "$1" = "ls-remote" ]; then
  touch "$SKILLMOD_REF_MARKERS/$$"
  attempts=0
  while [ "$(find "$SKILLMOD_REF_MARKERS" -type f | wc -l)" -lt "$SKILLMOD_EXPECTED_REPOS" ]; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 200 ]; then
      echo "refs queries did not overlap" >&2
      exit 97
    fi
    sleep 0.01
  done
fi
exec "$SKILLMOD_REAL_GIT" "$@"
`
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKILLMOD_REF_MARKERS", markerDir)
	t.Setenv("SKILLMOD_EXPECTED_REPOS", "2")
	t.Setenv("SKILLMOD_REAL_GIT", realGit)
	eng.Source.Git = wrapper

	updateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := eng.Update(updateCtx, nil, testIO()); err != nil {
		t.Fatalf("Update(two repositories) error = %v, want concurrent refs queries", err)
	}
	markers, err := os.ReadDir(markerDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 2 {
		t.Errorf("Update(two repositories) ls-remote calls = %d, want 2", len(markers))
	}
}

func TestUpdate_PseudoVersion(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "edge")
	r.CommitAll("c1")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL, "", testIO()); err != nil { // No ref resolves HEAD to a pseudo-version.
		t.Fatal(err)
	}
	lk1 := loadLockSkill(t, root, "edge")
	if !resolve.IsPseudoVersion(lk1.Version) {
		t.Fatalf("version %q is not a pseudo-version", lk1.Version)
	}

	r.Write("more.md", "x\n")
	r.CommitAll("c2")
	r.Evolve("", false)

	if _, err := eng.Update(ctx, nil, testIO()); err != nil {
		t.Fatal(err)
	}
	lk2 := loadLockSkill(t, root, "edge")
	if lk2.Version == lk1.Version || lk2.Commit == lk1.Commit {
		t.Error("updating a pseudo-version entry did not advance to the new HEAD")
	}
	if !resolve.IsPseudoVersion(lk2.Version) {
		t.Errorf("updated version %q should remain a pseudo-version", lk2.Version)
	}
}

// prune: remove an entry from mod, let sync report it, then confirm cleanup with prune.
func TestPrune(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("aa", "aa")
	r.WriteSkill("bb", "bb")
	r.CommitAll("init")
	r.Tag("aa/v1.0.0")
	r.Tag("bb/v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//aa@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Get(ctx, r.URL+"//bb@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	// Edit the mod file to remove bb.
	m, _ := modfile.LoadMod(root)
	var kept []modfile.ModSkill
	for _, sk := range m.Skills {
		if sk.Name != "bb" {
			kept = append(kept, sk)
		}
	}
	m.Skills = kept
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}

	// sync reports the entry without deleting files.
	rep, err := eng.Sync(ctx, false, testIO())
	if err != nil {
		t.Fatal(err)
	}
	staleFound := false
	for _, en := range rep.Entries {
		if en.Name == "bb" && en.Action == "stale" {
			staleFound = true
		}
	}
	if !staleFound {
		t.Error("sync did not report the stale entry")
	}
	if _, err := os.Stat(installedDir(root, "bb")); err != nil {
		t.Error("sync deleted files despite the never-delete contract")
	}

	// prune removes it.
	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installedDir(root, "bb")); !os.IsNotExist(err) {
		t.Error("prune did not delete the stale directory")
	}
	l, _ := modfile.LoadLock(root)
	for _, lk := range l.Skills {
		if lk.Name == "bb" {
			t.Error("lock still contains the stale entry")
		}
	}
}

// dry-run must list what would be deleted without requiring confirmation,
// even in a non-interactive context (regression: the confirmation gate used
// to reject --dry-run with advice that suggested using --dry-run).
func TestPrune_DryRunSkipsConfirmation(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Skills = nil
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	rep, err := eng.Prune(ctx, engine.IO{Out: &out, Err: io.Discard, DryRun: true})
	if err != nil {
		t.Fatalf("prune --dry-run was gated by confirmation: %v", err)
	}
	if len(rep.Entries) == 0 || rep.Entries[0].Action != "prune" {
		t.Fatalf("dry-run entries = %+v, want the stale install listed", rep.Entries)
	}
	if _, err := os.Stat(installedDir(root, "hello")); err != nil {
		t.Error("dry-run deleted files despite the never-delete contract")
	}
	if !strings.Contains(out.String(), "will be deleted") {
		t.Errorf("dry-run output %q is missing the deletion list", out.String())
	}
}

// The --dry-run flag promises to print the execution plan; sync must not be
// silent when everything is already consistent (regression: the early return
// used to happen before any output).
func TestSync_DryRunPrintsPlan(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	rep, err := eng.Sync(ctx, false, engine.IO{Out: &out, Err: io.Discard, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) == 0 {
		t.Fatalf("dry-run entries = %+v, want the plan", rep.Entries)
	}
	if !strings.Contains(out.String(), "dry-run: everything is already consistent") {
		t.Errorf("dry-run output = %q, want a plan summary", out.String())
	}
}

// A hand-edited remote→local conversion leaves the remote lock record behind;
// sync must replace it with a fresh local baseline so verify stops reporting
// drift and the follow-up guidance no longer loops (verify says sync, sync
// used to say init).
func TestSync_ReestablishesBaselineAfterRemoteToLocalConversion(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Skills[0] = modfile.ModSkill{Name: m.Skills[0].Name, Local: true}
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}

	rep, err := eng.Sync(ctx, false, testIO())
	if err != nil {
		t.Fatal(err)
	}
	lk := loadLockSkill(t, root, "hello")
	if lk.Source != "" || lk.Version != "" || lk.Commit != "" || lk.Dirhash == "" {
		t.Fatalf("lock record after conversion = %+v, want a fresh local baseline", lk)
	}
	reported := false
	for _, en := range rep.Entries {
		if en.Name == "hello" && strings.Contains(en.Note, "re-established") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("sync entries = %+v, want the baseline re-establishment reported", rep.Entries)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Fatalf("verify after baseline re-establishment = %v, want no drift", err)
	}
}

// The lock records the installation directory, so prune still locates and
// removes an aliased install after the alias was removed from SKILL.mod.
func TestPrune_AliasedInstallAfterAliasRemoval(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "aliased-hello", testIO()); err != nil {
		t.Fatal(err)
	}
	if got := loadLockSkill(t, root, "hello").Dir; got != "aliased-hello" {
		t.Errorf("aliased lock entry Dir = %q, want aliased-hello", got)
	}

	// Remove the entry (and its alias) from the mod file.
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Skills = nil
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}

	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installedDir(root, "aliased-hello")); !os.IsNotExist(err) {
		t.Error("prune did not delete the aliased directory recorded in the lock")
	}
	l, err := modfile.LoadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Skills) != 0 {
		t.Errorf("lock = %+v, want the stale aliased entry removed", l.Skills)
	}
}

func TestPrune_DoesNotDeleteDirectoryStillDeclaredWithNewSource(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}

	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Skills[0].Source = "file:///replacement/repo.git"
	if err := modfile.SaveMod(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatalf("Prune after source edit: %v", err)
	}
	if _, err := os.Stat(installedDir(root, "hello")); err != nil {
		t.Fatalf("Prune deleted an installation directory still declared in SKILL.mod: %v", err)
	}
	l, err := modfile.LoadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Skills) != 1 {
		t.Fatalf("lock entries after prune = %+v, want active directory lock retained for sync", l.Skills)
	}
}

// Offline hit: when the network is unavailable but the resolution index and version snapshot exist, continue and annotate the result (PRD §3.2 error table).
func TestGet_OfflineSnapshotHit(t *testing.T) {
	r := newHelloRepo(t)
	root1 := t.TempDir()
	sharedStore := t.TempDir()
	eng1 := newEngine(t, root1, sharedStore)
	if _, err := eng1.Get(ctx, r.URL, "", testIO()); err != nil {
		t.Fatal(err)
	}
	// Disconnect the remote.
	if err := os.Rename(r.Bare, r.Bare+".gone"); err != nil {
		t.Fatal(err)
	}
	root2 := t.TempDir()
	eng2 := newEngine(t, root2, sharedStore)
	rep, err := eng2.Get(ctx, r.URL, "", testIO())
	if err != nil {
		t.Fatalf("offline get should hit the version snapshot: %v", err)
	}
	if rep.Entries[0].Note != i18n.Text("from a local version snapshot; not verified online") {
		t.Errorf("Note = %q", rep.Entries[0].Note)
	}
	if _, err := os.Stat(installedDir(root2, "hello")); err != nil {
		t.Error("offline installation was not written to disk")
	}
}

// Reject installation of a skill containing a symlink (PRD §4).
func TestGet_SymlinkRejected(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "linky")
	if err := os.Symlink("SKILL.md", filepath.Join(r.Work, "link.md")); err != nil {
		t.Fatal(err)
	}
	r.CommitAll("with symlink")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	_, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO())
	var se *source.SymlinkError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want SymlinkError", err, err)
	}
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName)); !os.IsNotExist(err) {
		t.Error("SKILL.mod was written after the request was rejected")
	}
}

func TestGet_SymlinkOutsideSelectedSkillDoesNotBlockRepoSnapshot(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("good", "good", "good.txt")
	r.Write("other/target.txt", "target\n")
	if err := os.Symlink("target.txt", filepath.Join(r.Work, "other", "link.txt")); err != nil {
		t.Fatal(err)
	}
	r.CommitAll("skill plus unrelated symlink")
	r.Tag("v1.0.0")
	r.Finish()

	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//good@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("a symlink outside the target skill should not block the repository snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "good"), "good.txt")); err != nil {
		t.Fatal(err)
	}
}

// list reports status and available upgrades.
func TestList(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	rep, err := eng.List(ctx, testIO())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "installed" {
		t.Errorf("list = %+v", rep.Entries)
	}
	// A new upstream version produces an upgrade notice.
	r.Write("v2.md", "x\n")
	r.CommitAll("v2")
	r.Evolve("v2.0.0", false)
	rep, err = eng.List(ctx, testIO())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Entries[0].Note, i18n.Text("upgrade available → ")) {
		t.Errorf("list did not report an available upgrade: %+v", rep.Entries[0])
	}
}
