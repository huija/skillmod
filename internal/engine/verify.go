// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// Verify checks every installation against SKILL.lock (PRD §3.4).
// It is read-only and never modifies files; detected drift returns DriftError, mapped to exit code 2 for AC-12.
func (e *Engine) Verify(ctx context.Context, io IO) (*Report, error) {
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock, err := loadLockStrict(e.Root)
	if err != nil {
		return nil, err
	}
	adapters, err := e.adapters()
	if err != nil {
		return nil, err
	}

	rep := &Report{Action: "verify"}
	drift := false
	for _, sk := range m.Skills {
		lk := findLock(lock, sk.Name)
		if lk == nil {
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Source: sk.Source, Action: "drift", Note: i18n.Text("no record in SKILL.lock; run skillmod sync")})
			drift = true
			continue
		}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, sk.DirName())
			h, err := dirhash.HashDir(dst)
			switch {
			case err != nil:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: i18n.Text("missing: ") + dst, Targets: []string{dst}})
				drift = true
			case h != lk.Dirhash:
				kind := i18n.Text("contents do not match the lock")
				if sk.Local {
					kind = i18n.Text("local entry contents do not match the baseline (local changes)")
				}
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: kind, Targets: []string{dst}})
				drift = true
			default:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "ok", Version: lk.Version, Targets: []string{dst}})
			}
		}
	}
	// Report stale entries without treating them as drift.
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Action: "stale", Note: i18n.Text("removed from the mod; run skillmod prune to clean it")})
	}

	if drift {
		io.printf(i18n.Text("verification result: drift detected"))
		return rep, &DriftError{Report: rep}
	}
	io.printf(i18n.Text("verification result: all entries are consistent"))
	return rep, nil
}

// loadLockStrict treats a missing lock as an error under verify semantics (PRD §3.4 error table).
func loadLockStrict(root string) (*modfile.Lock, error) {
	l, err := modfile.LoadLock(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s", i18n.Text("SKILL.lock not found\nAdvice: run skillmod sync first to generate the lock file"))
	}
	return l, err
}
