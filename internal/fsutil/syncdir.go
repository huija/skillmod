// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build !windows

package fsutil

import "os"

// syncDir makes a completed rename durable by flushing its parent directory.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
