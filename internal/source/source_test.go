// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/huija/skillmod/internal/resolve"
)

func TestParseLsRemote(t *testing.T) {
	out := "ref: refs/heads/main\tHEAD\n" +
		"aaaa\tHEAD\n" +
		"aaaa\trefs/heads/main\n" +
		"bbbb\trefs/heads/dev\n" +
		"cccc\trefs/tags/v1.0.0\n" +
		"dddd\trefs/tags/v2.0.0\n" +
		"eeee\trefs/tags/v2.0.0^{}\n" +
		"ffff\trefs/tags/lark-doc/v0.8.1\n"
	refs := ParseLsRemote(out)
	if refs.DefaultBranch != "main" || refs.DefaultHead != "aaaa" {
		t.Errorf("HEAD 解析错: %+v", refs)
	}
	if refs.Heads["dev"] != "bbbb" {
		t.Errorf("Heads = %+v", refs.Heads)
	}
	// Use the peeled commit for an annotated tag.
	if refs.Tags["v2.0.0"] != "eeee" {
		t.Errorf("annotated tag 未取 peeled: %q", refs.Tags["v2.0.0"])
	}
	if refs.Tags["v1.0.0"] != "cccc" || refs.Tags["lark-doc/v0.8.1"] != "ffff" {
		t.Errorf("Tags = %+v", refs.Tags)
	}
}

func TestRepoIdentity_NormalizesTransportAndGitSuffix(t *testing.T) {
	want := "https://github.com/acme/skills"
	for _, repo := range []string{
		"https://github.com/acme/skills",
		"https://user:secret@GITHUB.com:443/acme/skills.git/?token=secret",
		"git@github.com:acme/skills.git",
		"ssh://git@github.com:22/acme/skills.git",
		"github.com/acme/skills.git",
	} {
		if got := RepoIdentity(repo); got != want {
			t.Errorf("RepoIdentity(%q) = %q, want %q", repo, got, want)
		}
	}
	if RepoIdentity("ssh://git@github.com:2222/acme/skills.git") == want {
		t.Error("非默认 SSH 端口不应与标准 HTTPS 身份合并")
	}
	if vcsKey("git@github.com:acme/skills.git") != vcsKey(want) {
		t.Error("同一逻辑 repo 未复用 VCS key")
	}
}

func TestOpenRepo_ReusesCanonicalIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := &Source{VCSRoot: t.TempDir()}
	httpsRepo := "https://example.com/acme/skills.git"
	sshRepo := "git@example.com:acme/skills.git"
	dir1, cleanup, err := s.openRepo(ctx, httpsRepo)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	dir2, cleanup, err := s.openRepo(ctx, sshRepo)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if dir1 != dir2 {
		t.Fatalf("同一逻辑 repo 使用了不同 bare repo: %s != %s", dir1, dir2)
	}
	remote, err := s.run(ctx, dir2, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(remote) != sshRepo {
		t.Fatalf("origin = %q, want 当前请求 URL %q", strings.TrimSpace(remote), sshRepo)
	}
}

// Integration fixture: construct a temporary local bare repository accessed through file:// for deterministic, network-free tests.
// Contents: two commits on main, lightweight tag v1.0.0, annotated tag v2.0.0,
// subdirectory tag lark-doc/v0.8.1, and a dev branch.
func newFixtureRepo(t *testing.T) (url string, headSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用")
	}
	dir := t.TempDir()
	work := filepath.Join(dir, "work")

	git(t, "", "init", "-b", "main", work)
	// Annotated tags and commits both require repository-level identity.
	git(t, work, "config", "user.email", "test@skillmod.dev")
	git(t, work, "config", "user.name", "test")
	writeFile(t, work, "SKILL.md", "---\nname: root-skill\n---\n# root\n")
	writeFile(t, work, "references/a.txt", "a\n")
	writeFile(t, work, "references/b.txt", "b\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "first")
	git(t, work, "tag", "v1.0.0")

	writeFile(t, work, "lark-doc/SKILL.md", "---\nname: lark-doc\n---\n# lark\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "second")
	git(t, work, "tag", "-a", "-m", "release", "v2.0.0")
	git(t, work, "tag", "lark-doc/v0.8.1")

	git(t, work, "checkout", "-b", "dev")
	writeFile(t, work, "dev.txt", "dev only\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "dev commit")
	git(t, work, "checkout", "main")

	bare := filepath.Join(dir, "repo.git")
	git(t, "", "clone", "--bare", work, bare)
	git(t, bare, "config", "uploadpack.allowFilter", "true")
	git(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")

	headSHA = strings.TrimSpace(git(t, work, "rev-parse", "main"))
	return "file://" + bare, headSHA
}

func TestPrefetchMissingBlobs_Batched(t *testing.T) {
	url, _ := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := &Source{VCSRoot: t.TempDir()}
	refs, err := s.Refs(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	commit := refs.Tags["v1.0.0"]
	repoDir, cleanup, err := s.openRepo(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := s.fetchTarget(ctx, repoDir, commit, "refs/tags/v1.0.0"); err != nil {
		t.Fatal(err)
	}
	out, err := s.run(ctx, repoDir, "ls-tree", "-r", "-z", commit)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseLsTree(out, "")
	if err != nil {
		t.Fatal(err)
	}
	missing, err := s.missingBlobs(ctx, repoDir, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) < 3 {
		t.Fatalf("blob:none 后缺失对象数 = %d，未覆盖批量抓取路径", len(missing))
	}
	if err := s.prefetchMissingBlobs(ctx, repoDir, entries); err != nil {
		t.Fatalf("批量抓取 blob: %v", err)
	}
	remaining, err := s.missingBlobs(ctx, repoDir, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("批量抓取后仍缺少 %d 个 blob", len(remaining))
	}
}

func TestFetchRef_BatchUnsupportedFallsBackToLazyFetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试使用 POSIX shell 包装器")
	}
	url, _ := newFixtureRepo(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git 不可用")
	}
	marker := filepath.Join(t.TempDir(), "batch-attempted")
	wrapper := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\nif [ \"$3\" = fetch-pack ]; then : > \"$SKILLMOD_BATCH_MARKER\"; exit 2; fi\nexec \"$SKILLMOD_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKILLMOD_BATCH_MARKER", marker)
	t.Setenv("SKILLMOD_REAL_GIT", realGit)
	s := &Source{Git: wrapper, VCSRoot: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	refs, err := s.Refs(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := s.FetchRef(ctx, url, refs.Tags["v1.0.0"], "refs/tags/v1.0.0")
	if err != nil {
		t.Fatalf("批量抓取失败后惰性兜底未生效: %v", err)
	}
	if len(tree.Files) < 3 {
		t.Fatalf("tree files = %d, want at least 3", len(tree.Files))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("未进入批量抓取路径: %v", err)
	}
}

func TestRefs_Integration(t *testing.T) {
	url, headSHA := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	refs, err := (&Source{}).Refs(ctx, url)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if refs.DefaultBranch != "main" || refs.DefaultHead != headSHA {
		t.Errorf("HEAD = %s/%s, want main/%s", refs.DefaultBranch, refs.DefaultHead, headSHA)
	}
	for _, tag := range []string{"v1.0.0", "v2.0.0", "lark-doc/v0.8.1"} {
		if _, ok := refs.Tags[tag]; !ok {
			t.Errorf("缺 tag %s, Tags = %+v", tag, refs.Tags)
		}
	}
	// An annotated tag must peel to the commit at main HEAD rather than the tag object.
	if refs.Tags["v2.0.0"] != headSHA {
		t.Errorf("v2.0.0 = %s, want peeled commit %s", refs.Tags["v2.0.0"], headSHA)
	}
	if _, ok := refs.Heads["dev"]; !ok {
		t.Error("缺 dev 分支")
	}
}

// Exercise the complete address → source → resolve path.
func TestResolveAgainstRealRepo(t *testing.T) {
	url, headSHA := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	refs, err := (&Source{}).Refs(ctx, url)
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}

	// A latest subdirectory request resolves to a monorepo tag.
	r, err := resolve.Resolve(resolve.Request{Repo: url, Subdir: "lark-doc"}, refs)
	if err != nil {
		t.Fatalf("Resolve subdir: %v", err)
	}
	if r.Version != "lark-doc/v0.8.1" {
		t.Errorf("subdir latest = %q, want lark-doc/v0.8.1", r.Version)
	}

	// A latest root request resolves to the highest semver annotated tag.
	r, err = resolve.Resolve(resolve.Request{Repo: url}, refs)
	if err != nil {
		t.Fatalf("Resolve root: %v", err)
	}
	if r.Version != "v2.0.0" || r.Commit != headSHA {
		t.Errorf("root latest = %+v, want v2.0.0 @ %s", r, headSHA)
	}

	// Reject a branch name using real ls-remote data.
	_, err = resolve.Resolve(resolve.Request{Repo: url, Ref: "dev"}, refs)
	var be *resolve.BranchError
	if !errors.As(err, &be) {
		t.Errorf("Ref=dev err = %v, want BranchError", err)
	}
}

func TestRefs_NotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := (&Source{}).Refs(ctx, "file:///nonexistent/path/repo.git")
	var re *RepoError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v (%T), want RepoError", err, err)
	}
	if re.Kind != RepoNotFound {
		t.Errorf("Kind = %v, want RepoNotFound（stderr: %s）", re.Kind, re.Stderr)
	}
}

func TestFetchRef_PersistentTargetedRepo(t *testing.T) {
	url, headSHA := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vcsRoot := t.TempDir()
	s := &Source{VCSRoot: vcsRoot}

	refs, err := s.Refs(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	v1 := refs.Tags["v1.0.0"]
	if _, err := s.FetchRef(ctx, url, v1, "refs/tags/v1.0.0"); err != nil {
		t.Fatal(err)
	}
	dents, err := os.ReadDir(vcsRoot)
	if err != nil {
		t.Fatal(err)
	}
	var repoDir string
	for _, d := range dents {
		if d.IsDir() {
			if repoDir != "" {
				t.Fatalf("同一远端创建了多个 bare repo: %s, %s", repoDir, d.Name())
			}
			repoDir = filepath.Join(vcsRoot, d.Name())
		}
	}
	if repoDir == "" {
		t.Fatal("未创建持久 bare repo")
	}
	if got := strings.TrimSpace(git(t, repoDir, "rev-parse", "--is-bare-repository")); got != "true" {
		t.Fatalf("is-bare-repository = %q", got)
	}
	// A targeted shallow fetch of v1 must not also obtain the newer v2 commit.
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "-e", headSHA+"^{commit}")
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("定向抓取 v1 时意外下载了 v2 commit")
	}

	if _, err := s.FetchRef(ctx, url, headSHA, "refs/tags/lark-doc/v0.8.1"); err != nil {
		t.Fatal(err)
	}
	dents, _ = os.ReadDir(vcsRoot)
	dirCount := 0
	for _, d := range dents {
		if d.IsDir() {
			dirCount++
		}
	}
	if dirCount != 1 {
		t.Fatalf("两次抓取后 bare repo 数 = %d, want 1", dirCount)
	}

	if err := os.Rename(strings.TrimPrefix(url, "file://"), strings.TrimPrefix(url, "file://")+".gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FetchRef(ctx, url, headSHA, "refs/tags/lark-doc/v0.8.1"); err != nil {
		t.Fatalf("远端消失后复用已有 Git 对象失败: %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), // Isolate the user's global configuration.
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
