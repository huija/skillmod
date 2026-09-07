// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build windows

package fsutil

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var replaceMu sync.Mutex

// Replace moves from onto to with MOVEFILE_REPLACE_EXISTING, so existing
// content is replaced, and MOVEFILE_WRITE_THROUGH, so the rename is flushed
// before it returns. This is a best-effort atomic replacement: readers see
// either the old or the new file, but crash durability is weaker than a
// POSIX rename and is not guaranteed.
func Replace(from, to string) error {
	fromp, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	top, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}

	// Windows can transiently reject replacement while another goroutine,
	// antivirus, or indexer has a handle open. Serialize in-process replacements
	// and retry only the errors that can be caused by those short-lived handles.
	replaceMu.Lock()
	defer replaceMu.Unlock()
	const attempts = 20
	delay := time.Millisecond
	var replaceErr error
	for attempt := range attempts {
		replaceErr = windows.MoveFileEx(fromp, top, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if replaceErr == nil {
			return nil
		}
		if !errors.Is(replaceErr, windows.ERROR_SHARING_VIOLATION) &&
			!errors.Is(replaceErr, windows.ERROR_ACCESS_DENIED) {
			return replaceErr
		}
		if attempt+1 < attempts {
			time.Sleep(delay)
			if delay < 25*time.Millisecond {
				delay *= 2
			}
		}
	}
	return replaceErr
}
