// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build !windows

package fsutil

import "os"

// Replace renames from onto to, atomically replacing to when it exists.
func Replace(from, to string) error { return os.Rename(from, to) }
