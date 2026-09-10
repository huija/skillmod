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
	unlock, err := e.lockState()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := e.loadMod()
	if err != nil {
		return nil, err
	}
	lock, err := loadLockStrict(e.manifestRoot())
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
		lk := findLock(lock, sk)
		if lk == nil {
			rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Source: sk.Source, Action: "drift", Note: i18n.Text("engine.verify.record_skill_lock_run")})
			drift = true
			continue
		}
		for _, a := range adapters {
			dst := adapterDir(a, e.Root, sk.DirName())
			h, err := dirhash.HashDir(dst)
			switch {
			case err != nil:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: i18n.Text("engine.sync.missing") + dst, Targets: []string{dst}, TargetResults: []TargetReport{{Path: dst, Action: "missing"}}})
				drift = true
			case h != lk.Dirhash:
				kind := i18n.Text("engine.verify.contents_match_lock")
				if sk.Local {
					kind = i18n.Text("engine.verify.local_entry_contents_match")
				}
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "drift", Note: kind, Targets: []string{dst}, TargetResults: []TargetReport{{Path: dst, Action: "drift"}}})
				drift = true
			default:
				rep.Entries = append(rep.Entries, EntryReport{Name: sk.Name, Action: "ok", Version: lk.Version, Targets: []string{dst}, TargetResults: []TargetReport{{Path: dst, Action: "installed"}}})
			}
		}
	}
	// Report stale entries without treating them as drift.
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Action: "stale", Note: i18n.Text("engine.verify.removed_mod_run_skillmod")})
	}

	if drift {
		io.printf(i18n.Text("engine.verify.verification_result_drift"))
		return rep, &DriftError{Report: rep}
	}
	io.printf(i18n.Text("engine.verify.verification_result_all"))
	return rep, nil
}

// loadLockStrict treats a missing lock as an error under verify semantics (PRD §3.4 error table).
func loadLockStrict(root string) (*modfile.Lock, error) {
	l, err := modfile.LoadLock(root)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s", i18n.Text("engine.verify.skill_lock_found_advice"))
	}
	if err != nil {
		return nil, fmt.Errorf("%w\nAdvice: %s", err, i18n.Text("engine.skill_lock_tool_maintained"))
	}
	return l, nil
}
