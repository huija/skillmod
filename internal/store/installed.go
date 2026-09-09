// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type snapshotLookupKey struct {
	repo    string
	version string
}

type snapshotLookupResult struct {
	snapshot *Snapshot
	err      error
}

// SnapshotLocator identifies installed directories while verifying each
// repository snapshot at most once. A locator is scoped to one command so a
// later command still revalidates immutable store contents.
type SnapshotLocator struct {
	store     *Store
	snapshots map[snapshotLookupKey]snapshotLookupResult
}

// NewSnapshotLocator returns a command-scoped snapshot directory locator.
func (s *Store) NewSnapshotLocator() *SnapshotLocator {
	return &SnapshotLocator{
		store:     s,
		snapshots: make(map[snapshotLookupKey]snapshotLookupResult),
	}
}

// SnapshotForDir identifies a directory inside this store and verifies its
// complete snapshot before returning repository provenance and a relative path.
// An unrelated directory returns a nil snapshot. No network requests are made.
func (s *Store) SnapshotForDir(dir string) (*Snapshot, string, error) {
	return s.NewSnapshotLocator().SnapshotForDir(dir)
}

// SnapshotForDir identifies a directory inside the locator's store. Complete
// snapshot verification is memoized for the lifetime of the locator.
func (l *SnapshotLocator) SnapshotForDir(dir string) (*Snapshot, string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, "", err
	}
	root, err := filepath.EvalSymlinks(l.store.ModRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return nil, "", nil
	}
	for candidate := resolved; candidate != root; candidate = filepath.Dir(candidate) {
		base := filepath.Base(candidate)
		at := strings.LastIndexByte(base, '@')
		if at < 0 {
			continue
		}
		parent, err := filepath.Rel(root, filepath.Dir(candidate))
		if err != nil {
			return nil, "", err
		}
		infoPath := filepath.Join(l.store.CacheRoot(), "download", parent, base[:at], "@v", base[at+1:]+".info")
		data, err := os.ReadFile(infoPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		var info SnapshotInfo
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, "", err
		}
		key := snapshotLookupKey{repo: info.Repo, version: info.Version}
		result, ok := l.snapshots[key]
		if !ok {
			result.snapshot, result.err = l.store.GetSnapshot(info.Repo, info.Version)
			l.snapshots[key] = result
		}
		if result.err != nil {
			return nil, "", result.err
		}
		snap := result.snapshot
		snapshotRoot, err := filepath.EvalSymlinks(snap.ContentDir)
		if err != nil {
			return nil, "", err
		}
		if snapshotRoot != candidate {
			continue
		}
		subdir, err := filepath.Rel(candidate, resolved)
		if err != nil {
			return nil, "", err
		}
		if subdir == "." {
			subdir = ""
		}
		return snap, filepath.ToSlash(subdir), nil
	}
	return nil, "", nil
}
