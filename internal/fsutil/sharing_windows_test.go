// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build windows

package fsutil

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientReadError reports whether err is a short-lived Windows sharing
// violation raised while the file is being replaced by another goroutine or
// briefly held by an external scanner. It matches the errors Replace retries
// on the write side, so readers can tolerate the same transient window.
func isTransientReadError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
