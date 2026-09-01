// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package install provides platform adapters and byte-preserving installation (PRD §3.5).
// Adapters may only choose installation directories; they must not change file names or content.
// This guarantees identical dirhash values across platform-specific copies.
package install

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
)

// Adapter defines directory conventions for a target platform.
type Adapter interface {
	Name() string
	// SkillsDir returns this platform's skill installation directory under the project root.
	SkillsDir(projectRoot string) string
}

type claudeCode struct{}

func (claudeCode) Name() string                 { return "claude-code" }
func (claudeCode) SkillsDir(root string) string { return filepath.Join(root, ".claude", "skills") }

type agentSkills struct{}

func (agentSkills) Name() string                 { return "agents" }
func (agentSkills) SkillsDir(root string) string { return filepath.Join(root, ".agents", "skills") }

var registry = map[string]Adapter{
	"claude-code": claudeCode{},
	"agents":      agentSkills{},
}

// ByName returns a named adapter or an error listing supported names (PRD §3.5 error table).
func ByName(name string) (Adapter, error) {
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf(i18n.Text("unsupported platform %q; supported platforms: %s"), name, strings.Join(Names(), ", "))
	}
	return a, nil
}

// Names returns supported platforms in stable sorted order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ByNames resolves multiple adapters, falling back to the default platform for an empty list.
func ByNames(names []string) ([]Adapter, error) {
	if len(names) == 0 {
		names = []string{"agents"}
	}
	out := make([]Adapter, 0, len(names))
	for _, n := range names {
		a, err := ByName(n)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// All returns every known adapter in stable name order. Discovery uses this
// independently of the configured installation targets.
func All() []Adapter {
	names := Names()
	out := make([]Adapter, 0, len(names))
	for _, name := range names {
		out = append(out, registry[name])
	}
	return out
}

// CopyDir copies src to a nonexistent dst byte for byte and preserves executable bits on regular files.
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf(i18n.Text("version snapshot contains an irregular file: %s"), p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if info.Mode().Perm()&0o100 != 0 {
			mode = 0o755
		}
		return os.WriteFile(to, data, mode)
	})
}

// Install atomically installs srcDir at dst by copying to a sibling temporary directory, backing up dst, and renaming.
// It returns restore, which swaps the backup back in, and commit, which removes the backup.
func Install(srcDir, dst string) (restore func() error, commit func(), err error) {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp(parent, ".skillmod-tmp-*")
	if err != nil {
		return nil, nil, err
	}
	// MkdirTemp created the directory, but CopyDir requires it not to exist, so remove it first.
	os.RemoveAll(tmp)
	if err := CopyDir(srcDir, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, nil, fmt.Errorf(i18n.Text("copy to temporary directory failed: %w"), err)
	}

	bak := ""
	if _, err := os.Lstat(dst); err == nil {
		bak = dst + ".skillmod-bak"
		os.RemoveAll(bak)
		if err := os.Rename(dst, bak); err != nil {
			os.RemoveAll(tmp)
			return nil, nil, fmt.Errorf(i18n.Text("backing up the existing directory failed: %w"), err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if bak != "" {
			os.Rename(bak, dst) // Best-effort restoration.
		}
		os.RemoveAll(tmp)
		return nil, nil, fmt.Errorf(i18n.Text("writing to disk failed: %w"), err)
	}

	restore = func() error {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if bak != "" {
			return os.Rename(bak, dst)
		}
		return nil
	}
	commit = func() {
		if bak != "" {
			os.RemoveAll(bak)
		}
	}
	return restore, commit, nil
}
