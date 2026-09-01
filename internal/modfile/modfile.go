// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package modfile defines the SKILL.mod and SKILL.lock schemas, I/O, and deterministic serialization.
//
// SKILL.mod is a human-maintained declaration, while SKILL.lock is a tool-maintained deterministic lock:
// it contains no timestamps or machine-specific fields, so identical input always produces identical bytes.
package modfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/pelletier/go-toml/v2"
)

const (
	// ModFileName is the committed declaration file at the repository root; capitalization follows SKILL.md conventions.
	ModFileName = "SKILL.mod"
	// LockFileName is the committed root lock file; it locks versions and hashes in the style of Cargo.lock rather than a .sum file.
	LockFileName = "SKILL.lock"
	// SchemaVersion is the current file-format version.
	SchemaVersion = 1
)

// Mod represents the human-maintained SKILL.mod file. Platform selection is a machine or user preference
// stored in the agents setting of ~/.config/skillmod/config.toml rather than in the mod file.
type Mod struct {
	SchemaVersion int        `toml:"schemaversion"`
	Skills        []ModSkill `toml:"skill,omitempty"`
}

// ModSkill is one declaration in SKILL.mod.
type ModSkill struct {
	Name    string `toml:"name"`              // name field from SKILL.md frontmatter
	Source  string `toml:"source,omitempty"`  // <repo>[//<subdir>]; omitted for local entries
	Version string `toml:"version,omitempty"` // exact tag, 40-character SHA, or pseudo-version
	Alias   string `toml:"alias,omitempty"`
	Local   bool   `toml:"local,omitempty"`
	// Requires is reserved; v0.0.1 uses flat 1:1 dependencies and does not parse it.
}

// DirName returns the installation directory name: alias ?? name.
func (s ModSkill) DirName() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Name
}

// Lock represents the tool-maintained SKILL.lock file, which must not be edited manually.
type Lock struct {
	Skills []LockSkill `toml:"skill,omitempty"`
}

// LockSkill is one locked entry in SKILL.lock.
type LockSkill struct {
	Name    string `toml:"name"`
	Source  string `toml:"source,omitempty"`  // omitted for local entries
	Version string `toml:"version,omitempty"` // omitted for local entries
	Commit  string `toml:"commit,omitempty"`  // resolved full SHA, required to resolve pseudo-versions across machines because they contain only sha12
	Dirhash string `toml:"dirhash"`           // "h1:..."; also present for local entries to detect drift
}

// ParseMod parses SKILL.mod bytes. Unknown fields are accepted for forward compatibility with reserved fields such as requires,
// but format versions newer than the current version are rejected.
func ParseMod(data []byte) (*Mod, error) {
	var m Mod
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf(i18n.Text("parse SKILL.mod: %w"), err)
	}
	if m.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf(i18n.Text("SKILL.mod schemaversion=%d is newer than the supported version %d; upgrade skillmod"), m.SchemaVersion, SchemaVersion)
	}
	return &m, nil
}

// ParseLock parses SKILL.lock bytes.
func ParseLock(data []byte) (*Lock, error) {
	var l Lock
	if err := toml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf(i18n.Text("parse SKILL.lock: %w"), err)
	}
	return &l, nil
}

// MarshalMod serializes SKILL.mod deterministically by sorting entries by (name, source),
// preserving struct field order, using \n line endings, and ending with exactly one newline.
func MarshalMod(m *Mod) ([]byte, error) {
	cp := *m
	cp.Skills = sortedModSkills(m.Skills)
	return marshalDeterministic(cp)
}

// MarshalLock serializes SKILL.lock deterministically using the same rules as MarshalMod.
// A lock file is a pure function of its content, so identical input must produce identical bytes.
func MarshalLock(l *Lock) ([]byte, error) {
	cp := *l
	cp.Skills = sortedLockSkills(l.Skills)
	return marshalDeterministic(cp)
}

func marshalDeterministic(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.SetIndentTables(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Ensure exactly one trailing \n; go-toml already writes one, and this defensively normalizes it.
	out := bytes.TrimRight(buf.Bytes(), "\n")
	return append(out, '\n'), nil
}

func sortedModSkills(skills []ModSkill) []ModSkill {
	cp := make([]ModSkill, len(skills))
	copy(cp, skills)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Name != cp[j].Name {
			return cp[i].Name < cp[j].Name
		}
		return cp[i].Source < cp[j].Source
	})
	return cp
}

func sortedLockSkills(skills []LockSkill) []LockSkill {
	cp := make([]LockSkill, len(skills))
	copy(cp, skills)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Name != cp[j].Name {
			return cp[i].Name < cp[j].Name
		}
		return cp[i].Source < cp[j].Source
	})
	return cp
}

// LoadMod reads SKILL.mod from a directory and returns an error wrapping os.ErrNotExist when absent.
func LoadMod(dir string) (*Mod, error) {
	data, err := os.ReadFile(filepath.Join(dir, ModFileName))
	if err != nil {
		return nil, err
	}
	return ParseMod(data)
}

// LoadLock reads SKILL.lock from a directory and returns an error wrapping os.ErrNotExist when absent.
func LoadLock(dir string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(dir, LockFileName))
	if err != nil {
		return nil, err
	}
	return ParseLock(data)
}

// SaveMod writes SKILL.mod atomically through a temporary file and rename, preventing partial files from becoming visible.
func SaveMod(dir string, m *Mod) error {
	data, err := MarshalMod(m)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, ModFileName), data)
}

// SaveLock writes SKILL.lock atomically; the engine calls it only after a successful transaction.
func SaveLock(dir string, l *Lock) error {
	data, err := MarshalLock(l)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, LockFileName), data)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf(i18n.Text("write %s: %w"), filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf(i18n.Text("commit %s to disk: %w"), filepath.Base(path), err)
	}
	return nil
}
