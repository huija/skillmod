// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build !unix && !windows

package filelock

import (
	"os"
	"path/filepath"
)

// Lock keeps the lock file open on platforms without an advisory-lock implementation.
func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
