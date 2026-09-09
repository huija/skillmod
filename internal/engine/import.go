// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"strings"

	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/store"
)

// identifyInstalled recovers provenance only from a matching lock baseline or
// a verified store snapshot. Ordinary directories and external links stay local
// unless init subsequently verifies a known remote's contents.
func (e *Engine) identifyInstalled(dirs []string, name, alias, hash string, lock *modfile.Lock, locator *store.SnapshotLocator) (*modfile.ModSkill, *modfile.LockSkill, error) {
	sk := &modfile.ModSkill{Name: name, Alias: alias}
	if old := findLockByDir(lock, sk.DirName()); old != nil && old.Source != "" && old.Name == name && old.Dirhash == hash {
		sk.Source, sk.Version = old.Source, old.Version
		lk := *old
		return sk, &lk, nil
	}
	if locator == nil {
		return nil, nil, nil
	}
	var lookupErrs []error
	for _, dir := range dirs {
		snap, subdir, err := locator.SnapshotForDir(dir)
		if err != nil {
			lookupErrs = append(lookupErrs, err)
			continue
		}
		if snap == nil || strings.Contains(subdir, "@") {
			continue
		}
		if _, err := snapshotSkillDir(snap, subdir); err != nil {
			lookupErrs = append(lookupErrs, err)
			continue
		}
		sk.Source, sk.Version = snap.Info.Repo, snap.Info.Version
		if subdir != "" {
			sk.Source += subdirSuffix(subdir)
		}
		lk := &modfile.LockSkill{Name: name, Dir: alias, Source: sk.Source, Version: sk.Version, Commit: snap.Info.Commit, Dirhash: hash}
		return sk, lk, nil
	}
	return nil, nil, errors.Join(lookupErrs...)
}
