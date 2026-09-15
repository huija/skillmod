// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/install"
)

func TestShareTracksManagedLinkReplacement(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	eng := newEngine(t, root, t.TempDir())
	eng.Config.InstallMode = install.Auto
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	managed := installedDir(root, "hello")
	if _, err := os.Readlink(managed); err != nil {
		t.Skipf("native directory symlinks unavailable: %v", err)
	}
	if _, err := eng.Share(ctx, engine.ShareOptions{All: true, Agents: []string{"claude"}}, testIO()); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(agentSkillsDir(root, "claude"), "hello")
	link, err := os.Readlink(shared)
	if err != nil || link != managed {
		t.Fatalf("Share link = %q, %v, want stable managed path %q", link, err, managed)
	}
	r.Write("new.md", "new version\n")
	r.CommitAll("new version")
	r.Evolve("v1.1.0", false)
	if _, err := eng.Update(ctx, nil, engine.UpdateOptions{}, testIO()); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(shared, "new.md")); got != "new version\n" {
		t.Errorf("shared content after Update = %q, want new version", got)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{Relink: true}, testIO()); err != nil {
		t.Fatalf("Sync(relink shared skill): %v", err)
	}
	assertManagedLink(t, managed, shared)
}

func TestShareRejectsSymlinkedDestinations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		suffix string
	}{
		{name: "managed directory", target: ".agents/skills"},
		{name: "managed ancestor", target: ".agents"},
		{name: "missing descendant", target: ".agents/skills", suffix: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLocalSkill(t, root, "hello", "keep\n")
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(filepath.Join(root, filepath.FromSlash(tc.target)), alias); err != nil {
				t.Skipf("native directory symlinks unavailable: %v", err)
			}
			destination := filepath.Join(alias, tc.suffix)
			_, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
				All: true, Dirs: []string{destination},
			}, testIO())
			if err == nil {
				t.Errorf("Share(dir=%q) succeeded, want overlap rejected", destination)
			}
			if got := readFile(t, filepath.Join(installedDir(root, "hello"), "run.sh")); got != "keep\n" {
				t.Errorf("managed content after rejected Share = %q, want keep", got)
			}
		})
	}
}
