// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/testutil"
)

func TestInitImportsExternalLinkAndReportsBrokenLink(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	body := "---\nname: external\n---\n# local\n"
	if err := os.WriteFile(filepath.Join(external, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(installedDir(root, "external"))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, installedDir(root, "external")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(external, "missing"), installedDir(root, "broken")); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local || m.Skills[0].Name != "external" {
		t.Errorf("Init declarations = %+v, want external local entry", m)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "broken") {
		t.Errorf("Init notes = %v, want broken link report", rep.Notes)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify imported link: %v", err)
	}
	if got := readFileString(t, filepath.Join(external, "SKILL.md")); got != body {
		t.Errorf("external contents = %q, want %q", got, body)
	}
}

func TestInitRecoversSnapshotLinkWithoutLock(t *testing.T) {
	r := newHelloRepo(t)
	eng := newEngine(t, t.TempDir(), t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := eng.Store.SnapshotPath(r.URL, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	parent := filepath.Dir(installedDir(root, "alias"))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(snapshot, installedDir(root, "alias")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	eng.Root = root
	eng.Source.Git = filepath.Join(t.TempDir(), "no-network")
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Local || m.Skills[0].Alias != "alias" || m.Skills[0].Version != "v1.0.0" {
		t.Fatalf("Init snapshot declarations = %+v, want remote hello alias at v1.0.0", m)
	}
	if _, err := eng.Verify(ctx, testIO()); err != nil {
		t.Errorf("Verify recovered snapshot: %v", err)
	}
}

func TestInitContinuesWhenInstalledSnapshotIsCorrupt(t *testing.T) {
	sourceRoot := t.TempDir()
	producer := newEngine(t, sourceRoot, t.TempDir())
	r := newHelloRepo(t)
	if _, err := producer.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := producer.Store.SnapshotPath(r.URL, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "SKILL.md"), []byte("corrupt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(installedDir(root, "hello")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(snapshot, installedDir(root, "hello")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	other := installedDir(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("---\nname: other\n---\n# other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, root, t.TempDir())
	eng.Store = producer.Store
	eng.Source = producer.Source
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 2 {
		t.Fatalf("Init declarations = %+v, want both skills despite corrupt snapshot", m.Skills)
	}
	for _, skill := range m.Skills {
		if !skill.Local {
			t.Errorf("skill %q = %+v, want local baseline", skill.Name, skill)
		}
	}
}

func TestInitDoesNotInferRemoteFromNameAlone(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "hello")
	r.CommitAll("init")
	r.Tag("v1.0.0")
	root := t.TempDir()
	skill := installedDir(root, "hello")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: hello\n---\n# locally changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, root, t.TempDir())
	// Name matching is deliberately possible, but the contents differ.
	eng.Config.KnownSources = []string{r.FinishNamed("hello")}
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local {
		t.Errorf("Init declarations = %+v, want modified skill kept local", m)
	}
}

func TestInitKeepsUnresolvedLegacyEntryAsLocalBaseline(t *testing.T) {
	root := t.TempDir()
	skill := installedDir(root, "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := struct {
		Version int            `json:"version"`
		Skills  map[string]any `json:"skills"`
	}{
		Version: 1,
		Skills: map[string]any{
			"demo": map[string]any{
				"source":     "demo-package",
				"sourceType": "npm",
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "unresolved" {
		t.Fatalf("Init report = %+v, want one unresolved entry", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local || m.Skills[0].Name != "demo" {
		t.Fatalf("Init declarations = %+v, want local demo baseline", m.Skills)
	}
	lk := loadLockSkill(t, root, "demo")
	if lk.Dirhash == "" || lk.Source != "" {
		t.Fatalf("Init lock = %+v, want local dirhash baseline", lk)
	}
}

func TestInitUsesLegacySourceWhenInstalledContentsDrift(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	skill := installedDir(root, "hello")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: hello\n---\n# edited after install\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := struct {
		Version int            `json:"version"`
		Skills  map[string]any `json:"skills"`
	}{
		Version: 1,
		Skills: map[string]any{
			"hello": map[string]any{
				"sourceType":      "git",
				"sourceUrl":       r.URL,
				"skillPath":       "SKILL.md",
				"ref":             "v1.0.0",
				"skillFolderHash": r.SHA("HEAD^{tree}"),
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	eng := newEngine(t, root, t.TempDir())
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "matched" || !strings.Contains(rep.Entries[0].Note, "run skillmod sync") {
		t.Fatalf("Init report = %+v, want matched source with drift guidance", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Local || m.Skills[0].Version != "v1.0.0" {
		t.Fatalf("Init declarations = %+v, want recorded remote version", m.Skills)
	}
	lk := loadLockSkill(t, root, "hello")
	if lk.Source == "" || lk.Version != "v1.0.0" || lk.Dirhash == "" {
		t.Fatalf("Init lock = %+v, want source revision baseline", lk)
	}
}

func TestInitLegacyTreeHashPreservesTagAfterUnrelatedCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("skills/demo", "demo")
	r.CommitAll("add demo")
	r.Write("README.md", "unrelated change\n")
	r.CommitAll("update readme")
	r.Tag("v1.0.0")
	treeHash := r.SHA("HEAD:skills/demo")
	r.Finish()

	root := t.TempDir()
	skill := installedDir(root, "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n# edited after install\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "demo", map[string]any{
		"sourceType":      "git",
		"sourceUrl":       r.URL,
		"skillPath":       "skills/demo/SKILL.md",
		"ref":             "v1.0.0",
		"skillFolderHash": treeHash,
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Version != "v1.0.0" {
		t.Fatalf("Init report = %+v, want recorded tag v1.0.0", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Local || m.Skills[0].Version != "v1.0.0" {
		t.Fatalf("Init declarations = %+v, want remote demo at v1.0.0", m.Skills)
	}
}

func TestInitLegacyWithoutImmutableRefKeepsMismatchedContentsLocal(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	skill := installedDir(root, "hello")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: hello\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "hello", map[string]any{
		"sourceType": "git",
		"sourceUrl":  r.URL,
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "unresolved" || !strings.Contains(rep.Entries[0].Note, "latest source does not match") {
		t.Fatalf("Init report = %+v, want mismatched ref-less source kept unresolved", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local {
		t.Fatalf("Init declarations = %+v, want local hello baseline", m.Skills)
	}
}

func TestInitLegacyWithoutImmutableRefAdoptsMatchingLatestContents(t *testing.T) {
	r := newHelloRepo(t)
	producer := newEngine(t, t.TempDir(), t.TempDir())
	if _, err := producer.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get(%q): %v", r.URL+"@v1.0.0", err)
	}

	root := t.TempDir()
	if err := install.CopyDir(installedDir(producer.Root, "hello"), installedDir(root, "hello")); err != nil {
		t.Fatalf("CopyDir installed hello: %v", err)
	}
	writeLegacyLock(t, root, "hello", map[string]any{
		"sourceType": "git",
		"sourceUrl":  r.URL,
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "matched" || rep.Entries[0].Version != "v1.0.0" {
		t.Fatalf("Init report = %+v, want ref-less source matched to latest v1.0.0", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod(%q): %v", root, err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Local || m.Skills[0].Source != r.URL || m.Skills[0].Version != "v1.0.0" {
		t.Errorf("Init declarations = %+v, want recovered remote %s@v1.0.0", m.Skills, r.URL)
	}
}

func TestInitLegacyRejectsMismatchedTreeAtRecordedTag(t *testing.T) {
	r := newHelloRepo(t)
	root := t.TempDir()
	skill := installedDir(root, "hello")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: hello\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "hello", map[string]any{
		"sourceType":      "git",
		"sourceUrl":       r.URL,
		"skillPath":       "SKILL.md",
		"ref":             "v1.0.0",
		"skillFolderHash": strings.Repeat("0", 40),
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "unresolved" || !strings.Contains(rep.Entries[0].Note, "does not match") {
		t.Fatalf("Init report = %+v, want mismatched tree kept unresolved", rep.Entries)
	}
}

func TestInitRejectsLegacyPathForDifferentSkill(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("skills/other", "other")
	r.CommitAll("add other")
	r.Tag("v1.0.0")
	treeHash := r.SHA("HEAD:skills/other")
	r.Finish()

	root := t.TempDir()
	skill := installedDir(root, "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "demo", map[string]any{
		"sourceType":      "git",
		"sourceUrl":       r.URL,
		"skillPath":       "skills/other/SKILL.md",
		"ref":             "v1.0.0",
		"skillFolderHash": treeHash,
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "unresolved" || !strings.Contains(rep.Entries[0].Note, "not installed skill") {
		t.Fatalf("Init report = %+v, want mismatched legacy path unresolved", rep.Entries)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local {
		t.Fatalf("Init declarations = %+v, want local demo baseline", m.Skills)
	}
}

func TestInitContinuesWhenLegacyLockIsMalformed(t *testing.T) {
	root := t.TempDir()
	skill := installedDir(root, "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "cannot read previous installer lock") {
		t.Fatalf("Init notes = %v, want malformed legacy lock notice", rep.Notes)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local {
		t.Fatalf("Init declarations = %+v, want local demo baseline", m.Skills)
	}
}

func writeLegacyLock(t *testing.T, root, name string, record map[string]any) {
	t.Helper()
	legacy := struct {
		Version int            `json:"version"`
		Skills  map[string]any `json:"skills"`
	}{Version: 1, Skills: map[string]any{name: record}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitDoesNotGuessBetweenAmbiguousLegacyNames(t *testing.T) {
	root := t.TempDir()
	skill := installedDir(root, "alias")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: published\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := struct {
		Version int            `json:"version"`
		Skills  map[string]any `json:"skills"`
	}{
		Version: 1,
		Skills: map[string]any{
			"alias":     map[string]any{"sourceType": "npm", "source": "wrong/alias"},
			"published": map[string]any{"sourceType": "npm", "source": "wrong/published"},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || !strings.Contains(rep.Entries[0].Note, "ambiguous") {
		t.Fatalf("Init report = %+v, want ambiguity note", rep.Entries)
	}
}

// Init rejects case-folded directory collisions so declarations stay portable
// to case-insensitive filesystems. The fixture requires two distinct paths.
func TestInitRejectsCaseCollidingDirectories(t *testing.T) {
	root := t.TempDir()
	if caseInsensitiveFS(root) {
		t.Skip("requires a case-sensitive filesystem")
	}
	for _, dirName := range []string{"Demo", "demo"} {
		dir := filepath.Join(install.SkillsDir(root), dirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + dirName + "\n---\n# demo\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eng := newEngine(t, root, t.TempDir())
	if _, err := eng.Init(ctx, false, testIO()); err == nil {
		t.Error("Init accepted installation directories that collide when case is folded")
	}
	if _, err := os.Stat(filepath.Join(root, modfile.ModFileName)); !os.IsNotExist(err) {
		t.Errorf("failed import wrote SKILL.mod: %v", err)
	}
}

func TestInitRecoversMonorepoSnapshotLink(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("skills/demo", "demo")
	r.CommitAll("init")
	r.Tag("skills/demo/v1.2.0")
	r.Finish()
	eng := newEngine(t, t.TempDir(), t.TempDir())
	if _, err := eng.Get(ctx, r.URL+"//skills/demo@v1.2.0", "", testIO()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := eng.Store.SnapshotPath(r.URL, "skills/demo/v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	eng.Root = t.TempDir()
	dst := installedDir(eng.Root, "demo")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(snapshot, "skills", "demo"), dst); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	eng.Source.Git = filepath.Join(t.TempDir(), "no-network")
	if _, err := eng.Init(ctx, false, testIO()); err != nil {
		t.Fatal(err)
	}
	lk := loadLockSkill(t, eng.Root, "demo")
	if lk.Source != r.URL+"//skills/demo" || lk.Version != "skills/demo/v1.2.0" {
		t.Errorf("recovered lock = %+v, want exact monorepo path and tag", lk)
	}
	if _, err := eng.Sync(ctx, engine.SyncOptions{}, testIO()); err != nil {
		t.Errorf("offline Sync after import: %v", err)
	}
}

// A skill the upstream installer recorded from a web discovery endpoint has no
// Git provenance, so init cannot import the record. A configured known source
// that publishes byte-identical content is the better answer, and the note
// still tells the user which web source was dropped.
func TestInitAdoptsWebSourcedSkillFromKnownSource(t *testing.T) {
	r := testutil.NewRepo(t)
	r.WriteSkill("", "hello")
	r.CommitAll("init")
	r.Tag("v1.0.0")
	r.FinishNamed("hello")

	producer := newEngine(t, t.TempDir(), t.TempDir())
	if _, err := producer.Get(ctx, r.URL+"@v1.0.0", "", testIO()); err != nil {
		t.Fatalf("Get(%q): %v", r.URL+"@v1.0.0", err)
	}
	root := t.TempDir()
	if err := install.CopyDir(installedDir(producer.Root, "hello"), installedDir(root, "hello")); err != nil {
		t.Fatalf("CopyDir installed hello: %v", err)
	}
	writeLegacyLock(t, root, "hello", map[string]any{
		"source":          "open.feishu.cn",
		"sourceType":      "well-known",
		"sourceUrl":       "https://open.feishu.cn/.well-known/skills/hello/SKILL.md",
		"skillFolderHash": "",
	})

	eng := newEngine(t, root, t.TempDir())
	eng.Config.KnownSources = []string{r.URL}
	rep, err := eng.Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "matched" {
		t.Fatalf("Init report = %+v, want the known source to supply hello", rep.Entries)
	}
	if !strings.Contains(rep.Entries[0].Note, "web source") {
		t.Errorf("Init note = %q, want the dropped web source named", rep.Entries[0].Note)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod(%q): %v", root, err)
	}
	if len(m.Skills) != 1 || m.Skills[0].Local || m.Skills[0].Source != r.URL {
		t.Fatalf("Init declarations = %+v, want remote hello from %s", m.Skills, r.URL)
	}
}

// Without a known source to fall back on, a web-sourced skill stays an
// unresolved local baseline instead of failing the whole import.
func TestInitKeepsWebSourcedSkillLocalWithoutKnownSource(t *testing.T) {
	root := t.TempDir()
	skill := installedDir(root, "lark-whiteboard")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: lark-whiteboard\ndescription: published on the web\n---\n# installed\n"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "lark-whiteboard", map[string]any{
		"source":     "open.feishu.cn",
		"sourceType": "well-known",
		"sourceUrl":  "https://open.feishu.cn/.well-known/skills/lark-whiteboard/SKILL.md",
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "unresolved" {
		t.Fatalf("Init report = %+v, want one unresolved entry", rep.Entries)
	}
	if note := rep.Entries[0].Note; !strings.Contains(note, "web source") {
		t.Errorf("Init note = %q, want the web source explained", note)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod(%q): %v", root, err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local || m.Skills[0].Name != "lark-whiteboard" {
		t.Fatalf("Init declarations = %+v, want a local baseline", m.Skills)
	}
	lk := loadLockSkill(t, root, "lark-whiteboard")
	if lk.Source != "" || lk.Dirhash == "" {
		t.Fatalf("Init lock = %+v, want a local dirhash baseline", lk)
	}
}

// A record the previous installer already classified as local names no remote
// origin, so init keeps the entry as an ordinary local baseline: there is no
// provenance to recover and no gap to report.
func TestInitKeepsLegacyLocalRecordLocal(t *testing.T) {
	root := t.TempDir()
	skill := installedDir(root, "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n# installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyLock(t, root, "demo", map[string]any{
		"source":     "./demo",
		"sourceType": "local",
	})

	rep, err := newEngine(t, root, t.TempDir()).Init(ctx, false, testIO())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Action != "local" {
		t.Fatalf("Init report = %+v, want one local entry", rep.Entries)
	}
	if note := rep.Entries[0].Note; note != "" {
		t.Errorf("Init note = %q, want no provenance gap for a local record", note)
	}
	if notes := strings.Join(rep.Notes, "\n"); strings.Contains(notes, "unresolved") {
		t.Errorf("Init notes = %v, want no unresolved-source notice", rep.Notes)
	}
	m, err := modfile.LoadMod(root)
	if err != nil {
		t.Fatalf("LoadMod(%q): %v", root, err)
	}
	if len(m.Skills) != 1 || !m.Skills[0].Local || m.Skills[0].Name != "demo" {
		t.Fatalf("Init declarations = %+v, want a local baseline", m.Skills)
	}
	lk := loadLockSkill(t, root, "demo")
	if lk.Source != "" || lk.Dirhash == "" {
		t.Fatalf("Init lock = %+v, want a local dirhash baseline", lk)
	}
}
