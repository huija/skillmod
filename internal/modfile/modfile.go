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

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/resolve"
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
	// Dir overrides Name as the installation directory when an alias is used.
	// Canonical lock files omit it when the installation directory equals Name.
	Dir string `toml:"dir,omitempty"`
}

// InstallDir returns the installation directory represented by the lock entry.
func (s LockSkill) InstallDir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return s.Name
}

// ParseMod parses SKILL.mod bytes. The current schema is decoded strictly so
// misspelled or unsupported fields cannot be silently ignored. Entries are
// validated against the portable-name and uniqueness rules so hand-edited
// manifests cannot introduce names that cannot install on every platform.
func ParseMod(data []byte) (*Mod, error) {
	var m Mod
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&m); err != nil {
		return nil, fmt.Errorf(i18n.Text("parse SKILL.mod: %w"), err)
	}
	if err := ValidateMod(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ParseLock parses SKILL.lock bytes and validates the locked entries.
func ParseLock(data []byte) (*Lock, error) {
	var l Lock
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&l); err != nil {
		return nil, fmt.Errorf(i18n.Text("parse SKILL.lock: %w"), err)
	}
	normalizeLock(&l)
	if err := ValidateLock(&l); err != nil {
		return nil, err
	}
	return &l, nil
}

// ValidateMod checks every SKILL.mod declaration for a portable name, a valid
// alias, exact entry uniqueness, and fold-uniqueness of all installation
// directory names (letter-case differences collapse to one directory on
// Windows and macOS). Different sources may publish the same skill name when
// aliases give them distinct installation directories.
func ValidateMod(m *Mod) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf(i18n.Text("SKILL.mod schemaversion=%d is unsupported; expected %d"), m.SchemaVersion, SchemaVersion)
	}
	seenEntry := map[string]bool{}
	seenDir := map[string]string{}
	for i := range m.Skills {
		sk := &m.Skills[i]
		if err := fsutil.ValidName(sk.Name); err != nil {
			return fmt.Errorf(i18n.Text("SKILL.mod declares skill %q with an invalid name: %w"), sk.Name, err)
		}
		if sk.Alias != "" {
			if err := fsutil.ValidAlias(sk.Alias); err != nil {
				return fmt.Errorf(i18n.Text("SKILL.mod declares skill %q with an invalid alias: %w"), sk.Name, err)
			}
		}
		entryKey := sk.Name + "\x00" + sk.Source + "\x00" + sk.Alias
		if seenEntry[entryKey] {
			return fmt.Errorf(i18n.Text("SKILL.mod declares skill %q more than once"), sk.Name)
		}
		seenEntry[entryKey] = true
		if prev, ok := seenDir[fsutil.FoldKey(sk.DirName())]; ok {
			return fmt.Errorf(i18n.Text("SKILL.mod entries %q and %q differ only in letter case and map to the same installation directory; rename one or set an alias"), prev, sk.DirName())
		}
		seenDir[fsutil.FoldKey(sk.DirName())] = sk.DirName()
	}
	return nil
}

// ValidateLock applies the same portable-name and fold-uniqueness rules to
// SKILL.lock, plus validity of the recorded installation directory.
func ValidateLock(l *Lock) error {
	seenEntry := map[string]bool{}
	seenDir := map[string]string{}
	for i := range l.Skills {
		sk := &l.Skills[i]
		if err := fsutil.ValidName(sk.Name); err != nil {
			return fmt.Errorf(i18n.Text("SKILL.lock declares skill %q with an invalid name: %w"), sk.Name, err)
		}
		if sk.Dir != "" {
			if err := fsutil.ValidAlias(sk.Dir); err != nil {
				return fmt.Errorf(i18n.Text("SKILL.lock declares skill %q with an invalid installation directory: %w"), sk.Name, err)
			}
		}
		if sk.Dirhash == "" {
			return fmt.Errorf(i18n.Text("SKILL.lock declares skill %q without a dirhash"), sk.Name)
		}
		if sk.Source == "" {
			if sk.Version != "" || sk.Commit != "" {
				return fmt.Errorf(i18n.Text("SKILL.lock declares local skill %q with remote-only version or commit fields"), sk.Name)
			}
		} else {
			if sk.Version == "" {
				return fmt.Errorf(i18n.Text("SKILL.lock declares remote skill %q without a version"), sk.Name)
			}
			if !resolve.IsSHA(sk.Commit) {
				return fmt.Errorf(i18n.Text("SKILL.lock declares remote skill %q without a valid 40-character commit SHA"), sk.Name)
			}
		}
		entryKey := sk.Name + "\x00" + sk.Source + "\x00" + sk.Dir
		if seenEntry[entryKey] {
			return fmt.Errorf(i18n.Text("SKILL.lock declares skill %q more than once"), sk.Name)
		}
		seenEntry[entryKey] = true
		dir := sk.InstallDir()
		if prev, ok := seenDir[fsutil.FoldKey(dir)]; ok {
			return fmt.Errorf(i18n.Text("SKILL.lock entries %q and %q differ only in letter case and map to the same installation directory"), prev, dir)
		}
		seenDir[fsutil.FoldKey(dir)] = dir
	}
	return nil
}

// MarshalMod serializes SKILL.mod deterministically by sorting entries by (name, source, alias),
// preserving struct field order, using \n line endings, and ending with exactly one newline.
func MarshalMod(m *Mod) ([]byte, error) {
	cp := *m
	cp.Skills = sortedModSkills(m.Skills)
	return marshalDeterministic(cp)
}

// MarshalLock serializes SKILL.lock deterministically by (name, source, dir).
// A lock file is a pure function of its content, so identical input must produce identical bytes.
func MarshalLock(l *Lock) ([]byte, error) {
	cp := *l
	cp.Skills = sortedLockSkills(l.Skills)
	return marshalDeterministic(cp)
}

// normalizeLock removes representationally redundant fields while preserving
// the installation directory represented by every entry.
func normalizeLock(l *Lock) {
	for i := range l.Skills {
		if l.Skills[i].Dir == l.Skills[i].Name {
			l.Skills[i].Dir = ""
		}
	}
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
		if cp[i].Source != cp[j].Source {
			return cp[i].Source < cp[j].Source
		}
		return cp[i].Alias < cp[j].Alias
	})
	return cp
}

func sortedLockSkills(skills []LockSkill) []LockSkill {
	cp := make([]LockSkill, len(skills))
	copy(cp, skills)
	normalizeLock(&Lock{Skills: cp})
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Name != cp[j].Name {
			return cp[i].Name < cp[j].Name
		}
		if cp[i].Source != cp[j].Source {
			return cp[i].Source < cp[j].Source
		}
		return cp[i].Dir < cp[j].Dir
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
	if err := ValidateMod(m); err != nil {
		return err
	}
	data, err := MarshalMod(m)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, ModFileName), data)
}

// SaveLock writes SKILL.lock atomically; the engine calls it only after a successful transaction.
func SaveLock(dir string, l *Lock) error {
	if err := ValidateLock(l); err != nil {
		return err
	}
	data, err := MarshalLock(l)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, LockFileName), data)
}

func atomicWrite(path string, data []byte) error {
	return fsutil.WriteFile(path, data, 0o644)
}
