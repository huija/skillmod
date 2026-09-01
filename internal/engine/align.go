// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// alignEntry is the result of reconciling and materializing a remote entry.
type alignEntry struct {
	skill      modfile.ModSkill
	version    string
	dirhash    string
	contentDir string
	note       string // change description, such as a version change
}

// align reconciles the mod and lock files and materializes all remote entries in phase 1, which reads only remotes and the global store.
// It does not touch installation directories or write mod or lock files, so failure leaves the project unchanged.
//
// Authority rule (PRD §3.3 rule 1): when mod and lock agree, follow the lock exactly;
// when the mod version was edited, resolve the entry again and update the lock.
func (e *Engine) align(ctx context.Context, m *modfile.Mod, lock *modfile.Lock) ([]alignEntry, *modfile.Lock, error) {
	newLock := &modfile.Lock{Skills: append([]modfile.LockSkill(nil), lock.Skills...)}
	var out []alignEntry
	memo := refsMemo{}
	for _, sk := range m.Skills {
		if sk.Local {
			continue // Callers validate local entries without modifying them.
		}
		repo, subdir, err := splitSource(sk.Source)
		if err != nil {
			return nil, nil, err
		}
		lk := findLock(lock, sk.Name)
		if lk != nil && lk.Version == sk.Version && lk.Dirhash != "" {
			// Agreement path: the lock is authoritative (AC-10), so do not call ls-remote.
			dir, err := e.materializeLocked(ctx, repo, subdir, *lk, memo)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, alignEntry{skill: sk, version: lk.Version, dirhash: lk.Dirhash, contentDir: dir})
			continue
		}
		// Resolve a new entry or a version edited in the mod file.
		mat, err := e.resolveAndFetch(ctx, repo, subdir, sk.Version, memo)
		if err != nil {
			return nil, nil, err
		}
		note := ""
		if lk != nil {
			note = i18n.Format("version changed %s → %s (manual mod edit triggered re-resolution)", lk.Version, mat.version)
		}
		upsertLock(newLock, modfile.LockSkill{
			Name: sk.Name, Source: sk.Source, Version: mat.version,
			Commit: mat.commit, Dirhash: mat.dirhash,
		})
		out = append(out, alignEntry{
			skill: sk, version: mat.version, dirhash: mat.dirhash,
			contentDir: mat.contentDir, note: note,
		})
	}
	return out, newLock, nil
}

// staleEntries returns entries still present in the lock but removed from the mod; sync reports them and prune removes them.
func staleEntries(m *modfile.Mod, lock *modfile.Lock) []modfile.LockSkill {
	inMod := map[string]bool{}
	for _, sk := range m.Skills {
		inMod[sk.Name] = true
	}
	var stale []modfile.LockSkill
	for _, lk := range lock.Skills {
		if !inMod[lk.Name] {
			stale = append(stale, lk)
		}
	}
	return stale
}
