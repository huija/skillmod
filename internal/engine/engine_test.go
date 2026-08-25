// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Engine integration tests follow the AC traceability table in dev-design §10 and use local bare repositories through file://.
package engine_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/config"
	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/testutil"
)

var ctx = context.Background()

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
	t.Fatalf("lock 中无条目 %s: %+v", name, l.Skills)
	return modfile.LockSkill{}
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
		t.Errorf("lock 条目缺 dirhash/commit: %+v", lk)
	}
	// Installed bytes match the Git source.
	got, err := os.ReadFile(filepath.Join(installedDir(root, "hello"), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(r.Work, "SKILL.md"))
	if string(got) != string(want) {
		t.Error("安装内容与源不一致")
	}
	// Recomputing the installation directory produces the locked dirhash.
	h, err := dirhash.HashDir(installedDir(root, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if h != lk.Dirhash {
		t.Errorf("安装目录重算哈希 %s != lock %s", h, lk.Dirhash)
	}
}

func TestGet_DefaultTargetIsGenericAgents(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "hello", "SKILL.md")); err != nil {
		t.Fatalf("通用目录未安装: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("默认不应创建 .claude，stat err = %v", err)
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
		t.Fatalf("双目标 dirhash 不同: agents=%s claude=%s", hGeneric, hClaude)
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
		t.Skip("测试使用 POSIX shell 包装器")
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
		t.Fatalf("精确版本快照命中失败: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("精确版本命中仍调用了 git: %v", err)
	}
	if rep.Entries[0].Note != "来自本地版本快照，未联网校验" {
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
		t.Errorf("跨机不一致: A=%s B=%s lock=%s", hA, hB, lk.Dirhash)
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
			t.Errorf("二次 sync 仍有安装动作: %+v", en)
		}
	}
	st2, _ := os.Stat(lockPath)
	if st1.ModTime() != st2.ModTime() {
		t.Error("无变更时 SKILL.lock 被重写")
	}
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
	if !strings.Contains(err.Error(), "分支不可锁定，请用 tag 或 commit SHA") {
		t.Errorf("文案 = %q", err)
	}
	// No files were written.
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName)); !os.IsNotExist(err) {
		t.Error("拒绝后仍写入了 SKILL.mod")
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
		t.Errorf("lock 版本 %q 不是伪版本", lk.Version)
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
		t.Errorf("伪版本不可复现: %q vs %q", lk2.Version, lk.Version)
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
		t.Error("子树文件缺失")
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "code-review"), "pdf")); !os.IsNotExist(err) {
		t.Error("安装目录混入了仓库其他内容")
	}
	h, _ := dirhash.HashDir(installedDir(root, "code-review"))
	if h != lk.Dirhash {
		t.Error("子树哈希与 lock 不一致")
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
		t.Fatalf("同 repo@version 第二个 skill 未纯本地命中: %v", err)
	}
	if rep.Entries[0].Note != "来自本地 repo 版本快照，未联网校验" {
		t.Fatalf("同仓缓存提示 = %q", rep.Entries[0].Note)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "beta.txt")); err != nil {
		t.Fatalf("第二个 skill 未安装: %v", err)
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
		t.Fatalf("同 repo 两个 skill 创建了 %d 个 bare repo，want 1", vcsRepos)
	}
	snapshotPath, err := eng.Store.SnapshotPath(r.URL, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snapshotPath, "alpha", "alpha.txt")); err != nil {
		t.Fatalf("repo 快照缺少 alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snapshotPath, "beta", "beta.txt")); err != nil {
		t.Fatalf("repo 快照缺少 beta: %v", err)
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
		t.Fatalf("同 repo@commit 第二个 skill 未纯本地命中: %v", err)
	}
	if rep.Entries[0].Note != "来自本地 repo commit 快照，未联网校验" {
		t.Fatalf("commit 缓存提示 = %q", rep.Entries[0].Note)
	}
	alpha := loadLockSkill(t, root, "alpha")
	beta := loadLockSkill(t, root, "beta")
	if alpha.Commit != commit || beta.Commit != commit || alpha.Version != beta.Version {
		t.Fatalf("同 commit 未共享版本: alpha=%+v beta=%+v", alpha, beta)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "beta.txt")); err != nil {
		t.Fatalf("第二个 skill 未安装: %v", err)
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
		t.Fatalf("version = %q, want 本地根版本 v1.0.0", lk.Version)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "old.txt")); err != nil {
		t.Fatalf("未从本地 repo 版本安装 beta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "beta"), "new.txt")); !os.IsNotExist(err) {
		t.Fatal("显式本地版本意外使用了后来新增的远端子目录 tag")
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
		t.Error("别名目录未安装")
	}
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
		t.Error("未标记冲突")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "user modified\n" {
		t.Error("用户修改被覆盖")
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
		t.Errorf("sync 后版本 = %s, want v1.0.0（lock 权威）", lk.Version)
	}
	for _, en := range rep.Entries {
		if en.Name == "hello" && en.Action == "install" {
			t.Error("lock 未变时不应重装")
		}
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "new-feature.md")); !os.IsNotExist(err) {
		t.Error("新版本内容被安装")
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
		t.Fatalf("一致时 verify 报错: %v", err)
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
		t.Errorf("pdf 条目 = %+v", byName["pdf"])
	}
	if byName["solo"].Source != soloURL || byName["solo"].Version != "v1.0.0" {
		t.Errorf("solo 条目 = %+v", byName["solo"])
	}
	if !byName["scratch"].Local {
		t.Errorf("scratch 应为 local: %+v", byName["scratch"])
	}
	// Lock baseline for a local entry.
	lk := loadLockSkill(t, root, "scratch")
	if !strings.HasPrefix(lk.Dirhash, "h1:") {
		t.Error("local 条目缺 dirhash 基线")
	}
	// Original files remain unchanged.
	after, _ := os.ReadFile(filepath.Join(installedDir(root, "scratch"), "SKILL.md"))
	if string(after) != string(scratchMD) {
		t.Error("init 改动了原文件")
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
		t.Error("已有 SKILL.mod 时 init 应拒绝")
	}
	if _, err := eng.Init(ctx, true, testIO()); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName+".bak")); err != nil {
		t.Error("未生成 .bak 备份")
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
			t.Fatalf("init 未发现 %s: %+v", name, m.Skills)
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
		t.Errorf("update 后版本 = %s, want v1.1.0", lk.Version)
	}
	if _, err := os.Stat(filepath.Join(installedDir(root, "hello"), "v1.md")); err != nil {
		t.Error("新版本内容未安装")
	}
	_ = rep

	// A second update is already current.
	rep, err = eng.Update(ctx, nil, testIO())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries[0].Note != "已是最新" {
		t.Errorf("二次 update = %+v, want 已是最新", rep.Entries[0])
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
		t.Fatalf("版本 %q 非伪版本", lk1.Version)
	}

	r.Write("more.md", "x\n")
	r.CommitAll("c2")
	r.Evolve("", false)

	if _, err := eng.Update(ctx, nil, testIO()); err != nil {
		t.Fatal(err)
	}
	lk2 := loadLockSkill(t, root, "edge")
	if lk2.Version == lk1.Version || lk2.Commit == lk1.Commit {
		t.Error("伪版本条目 update 未升到新 HEAD")
	}
	if !resolve.IsPseudoVersion(lk2.Version) {
		t.Errorf("升级后版本 %q 仍应为伪版本", lk2.Version)
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
		t.Error("sync 未提示过期条目")
	}
	if _, err := os.Stat(installedDir(root, "bb")); err != nil {
		t.Error("sync 删除了文件（违反永不自动删除）")
	}

	// prune removes it.
	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installedDir(root, "bb")); !os.IsNotExist(err) {
		t.Error("prune 未删除残留目录")
	}
	l, _ := modfile.LoadLock(root)
	for _, lk := range l.Skills {
		if lk.Name == "bb" {
			t.Error("lock 仍含过期条目")
		}
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
		t.Fatalf("离线 get 应命中版本快照: %v", err)
	}
	if rep.Entries[0].Note != "来自本地版本快照，未联网校验" {
		t.Errorf("Note = %q", rep.Entries[0].Note)
	}
	if _, err := os.Stat(installedDir(root2, "hello")); err != nil {
		t.Error("离线安装未落盘")
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
		t.Error("拒绝后仍写入了 SKILL.mod")
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
		t.Fatalf("目标 skill 外的 symlink 不应阻塞整仓快照: %v", err)
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
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "已安装" {
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
	if !strings.Contains(rep.Entries[0].Note, "可升级") {
		t.Errorf("list 未提示可升级: %+v", rep.Entries[0])
	}
}
