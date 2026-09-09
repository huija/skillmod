// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestAutoFallsBackWhenLinksAreUnavailable(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "demo")
	noLinks := func(string, string) error { return fs.ErrPermission }
	_, commit, err := installWithLink(src, dst, Auto, noLinks)
	if err != nil {
		t.Fatalf("Auto installation without link privilege: %v", err)
	}
	commit()
	st, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatalf("Auto fallback mode = %v, want directory", st.Mode())
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(src, "SKILL.md"), "snapshot")
}

func TestLinkReplacementRollbackAndCopyDetach(t *testing.T) {
	old, next := t.TempDir(), t.TempDir()
	for dir, content := range map[string]string{old: "old", next: "new"} {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	parent := t.TempDir()
	dst := filepath.Join(parent, "demo")
	oldRel, err := filepath.Rel(parent, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldRel, dst); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	restore, _, err := InstallWithMode(next, dst, Auto)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "new")
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	gotLink, err := os.Readlink(dst)
	if err != nil || gotLink != oldRel {
		t.Fatalf("restored link = %q, %v, want %q", gotLink, err, oldRel)
	}
	_, commit, err := InstallWithMode(next, dst, Copy)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if st, err := os.Lstat(dst); err != nil || !st.IsDir() {
		t.Fatalf("Copy detach = %v, %v, want independent directory", st, err)
	}
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(old, "SKILL.md"), "old")
	assertFileContent(t, filepath.Join(next, "SKILL.md"), "new")
}

func TestInvalidInstallModeLeavesTargetUntouched(t *testing.T) {
	for _, mode := range []Mode{"typo", "symlink"} {
		t.Run(string(mode), func(t *testing.T) {
			src := t.TempDir()
			dst := filepath.Join(t.TempDir(), "demo")
			if _, _, err := InstallWithMode(src, dst, mode); err == nil {
				t.Errorf("InstallWithMode accepted unsupported policy %q", mode)
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				t.Errorf("invalid policy created target: %v", err)
			}
		})
	}
}

func TestAutoUsesNativeDirectoryLinkWhenSupported(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	probe := filepath.Join(parent, "probe")
	if err := os.Symlink(src, probe); err != nil {
		t.Skipf("native directory symlinks unavailable: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "demo")
	_, commit, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("Auto did not create a native directory link: %v", err)
	}
	want, err := filepath.EvalSymlinks(src)
	if err != nil {
		t.Fatal(err)
	}
	if target != want {
		t.Errorf("Auto link target = %q, want %q", target, want)
	}
}

func TestInstallRejectsSourceInsideDestination(t *testing.T) {
	dst := t.TempDir()
	src := filepath.Join(dst, "snapshot")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(src, dst); err == nil {
		t.Error("Install accepted a source inside the directory it would replace")
	}
	assertFileContent(t, filepath.Join(src, "SKILL.md"), "keep")
}
