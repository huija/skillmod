// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package modfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

// This PRD §3.0 format example keeps the implementation textually aligned with the specification.
const prdModExample = `schemaversion = 1

[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"

[[skill]]
name = "legacy-notes"
local = true
`

const prdLockExample = `[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"
commit = "7f3a9c1e00000000000000000000000000000000"
dirhash = "h1:4wYq0b..."
`

func TestParseMod_PRDExample(t *testing.T) {
	m, err := ParseMod([]byte(prdModExample))
	if err != nil {
		t.Fatalf("ParseMod: %v", err)
	}
	if m.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", m.SchemaVersion)
	}
	if len(m.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2", len(m.Skills))
	}
	s := m.Skills[0]
	if s.Name != "code-review" || s.Source != "github.com/acme/agent-skills//code-review" || s.Version != "code-review/v1.2.0" {
		t.Errorf("skill[0] = %+v", s)
	}
	if !m.Skills[1].Local {
		t.Errorf("skill[1].Local = false, want true")
	}
}

func TestParseLock_PRDExample(t *testing.T) {
	l, err := ParseLock([]byte(prdLockExample))
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}
	if len(l.Skills) != 1 || l.Skills[0].Dirhash != "h1:4wYq0b..." || l.Skills[0].InstallDir() != "code-review" {
		t.Errorf("Skills = %+v", l.Skills)
	}
}

func TestParseLock_NormalizesRedundantDirectory(t *testing.T) {
	data := []byte("[[skill]]\nname = \"algorithmic-art\"\ndirhash = \"h1:test\"\ndir = \"algorithmic-art\"\n")
	l, err := ParseLock(data)
	if err != nil {
		t.Fatalf("ParseLock(redundant dir) error = %v, want nil", err)
	}
	if got := l.Skills[0].Dir; got != "" {
		t.Errorf("ParseLock(redundant dir).Skills[0].Dir = %q, want empty", got)
	}
}

func TestParseMod_UnsupportedSchemaRejected(t *testing.T) {
	for name, data := range map[string]string{
		"missing":  "",
		"zero":     "schemaversion = 0\n",
		"negative": "schemaversion = -1\n",
		"future":   "schemaversion = 99\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMod([]byte(data))
			if err == nil || !strings.Contains(err.Error(), "schemaversion") {
				t.Errorf("ParseMod(%q) error = %v, want unsupported schemaversion rejection", data, err)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	for name, parse := range map[string]func([]byte) error{
		"SKILL.mod": func(data []byte) error {
			_, err := ParseMod(data)
			return err
		},
		"SKILL.lock": func(data []byte) error {
			_, err := ParseLock(data)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte("unknown = true\n")
			if name == "SKILL.mod" {
				data = []byte("schemaversion = 1\nunknown = true\n")
			}
			if err := parse(data); err == nil {
				t.Errorf("%s parser accepted unknown field, want error", name)
			}
		})
	}
}

func TestSkillInstallationDirectories(t *testing.T) {
	if got := (ModSkill{Name: "a", Alias: "b"}).DirName(); got != "b" {
		t.Errorf("ModSkill.DirName() with alias = %q, want b", got)
	}
	if got := (ModSkill{Name: "a"}).DirName(); got != "a" {
		t.Errorf("ModSkill.DirName() without alias = %q, want a", got)
	}
	if got := (LockSkill{Name: "a", Dir: "b"}).InstallDir(); got != "b" {
		t.Errorf("LockSkill.InstallDir() with override = %q, want b", got)
	}
	if got := (LockSkill{Name: "a"}).InstallDir(); got != "a" {
		t.Errorf("LockSkill.InstallDir() without override = %q, want a", got)
	}
}

func TestMarshalLock_Deterministic(t *testing.T) {
	l := &Lock{Skills: []LockSkill{
		{Name: "b", Source: "x//b", Version: "v1.0.0", Commit: strings.Repeat("b", 40), Dirhash: "h1:bbb"},
		{Name: "a", Source: "x//a", Version: "v2.0.0", Commit: strings.Repeat("a", 40), Dirhash: "h1:aaa"},
		{Name: "same", Source: "x//same", Version: "v1.0.0", Commit: strings.Repeat("c", 40), Dirhash: "h1:z", Dir: "same-z"},
		{Name: "same", Source: "x//same", Version: "v1.0.0", Commit: strings.Repeat("d", 40), Dirhash: "h1:a", Dir: "same-a"},
	}}
	first, err := MarshalLock(l)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		again, err := MarshalLock(l)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("repeated serialization produced different bytes")
		}
	}
	// Entry sorting makes unordered input produce the same bytes as ordered input.
	rev := &Lock{Skills: []LockSkill{l.Skills[3], l.Skills[2], l.Skills[1], l.Skills[0]}}
	revOut, err := MarshalLock(rev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, revOut) {
		t.Errorf("sorting is nondeterministic:\nordered:\n%s\nreversed:\n%s", first, revOut)
	}
	// The file ends with exactly one newline and contains no \r.
	if !bytes.HasSuffix(first, []byte("}\n")) && !strings.HasSuffix(string(first), "\n") {
		t.Error("serialized file is missing its trailing newline")
	}
	if bytes.HasSuffix(first, []byte("\n\n")) || bytes.Contains(first, []byte("\r")) {
		t.Error("serialized file has extra trailing newlines or contains \\r")
	}
}

func TestMarshalLock_OmitsDefaultDirectory(t *testing.T) {
	plain, err := MarshalLock(&Lock{Skills: []LockSkill{{Name: "plain", Dirhash: "h1:x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("dir =")) {
		t.Errorf("MarshalLock(plain) = %s, want dir omitted", plain)
	}
	redundant, err := MarshalLock(&Lock{Skills: []LockSkill{{Name: "plain", Dir: "plain", Dirhash: "h1:x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(redundant, []byte("dir =")) {
		t.Errorf("MarshalLock(redundant dir) = %s, want dir omitted", redundant)
	}

	aliased, err := MarshalLock(&Lock{Skills: []LockSkill{{Name: "published", Dir: "installed", Dirhash: "h1:x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(aliased, []byte("dir =")) {
		t.Errorf("MarshalLock(aliased) = %s, want dir override", aliased)
	}
}

func TestMarshalMod_DoesNotMutateInput(t *testing.T) {
	m := &Mod{SchemaVersion: 1, Skills: []ModSkill{{Name: "b"}, {Name: "a"}}}
	if _, err := MarshalMod(m); err != nil {
		t.Fatal(err)
	}
	if m.Skills[0].Name != "b" {
		t.Error("MarshalMod mutated the caller's slice order")
	}
}

func TestMarshalMod_DeterministicWithDuplicateNames(t *testing.T) {
	entries := []ModSkill{
		{Name: "demo", Source: "repo//demo", Alias: "demo-z"},
		{Name: "demo", Source: "repo//demo", Alias: "demo-a"},
	}
	forward, err := MarshalMod(&Mod{SchemaVersion: 1, Skills: entries})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := MarshalMod(&Mod{SchemaVersion: 1, Skills: []ModSkill{entries[1], entries[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forward, reverse) {
		t.Errorf("MarshalMod duplicate-name order is unstable:\nforward:\n%s\nreverse:\n%s", forward, reverse)
	}
}

func TestMod_RoundTrip(t *testing.T) {
	m := &Mod{SchemaVersion: 1, Skills: []ModSkill{
		{Name: "code-review", Source: "github.com/acme/agent-skills//code-review", Version: "code-review/v1.2.0"},
		{Name: "legacy-notes", Local: true},
		{Name: "cr", Alias: "cr-alias", Source: "github.com/acme/cr", Version: "v0.3.0"},
	}}
	data, err := MarshalMod(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseMod(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := MarshalMod(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Errorf("round trip is unstable:\n%s\n---\n%s", data, again)
	}
}

func TestSaveAndLoad_Atomic(t *testing.T) {
	dir := t.TempDir()
	m := &Mod{SchemaVersion: SchemaVersion, Skills: []ModSkill{{Name: "local", Local: true}}}
	if err := SaveMod(dir, m); err != nil {
		t.Fatal(err)
	}
	loadedMod, err := LoadMod(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedMod.Skills) != 1 || loadedMod.Skills[0].Name != "local" {
		t.Fatalf("LoadMod = %+v", loadedMod.Skills)
	}

	l := &Lock{Skills: []LockSkill{{
		Name: "a", Source: "s", Version: "v1.0.0", Commit: strings.Repeat("a", 40), Dirhash: "h1:x",
	}}}
	if err := SaveLock(dir, l); err != nil {
		t.Fatal(err)
	}
	// Leave no temporary files.
	if _, err := os.Stat(filepath.Join(dir, LockFileName+".tmp")); !os.IsNotExist(err) {
		t.Error("temporary file remains after atomic save")
	}
	back, err := LoadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Skills) != 1 || back.Skills[0].Dirhash != "h1:x" {
		t.Errorf("LoadLock = %+v", back.Skills)
	}
	if err := os.Remove(filepath.Join(dir, ModFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMod(dir); !os.IsNotExist(err) {
		t.Errorf("missing SKILL.mod error = %v, want ErrNotExist", err)
	}
}

func TestParseRejectsMalformedTOML(t *testing.T) {
	if _, err := ParseMod([]byte("schemaversion = [")); err == nil || !strings.Contains(err.Error(), "SKILL.mod") {
		t.Fatalf("ParseMod error = %v", err)
	}
	if _, err := ParseLock([]byte("[[skill]\n")); err == nil || !strings.Contains(err.Error(), "SKILL.lock") {
		t.Fatalf("ParseLock error = %v", err)
	}
}

func TestAtomicWriteFailureDoesNotLeaveTemporaryFile(t *testing.T) {
	missingParent := filepath.Join(t.TempDir(), "missing", ModFileName)
	if err := atomicWrite(missingParent, []byte("data")); err == nil || !strings.Contains(err.Error(), "write SKILL.mod") {
		t.Fatalf("missing parent error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ModFileName)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("data")); err == nil || !strings.Contains(err.Error(), "commit SKILL.mod") {
		t.Fatalf("rename error = %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ModFileName+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files remain after failed rename: %v", leftovers)
	}
}

func TestValidateMod(t *testing.T) {
	valid := &Mod{SchemaVersion: 1, Skills: []ModSkill{
		{Name: "demo", Source: "r//demo", Version: "v1.0.0"},
		{Name: "demo", Alias: "other-demo", Source: "other//demo", Version: "v1.0.0"},
		{Name: "Demo", Alias: "capital-demo", Source: "capital//demo", Version: "v1.0.0"},
		{Name: "cr", Alias: "cr-alias", Source: "r"},
		{Name: "中文技能", Local: true},
	}}
	if err := ValidateMod(valid); err != nil {
		t.Fatalf("ValidateMod(valid) = %v, want nil", err)
	}
	invalid := map[string]Mod{
		"invalid name":      {Skills: []ModSkill{{Name: "a:b"}}},
		"reserved name":     {Skills: []ModSkill{{Name: "CON"}}},
		"trailing dot":      {Skills: []ModSkill{{Name: "demo."}}},
		"empty name":        {Skills: []ModSkill{{Name: ""}}},
		"invalid alias":     {Skills: []ModSkill{{Name: "a", Alias: "a:b"}}},
		"reserved alias":    {Skills: []ModSkill{{Name: "a", Alias: "con"}}},
		"duplicate name":    {Skills: []ModSkill{{Name: "a"}, {Name: "a"}}},
		"case dir conflict": {Skills: []ModSkill{{Name: "Demo"}, {Name: "demo"}}},
		"alias dir conflict": {Skills: []ModSkill{
			{Name: "a", Alias: "shared"},
			{Name: "b", Alias: "SHARED"},
		}},
	}
	for label, m := range invalid {
		m.SchemaVersion = SchemaVersion
		if err := ValidateMod(&m); err == nil {
			t.Errorf("ValidateMod(%s) = nil, want error", label)
		}
	}
}

func TestValidateLock(t *testing.T) {
	valid := &Lock{Skills: []LockSkill{
		{Name: "demo", Dirhash: "h1:a"},
		{Name: "plain", Dir: "plain", Dirhash: "h1:e"},
		{Name: "demo", Source: "other//demo", Version: "v1.0.0", Commit: strings.Repeat("a", 40), Dir: "other-demo", Dirhash: "h1:c"},
		{Name: "Demo", Source: "capital//demo", Version: "v1.0.0", Commit: strings.Repeat("b", 40), Dir: "capital-demo", Dirhash: "h1:d"},
		{Name: "cr", Dir: "cr-alias", Dirhash: "h1:b"},
	}}
	if err := ValidateLock(valid); err != nil {
		t.Fatalf("ValidateLock(valid) = %v, want nil", err)
	}
	invalid := map[string]Lock{
		"invalid name":    {Skills: []LockSkill{{Name: "a*b", Dirhash: "h1:x"}}},
		"invalid dir":     {Skills: []LockSkill{{Name: "a", Dir: "con", Dirhash: "h1:x"}}},
		"missing dirhash": {Skills: []LockSkill{{Name: "a"}}},
		"local remote fields": {Skills: []LockSkill{{
			Name: "a", Version: "v1.0.0", Commit: strings.Repeat("a", 40), Dirhash: "h1:x",
		}}},
		"missing version": {Skills: []LockSkill{{
			Name: "a", Source: "repo", Commit: strings.Repeat("a", 40), Dirhash: "h1:x",
		}}},
		"missing commit":    {Skills: []LockSkill{{Name: "a", Source: "repo", Version: "v1.0.0", Dirhash: "h1:x"}}},
		"invalid commit":    {Skills: []LockSkill{{Name: "a", Source: "repo", Version: "v1.0.0", Commit: "abc", Dirhash: "h1:x"}}},
		"duplicate name":    {Skills: []LockSkill{{Name: "a", Dirhash: "h1:x"}, {Name: "a", Dirhash: "h1:x"}}},
		"case dir conflict": {Skills: []LockSkill{{Name: "Demo", Dirhash: "h1:x"}, {Name: "demo", Dirhash: "h1:y"}}},
		"dir conflict":      {Skills: []LockSkill{{Name: "a", Dir: "shared", Dirhash: "h1:x"}, {Name: "b", Dir: "SHARED", Dirhash: "h1:y"}}},
	}
	for label, l := range invalid {
		if err := ValidateLock(&l); err == nil {
			t.Errorf("ValidateLock(%s) = nil, want error", label)
		}
	}
}

func TestParseMod_RejectsInvalidEntries(t *testing.T) {
	cases := map[string]string{
		"case conflict": "schemaversion = 1\n\n[[skill]]\nname = \"Demo\"\n\n[[skill]]\nname = \"demo\"\n",
		"invalid name":  "schemaversion = 1\n\n[[skill]]\nname = \"a:b\"\n",
		"reserved name": "schemaversion = 1\n\n[[skill]]\nname = \"con.txt\"\n",
	}
	for label, data := range cases {
		if _, err := ParseMod([]byte(data)); err == nil {
			t.Errorf("ParseMod(%s) = nil error, want rejection", label)
		}
	}
}
