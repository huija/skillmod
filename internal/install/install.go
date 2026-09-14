// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package install provides platform adapters and byte-preserving installation.
// Adapters may only choose installation directories; they must not change file names or content.
// This guarantees identical dirhash values across platform-specific copies.
package install

import (
	"errors"
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

// ByName returns a named adapter or an error listing supported names.
func ByName(name string) (Adapter, error) {
	a, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf(i18n.Text("install.unsupported_platform"), name, strings.Join(Names(), ", "))
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
			return fmt.Errorf(i18n.Text("install.snapshot_irregular_file"), p)
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

// Install uses Auto mode. srcDir must be a persistent immutable snapshot.
// It returns restore, which swaps the backup back in, and commit, which removes
// the backup. Neither operation follows the installation link into the snapshot.
func Install(srcDir, dst string) (restore func() error, commit func(), err error) {
	return InstallWithMode(srcDir, dst, Auto)
}

// Mode controls machine-local installation representation, never manifest data.
type Mode string

const (
	// Auto prefers a directory symlink and falls back to a byte-preserving copy.
	Auto Mode = "auto"
	// Copy installs an independent writable directory.
	Copy Mode = "copy"
)

// ValidateMode rejects unknown modes before any installation changes are made.
func ValidateMode(mode Mode) error {
	switch mode {
	case "", Auto, Copy:
		return nil
	default:
		return fmt.Errorf(i18n.Text("install.unknown_install_mode"), mode)
	}
}

// InstallWithMode installs through a sibling temporary link or directory and
// preserves the existing target until commit. Directory links point to an
// absolute immutable snapshot path. Go uses native symlinks on Unix and Windows;
// Auto falls back to Copy when Windows privileges or the filesystem forbid links.
func InstallWithMode(srcDir, dst string, mode Mode) (restore func() error, commit func(), err error) {
	return installWithLink(srcDir, dst, mode, os.Symlink)
}

func installWithLink(srcDir, dst string, mode Mode, link func(string, string) error) (restore func() error, commit func(), err error) {
	if err := ValidateMode(mode); err != nil {
		return nil, nil, err
	}
	srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		return nil, nil, err
	}
	srcDir, err = filepath.EvalSymlinks(srcDir)
	if err != nil {
		return nil, nil, err
	}
	if err := validateSource(srcDir); err != nil {
		return nil, nil, err
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, nil, err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, nil, err
	}
	resolvedParent, err = filepath.Abs(resolvedParent)
	if err != nil {
		return nil, nil, err
	}
	target := filepath.Join(resolvedParent, filepath.Base(dst))
	insideSource, sourceErr := filepath.Rel(srcDir, target)
	insideTarget, targetErr := filepath.Rel(target, srcDir)
	if (sourceErr == nil && filepath.IsLocal(insideSource)) || (targetErr == nil && filepath.IsLocal(insideTarget)) {
		return nil, nil, fmt.Errorf(i18n.Text("install.source_target_overlap"), srcDir, dst)
	}
	tmp, err := os.MkdirTemp(parent, ".skillmod-tmp-*")
	if err != nil {
		return nil, nil, err
	}
	// Both the link and CopyDir need a nonexistent destination.
	if err := os.Remove(tmp); err != nil {
		return nil, nil, err
	}
	var stageErr error
	if mode != Copy {
		stageErr = link(srcDir, tmp)
	}
	if mode == Copy || stageErr != nil {
		stageErr = CopyDir(srcDir, tmp)
	}
	if stageErr != nil {
		_ = os.RemoveAll(tmp)
		return nil, nil, fmt.Errorf(i18n.Text("install.stage_installation_failed"), stageErr)
	}

	// A unique backup path never collides with a user skill whose name merely
	// resembles the backup suffix, so no pre-existing directory is deleted here.
	bak := ""
	if _, err := os.Lstat(dst); err == nil {
		b, err := os.MkdirTemp(parent, ".skillmod-bak-*")
		if err != nil {
			_ = os.RemoveAll(tmp)
			return nil, nil, err
		}
		// MkdirTemp created the directory, but Rename requires the target not to exist.
		if err := os.RemoveAll(b); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, nil, err
		}
		bak = b
		if err := os.Rename(dst, bak); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, nil, fmt.Errorf(i18n.Text("install.backing_up_existing_directory"), err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		var restoreErr error
		if bak != "" {
			restoreErr = os.Rename(bak, dst)
		}
		_ = os.RemoveAll(tmp)
		return nil, nil, errors.Join(fmt.Errorf(i18n.Text("install.write_disk_failed"), err), restoreErr)
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
			_ = os.RemoveAll(bak)
		}
	}
	return restore, commit, nil
}

// validateSource applies the same tree rules to linked and copied installations.
func validateSource(src string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src && !d.IsDir() {
			return fmt.Errorf(i18n.Text("dirhash.not_skill_directory"), src)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf(i18n.Text("install.snapshot_irregular_file"), p)
		}
		return nil
	})
}
