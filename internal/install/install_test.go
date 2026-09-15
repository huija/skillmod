// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestManagedDirectories(t *testing.T) {
	if got := SkillsDir("/project"); got != filepath.Join("/project", ".agents", "skills") {
		t.Fatalf("SkillsDir = %q", got)
	}
	if got := SkillDir("/project", "demo"); got != filepath.Join("/project", ".agents", "skills", "demo") {
		t.Fatalf("SkillDir = %q", got)
	}
	if SkillsDirName != ".agents/skills" {
		t.Fatalf("SkillsDirName = %q, want the single managed convention", SkillsDirName)
	}
}

func TestCopyDirPreservesBytesAndExecutableBit(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")
	regular := filepath.Join(src, "SKILL.md")
	executable := filepath.Join(src, "scripts", "run.sh")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regular, []byte{0, 1, '\n', 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{0, 1, '\n', 255}) {
		t.Fatalf("copied bytes = %v", data)
	}
	if st, err := os.Stat(filepath.Join(dst, "scripts", "run.sh")); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable bit not preserved: mode=%o", st.Mode().Perm())
	}
}

func TestCopyDirRejectsIrregularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	src := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	err := CopyDir(src, filepath.Join(t.TempDir(), "copy"))
	if err == nil {
		t.Fatalf("CopyDir error = %v", err)
	}
}

func TestInstallRestoreAndCommit(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	dst := filepath.Join(parent, "demo")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore, _, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "new")
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "old")

	// A fresh transaction committed over an existing destination keeps the new
	// content and removes its backup.
	_, commit, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "new")
	matches, err := filepath.Glob(filepath.Join(parent, ".skillmod-bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("backup remains after commit: %v", matches)
	}
}

// Regression: updating one skill must not delete a sibling skill whose
// directory name merely resembles the backup suffix. v0.0.1 reused the fixed
// dst+".skillmod-bak" path and removed any directory already occupying it.
func TestInstallDoesNotDeleteSiblingBackupNamedSkill(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	dst := filepath.Join(parent, "demo")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(parent, "demo.skillmod-bak")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "SKILL.md"), []byte("sibling"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore, _, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "new")
	assertFileContent(t, filepath.Join(sibling, "SKILL.md"), "sibling")
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "old")
	assertFileContent(t, filepath.Join(sibling, "SKILL.md"), "sibling")

	_, commit2, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	commit2()
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "new")
	assertFileContent(t, filepath.Join(sibling, "SKILL.md"), "sibling")
}

func TestInstallNewDestinationCanBeRestored(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "demo")
	restore, _, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("new destination remains after restore: %v", err)
	}

	committedDst := filepath.Join(t.TempDir(), "demo")
	_, commit, err := Install(src, committedDst)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	assertFileContent(t, filepath.Join(committedDst, "SKILL.md"), "new")
}

func TestInstallCopyFailurePreservesExistingDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	src := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Install(src, dst); err == nil {
		t.Fatal("Install accepted an irregular source tree")
	}
	assertFileContent(t, filepath.Join(dst, "SKILL.md"), "old")
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dst), ".skillmod-tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary installation directories remain: %v", matches)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
