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
	if len(l.Skills) != 1 || l.Skills[0].Dirhash != "h1:4wYq0b..." {
		t.Errorf("Skills = %+v", l.Skills)
	}
}

func TestParseMod_FutureSchemaRejected(t *testing.T) {
	_, err := ParseMod([]byte("schemaversion = 99\n"))
	if err == nil || !strings.Contains(err.Error(), "schemaversion") {
		t.Errorf("err = %v, want unsupported schemaversion rejection", err)
	}
}

func TestParseMod_UnknownFieldsTolerated(t *testing.T) {
	// requires is reserved; v0.0.1 does not parse it but must not reject it.
	m, err := ParseMod([]byte("schemaversion = 1\n\n[[skill]]\nname = \"a\"\nrequires = [\"b\"]\n"))
	if err != nil {
		t.Fatalf("ParseMod with requires: %v", err)
	}
	if m.Skills[0].Name != "a" {
		t.Errorf("Name = %q", m.Skills[0].Name)
	}
}

func TestModSkill_DirName(t *testing.T) {
	if got := (ModSkill{Name: "a", Alias: "b"}).DirName(); got != "b" {
		t.Errorf("DirName with alias = %q, want b", got)
	}
	if got := (ModSkill{Name: "a"}).DirName(); got != "a" {
		t.Errorf("DirName without alias = %q, want a", got)
	}
}

func TestMarshalLock_Deterministic(t *testing.T) {
	l := &Lock{Skills: []LockSkill{
		{Name: "b", Source: "x//b", Version: "v1.0.0", Dirhash: "h1:bbb"},
		{Name: "a", Source: "x//a", Version: "v2.0.0", Dirhash: "h1:aaa"},
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
	rev := &Lock{Skills: []LockSkill{l.Skills[1], l.Skills[0]}}
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

func TestMarshalMod_DoesNotMutateInput(t *testing.T) {
	m := &Mod{SchemaVersion: 1, Skills: []ModSkill{{Name: "b"}, {Name: "a"}}}
	if _, err := MarshalMod(m); err != nil {
		t.Fatal(err)
	}
	if m.Skills[0].Name != "b" {
		t.Error("MarshalMod mutated the caller's slice order")
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

	l := &Lock{Skills: []LockSkill{{Name: "a", Source: "s", Version: "v1.0.0", Dirhash: "h1:x"}}}
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
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains after failed rename: %v", err)
	}
}
