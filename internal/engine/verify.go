// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

	rep := &Report{Action: CommandVerify}
	drift := false
	for _, sk := range m.Skills {
		lk := findLock(lock, sk)
		entry := e.inspectSkill(sk, lk)
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

	shareDrift, err := e.verifyShareLinks(m, rep)
	if err != nil {
		return nil, err
	}
	drift = drift || shareDrift

	if drift {
		writeErr := io.printf(i18n.Text("engine.verify.verification_result_drift"))
		return rep, errors.Join(writeErr, &DriftError{Report: rep})
	}
	return rep, io.printf(i18n.Text("engine.verify.verification_result_all"))
}

// verifyShareLinks reports whether each skill's declared share destinations
// match the managed copy. An agent directory absent from this machine is
// skipped — the declaration expresses team intent, not a machine requirement —
// while a missing or foreign link under an existing agent directory is drift.
// Each destination is recorded on the entry for the skill it links, so a single
// skill whose share link drifted is reported once per destination. A skill that
// declares no agents is verified as staying only in the managed directory, so
// a link left behind by a hand edit is not this check's business.
func (e *Engine) verifyShareLinks(m *modfile.Mod, rep *Report) (bool, error) {
	listed, err := e.shareableSkills(&modfile.Lock{})
	if err != nil {
		return false, err
	}
	drifted := false
	for _, sk := range listed {
		entry := findEntryByDirectory(rep, sk.dirName)
		if entry == nil {
			continue
		}
		targets, err := e.shareTargetsFor(m, sk.dirName)
		if err != nil {
			return false, err
		}
		for _, target := range targets {
			if _, statErr := os.Stat(target.path); statErr != nil {
				continue // Agent directory absent on this machine.
			}
			dst := filepath.Join(target.path, sk.dirName)
			action, note, err := e.classifyShareTarget(sk, dst)
			if err != nil {
				return false, err
			}
			switch action {
			case ActionInstall:
				action, note = ActionMissing, i18n.Text("engine.verify.share_link_missing")
			case ActionConflict:
				action, note = ActionDrift, i18n.Text("engine.verify.share_link_drifted")
			}
			entry.TargetResults = append(entry.TargetResults, TargetReport{Path: dst, Action: action, Note: note})
			if action != ActionKeep {
				drifted = true
				entry.Action = mergeInspectionStatus(entry.Action, action)
			}
		}
	}
	return drifted, nil
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
