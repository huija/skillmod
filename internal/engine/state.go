// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/filelock"
	"github.com/huija/skillmod/internal/i18n"
)

// lockState serializes commands that observe or mutate one manifest scope.
// The lock lives in the user store so projects do not gain an untracked lock
// file. Its key uses the canonical absolute manifest path.
func (e *Engine) lockState() (func(), error) {
	root, err := filepath.Abs(e.manifestRoot())
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("resolve manifest root: %w"), err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	} else if !os.IsNotExist(resolveErr) {
		return nil, fmt.Errorf(i18n.Text("resolve manifest root: %w"), resolveErr)
	} else if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(root)); parentErr == nil {
		root = filepath.Join(parent, filepath.Base(root))
	} else if !errors.Is(parentErr, fs.ErrNotExist) {
		return nil, fmt.Errorf(i18n.Text("resolve manifest parent: %w"), parentErr)
	}

	lockRoot := filepath.Join(os.TempDir(), "skillmod-locks")
	if e.Store != nil {
		lockRoot = filepath.Join(e.Store.CacheRoot(), "locks")
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	path := filepath.Join(lockRoot, "state-"+hex.EncodeToString(sum[:])+".lock")
	unlock, err := filelock.Lock(path)
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("lock manifest state: %w"), err)
	}
	return unlock, nil
}

// applyRemovals moves paths to unique sibling backups. The caller commits the
// removal only after its lock-file update succeeds, or rolls it back on error.
func applyRemovals(paths []string) (finalize func(bool) error, err error) {
	type removal struct {
		path   string
		backup string
	}
	var applied []removal
	rollback := func() error {
		var rollbackErrs []error
		for i := len(applied) - 1; i >= 0; i-- {
			if err := os.Rename(applied[i].backup, applied[i].path); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf(i18n.Text("restore %s: %w"), applied[i].path, err))
			}
		}
		return errors.Join(rollbackErrs...)
	}
	for _, path := range paths {
		backup, err := os.MkdirTemp(filepath.Dir(path), ".skillmod-prune-*")
		if err != nil {
			return nil, errors.Join(err, rollback())
		}
		if err := os.Remove(backup); err != nil {
			return nil, errors.Join(err, os.RemoveAll(backup), rollback())
		}
		if err := os.Rename(path, backup); err != nil {
			return nil, errors.Join(fmt.Errorf(i18n.Text("stage removal of %s: %w"), path, err), rollback())
		}
		applied = append(applied, removal{path: path, backup: backup})
	}
	return func(commit bool) error {
		if !commit {
			return rollback()
		}
		var cleanupErrs []error
		for _, removal := range applied {
			if err := os.RemoveAll(removal.backup); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf(i18n.Text("remove backup for %s: %w"), removal.path, err))
			}
		}
		return errors.Join(cleanupErrs...)
	}, nil
}
