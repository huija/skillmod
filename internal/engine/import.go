// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"strings"

	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/store"
)

// identifyInstalled recovers provenance only from a matching lock baseline or
// a verified store snapshot. Ordinary directories and external links stay local
// unless init subsequently verifies a known remote's contents.
func (e *Engine) identifyInstalled(dir, name, alias, hash string, lock *modfile.Lock, locator *store.SnapshotLocator) (*modfile.ModSkill, *modfile.LockSkill, error) {
	sk := &modfile.ModSkill{Name: name, Alias: alias}
	if old := findLockByDir(lock, sk.DirName()); old != nil && old.Source != "" && old.Name == name && old.Dirhash == hash {
		sk.Source, sk.Version = old.Source, old.Version
		lk := *old
		return sk, &lk, nil
	}
	if locator == nil {
		return nil, nil, nil
	}
	snap, subdir, err := locator.SnapshotForDir(dir)
	if err != nil {
		return nil, nil, err
	}
	if snap == nil || strings.Contains(subdir, "@") {
		return nil, nil, nil
	}
	if _, err := snapshotSkillDir(snap, subdir); err != nil {
		return nil, nil, err
	}
	sk.Source, sk.Version = snap.Info.Repository(), snap.Info.Version
	if subdir != "" {
		sk.Source += subdirSuffix(subdir)
	}
	lk := &modfile.LockSkill{Name: name, Dir: alias, Source: sk.Source, Version: sk.Version, Commit: snap.Info.Commit, Dirhash: hash}
	return sk, lk, nil
}
