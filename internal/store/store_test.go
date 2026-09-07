// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func testFiles() []source.File {
	return []source.File{
		{Path: "SKILL.md", Data: []byte("---\nname: demo\ndescription: test\n---\n# demo\n")},
		{Path: "scripts/run.sh", Data: []byte("#!/bin/sh\n"), Exec: true},
	}
}

func hashFiles(t *testing.T, files []source.File) string {
	t.Helper()
	data := make(map[string][]byte, len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
		data[f.Path] = f.Data
	}
	h, err := dirhash.HashBlobs(paths, func(name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data[name])), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestOpen_DefaultAndOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HomeEnv, "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s, err := Open("v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".agents", "skillmod"); s.Root() != want {
		t.Fatalf("Root = %q, want %q", s.Root(), want)
	}
	if want := filepath.Join(home, ".agents", "skillmod", "pkg", "mod", "cache", "vcs"); s.VCSRoot() != want {
		t.Fatalf("VCSRoot = %q, want %q", s.VCSRoot(), want)
	}

	override := filepath.Join(t.TempDir(), "custom-store")
	t.Setenv(HomeEnv, override)
	s, err = Open("v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != override {
		t.Fatalf("override Root = %q, want %q", s.Root(), override)
	}

	t.Setenv(HomeEnv, "relative/path")
	if _, err := Open("v0.0.1"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative %s err = %v", HomeEnv, err)
	}
}

func TestSnapshot_RoundTripAndConflict(t *testing.T) {
	s := New(t.TempDir())
	files := testFiles()
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "skills/demo/v1.2.3",
		Commit: strings.Repeat("a", 40), Treehash: hashFiles(t, files),
	}
	snap, err := s.PutSnapshot(info, files)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("example.com", "acme", "skills@skills%2Fdemo%2Fv1.2.3"); !strings.HasSuffix(snap.ContentDir, want) {
		t.Fatalf("snapshot path = %s, want suffix %s", snap.ContentDir, want)
	}
	rootTagPath, err := s.SnapshotPath(info.Repo, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rootTagPath == snap.ContentDir {
		t.Fatal("root tag and subdirectory-prefixed tag mapped to the same snapshot")
	}
	sshPath, err := s.SnapshotPath("git@example.com:acme/skills.git", info.Version)
	if err != nil {
		t.Fatal(err)
	}
	httpsPath, err := s.SnapshotPath("https://example.com/acme/skills", info.Version)
	if err != nil {
		t.Fatal(err)
	}
	if sshPath != httpsPath {
		t.Fatalf("snapshot paths differ for the same logical repository: ssh=%s https=%s", sshPath, httpsPath)
	}
	got, err := s.GetSnapshot(info.Repo, info.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Info.Format != "" {
		t.Fatalf("Format = %q, want empty for a test store", got.Info.Format)
	}
	info.Format = got.Info.Format
	if !reflect.DeepEqual(got.Info, info) {
		t.Fatalf("Info = %+v, want %+v", got.Info, info)
	}
	if version, ok, err := s.FindSnapshotVersionByCommit(info.Repo, info.Commit); err != nil || !ok || version != info.Version {
		t.Fatalf("FindSnapshotVersionByCommit = %q, %v, %v; want %q, true, nil", version, ok, err, info.Version)
	}
	if _, err := os.Stat(filepath.Join(got.ContentDir, "scripts", "run.sh")); err != nil {
		t.Fatal(err)
	}
	aliasSnapshot, err := s.GetSnapshot("git@example.com:acme/skills.git", info.Version)
	if err != nil {
		t.Fatalf("read HTTPS snapshot through equivalent SSH URL: %v", err)
	}
	if aliasSnapshot.ContentDir != got.ContentDir {
		t.Fatalf("equivalent URLs did not reuse the snapshot: %s != %s", aliasSnapshot.ContentDir, got.ContentDir)
	}

	changed := info
	changed.Commit = strings.Repeat("b", 40)
	_, err = s.PutSnapshot(changed, files)
	var conflict *SnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v (%T), want SnapshotConflictError", err, err)
	}
}

func TestSnapshot_CorruptionDetected(t *testing.T) {
	s := New(t.TempDir())
	files := testFiles()
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("c", 40), Treehash: hashFiles(t, files),
	}
	snap, err := s.PutSnapshot(info, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap.ContentDir, "SKILL.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetSnapshot(info.Repo, info.Version)
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("err = %v (%T), want CorruptError", err, err)
	}
}

func TestOpen_CreatesReadOnlySnapshots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	t.Setenv(HomeEnv, root)
	// Restore permissions before TempDir's cleanup runs.
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	s, err := Open("v0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	files := testFiles()
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("e", 40), Treehash: hashFiles(t, files),
	}
	snap, err := s.PutSnapshot(info, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{snap.ContentDir, filepath.Join(snap.ContentDir, "SKILL.md")} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o222 != 0 {
			t.Fatalf("version snapshot remains writable: %s mode=%o", path, st.Mode().Perm())
		}
	}
	infoPath, err := s.snapshotInfoPath(info.Repo, info.Version)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(infoPath); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm()&0o222 != 0 {
		t.Fatalf("version metadata remains writable: %s mode=%o", infoPath, st.Mode().Perm())
	}
}

func TestResolveIndex_PerEntryRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := ResolveEntry{Version: "v1.0.0", Commit: strings.Repeat("d", 40), Dirhash: "h1:test"}
	if err := s.PutResolved("https://example.com/a/b", "skills/demo", "v1.0.0", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetResolved("https://example.com/a/b", "skills/demo", "v1.0.0")
	if err != nil || !ok || got != want {
		t.Fatalf("GetResolved = %+v, %v, %v; want %+v, true, nil", got, ok, err, want)
	}
	alias, ok, err := s.GetResolved("git@example.com:a/b.git", "skills/demo", "v1.0.0")
	if err != nil || !ok || alias != want {
		t.Fatalf("alias GetResolved = %+v, %v, %v; want %+v, true, nil", alias, ok, err, want)
	}
	replaced := ResolveEntry{Version: "v1.0.1", Commit: strings.Repeat("e", 40), Dirhash: "h1:replaced"}
	if err := s.PutResolved("https://example.com/a/b", "skills/demo", "v1.0.0", replaced); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetResolved("https://example.com/a/b", "skills/demo", "v1.0.0")
	if err != nil || !ok || got != replaced {
		t.Fatalf("replaced GetResolved = %+v, %v, %v; want %+v, true, nil", got, ok, err, replaced)
	}
	if _, ok, err := s.GetResolved("https://example.com/a/b", "skills/demo", "missing"); err != nil || ok {
		t.Fatalf("missing = ok %v err %v", ok, err)
	}

	path := s.resolvePath("https://example.com/a/b", "skills/demo", "v1.0.0")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetResolved("https://example.com/a/b", "skills/demo", "v1.0.0"); err == nil {
		t.Fatal("corrupt index did not return an error")
	}
}

func TestRepoRefs_RoundTripAndCanonicalIdentity(t *testing.T) {
	s := New(t.TempDir())
	want := &resolve.Refs{
		Tags:          map[string]string{"alpha/v1.0.0": strings.Repeat("a", 40), "beta/v1.0.0": strings.Repeat("b", 40)},
		Heads:         map[string]string{"main": strings.Repeat("c", 40)},
		DefaultBranch: "main",
		DefaultHead:   strings.Repeat("c", 40),
	}
	if err := s.PutRepoRefs("https://example.com/acme/skills", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetRepoRefs("git@example.com:acme/skills.git")
	if err != nil || !ok {
		t.Fatalf("GetRepoRefs = %+v, %v, %v", got, ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRepoRefs = %+v, want %+v", got, want)
	}
	if err := s.PutRepoRefs("ssh://git@example.com:22/acme/skills.git", want); err != nil {
		t.Fatal(err)
	}
	if gotPath, aliasPath := s.repoRefsPath("https://example.com/acme/skills"), s.repoRefsPath("git@example.com:acme/skills.git"); gotPath != aliasPath {
		t.Fatalf("refs paths differ for equivalent URLs: %s != %s", gotPath, aliasPath)
	} else if _, err := os.Stat(gotPath); err != nil {
		t.Fatal(err)
	}
}

func TestGetSnapshot_Missing(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.GetSnapshot("https://example.com/a/b", "v1.0.0")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestSnapshotPathValidationAndEscaping(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.SnapshotPath("https://example.com", "v1.0.0"); err == nil {
		t.Fatal("repository without a project path was accepted")
	}
	if _, err := s.SnapshotPath("https://example.com/acme/skills", "latest"); err == nil {
		t.Fatal("non-semver version was accepted")
	}

	got, err := s.SnapshotPath("https://example.com/Acme/skill set", "tools/demo/v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join("example.com", "!acme", "skill%20set@tools%2Fdemo%2Fv1.2.3")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("escaped snapshot path = %q, want suffix %q", got, wantSuffix)
	}
}

func TestGetSnapshotDetectsBrokenComponents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, SnapshotInfo, *Snapshot, string)
		want   string
	}{
		{
			name: "missing metadata",
			mutate: func(t *testing.T, _ *Store, _ SnapshotInfo, _ *Snapshot, infoPath string) {
				if err := os.Remove(infoPath); err != nil {
					t.Fatal(err)
				}
			},
			want: "metadata is unreadable",
		},
		{
			name: "missing content directory",
			mutate: func(t *testing.T, _ *Store, _ SnapshotInfo, snap *Snapshot, _ string) {
				if err := os.RemoveAll(snap.ContentDir); err != nil {
					t.Fatal(err)
				}
			},
			want: "directory is unreadable",
		},
		{
			name: "invalid metadata JSON",
			mutate: func(t *testing.T, _ *Store, _ SnapshotInfo, _ *Snapshot, infoPath string) {
				if err := os.WriteFile(infoPath, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "cannot parse version metadata",
		},
		{
			name: "metadata identity mismatch",
			mutate: func(t *testing.T, _ *Store, info SnapshotInfo, _ *Snapshot, infoPath string) {
				info.Repo = "https://example.com/other/repository"
				data, err := json.Marshal(info)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(infoPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, info, snap, infoPath := newTestSnapshot(t)
			tt.mutate(t, s, info, snap, infoPath)
			_, err := s.GetSnapshot(info.Repo, info.Version)
			var corrupt *CorruptError
			if !errors.As(err, &corrupt) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v (%T), want CorruptError containing %q", err, err, tt.want)
			}
		})
	}
}

func TestSnapshot_FormatVersioning(t *testing.T) {
	files := testFiles()
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("f", 40), Treehash: hashFiles(t, files),
	}

	t.Run("records the running version", func(t *testing.T) {
		root := t.TempDir()
		s := newStore(root, "v0.0.1", false)
		snap, err := s.PutSnapshot(info, files)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Info.Format != "v0.0.1" {
			t.Fatalf("Format = %q, want %q", snap.Info.Format, "v0.0.1")
		}
		got, err := s.GetSnapshot(info.Repo, info.Version)
		if err != nil || got.Info.Format != "v0.0.1" {
			t.Fatalf("GetSnapshot = %+v, %v", got.Info, err)
		}
	})

	// Release and development builds of the same generation must accept each
	// other's snapshots: the treehash is re-verified on every load regardless.
	t.Run("accepts snapshots across builds of the same release", func(t *testing.T) {
		root := t.TempDir()
		release := newStore(root, "v0.0.1", false)
		if _, err := release.PutSnapshot(info, files); err != nil {
			t.Fatal(err)
		}
		dev := newStore(root, "v0.0.1-1-g21882fe", false)
		if _, err := dev.GetSnapshot(info.Repo, info.Version); err != nil {
			t.Fatalf("release-written snapshot rejected by dev build: %v", err)
		}

		devInfo := info
		devInfo.Version = "v1.1.0"
		if _, err := dev.PutSnapshot(devInfo, files); err != nil {
			t.Fatal(err)
		}
		if _, err := release.GetSnapshot(info.Repo, devInfo.Version); err != nil {
			t.Fatalf("dev-written snapshot rejected by release build: %v", err)
		}
	})

	t.Run("accepts snapshots written by an older release", func(t *testing.T) {
		root := t.TempDir()
		older := newStore(root, "v0.0.1", false)
		if _, err := older.PutSnapshot(info, files); err != nil {
			t.Fatal(err)
		}
		current := newStore(root, "v0.1.0", false)
		if _, err := current.GetSnapshot(info.Repo, info.Version); err != nil {
			t.Fatalf("snapshot written by an older release was rejected: %v", err)
		}
	})

	t.Run("rejects snapshots written by a newer release", func(t *testing.T) {
		root := t.TempDir()
		newer := newStore(root, "v0.0.2", false)
		if _, err := newer.PutSnapshot(info, files); err != nil {
			t.Fatal(err)
		}
		older := newStore(root, "v0.0.1", false)
		if _, err := older.GetSnapshot(info.Repo, info.Version); err == nil || !strings.Contains(err.Error(), "newer than this build") {
			t.Fatalf("GetSnapshot error = %v, want newer-build rejection", err)
		}
	})

	t.Run("ignores non-semver dev versions", func(t *testing.T) {
		root := t.TempDir()
		s := newStore(root, "21882fe", false) // Pre-tag dev builds record a bare commit.
		if _, err := s.PutSnapshot(info, files); err != nil {
			t.Fatal(err)
		}
		release := newStore(root, "v0.0.1", false)
		if _, err := release.GetSnapshot(info.Repo, info.Version); err != nil {
			t.Fatalf("bare-commit snapshot rejected by release build: %v", err)
		}
	})
}

func TestFindSnapshotVersionRejectsInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(SnapshotInfo) SnapshotInfo
		data   []byte
		want   string
	}{
		{name: "invalid JSON", data: []byte("{"), want: "cannot parse version metadata"},
		{name: "repository mismatch", mutate: func(info SnapshotInfo) SnapshotInfo {
			info.Repo = "https://example.com/other/repository"
			return info
		}, want: "repository identity mismatch"},
		{name: "version mismatch", mutate: func(info SnapshotInfo) SnapshotInfo {
			info.Version = "v2.0.0"
			return info
		}, want: "version does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, info, _, infoPath := newTestSnapshot(t)
			data := tc.data
			if data == nil {
				changed := tc.mutate(info)
				var err error
				data, err = json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(infoPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.FindSnapshotVersionByCommit(info.Repo, info.Commit); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestPutSnapshotRejectsInvalidTrees(t *testing.T) {
	s := New(t.TempDir())
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("a", 40), Treehash: "h1:incorrect",
	}
	if _, err := s.PutSnapshot(info, testFiles()); err == nil || !strings.Contains(err.Error(), "treehash verification failed") {
		t.Fatalf("wrong treehash error = %v", err)
	}

	files := []source.File{{Path: "../escape", Data: []byte("unsafe")}}
	info.Treehash = hashFiles(t, files)
	if _, err := s.PutSnapshot(info, files); err == nil || !strings.Contains(err.Error(), "cannot be materialized") {
		t.Fatalf("unsafe path error = %v", err)
	}
}

func TestPutSnapshot_RejectsUnmaterializableFiles(t *testing.T) {
	s := New(t.TempDir())
	base := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("a", 40), Treehash: "h1:x",
	}
	for name, tc := range map[string]struct {
		files []source.File
		want  string
	}{
		"reserved name":     {[]source.File{{Path: "CON", Data: []byte("x")}}, "cannot be materialized"},
		"reserved with ext": {[]source.File{{Path: "doc/con.txt", Data: []byte("x")}}, "cannot be materialized"},
		"trailing dot":      {[]source.File{{Path: "demo.", Data: []byte("x")}}, "cannot be materialized"},
		"illegal char":      {[]source.File{{Path: "a:b.txt", Data: []byte("x")}}, "cannot be materialized"},
		"backslash":         {[]source.File{{Path: `a\b.txt`, Data: []byte("x")}}, "backslash"},
		"case collision":    {[]source.File{{Path: "doc/Readme", Data: []byte("x")}, {Path: "doc/README", Data: []byte("y")}}, "collide"},
		"ancestor case collision": {[]source.File{
			{Path: "Docs/guide.md", Data: []byte("x")},
			{Path: "docs/reference.md", Data: []byte("y")},
		}, "collide"},
		"ancestor normalization collision": {[]source.File{
			{Path: "café/guide.md", Data: []byte("x")},
			{Path: "cafe\u0301/reference.md", Data: []byte("y")},
		}, "collide"},
		"file directory collision": {[]source.File{
			{Path: "docs", Data: []byte("x")},
			{Path: "DOCS/readme.md", Data: []byte("y")},
		}, "collide"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.PutSnapshot(base, tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "v1.0.0") {
				t.Fatalf("PutSnapshot error = %v, want %q with snapshot context", err, tc.want)
			}
		})
	}
	// Positive control: non-ASCII names materialize everywhere.
	files := []source.File{{Path: "技能/SKILL.md", Data: []byte("---\nname: 技能\n---\n")}}
	info := base
	info.Version = "v1.0.1"
	info.Treehash = hashFiles(t, files)
	if _, err := s.PutSnapshot(info, files); err != nil {
		t.Fatalf("PutSnapshot unicode path: %v", err)
	}
}

func TestEscapePathSegment_WindowsSafety(t *testing.T) {
	cases := map[string]string{
		"con":     "%63on", // lowercase reserved device name
		"aux":     "%61ux",
		"con.txt": "%63on.txt",
		"demo.":   "demo%2E", // trailing dot
		"CON":     "!c!o!n",  // !upper convention already protects uppercase
		"simple":  "simple",
		"a b":     "a%20b",
		"v1.2.3":  "v1.2.3",
	}
	for in, want := range cases {
		if got := escapePathSegment(in); got != want {
			t.Errorf("escapePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoPath_ReservedOwnerOrRepo(t *testing.T) {
	s := New(t.TempDir())
	for _, repo := range []string{"https://example.com/con/skills", "https://example.com/acme/aux", "https://example.com/acme/skills/demo."} {
		path, err := s.SnapshotPath(repo, "v1.0.0")
		if err != nil {
			t.Fatalf("SnapshotPath(%q): %v", repo, err)
		}
		// Every component of the cache path must survive portable validation.
		rel, err := filepath.Rel(s.ModRoot(), path)
		if err != nil {
			t.Fatal(err)
		}
		for comp := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
			comp = strings.TrimSuffix(comp, "@v1.0.0")
			if err := fsutil.ValidPath(comp); err != nil {
				t.Errorf("SnapshotPath(%q) produced unsafe component %q: %v", repo, comp, err)
			}
		}
	}
}

func TestRepoRefsCacheValidation(t *testing.T) {
	repo := "https://example.com/acme/skills"
	s := New(t.TempDir())
	if refs, ok, err := s.GetRepoRefs(repo); err != nil || ok || refs != nil {
		t.Fatalf("missing refs = %+v, %v, %v", refs, ok, err)
	}
	if err := s.PutRepoRefs(repo, nil); err == nil {
		t.Fatal("nil refs cache was accepted")
	}
	if err := s.PutRepoRefs(repo, &resolve.Refs{}); err != nil {
		t.Fatal(err)
	}
	refs, ok, err := s.GetRepoRefs(repo)
	if err != nil || !ok || refs.Tags == nil || refs.Heads == nil {
		t.Fatalf("normalized refs = %+v, %v, %v", refs, ok, err)
	}

	path := s.repoRefsPath(repo)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetRepoRefs(repo); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt refs error = %v", err)
	}

	rec := repoRefsRecord{Repo: "https://example.com/other/repository", Refs: resolve.Refs{}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetRepoRefs(repo); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("mismatched refs error = %v", err)
	}
}

func TestStoreErrorDiagnostics(t *testing.T) {
	conflict := (&SnapshotConflictError{
		Path: "/store/repo@v1.0.0",
		Have: SnapshotInfo{Version: "v1.0.0"},
		Want: SnapshotInfo{Version: "v1.0.0", Commit: strings.Repeat("a", 40), Treehash: "h1:new"},
	}).Error()
	for _, want := range []string{"version snapshot conflict", "commit pending resolution", "hash pending verification", "aaaaaaaaaaaa", "h1:new"} {
		if !strings.Contains(conflict, want) {
			t.Errorf("conflict message %q is missing %q", conflict, want)
		}
	}
	if got := (&CorruptError{Path: "/store/repo", Detail: "bad metadata"}).Error(); !strings.Contains(got, "/store/repo") || !strings.Contains(got, "bad metadata") {
		t.Errorf("corruption message = %q", got)
	}
}

func newTestSnapshot(t *testing.T) (*Store, SnapshotInfo, *Snapshot, string) {
	t.Helper()
	s := New(t.TempDir())
	files := testFiles()
	info := SnapshotInfo{
		Repo: "https://example.com/acme/skills", Version: "v1.0.0",
		Commit: strings.Repeat("f", 40), Treehash: hashFiles(t, files),
	}
	snap, err := s.PutSnapshot(info, files)
	if err != nil {
		t.Fatal(err)
	}
	info.Format = snap.Info.Format
	infoPath, err := s.snapshotInfoPath(info.Repo, info.Version)
	if err != nil {
		t.Fatal(err)
	}
	return s, info, snap, infoPath
}
