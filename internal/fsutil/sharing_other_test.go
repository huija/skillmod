// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build !windows

package fsutil

// isTransientReadError is always false on POSIX platforms: rename gives a
// concurrent reader either the old or the new file, never an error.
func isTransientReadError(error) bool { return false }
