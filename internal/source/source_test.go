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
	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

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
		t.Errorf("HEAD parsed incorrectly: %+v", refs)
	}
	if refs.Heads["dev"] != "bbbb" {
		t.Errorf("Heads = %+v", refs.Heads)
	}
	// Use the peeled commit for an annotated tag.
	if refs.Tags["v2.0.0"] != "eeee" {
		t.Errorf("annotated tag did not use peeled commit: %q", refs.Tags["v2.0.0"])
	}
	if refs.Tags["v1.0.0"] != "cccc" || refs.Tags["lark-doc/v0.8.1"] != "ffff" {
		t.Errorf("Tags = %+v", refs.Tags)
	}
}

func TestRepoArgForFilteredLsRemote(t *testing.T) {
	args := []string{"ls-remote", "--symref", "https://example.com/acme/skills", "HEAD", "refs/heads/*", "refs/tags/*"}
	if got := repoArg(args); got != "https://example.com/acme/skills" {
		t.Errorf("repoArg(%q) = %q, want %q", args, got, "https://example.com/acme/skills")
	}
}

func TestParseLsTree(t *testing.T) {
	out := "100644 blob aaaa\troot.txt\x00" +
		"100755 blob bbbb\tskills/demo/run.sh\x00" +
		"160000 commit cccc\tskills/demo/vendor\x00" +
		"040000 tree dddd\tskills/demo/ignored\x00"
	entries, err := parseLsTree(out, "skills/demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0] != (lsEntry{mode: "100755", typ: "blob", sha: "bbbb", path: "run.sh"}) {
		t.Errorf("blob entry = %+v", entries[0])
	}
	if entries[1].typ != "commit" || entries[1].path != "vendor" {
		t.Errorf("submodule entry = %+v", entries[1])
	}

	for _, malformed := range []string{"no-tab", "100644 blob\tfile"} {
		if _, err := parseLsTree(malformed, ""); err == nil {
			t.Errorf("parseLsTree(%q) succeeded", malformed)
		}
	}
}

func TestSkillNameParsing(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		want    string
	}{
		"plain":         {content: "---\nname: demo\ndescription: test\n---\n# Demo\n", want: "demo"},
		"double quoted": {content: "---\nname: \"demo skill\"\n---\n", want: "demo skill"},
		"single quoted": {content: "---\nname: 'demo'\n---", want: "demo"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseSkillName(tc.content)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSkillMetadataParsing(t *testing.T) {
	metadata, err := ParseSkillMetadata("---\nname: demo\ndescription: 'A useful demo'\n---\n# Demo\n")
	if err != nil {
		t.Fatal(err)
	}
	if metadata != (SkillMetadata{Name: "demo", Description: "A useful demo"}) {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestSkillNameParsingErrors(t *testing.T) {
	for name, content := range map[string]string{
		"opening delimiter": "name: demo\n---\n",
		"closing delimiter": "---\nname: demo\n",
		"missing name":      "---\ndescription: test\n---\n",
		"empty name":        "---\nname: \"\"\n---\n",
		"slash":             "---\nname: a/b\n---\n",
		"backslash":         "---\nname: a\\b\n---\n",
		"dot":               "---\nname: .\n---\n",
		"dot dot":           "---\nname: ..\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSkillName(content)
			var invalid *NoSkillMDError
			if !errors.As(err, &invalid) || invalid.Detail == "" {
				t.Fatalf("error = %v (%T), want NoSkillMDError", err, err)
			}
		})
	}
}

func TestTreeAndDirectorySkillName(t *testing.T) {
	content := []byte("---\nname: demo\n---\n")
	tree := &Tree{Files: []File{{Path: "other.txt"}, {Path: "SKILL.md", Data: content}}}
	if got, err := tree.SkillName(); err != nil || got != "demo" {
		t.Fatalf("Tree.SkillName = %q, %v", got, err)
	}
	if _, err := (&Tree{}).SkillName(); err == nil {
		t.Fatal("Tree.SkillName succeeded without SKILL.md")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := SkillNameFromDir(dir); err != nil || got != "demo" {
		t.Fatalf("SkillNameFromDir = %q, %v", got, err)
	}
	if _, err := SkillNameFromDir(t.TempDir()); err == nil {
		t.Fatal("SkillNameFromDir succeeded without SKILL.md")
	}
}

func TestSourceErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: &SymlinkError{Path: "link"}, want: "symlink"},
		{err: &SubmoduleError{Path: "vendor"}, want: "submodule"},
		{err: &NoSkillMDError{Detail: "bad frontmatter"}, want: "bad frontmatter"},
		{err: &RepoError{Kind: RepoNotFound, Repo: "repo"}, want: "not found"},
		{err: &RepoError{Kind: RepoAuth, Repo: "repo"}, want: "authentication"},
		{err: &RepoError{Kind: RepoOther, Repo: "repo", Stderr: "network down"}, want: "network down"},
	} {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%T.Error() = %q, want substring %q", tc.err, got, tc.want)
		}
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
		t.Error("a non-default SSH port must not share the standard HTTPS identity")
	}
	if vcsKey("git@github.com:acme/skills.git") != vcsKey(want) {
		t.Error("the same logical repository did not reuse its VCS key")
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
		t.Fatalf("the same logical repository used different bare repositories: %s != %s", dir1, dir2)
	}
	remote, err := s.run(ctx, dir2, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(remote) != sshRepo {
		t.Fatalf("origin = %q, want current request URL %q", strings.TrimSpace(remote), sshRepo)
	}
}

// Integration fixture: construct a temporary local bare repository accessed through file:// for deterministic, network-free tests.
// Contents: two commits on main, lightweight tag v1.0.0, annotated tag v2.0.0,
// subdirectory tag lark-doc/v0.8.1, and a dev branch.
func newFixtureRepo(t *testing.T) (url string, headSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
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
		t.Fatalf("missing objects after blob:none fetch = %d; batch-fetch path was not exercised", len(missing))
	}
	if err := s.prefetchMissingBlobs(ctx, repoDir, entries); err != nil {
		t.Fatalf("batch-fetch blobs: %v", err)
	}
	remaining, err := s.missingBlobs(ctx, repoDir, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("%d blobs remain missing after batch fetch", len(remaining))
	}
}

func TestFetchRef_BatchUnsupportedFallsBackToLazyFetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell wrapper")
	}
	url, _ := newFixtureRepo(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
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
		t.Fatalf("lazy-fetch fallback failed after batch-fetch failure: %v", err)
	}
	if len(tree.Files) < 3 {
		t.Fatalf("tree files = %d, want at least 3", len(tree.Files))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("batch-fetch path was not exercised: %v", err)
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
			t.Errorf("missing tag %s; Tags = %+v", tag, refs.Tags)
		}
	}
	// An annotated tag must peel to the commit at main HEAD rather than the tag object.
	if refs.Tags["v2.0.0"] != headSHA {
		t.Errorf("v2.0.0 = %s, want peeled commit %s", refs.Tags["v2.0.0"], headSHA)
	}
	if _, ok := refs.Heads["dev"]; !ok {
		t.Error("dev branch is missing")
	}
}

func TestRefsFiltersUnrelatedNamespaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell wrapper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url, _ := newFixtureRepo(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	marker := filepath.Join(t.TempDir(), "args")
	wrapper := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SKILLMOD_REFS_ARGS\"\nexec \"$SKILLMOD_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKILLMOD_REFS_ARGS", marker)
	t.Setenv("SKILLMOD_REAL_GIT", realGit)
	if _, err := (&Source{Git: wrapper}).Refs(ctx, url); err != nil {
		t.Fatalf("Refs(%q) error = %v, want nil", url, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	want := "ls-remote\n--symref\n" + url + "\nHEAD\nrefs/heads/*\nrefs/tags/*\n"
	if got := string(data); got != want {
		t.Errorf("Refs(%q) git arguments = %q, want %q", url, got, want)
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
				t.Fatalf("one remote created multiple bare repositories: %s, %s", repoDir, d.Name())
			}
			repoDir = filepath.Join(vcsRoot, d.Name())
		}
	}
	if repoDir == "" {
		t.Fatal("persistent bare repository was not created")
	}
	if got := strings.TrimSpace(git(t, repoDir, "rev-parse", "--is-bare-repository")); got != "true" {
		t.Fatalf("is-bare-repository = %q", got)
	}
	// A targeted shallow fetch of v1 must not also obtain the newer v2 commit.
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "-e", headSHA+"^{commit}")
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("targeted v1 fetch unexpectedly downloaded the v2 commit")
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
		t.Fatalf("bare repository count after two fetches = %d, want 1", dirCount)
	}

	if err := os.Rename(strings.TrimPrefix(url, "file://"), strings.TrimPrefix(url, "file://")+".gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FetchRef(ctx, url, headSHA, "refs/tags/lark-doc/v0.8.1"); err != nil {
		t.Fatalf("failed to reuse existing Git objects after the remote disappeared: %v", err)
	}
}

func TestFetchWrapper(t *testing.T) {
	url, headSHA := newFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tree, err := (&Source{VCSRoot: t.TempDir()}).Fetch(ctx, url, headSHA)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Commit != headSHA || len(tree.Files) == 0 {
		t.Fatalf("Fetch tree = commit %q, %d files", tree.Commit, len(tree.Files))
	}
}

func TestOpenRepoRejectsCacheIdentityConflict(t *testing.T) {
	repo := "https://example.com/acme/skills"
	s := &Source{VCSRoot: t.TempDir()}
	dir := filepath.Join(s.VCSRoot, vcsKey(repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".info", []byte("git:https://example.com/other/repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := s.openRepo(context.Background(), repo); err == nil || !strings.Contains(err.Error(), "identity conflict") {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("openRepo error = %v", err)
	}
}

func TestRepositoryFetchHelpers(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name     string
		fetchRef string
		want     string
	}{
		{name: "HEAD", fetchRef: "HEAD", want: "HEAD:refs/skillmod/" + commit},
		{name: "full ref", fetchRef: "refs/tags/v1.0.0", want: "+refs/tags/v1.0.0:refs/tags/v1.0.0"},
		{name: "direct commit", fetchRef: "", want: commit + ":refs/skillmod/" + commit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refspec(commit, tc.fetchRef); got != tc.want {
				t.Fatalf("refspec = %q, want %q", got, tc.want)
			}
		})
	}

	dir := filepath.Join(t.TempDir(), "repo")
	if got := repoOrigin(dir); got != dir {
		t.Fatalf("repoOrigin without metadata = %q, want %q", got, dir)
	}
	if err := os.WriteFile(dir+".info", []byte("git:https://example.com/acme/skills\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := repoOrigin(dir); got != "https://example.com/acme/skills" {
		t.Fatalf("repoOrigin = %q", got)
	}
	if got := shortSHA(commit); got != commit[:12] {
		t.Fatalf("shortSHA = %q", got)
	}
	if got := shortSHA("short"); got != "short" {
		t.Fatalf("shortSHA(short) = %q", got)
	}
}

func TestMapGitErrorClassificationAndTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test obtains a deterministic exit status through a POSIX shell")
	}
	exitErr := exec.Command("sh", "-c", "exit 128").Run()
	for _, tc := range []struct {
		name   string
		stderr string
		kind   RepoErrorKind
	}{
		{name: "not found", stderr: "repository not found", kind: RepoNotFound},
		{name: "authentication", stderr: "Authentication failed", kind: RepoAuth},
		{name: "permission", stderr: "Permission denied", kind: RepoAuth},
		{name: "other", stderr: strings.Repeat("network failure ", 30), kind: RepoOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mapGitError(exitErr, tc.stderr, "repo")
			var repoErr *RepoError
			if !errors.As(err, &repoErr) || repoErr.Kind != tc.kind {
				t.Fatalf("error = %v (%T), want RepoError kind %v", err, err, tc.kind)
			}
			if len(repoErr.Stderr) > 303 {
				t.Fatalf("stderr was not truncated: %d bytes", len(repoErr.Stderr))
			}
		})
	}
	plain := errors.New("start failed")
	if err := mapGitError(plain, "", "repo"); !errors.Is(err, plain) {
		t.Fatalf("non-exit error = %v, want wrapped original error", err)
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
