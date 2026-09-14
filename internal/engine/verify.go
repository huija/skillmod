// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
)

// Verify checks every installation against SKILL.lock.
// It is read-only and never modifies files; detected drift returns DriftError, mapped to exit code 2.
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

	rep := &Report{Action: CommandVerify}
	drift := false
	for _, sk := range m.Skills {
		lk := findLock(lock, sk)
		entry := e.inspectSkill(sk, lk, adapters)
		if lk == nil {
			entry.Note = i18n.Text("engine.verify.record_skill_lock_run")
		}
		if entry.Action != ActionInstalled {
			drift = true
			if entry.Note == "" && entry.Action == ActionDrift {
				entry.Note = i18n.Text("engine.verify.contents_match_lock")
				if sk.Local {
					entry.Note = i18n.Text("engine.verify.local_entry_contents_match")
				}
			}
		}
		rep.Entries = append(rep.Entries, entry)
	}
	// Report stale entries without treating them as drift.
	for _, lk := range staleEntries(m, lock) {
		rep.Entries = append(rep.Entries, EntryReport{
			Name: lk.Name, Action: ActionStale, Note: i18n.Text("engine.verify.removed_mod_run_skillmod")})
	}

	if drift {
		writeErr := io.printf(i18n.Text("engine.verify.verification_result_drift"))
		return rep, errors.Join(writeErr, &DriftError{Report: rep})
	}
	return rep, io.printf(i18n.Text("engine.verify.verification_result_all"))
}

// loadLockStrict treats a missing lock as an error under verify semantics.
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
