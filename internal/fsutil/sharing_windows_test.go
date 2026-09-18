// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build windows

package fsutil

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientReadError reports whether err is a short-lived failure a reader
// can hit while another goroutine replaces the file. Sharing violations cover
// scanners or writers holding the target open. File-not-found covers the
// MoveFileEx replacement window: replacing an existing target is not one
// atomic step, the kernel disposes of the target and then links the
// replacement, and a reader opening the path in between is told the file
// does not exist even though a complete file lands moments later. It matches
// the errors Replace retries on the write side, so readers tolerate the same
// transient window.
func isTransientReadError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}
