// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
)

func TestProjectAndGlobalShareSnapshotAcrossInstallModes(t *testing.T) {
	r := newHelloRepo(t)
	storeRoot := t.TempDir()
	project := newEngine(t, t.TempDir(), storeRoot)
	global := newEngine(t, t.TempDir(), storeRoot)
	project.Config.InstallMode = install.Auto
	global.Config.InstallMode = install.Copy
	global.ManifestRoot = filepath.Join(storeRoot, "global")
	for _, eng := range []*engine.Engine{project, global} {
		if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Verify(ctx, testIO()); err != nil {
			t.Errorf("Verify %s: %v", eng.Root, err)
		}
	}
	for _, name := range []string{modfile.ModFileName, modfile.LockFileName} {
		a := readFileString(t, filepath.Join(project.Root, name))
		b := readFileString(t, filepath.Join(global.ManifestRoot, name))
		if a != b {
			t.Errorf("project/global %s differ:\n%s\n%s", name, a, b)
		}
	}
	// Relink a previously copied installation; all metadata stays unchanged.
	beforeMod := readFileString(t, filepath.Join(global.ManifestRoot, modfile.ModFileName))
	beforeLock := readFileString(t, filepath.Join(global.ManifestRoot, modfile.LockFileName))
	global.Config.InstallMode = install.Auto
	relink := testIO()
	relink.Relink = true
	if _, err := global.Sync(ctx, false, relink); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, filepath.Join(global.ManifestRoot, modfile.ModFileName)); got != beforeMod {
		t.Error("relink changed global declarations")
	}
	if got := readFileString(t, filepath.Join(global.ManifestRoot, modfile.LockFileName)); got != beforeLock {
		t.Error("relink changed global lock")
	}
	projectLink, projectErr := os.Readlink(installedDir(project.Root, "hello"))
	globalLink, globalErr := os.Readlink(installedDir(global.Root, "hello"))
	if projectErr == nil && globalErr == nil && projectLink != globalLink {
		t.Errorf("scope links = %q and %q, want shared snapshot", projectLink, globalLink)
	}
	// Pruning the project cannot remove the other scope or its shared snapshot.
	if err := modfile.SaveMod(project.Root, &modfile.Mod{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Prune(ctx, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(installedDir(project.Root, "hello")); !os.IsNotExist(err) {
		t.Errorf("pruned entry remains: %v", err)
	}
	if _, err := global.Verify(ctx, testIO()); err != nil {
		t.Errorf("prune damaged global installation: %v", err)
	}
	if _, err := project.Store.GetSnapshot(r.URL, "v1.0.0"); err != nil {
		t.Errorf("prune damaged shared snapshot: %v", err)
	}
}

func TestRelinkDryRunAndCopyDetachment(t *testing.T) {
	r := newHelloRepo(t)
	eng := newEngine(t, t.TempDir(), t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	dst := installedDir(eng.Root, "hello")
	eng.Config.InstallMode = install.Auto
	relink := testIO()
	relink.Relink, relink.DryRun = true, true
	var out bytes.Buffer
	relink.Out = &out
	if _, err := eng.Sync(ctx, false, relink); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(dst); err != nil || !st.IsDir() {
		t.Fatalf("relink dry-run changed copy: %v, %v", st, err)
	}
	relink.DryRun = false
	if _, err := eng.Sync(ctx, false, relink); err != nil {
		t.Fatal(err)
	}
	eng.Config.InstallMode = install.Copy
	if _, err := eng.Sync(ctx, false, relink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Store.GetSnapshot(r.URL, "v1.0.0"); err != nil {
		t.Errorf("detached edit damaged snapshot: %v", err)
	}
	// Relink must obey conflict handling and keep local modifications.
	if _, err := eng.Sync(ctx, false, relink); err == nil {
		t.Fatal("relink conflict did not report partial completion")
	}
	if got := readFileString(t, filepath.Join(dst, "SKILL.md")); got != "local edit" {
		t.Errorf("relink overwrote local edit: %q", got)
	}
}

func TestLinkedUpdateKeepsOtherProjectPinned(t *testing.T) {
	r := newHelloRepo(t)
	shared := t.TempDir()
	first, second := newEngine(t, t.TempDir(), shared), newEngine(t, t.TempDir(), shared)
	first.Config.InstallMode, second.Config.InstallMode = install.Auto, install.Auto
	for _, eng := range []*engine.Engine{first, second} {
		if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
			t.Fatal(err)
		}
	}
	r.Write("next.txt", "next version\n")
	r.CommitAll("v1.1")
	r.Evolve("v1.1.0", false)
	if _, err := first.Update(ctx, nil, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installedDir(first.Root, "hello"), "next.txt")); err != nil {
		t.Errorf("updated content unavailable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installedDir(second.Root, "hello"), "next.txt")); !os.IsNotExist(err) {
		t.Errorf("update changed pinned project: %v", err)
	}
	for _, eng := range []*engine.Engine{first, second} {
		if _, err := eng.Verify(ctx, testIO()); err != nil {
			t.Errorf("Verify after shared update: %v", err)
		}
	}
}

func TestSyncRepairsAndPruneRemovesDanglingInstallationLink(t *testing.T) {
	r := newHelloRepo(t)
	eng := newEngine(t, t.TempDir(), t.TempDir())
	eng.Config.InstallMode = install.Auto
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	dst := installedDir(eng.Root, "hello")
	breakLink := func() {
		t.Helper()
		if err := os.RemoveAll(dst); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dst); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
	}
	breakLink()
	if _, err := eng.Verify(ctx, testIO()); err == nil {
		t.Error("Verify accepted dangling installation link")
	}
	if _, err := eng.Sync(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify repaired link: %v", err)
	}
	breakLink()
	if err := modfile.SaveMod(eng.Root, &modfile.Mod{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prune(ctx, testIO()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("Prune left dangling link: %v", err)
	}
	if _, err := eng.Store.GetSnapshot(r.URL, "v1.0.0"); err != nil {
		t.Errorf("link cleanup damaged snapshot: %v", err)
	}
}
