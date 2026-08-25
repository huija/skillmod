// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/resolve"
	"github.com/huija/skillmod/internal/source"
)

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
	s, err := Open()
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
	s, err = Open()
	if err != nil {
		t.Fatal(err)
	}
	if s.Root() != override {
		t.Fatalf("override Root = %q, want %q", s.Root(), override)
	}

	t.Setenv(HomeEnv, "relative/path")
	if _, err := Open(); err == nil || !strings.Contains(err.Error(), "绝对路径") {
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
		t.Fatal("根 tag 与子目录前缀 tag 映射到了同一版本快照")
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
		t.Fatalf("同一逻辑 repo 的快照路径不同: ssh=%s https=%s", sshPath, httpsPath)
	}
	got, err := s.GetSnapshot(info.Repo, info.Version)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("通过等价 SSH URL 读取 HTTPS 快照: %v", err)
	}
	if aliasSnapshot.ContentDir != got.ContentDir {
		t.Fatalf("等价 URL 未复用快照: %s != %s", aliasSnapshot.ContentDir, got.ContentDir)
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
	s, err := Open()
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
			t.Fatalf("版本快照仍可写: %s mode=%o", path, st.Mode().Perm())
		}
	}
	infoPath, err := s.snapshotInfoPath(info.Repo, info.Version)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(infoPath); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm()&0o222 != 0 {
		t.Fatalf("版本元数据仍可写: %s mode=%o", infoPath, st.Mode().Perm())
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
		t.Fatal("损坏索引未报错")
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
		t.Fatalf("等价 URL 的 refs 路径不同: %s != %s", gotPath, aliasPath)
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
