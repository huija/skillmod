// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package install provides byte-preserving installation. skillmod manages one
// installation directory convention, so file names and content are never
// changed to suit a platform and dirhash values stay comparable everywhere.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/i18n"
)

// SkillsDirName is the installation directory, relative to a scope root, that
// skillmod manages. There is deliberately one convention: a project has a
// single place to review, and one content hash describes a skill everywhere.
const SkillsDirName = ".agents/skills"

// SkillsDir returns the skill installation directory under a scope root.
func SkillsDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(SkillsDirName))
}

// SkillDir returns one skill's installation directory under a scope root.
func SkillDir(root, dirName string) string {
	return filepath.Join(SkillsDir(root), dirName)
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

// Link installs a symlink to srcDir itself, preserving that path even when it
// currently points at a snapshot. Replacing the source link later therefore
// updates the shared installation too. Platforms without symlink support get
// a byte-preserving copy. The returned callbacks have Install's semantics.
func Link(srcDir, dst string) (restore func() error, commit func(), err error) {
	linkTarget, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, nil, err
	}
	return installWithLink(srcDir, dst, Auto, func(_ string, staged string) error {
		return os.Symlink(linkTarget, staged)
	})
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
