// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build windows

package fsutil

// Replace uses MOVEFILE_WRITE_THROUGH on Windows, so no separate directory
// handle flush is required after it returns.
func syncDir(string) error { return nil }
