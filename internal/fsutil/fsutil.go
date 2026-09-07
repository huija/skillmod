// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package fsutil provides portable filesystem primitives shared by the store,
// modfile, source, and engine packages: atomic file replacement, durable
// writes, Unicode case-fold keys, and path-component names that every
// supported platform (Linux, macOS, Windows) can represent.
package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/huija/skillmod/internal/i18n"
)

// WriteFile writes data to path atomically and durably: a unique temporary
// file in the same directory is written with the requested permission (the
// kernel applies the process umask at creation), synced, closed, and then
// renamed over path. The temporary file is removed on any failure. Readers
// observe either the old or the new content, never a partially written file.
//
// On Windows the final replacement goes through MoveFileEx with
// MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH; this is a best-effort
// atomic replacement and flush, not the same guarantee as a POSIX rename.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	base := filepath.Base(path)
	f, err := createTempFile(filepath.Dir(path), base, perm)
	if err != nil {
		return fmt.Errorf(i18n.Text("write %s: %w"), base, err)
	}
	tmpName := f.Name()
	defer func() {
		// no-op once the rename below has succeeded
		_ = os.Remove(tmpName)
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf(i18n.Text("write %s: %w"), base, err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf(i18n.Text("write %s: %w"), base, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf(i18n.Text("write %s: %w"), base, err)
	}
	if err := Replace(tmpName, path); err != nil {
		return fmt.Errorf(i18n.Text("commit %s to disk: %w"), base, err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf(i18n.Text("commit %s to disk: %w"), base, err)
	}
	return nil
}

// createTempFile opens a unique, exclusively created file with the requested
// permission bits so the kernel applies the process umask, instead of chmod
// over an 0600 temporary and accidentally widening a restrictive umask.
func createTempFile(dir, base string, perm fs.FileMode) (*os.File, error) {
	for range 10 {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, fmt.Sprintf("%s.tmp-%d-%s", base, os.Getpid(), hex.EncodeToString(suffix)))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return nil, errors.New("cannot create a unique temporary file name")
}
