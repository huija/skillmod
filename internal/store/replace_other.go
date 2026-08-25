// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

//go:build !windows

package store

import "os"

func replaceFile(from, to string) error {
	return os.Rename(from, to)
}
