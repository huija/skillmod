// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package dirhash wraps the official x/mod dirhash.Hash1 implementation without rewriting it (dev-design §1).
// Fetching hashes Git blob bytes while verification hashes installed files through the same implementation,
// ensuring that downloaded and recomputed installation hashes match for AC-1.
package dirhash

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/sumdb/dirhash"
)

// HashBlobs computes an h1: hash for content addressed by slash-separated subtree-relative paths.
func HashBlobs(files []string, open func(string) (io.ReadCloser, error)) (string, error) {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	return dirhash.Hash1(sorted, open)
}

// HashDir recomputes an h1: hash for a directory by walking regular files and slash-normalizing paths.
// Installation directories must not contain symlinks because v0.1 rejects skills that contain them.
func HashDir(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("安装目录含 symlink，无法校验: %s", p)
		}
		if !d.Type().IsRegular() {
			return nil // Exclude special files such as FIFOs and sockets from the hash.
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("目录为空或不存在: %s", dir)
	}
	return HashBlobs(files, func(name string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	})
}

// Validate checks the h1: prefix format at lock-file boundaries.
func Validate(h string) error {
	if !strings.HasPrefix(h, "h1:") {
		return fmt.Errorf("dirhash 缺少 h1: 前缀: %q", h)
	}
	return nil
}
