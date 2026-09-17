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

// TestShareRejectsAgentDirectoriesThatResolveIntoTheManagedTree covers the
// one way an agent directory can hide an overlap: a symlink standing in for
// the agent root. A name resolves to ".<name>/skills", so the check has to
// look through the existing ancestors rather than compare the spelling.
func TestShareRejectsAgentDirectoriesThatResolveIntoTheManagedTree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "agent directory resolves to the managed ancestor", target: ".agents", wantErr: true},
		{name: "agent directory resolves to the managed directory", target: ".agents/skills", wantErr: true},
		{name: "agent directory resolves inside a managed skill", target: ".agents/skills/hello", wantErr: true},
		{name: "agent directory elsewhere is allowed", target: "", wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLocalSkill(t, root, "hello", "keep\n")
			target := filepath.Join(root, "elsewhere")
			if tc.target != "" {
				target = filepath.Join(root, filepath.FromSlash(tc.target))
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, ".claude")); err != nil {
				t.Skipf("native directory symlinks unavailable: %v", err)
			}
			_, err := newEngine(t, root, t.TempDir()).Share(ctx, engine.ShareOptions{
				All: true, Agents: []string{"claude"},
			}, testIO())
			if tc.wantErr && err == nil {
				t.Error("Share succeeded, want the overlap rejected")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Share: %v; a symlinked agent directory outside the managed tree is usable", err)
			}
			if got := readFile(t, filepath.Join(installedDir(root, "hello"), "run.sh")); got != "keep\n" {
				t.Errorf("managed content after Share = %q, want keep", got)
			}
		})
	}
}
