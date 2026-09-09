// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacySkillLocation(t *testing.T) {
	tests := []struct {
		name string
		in   legacySkill
		repo string
		sub  string
	}{
		{
			name: "github path",
			in:   legacySkill{SourceType: "github", Source: "vercel-labs/skills", SourceURL: "https://github.com/vercel-labs/skills.git", SkillPath: "skills/find-skills/SKILL.md", SkillFolderHash: "3013fdeb8a11b10b1eb795ec3ae8bfca38f7c26d"},
			repo: "https://github.com/vercel-labs/skills.git",
			sub:  "skills/find-skills",
		},
		{
			name: "github source fallback",
			in:   legacySkill{SourceType: "github", Source: "acme/skills", SkillPath: "skills/demo"},
			repo: "https://github.com/acme/skills",
			sub:  "skills/demo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, subdir, err := tt.in.location()
			if err != nil {
				t.Fatal(err)
			}
			if repo != tt.repo || subdir != tt.sub {
				t.Fatalf("location = %q, %q; want %q, %q", repo, subdir, tt.repo, tt.sub)
			}
		})
	}
}

func TestLegacySkillLocationRejectsUnsafeSources(t *testing.T) {
	for _, in := range []legacySkill{
		{SourceType: "npm", Source: "acme/skills"},
		{SourceType: "github", SourceURL: "https://github.com/acme/skills", SkillPath: "../outside"},
		{SourceType: "github", SourceURL: "https://github.com/acme/skills", SkillFolderHash: "not-a-sha"},
	} {
		if _, _, err := in.location(); err == nil {
			t.Errorf("location(%+v) succeeded", in)
		}
	}
}

func TestLegacySkillsGlobalFallsBackWhenXDGStateLockIsAbsent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	legacyDir := filepath.Join(root, ".agents")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":3,"skills":{"demo":{"sourceType":"github","source":"acme/demo"}}}`)
	if err := os.WriteFile(filepath.Join(legacyDir, ".skill-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Root: root, ManifestRoot: filepath.Join(root, "skillmod", "global")}
	got, err := e.legacySkills()
	if err != nil {
		t.Fatal(err)
	}
	if got["demo"].Source != "acme/demo" {
		t.Fatalf("legacy skills = %+v, want fallback .agents lock", got)
	}
}

func TestLegacySkillsIgnoresUnsupportedLockVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), []byte(`{"version":99,"skills":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Root: root}
	got, err := e.legacySkills()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("legacy skills = %+v, want unsupported version ignored", got)
	}
}
