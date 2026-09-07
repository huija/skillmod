// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/store"
)

// loadLock must treat an absent lock as empty but propagate validation errors
// (for example, a lock whose name is not portable), so sync/prune never act on
// a silently emptied lock.
func TestLoadLock_PropagatesValidationErrors(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	l, err := e.loadLock()
	if err != nil || len(l.Skills) != 0 {
		t.Fatalf("missing lock = %+v, %v; want empty lock, nil error", l, err)
	}
	lockPath := filepath.Join(e.Root, modfile.LockFileName)
	if err := os.WriteFile(lockPath, []byte("[[skill]]\nname = \"con\"\ndirhash = \"h1:x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = e.loadLock()
	if err == nil {
		t.Fatal("loadLock swallowed a lock validation error")
	}
	if !strings.Contains(err.Error(), "Advice:") {
		t.Fatalf("loadLock error %q is missing remediation advice", err)
	}
}

func TestValidDirName(t *testing.T) {
	for _, name := range []string{"demo", "Demo-1.2_skill", "com10", "consolidated"} {
		if !validDirName(name) {
			t.Errorf("validDirName(%q) = false", name)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "with space", "café", "a:b", "a|b", "CON", "con.txt", "demo."} {
		if validDirName(name) {
			t.Errorf("validDirName(%q) = true", name)
		}
	}
}

func TestVacatedDir(t *testing.T) {
	const src = "https://example.com/acme/skills//pdf"
	incoming := modfile.ModSkill{Name: "pdf", Source: src}

	tests := []struct {
		name string
		mod  *modfile.Mod
		dir  string
		want string
	}{
		{
			name: "no existing entry",
			mod:  &modfile.Mod{},
			dir:  "pdf",
			want: "",
		},
		{
			name: "entry already occupies the target directory",
			mod:  &modfile.Mod{Skills: []modfile.ModSkill{{Name: "pdf", Source: src}}},
			dir:  "pdf",
			want: "",
		},
		{
			name: "single aliased entry moves to the target directory",
			mod:  &modfile.Mod{Skills: []modfile.ModSkill{{Name: "pdf", Source: src, Alias: "a"}}},
			dir:  "pdf",
			want: "a",
		},
		{
			// upsertMod replaces the first same-source entry, which already
			// occupies the target directory, so nothing is vacated even though a
			// later aliased entry exists.
			name: "first entry wins over a later aliased entry",
			mod: &modfile.Mod{Skills: []modfile.ModSkill{
				{Name: "pdf", Source: src},
				{Name: "pdf", Source: src, Alias: "b"},
			}},
			dir:  "pdf",
			want: "",
		},
		{
			name: "first same-source entry is the vacated one",
			mod: &modfile.Mod{Skills: []modfile.ModSkill{
				{Name: "pdf", Source: src, Alias: "b"},
				{Name: "pdf", Source: src},
			}},
			dir:  "pdf",
			want: "b",
		},
		{
			name: "other sources do not block",
			mod: &modfile.Mod{Skills: []modfile.ModSkill{
				{Name: "other", Source: "https://example.com/acme/skills//other"},
				{Name: "pdf", Source: src, Alias: "a"},
			}},
			dir:  "pdf",
			want: "a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vacatedDir(tt.mod, incoming, tt.dir); got != tt.want {
				t.Errorf("vacatedDir(%+v, %q) = %q, want %q", tt.mod.Skills, tt.dir, got, tt.want)
			}
		})
	}
}

func TestSourceComparisonAndParsing(t *testing.T) {
	repo, subdir, err := splitSource("https://example.com/acme/skills//tools/demo")
	if err != nil || repo != "https://example.com/acme/skills" || subdir != "tools/demo" {
		t.Fatalf("splitSource = %q, %q, %v", repo, subdir, err)
	}
	if _, _, err := splitSource("https://example.com/acme/skills@v1.0.0"); err == nil {
		t.Fatal("source field with a version was accepted")
	}
	if _, _, err := splitSource("https://example.com/acme/skills//../demo"); err == nil {
		t.Fatal("source field with a non-canonical subdirectory was accepted")
	}

	if !sameRemoteSource("https://example.com/acme/skills//demo", "git@example.com:acme/skills.git//demo") {
		t.Error("equivalent HTTPS and SSH sources were considered different")
	}
	if sameRemoteSource("https://example.com/acme/skills//demo", "https://example.com/acme/skills//other") {
		t.Error("different subdirectories were considered the same source")
	}
	if !sameRemoteSource("invalid source//", "invalid source//") {
		// Exact strings are intentionally equal without reparsing.
		t.Error("identical source strings were considered different")
	}
	if sameRemoteSource("invalid source//", "https://example.com/acme/skills") {
		t.Error("an invalid source was considered equivalent to a valid source")
	}
}

func TestCandidateAddress(t *testing.T) {
	tests := []struct {
		name   string
		repo   string
		subdir string
		want   string
	}{
		{
			name:   "HTTPS omits scheme",
			repo:   "https://github.com/acme/skills",
			subdir: "skills/demo",
			want:   "skillmod get github.com/acme/skills//skills/demo",
		},
		{
			name: "SSH remains unchanged",
			repo: "ssh://git@example.com/acme/skills",
			want: "skillmod get ssh://git@example.com/acme/skills",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := candidateAddress(tt.repo, tt.subdir); got != tt.want {
				t.Errorf("candidateAddress(%q, %q) = %q, want %q", tt.repo, tt.subdir, got, tt.want)
			}
		})
	}
}

func TestCandidateDisplaySubdirs(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidates := []skillCandidate{
		{subdir: "skills/.curated/unique", name: "unique"},
		{subdir: "exact", name: "exact"},
		{subdir: "skills/.curated/duplicate", name: "duplicate"},
		{subdir: "skills/.system/duplicate", name: "duplicate"},
		{subdir: "skills/.curated/occupied", name: "occupied"},
		{subdir: "skills/.curated/invalid", name: "invalid/name"},
	}
	want := []string{
		"unique",
		"exact",
		"skills/.curated/duplicate",
		"skills/.system/duplicate",
		"skills/.curated/occupied",
		"skills/.curated/invalid",
	}
	if got := candidateDisplaySubdirs(root, candidates); !slices.Equal(got, want) {
		t.Errorf("candidateDisplaySubdirs(%q, %+v) = %v, want %v", root, candidates, got, want)
	}
}

func TestUpdateRepositoriesCanonicalizesAndDeduplicates(t *testing.T) {
	targets := []modfile.ModSkill{
		{Source: "https://github.com/acme/skills//one"},
		{Source: "git@github.com:acme/skills.git//two"},
		{Source: "https://github.com/other/skills//three"},
	}
	want := []string{
		"https://github.com/acme/skills",
		"https://github.com/other/skills",
	}
	got, err := updateRepositories(targets)
	if err != nil {
		t.Fatalf("updateRepositories(%+v) error = %v, want nil", targets, err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("updateRepositories(%+v) = %v, want %v", targets, got, want)
	}
}

func TestSnapshotMemoReusesVerifiedRepository(t *testing.T) {
	repo, version, commit := "https://example.com/acme/skills", "v1.0.0", strings.Repeat("a", 40)
	files := []source.File{{Path: "SKILL.md", Data: []byte("---\nname: demo\n---\n")}}
	treeHash, err := hashTree(&source.Tree{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	s := store.New(t.TempDir())
	if _, err := s.PutSnapshot(store.SnapshotInfo{
		Repo: repo, Version: version, Commit: commit, Treehash: treeHash,
	}, files); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{Store: s}
	memo := newOperationMemo(nil)
	first, err := eng.snapshot(repo, version, memo)
	if err != nil {
		t.Fatalf("Engine.snapshot(%q, %q, first call) error = %v, want nil", repo, version, err)
	}
	second, err := eng.snapshot("git@example.com:acme/skills.git", version, memo)
	if err != nil {
		t.Fatalf("Engine.snapshot(%q, %q, equivalent repo) error = %v, want nil", repo, version, err)
	}
	if second != first {
		t.Errorf("Engine.snapshot(%q, %q) returned distinct snapshots %p and %p, want one memoized verification", repo, version, first, second)
	}
}

func TestSnapshotSkillDirRejectsUnmaterializablePaths(t *testing.T) {
	material := t.TempDir()
	if err := os.MkdirAll(filepath.Join(material, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A legacy snapshot (written before the portability rules) can contain a
	// Windows reserved name that would only exist because it was created on
	// Linux; the read path must reject it instead of reinstalling it.
	if err := os.WriteFile(filepath.Join(material, "skills", "demo", "con.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := &store.Snapshot{Info: store.SnapshotInfo{Version: "v1.0.0", Treehash: "h1:legacy"}, ContentDir: material}

	if _, err := snapshotSkillDir(snap, "skills/demo"); err == nil || !strings.Contains(err.Error(), "con.md") {
		t.Fatalf("snapshotSkillDir error = %v, want unmaterializable-path rejection", err)
	}

	// Clean subtrees remain usable.
	clean := filepath.Join(material, "skills", "clean")
	if err := os.MkdirAll(clean, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clean, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := snapshotSkillDir(snap, "skills/clean"); err != nil || got != clean {
		t.Fatalf("snapshotSkillDir(clean) = %q, %v", got, err)
	}
}

func TestResolveConflicts(t *testing.T) {
	conflicts := []conflict{{name: "a", dir: "/skills/a"}, {name: "b", dir: "/skills/b"}}

	if skip, err := resolveConflicts(IO{}, nil); err != nil || len(skip) != 0 {
		t.Fatalf("no conflicts = %v, %v", skip, err)
	}
	if _, err := resolveConflicts(IO{}, conflicts); err == nil || !strings.Contains(err.Error(), "/skills/a") {
		t.Fatalf("non-interactive conflict error = %v", err)
	}

	var out bytes.Buffer
	skip, err := resolveConflicts(IO{Yes: true, Out: &out}, conflicts)
	if err != nil || !skip["/skills/a"] || !skip["/skills/b"] || !strings.Contains(out.String(), "/skills/a") {
		t.Fatalf("--yes conflicts = %v, %v, output %q", skip, err, out.String())
	}

	chooser := &choiceConfirmer{choices: []int{0, 1}}
	skip, err = resolveConflicts(IO{Confirm: chooser}, conflicts)
	if err != nil || skip["/skills/a"] || !skip["/skills/b"] {
		t.Fatalf("interactive conflicts = %v, %v", skip, err)
	}
	if chooser.calls != 2 {
		t.Fatalf("Choose calls = %d, want 2", chooser.calls)
	}

	if _, err := resolveConflicts(IO{Confirm: &choiceConfirmer{choices: []int{2}}}, conflicts[:1]); err == nil {
		t.Fatal("abort choice did not return an error")
	}
}

func TestClassifyTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	currentHash, err := dirhash.HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got := classifyTarget(filepath.Join(t.TempDir(), "missing"), "h1:new", ""); got != "install" {
		t.Fatalf("missing target = %q", got)
	}
	if got := classifyTarget(dir, currentHash, ""); got != "keep" {
		t.Fatalf("matching target = %q", got)
	}
	if got := classifyTarget(dir, "h1:new", currentHash); got != "install" {
		t.Fatalf("clean previous target = %q", got)
	}
	if got := classifyTarget(dir, "h1:new", "h1:other"); got != "conflict" {
		t.Fatalf("modified target = %q", got)
	}
}

func TestClassifyTarget_SymlinkIsLocalModification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the installation directory can only be user-created;
	// classifying it as conflict prevents a silent overwrite that would delete
	// the link (data loss), instead of treating it as clean content.
	if err := os.Symlink(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	if got := classifyTarget(dir, "h1:new", "h1:other"); got != "conflict" {
		t.Fatalf("symlink-bearing target = %q, want conflict", got)
	}
}

func TestClassifyTarget_EmptyDirIsInstallable(t *testing.T) {
	dir := t.TempDir()
	if got := classifyTarget(dir, "h1:new", ""); got != "install" {
		t.Fatalf("empty existing target = %q, want install", got)
	}
}

func TestDisplayListAction(t *testing.T) {
	for action, want := range map[string]string{
		"installed": "installed",
		"unlocked":  "unlocked",
		"missing":   "missing",
		"drift":     "drift",
		"local":     "local",
	} {
		if got := displayListAction(action); got != want {
			t.Errorf("displayListAction(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestEngineErrorDiagnostics(t *testing.T) {
	tests := []struct {
		err  error
		want []string
	}{
		{err: &DriftError{}, want: []string{"drift detected"}},
		{err: &TamperError{Name: "demo", Want: "h1:want", Got: "h1:got"}, want: []string{"demo", "h1:want", "h1:got"}},
		{err: &NameConflictError{Name: "demo", Existing: "old", Incoming: "new"}, want: []string{"demo", "old", "new", "--alias"}},
		{err: &NameConflictError{Name: "Demo", OtherName: "demo", Existing: "old", Incoming: "new"}, want: []string{"Demo", "demo", "differ only in letter case", "--alias"}},
		{err: &skillSubdirError{Subdir: "tools/demo", Version: "v1.0.0"}, want: []string{"tools/demo", "v1.0.0"}},
	}
	for _, tt := range tests {
		message := tt.err.Error()
		for _, want := range tt.want {
			if !strings.Contains(message, want) {
				t.Errorf("%T message %q is missing %q", tt.err, message, want)
			}
		}
	}
}

func TestApplyInstallsCommitAndRollback(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("commit", func(t *testing.T) {
		target := newInstallationTarget(t, "old")
		finalize, err := applyInstalls([]plannedInstall{{name: "demo", contentDir: src, targets: []string{target}}})
		if err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, target, "new")
		if err := finalize(true); err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, target, "new")
		if _, err := os.Stat(target + ".skillmod-bak"); !os.IsNotExist(err) {
			t.Fatalf("backup remains after commit: %v", err)
		}
	})

	t.Run("explicit rollback", func(t *testing.T) {
		first := newInstallationTarget(t, "old-first")
		second := newInstallationTarget(t, "old-second")
		finalize, err := applyInstalls([]plannedInstall{{name: "demo", contentDir: src, targets: []string{first, second}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := finalize(false); err != nil {
			t.Fatal(err)
		}
		assertInstallationContent(t, first, "old-first")
		assertInstallationContent(t, second, "old-second")
	})

	t.Run("later failure rolls back earlier target", func(t *testing.T) {
		target := newInstallationTarget(t, "old")
		plans := []plannedInstall{
			{name: "good", contentDir: src, targets: []string{target}},
			{name: "bad", contentDir: filepath.Join(t.TempDir(), "missing"), targets: []string{filepath.Join(t.TempDir(), "bad")}},
		}
		if _, err := applyInstalls(plans); err == nil || !strings.Contains(err.Error(), "all changes were rolled back") {
			t.Fatalf("applyInstalls error = %v", err)
		}
		assertInstallationContent(t, target, "old")
	})
}

type choiceConfirmer struct {
	choices []int
	calls   int
}

func (*choiceConfirmer) Confirm(string) bool { return false }

func (c *choiceConfirmer) Choose(string, []string) int {
	choice := c.choices[c.calls]
	c.calls++
	return choice
}

func newInstallationTarget(t *testing.T, content string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return target
}

func assertInstallationContent(t *testing.T, target, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("installation content = %q, want %q", data, want)
	}
}
